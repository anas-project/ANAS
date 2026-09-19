//go:build linux

package hostaction

import (
	"context"
	"net"
	"os"
	"time"

	"golang.org/x/sys/unix"
)

const brokerSocketPath = "/run/anas-job-broker/socket"

// AcceptJobBroker is called by the execution owner on a connection
// its private broker listener accepted. The endpoint must be owned by this
// process, not inherited from systemd: SO_PEERCRED is captured at listen time.
// Only root callers can attest to the peer of an activated host connection.
func AcceptJobBroker(connection *net.UnixConn) (*BrokerSession, error) {
	if os.Getuid() != os.Geteuid() || os.Getgid() != os.Getegid() {
		return nil, ErrDenied
	}
	session, err := acceptJobBrokerAs(connection, PeerIdentity{int32(os.Getpid()), uint32(os.Geteuid()), uint32(os.Getegid())}, 0, 0)
	if err == nil {
		// Native production sessions require independent manager exit evidence.
		// Private process-only fixtures do not pretend to be PID 1 services.
		session.requireSystemd = true
	}
	return session, err
}

// Private UID seams are for unprivileged subprocess tests; production always
// expects root on the executor side and the current process as owner.
func acceptJobBrokerAs(connection *net.UnixConn, self PeerIdentity, executorUID, executorGID uint32) (*BrokerSession, error) {
	peer, watch, err := socketBrokerProcess(connection)
	if err != nil {
		return nil, err
	}
	if self.PID <= 1 || peer.pid <= 1 || peer.pid == self.PID || peer.uid != executorUID || peer.gid != executorGID || watch.alive() != nil {
		_ = watch.close()
		return nil, ErrDenied
	}
	peer.verified = true
	return &BrokerSession{conn: connection, process: watch, self: self, peer: peer}, nil
}

type brokerPIDFD struct{ file *os.File }

func pinBrokerOrigin(connection *net.UnixConn, expected PeerIdentity) (brokerProcess, error) {
	peer, process, err := socketBrokerProcess(connection)
	if err != nil {
		return nil, err
	}
	if expected.PID <= 1 || peer.pid != expected.PID || peer.uid != expected.UID || peer.gid != expected.GID || process.alive() != nil {
		_ = process.close()
		return nil, ErrDenied
	}
	return process, nil
}

// SO_PEERPIDFD binds directly to the socket peer. pidfd_open(peer.PID) is NOT
// an equivalent fallback: a disconnected peer's number may have been reused.
// Unsupported kernels fail closed instead of trusting a PID from JSON/proc.
func socketBrokerProcess(connection *net.UnixConn) (Peer, *brokerPIDFD, error) {
	if connection == nil {
		return Peer{}, nil, ErrUnavailable
	}
	raw, err := connection.SyscallConn()
	if err != nil {
		return Peer{}, nil, ErrUnavailable
	}
	var peer Peer
	pidfd := -1
	var socketErr error
	err = raw.Control(func(fd uintptr) {
		for _, option := range []struct{ name, want int }{{unix.SO_TYPE, unix.SOCK_STREAM}, {unix.SO_DOMAIN, unix.AF_UNIX}, {unix.SO_ACCEPTCONN, 0}} {
			value, e := unix.GetsockoptInt(int(fd), unix.SOL_SOCKET, option.name)
			if e != nil || value != option.want {
				socketErr = ErrUnavailable
				return
			}
		}
		creds, e := unix.GetsockoptUcred(int(fd), unix.SOL_SOCKET, unix.SO_PEERCRED)
		if e != nil || creds == nil || creds.Pid <= 1 {
			socketErr = ErrDenied
			return
		}
		peer = Peer{pid: creds.Pid, uid: creds.Uid, gid: creds.Gid}
		pidfd, socketErr = unix.GetsockoptInt(int(fd), unix.SOL_SOCKET, unix.SO_PEERPIDFD)
	})
	if err != nil || socketErr != nil || pidfd < 0 {
		if pidfd >= 0 {
			_ = unix.Close(pidfd)
		}
		return Peer{}, nil, ErrUnavailable
	}
	flags, err := unix.FcntlInt(uintptr(pidfd), unix.F_GETFD, 0)
	if err != nil || flags&unix.FD_CLOEXEC == 0 {
		_ = unix.Close(pidfd)
		return Peer{}, nil, ErrUnavailable
	}
	return peer, &brokerPIDFD{os.NewFile(uintptr(pidfd), "host-broker-peer")}, nil
}

func (p *brokerPIDFD) exited() (bool, error) {
	if p == nil || p.file == nil {
		return false, ErrUnavailable
	}
	fds := []unix.PollFd{{Fd: int32(p.file.Fd()), Events: unix.POLLIN}}
	if _, err := unix.Poll(fds, 0); err != nil || fds[0].Revents&(unix.POLLERR|unix.POLLNVAL) != 0 {
		return false, ErrUnavailable
	}
	return fds[0].Revents&(unix.POLLIN|unix.POLLHUP) != 0, nil
}

func (p *brokerPIDFD) alive() error {
	ended, err := p.exited()
	if err != nil || ended {
		return ErrUnavailable
	}
	return nil
}

func (p *brokerPIDFD) waitExited(ctx context.Context) error {
	if ctx == nil {
		return ErrUnavailable
	}
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		done, err := p.exited()
		if err != nil {
			return err
		}
		if done {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func (p *brokerPIDFD) close() error {
	if p == nil || p.file == nil {
		return nil
	}
	err := p.file.Close()
	p.file = nil
	if err != nil {
		return ErrUnavailable
	}
	return nil
}

func remoteBrokerOnConnection(connection *net.UnixConn, expected PeerIdentity) (*remoteJobBinding, error) {
	peer, watch, err := socketBrokerProcess(connection)
	if err != nil {
		return nil, err
	}
	if expected.PID <= 1 || peer.pid != expected.PID || peer.uid != expected.UID || peer.gid != expected.GID || watch.alive() != nil {
		_ = watch.close()
		return nil, ErrDenied
	}
	return &remoteJobBinding{conn: connection, watch: watch, peer: expected}, nil
}

// Only this fixed installation endpoint may be dialled by the root executor.
// The service-owned private directory contains sockets, NEVER code/scripts or
// a job database for root to open. Authentication still requires the original
// caller's exact kernel PID/UID/GID, so a restarted broker cannot adopt a call.
func dialJobBroker(ctx context.Context, policy installationPolicy, original PeerIdentity) (*remoteJobBinding, error) {
	if ctx == nil || ctx.Err() != nil || os.Getuid() != 0 || os.Geteuid() != 0 || os.Getgid() != 0 || os.Getegid() != 0 ||
		original.PID <= 1 || !policy.peers().authorizes(ctx, Peer{pid: original.PID, uid: original.UID, gid: original.GID}) {
		return nil, ErrDenied
	}
	root, err := unix.Open("/", unix.O_RDONLY|unix.O_CLOEXEC|unix.O_DIRECTORY|unix.O_NOFOLLOW, 0)
	if err != nil {
		return nil, ErrUnavailable
	}
	defer unix.Close(root)
	guard, err := openBrokerEndpoint(root, 0, original.UID, original.GID)
	if err != nil {
		return nil, err
	}
	defer guard.close()
	dial := net.Dialer{Timeout: brokerIOTimeout}
	connection, err := dial.DialContext(ctx, "unix", brokerSocketPath)
	if err != nil {
		return nil, ErrUnavailable
	}
	stream, ok := connection.(*net.UnixConn)
	if !ok || guard.check() != nil {
		_ = connection.Close()
		return nil, ErrUnavailable
	}
	bound, err := remoteBrokerOnConnection(stream, original)
	if err != nil {
		_ = stream.Close()
		return nil, err
	}
	if guard.check() != nil || ctx.Err() != nil {
		_ = bound.Close()
		return nil, ErrUnavailable
	}
	return bound, nil
}

// Fixed four-node tree: / and /run root-owned; the one private broker
// directory and its socket service-owned. Each name is rechecked against its
// pinned parent; no arbitrary path parser or permission repair is provided.
type brokerEndpoint struct {
	nodes                 []*os.File
	stats                 []unix.Stat_t
	names                 []string
	ancestorUID, uid, gid uint32
}

func openBrokerEndpoint(root int, ancestorUID, uid, gid uint32) (*brokerEndpoint, error) {
	return openBrokerEndpointNodes(root, ancestorUID, uid, gid, 4)
}

// The listener pins the installed directory before it creates the socket.
// No path/owner is supplied by a request; shorter trees are private setup seams.
func openBrokerEndpointNodes(root int, ancestorUID, uid, gid uint32, count int) (*brokerEndpoint, error) {
	if count != 3 && count != 4 {
		return nil, ErrUnavailable
	}
	b := &brokerEndpoint{ancestorUID: ancestorUID, uid: uid, gid: gid, names: []string{".", "run", "anas-job-broker", "socket"}}
	parent := root
	for i, name := range b.names[:count] {
		flags := unix.O_RDONLY | unix.O_CLOEXEC | unix.O_NOFOLLOW | unix.O_DIRECTORY
		if i == 3 {
			flags = unix.O_PATH | unix.O_CLOEXEC | unix.O_NOFOLLOW
		}
		fd, err := unix.Openat(parent, name, flags, 0)
		if err != nil {
			_ = b.close()
			return nil, ErrUnavailable
		}
		b.nodes = append(b.nodes, os.NewFile(uintptr(fd), "host-broker-endpoint"))
		var stat unix.Stat_t
		if unix.Fstat(fd, &stat) != nil || !b.valid(i, stat) {
			_ = b.close()
			return nil, ErrUnavailable
		}
		b.stats = append(b.stats, stat)
		parent = fd
	}
	if b.checkNodes(count) != nil {
		_ = b.close()
		return nil, ErrUnavailable
	}
	return b, nil
}

func (b *brokerEndpoint) valid(i int, s unix.Stat_t) bool {
	if s.Mode&07000 != 0 {
		return false
	}
	if i < 2 {
		return s.Uid == b.ancestorUID && s.Mode&unix.S_IFMT == unix.S_IFDIR && s.Mode&0022 == 0
	}
	if s.Uid != b.uid || s.Gid != b.gid {
		return false
	}
	if i == 2 {
		return s.Mode&unix.S_IFMT == unix.S_IFDIR && s.Mode&0777 == 0700
	}
	return s.Mode&unix.S_IFMT == unix.S_IFSOCK && s.Mode&0777 == 0600 && s.Nlink == 1
}

func (b *brokerEndpoint) check() error {
	return b.checkNodes(4)
}

func (b *brokerEndpoint) checkNodes(count int) error {
	if b == nil || (count != 3 && count != 4) || len(b.nodes) != count || len(b.stats) != count {
		return ErrUnavailable
	}
	for i, f := range b.nodes {
		var s, named unix.Stat_t
		if unix.Fstat(int(f.Fd()), &s) != nil || !b.valid(i, s) || !sameInstallationStat(b.stats[i], s, i == 3) {
			return ErrUnavailable
		}
		if i > 0 && (unix.Fstatat(int(b.nodes[i-1].Fd()), b.names[i], &named, unix.AT_SYMLINK_NOFOLLOW) != nil || !sameInstallationStat(b.stats[i], named, i == 3)) {
			return ErrUnavailable
		}
	}
	return nil
}

func (b *brokerEndpoint) close() error {
	failed := false
	for _, f := range b.nodes {
		failed = f.Close() != nil || failed
	}
	b.nodes = nil
	if failed {
		return ErrUnavailable
	}
	return nil
}
