//go:build linux

package hostaction

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/anas-project/ANAS/internal/actionabi"
	"golang.org/x/sys/unix"
)

func activationFixture(t *testing.T) (*Activation, *net.UnixConn, string) {
	t.Helper()
	withTestPeerVerifier(t)
	root, err := os.MkdirTemp("", "anas-activation-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(root) })
	for _, dir := range []string{"etc/anas", "run/anas"} {
		if err := os.MkdirAll(filepath.Join(root, dir), 0700); err != nil {
			t.Fatal(err)
		}
	}
	p := installedPolicy()
	p.SocketGID = uint32(os.Getegid())
	body, _ := json.Marshal(p)
	if err := os.WriteFile(filepath.Join(root, "etc/anas/hostd.json"), body, 0600); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "run/anas/hostd.sock")
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { listener.Close() })
	if err := os.Chmod(path, 0600); err != nil {
		t.Fatal(err)
	}
	client, err := net.DialUnix("unix", nil, &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { client.Close() })
	server, err := listener.AcceptUnix()
	if err != nil {
		t.Fatal(err)
	}
	file, err := server.File()
	server.Close()
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	rootFile, err := os.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer rootFile.Close()
	a, err := openActivationAt(context.Background(), int(rootFile.Fd()), file, installedRelease(), uint32(os.Geteuid()), installationPath, activationSocketPath, path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { a.Close() })
	return a, client, root
}

func withTestPeerVerifier(t *testing.T) {
	t.Helper()
	old := verifyPeerSystemdUnit
	verifyPeerSystemdUnit = func(ctx context.Context, peer Peer, unit string) error {
		if ctx == nil || ctx.Err() != nil || peer.pid <= 1 || unit != "anasd.service" {
			return ErrUnavailable
		}
		return nil
	}
	t.Cleanup(func() { verifyPeerSystemdUnit = old })
}

func passBinding() JobBinding {
	return bindingFixture(func(ctx context.Context, _ actionabi.Request, _ ReleaseIdentity, _ PeerIdentity, run func(context.Context) error) error {
		return run(ctx)
	})
}

func writeActivationRequest(t *testing.T, client *net.UnixConn) {
	t.Helper()
	body, err := actionabi.EncodeRequest(testRequest())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.Write(body); err != nil {
		t.Fatal(err)
	}
	if err := client.CloseWrite(); err != nil {
		t.Fatal(err)
	}
}

func TestActivationServesOneBoundAuditedRequest(t *testing.T) {
	a, client, _ := activationFixture(t)
	writeActivationRequest(t, client)
	journal := &memoryAudit{}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := a.Serve(ctx, journal, passBinding()); err != nil {
		t.Fatal(err)
	}
	if err := client.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(client)
	if err != nil {
		t.Fatal(err)
	}
	event, err := actionabi.DecodeExecutorEvent(body)
	if err != nil || event.JobID != "job-host" || event.InvocationID != "call-host" || event.Seq != 0 {
		t.Fatal(event, err)
	}
	if len(journal.events) != 2 || journal.events[0].Type != "host_action_started" || journal.events[1].Type != "host_action_completed" {
		t.Fatal("missing action audit pair")
	}
	if err := a.Serve(ctx, journal, passBinding()); err == nil {
		t.Fatal("connection handled a second request")
	}
}

func TestActivationDisconnectDoesNotCancelExecutionAudit(t *testing.T) {
	a, client, _ := activationFixture(t)
	writeActivationRequest(t, client)
	journal := &memoryAudit{}
	bound := false
	binding := bindingFixture(func(ctx context.Context, _ actionabi.Request, _ ReleaseIdentity, _ PeerIdentity, run func(context.Context) error) error {
		bound = true
		client.Close() // Subscriber disappears AFTER complete request admission.
		if ctx.Err() != nil {
			t.Fatal("transport disconnect cancelled owner")
		}
		return run(ctx)
	})
	if err := a.Serve(context.Background(), journal, binding); err == nil {
		t.Fatal("closed subscriber unexpectedly received response")
	}
	if !bound || len(journal.events) != 2 || journal.events[1].Type != "host_action_completed" {
		t.Fatal("disconnect suppressed completion audit")
	}
}

type failingCloseGuard struct{ activationGuard }

func (g failingCloseGuard) close() error {
	_ = g.activationGuard.close()
	return ErrUnavailable
}

func TestActivationCleanupFailureCannotReportCleanCompletion(t *testing.T) {
	a, client, _ := activationFixture(t)
	a.guard = failingCloseGuard{a.guard}
	writeActivationRequest(t, client)
	journal := &memoryAudit{}
	if err := a.Serve(context.Background(), journal, passBinding()); !errors.Is(err, ErrUnavailable) {
		t.Fatal("cleanup failure reported clean process completion", err)
	}
	if len(journal.events) != 2 || journal.events[1].Type != "host_action_completed" {
		t.Fatal("fixture did not exercise post-execution cleanup")
	}
	if !a.closed {
		t.Fatal("activation descriptors were not retired")
	}
}

func TestActivationPinsFileAndSocketOwnership(t *testing.T) {
	for _, name := range []string{"policy mode", "policy contents", "policy symlink", "policy hardlink", "ancestor replacement", "socket mode", "socket replacement"} {
		t.Run(name, func(t *testing.T) {
			a, client, root := activationFixture(t)
			config := filepath.Join(root, "etc/anas/hostd.json")
			socket := filepath.Join(root, "run/anas/hostd.sock")
			var err error
			switch name {
			case "policy mode":
				err = os.Chmod(config, 0644)
			case "policy contents":
				err = os.WriteFile(config, []byte(`{}`), 0600)
			case "policy symlink":
				if err = os.Rename(config, config+".old"); err == nil {
					err = os.Symlink(config+".old", config)
				}
			case "policy hardlink":
				err = os.Link(config, config+".link")
			case "ancestor replacement":
				err = os.Rename(filepath.Join(root, "etc"), filepath.Join(root, "etc-old"))
			case "socket mode":
				err = os.Chmod(socket, 0666)
			case "socket replacement":
				err = os.Rename(socket, socket+".old")
			}
			if err != nil {
				t.Fatal(err)
			}
			writeActivationRequest(t, client)
			journal := &memoryAudit{}
			if err := a.Serve(context.Background(), journal, passBinding()); err == nil {
				t.Fatal("drift accepted")
			}
			for _, e := range journal.events {
				if e.Type == "host_action_started" {
					t.Fatal("handler ran after installation drift")
				}
			}
		})
	}
}

func TestActivationRejectsWrongDescriptorKinds(t *testing.T) {
	client, server := testUnixPair(t)
	file, err := server.File()
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	path := server.LocalAddr().String()
	if err := validateActivatedFD(int(file.Fd()), path); err != nil {
		t.Fatal(err)
	}
	if err := validateActivatedFD(int(file.Fd()), path+"-other"); err == nil {
		t.Fatal("wrong name accepted")
	}
	clientFile, err := client.File()
	if err != nil {
		t.Fatal(err)
	}
	defer clientFile.Close()
	if validateActivatedFD(int(clientFile.Fd()), path) == nil {
		t.Fatal("outbound/unnamed socket accepted")
	}
	regular, err := os.CreateTemp(t.TempDir(), "file")
	if err != nil {
		t.Fatal(err)
	}
	defer regular.Close()
	if validateActivatedFD(int(regular.Fd()), path) == nil {
		t.Fatal("regular file accepted")
	}
	pair, err := unix.Socketpair(unix.AF_UNIX, unix.SOCK_STREAM|unix.SOCK_CLOEXEC, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer unix.Close(pair[0])
	defer unix.Close(pair[1])
	if validateActivatedFD(pair[0], path) == nil {
		t.Fatal("socketpair accepted")
	}
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: filepath.Join(t.TempDir(), "s"), Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	listenerFile, err := listener.File()
	if err != nil {
		t.Fatal(err)
	}
	defer listenerFile.Close()
	if validateActivatedFD(int(listenerFile.Fd()), listener.Addr().String()) == nil {
		t.Fatal("listening fd accepted")
	}
}

func TestRejectedActivationInputIsAuditedWithoutPayload(t *testing.T) {
	withTestPeerVerifier(t)
	policy := installedPolicy().peers()
	for _, bad := range []string{`{"password":"private-marker"}`, "{}\n{}\n"} {
		client, server := testUnixPair(t)
		client.Write([]byte(bad))
		client.CloseWrite()
		journal := &memoryAudit{}
		if _, err := ReceiveObserved(context.Background(), server, policy, journal); err == nil {
			t.Fatal("invalid input accepted")
		}
		body, _ := json.Marshal(journal.events)
		if len(journal.events) != 1 || journal.events[0].Type != "host_action_rejected" || strings.Contains(string(body), "private-marker") {
			t.Fatal("unsafe or missing rejection audit")
		}
	}
	_, server := testUnixPair(t)
	journal := &memoryAudit{}
	_, err := ReceiveObserved(context.Background(), server, PeerPolicy{ServiceMode: serviceModeSystemdRoot, ServiceUnit: "other.service"}, journal)
	if !errors.Is(err, ErrDenied) || len(journal.events) != 1 || journal.events[0].Details["peer_uid"] != uint32(os.Geteuid()) {
		t.Fatal("denial lacked kernel attribution", err)
	}
}
