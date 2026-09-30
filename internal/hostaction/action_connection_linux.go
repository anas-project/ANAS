//go:build linux

package hostaction

import (
	"context"
	"net"
	"os"
	"time"

	"github.com/anas-project/ANAS/internal/actionabi"
	"github.com/anas-project/ANAS/internal/buildinfo"
	"github.com/anas-project/ANAS/internal/securefs"
	"golang.org/x/sys/unix"
)

// CheckHostActionClient performs only fixed-path installation checks. It does
// not connect to the activation socket or launch a process. Used before the
// daemon exposes queue admission; it is not a real-host readiness assertion.
func CheckHostActionClient() (result error) {
	if os.Getuid() != 0 || os.Getgid() != 0 || os.Getuid() != os.Geteuid() || os.Getgid() != os.Getegid() {
		return ErrDenied
	}
	root, err := unix.Open("/", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return ErrUnavailable
	}
	defer unix.Close(root)
	policy, err := readInstalledPolicyAt(root)
	if err != nil {
		return err
	}
	if !policy.peers().authorizes(context.Background(), Peer{pid: int32(os.Getpid()), uid: 0, gid: 0}) {
		return ErrDenied
	}
	guard, err := openPinnedInstallationPath(root, activationSocketPath, 0, true)
	if err != nil {
		return ErrUnavailable
	}
	defer func() {
		if guard.close() != nil {
			result = ErrUnavailable
		}
	}()
	if guard.last().stat.Gid != policy.socketGroup() || guard.check() != nil {
		return ErrUnavailable
	}
	return nil
}

// DialHostAction is the execution owner's fixed activation client for a job
// that is already running in the shared store. The request is written and its
// write half closed; the caller reads the event stream. This is not an invoke
// API; destinations and write actions are not parameters.
func DialHostAction(ctx context.Context, request actionabi.Request) (*net.UnixConn, error) {
	if ctx == nil || ctx.Err() != nil || os.Getuid() != 0 || os.Getgid() != 0 || os.Getuid() != os.Geteuid() || os.Getgid() != os.Getegid() {
		return nil, ErrDenied
	}
	if _, err := prepare(request, Peer{pid: int32(os.Getpid()), uid: uint32(os.Geteuid()), gid: uint32(os.Getegid()), verified: true}); err != nil {
		return nil, err
	}
	body, err := actionabi.EncodeRequest(request)
	if err != nil {
		return nil, ErrRequest
	}
	root, err := unix.Open("/", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, ErrUnavailable
	}
	defer unix.Close(root)
	policy, err := readInstalledPolicyAt(root)
	if err != nil {
		return nil, ErrNoExecution
	}
	if !policy.peers().authorizes(ctx, Peer{pid: int32(os.Getpid()), uid: 0, gid: 0}) {
		return nil, ErrNoExecution
	}
	guard, err := openPinnedInstallationPath(root, activationSocketPath, 0, true)
	if err != nil {
		return nil, ErrNoExecution
	}
	defer guard.close()
	dialer := net.Dialer{Timeout: 3 * time.Second}
	c, err := dialer.DialContext(ctx, "unix", activationSocketPath)
	if err != nil {
		return nil, ErrNoExecution
	}
	stream, ok := c.(*net.UnixConn)
	if !ok {
		_ = c.Close()
		return nil, ErrUnavailable
	}
	keep := false
	defer func() {
		if !keep {
			_ = stream.Close()
		}
	}()
	// SO_PEERCRED here identifies the listener's creator, PID 1: the socket
	// really is the service manager's root-owned activation socket.
	peer, _ := authenticatePeer(ctx, stream, PeerPolicy{})
	if peer.pid != 1 || peer.uid != 0 || peer.gid != 0 || guard.check() != nil {
		return nil, ErrNoExecution
	}
	deadline := time.Now().Add(ioTimeout)
	if bound, ok := ctx.Deadline(); ok && bound.Before(deadline) {
		deadline = bound
	}
	if stream.SetWriteDeadline(deadline) != nil {
		return nil, ErrNoExecution
	}
	if securefs.WriteAll(stream, body) != nil || stream.CloseWrite() != nil || guard.check() != nil || ctx.Err() != nil {
		return nil, ErrUnavailable
	}
	// The full compiled action budget plus the final exchange. Sending the
	// request has happened: any failure from here is uncertain execution,
	// never "not submitted"; the caller settles it with QueryHostInvocation.
	readDeadline := time.Now().Add(ExecutionTimeout(request.Action) + ioTimeout)
	if bound, ok := ctx.Deadline(); ok && bound.Before(readDeadline) {
		readDeadline = bound
	}
	if stream.SetReadDeadline(readDeadline) != nil {
		return nil, ErrUnavailable
	}
	keep = true
	return stream, nil
}

// QueryHostInvocation asks a fresh hostd activation what its ledger recorded
// for the job's invocation. It never starts or retries the action.
func QueryHostInvocation(ctx context.Context, jobID, invocationID string) (InvocationStatus, error) {
	request := actionabi.Request{ABI: actionabi.Version, JobID: jobID, InvocationID: invocationID, Action: ActionInvocationStatus,
		Parameters: []byte("{}")}
	stream, err := DialHostAction(ctx, request)
	if err != nil {
		return InvocationStatus{}, err
	}
	defer stream.Close()
	return readInvocationStatus(stream, jobID, invocationID)
}

func readInstalledPolicyAt(root int) (installationPolicy, error) {
	config, err := openPinnedInstallationPath(root, installationPath, 0, false)
	if err != nil {
		return installationPolicy{}, ErrUnavailable
	}
	defer config.close()
	body, err := config.readPolicy()
	if err != nil {
		return installationPolicy{}, err
	}
	return decodeInstallation(body, ReleaseIdentity{Version: buildinfo.Version, Commit: buildinfo.Commit})
}
