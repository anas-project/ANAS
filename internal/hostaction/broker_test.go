package hostaction

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/anas-project/ANAS/internal/actionabi"
)

// Socket protocols are tested on both platforms. These private process fakes
// supply NO kernel-authentication evidence; native tests exercise that boundary.
type brokerTestProcess struct{ ended, closed atomic.Bool }

func (p *brokerTestProcess) alive() error {
	if p.ended.Load() || p.closed.Load() {
		return ErrUnavailable
	}
	return nil
}
func (p *brokerTestProcess) exited() (bool, error) {
	if p.closed.Load() {
		return false, ErrUnavailable
	}
	return p.ended.Load(), nil
}
func (p *brokerTestProcess) close() error { p.closed.Store(true); return nil }
func (p *brokerTestProcess) waitExited(ctx context.Context) error {
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if p.ended.Load() {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Millisecond):
		}
	}
}

func brokerTestPair(t *testing.T) (*net.UnixConn, *net.UnixConn) {
	t.Helper()
	dir, err := os.MkdirTemp("", "anas-broker-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: filepath.Join(dir, "s"), Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	client, err := net.DialUnix("unix", nil, listener.Addr().(*net.UnixAddr))
	if err != nil {
		t.Fatal(err)
	}
	server, err := listener.AcceptUnix()
	if err != nil {
		_ = client.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close(); _ = server.Close() })
	return client, server
}

func brokerClaimFixture() brokerClaim {
	return brokerClaim{brokerSchema, strings.Repeat("a", 32), installedRelease(), brokerIdentity{42, 1001, 1002}, testRequest()}
}

func brokerServerFixture(t *testing.T) (*remoteJobBinding, *BrokerSession, *brokerTestProcess) {
	t.Helper()
	client, server := brokerTestPair(t)
	peer := brokerClaimFixture().Peer.peer()
	process := &brokerTestProcess{}
	remote := &remoteJobBinding{conn: client, peer: peer, watch: &brokerTestProcess{}}
	session := &BrokerSession{conn: server, process: process, self: peer, peer: Peer{pid: 99, uid: 0, gid: 0, verified: true}}
	t.Cleanup(func() { process.ended.Store(true); _ = session.Close(); _ = remote.Close() })
	return remote, session, process
}

func brokerRunBinding() JobBinding {
	return bindingFixture(func(ctx context.Context, _ actionabi.Request, _ ReleaseIdentity, _ PeerIdentity, run func(context.Context) error) error {
		return run(ctx)
	})
}

func TestBrokerCanonicalFramesRejectUntrustedFields(t *testing.T) {
	claim := brokerClaimFixture()
	var wire bytes.Buffer
	if err := writeBrokerFrame(&wire, claim); err != nil {
		t.Fatal(err)
	}
	var parsed brokerClaim
	if err := readBrokerFrame(bytes.NewReader(wire.Bytes()), &parsed); err != nil || !validBrokerClaim(parsed) {
		t.Fatal(err)
	}
	valid := wire.String()
	for name, frame := range map[string]string{
		"unknown":   strings.Replace(valid, `"schema":`, `"password":"private-marker","schema":`, 1),
		"duplicate": strings.Replace(valid, `"schema":`, `"nonce":"bad","schema":`, 1),
		"escaped":   strings.Replace(valid, `"nonce":`, `"non\u0063e":`, 1),
		"case":      strings.Replace(valid, `"nonce":`, `"Nonce":`, 1),
		"null":      strings.Replace(valid, `"uid":1001`, `"uid":null`, 1),
		"CRLF":      strings.TrimSuffix(valid, "\n") + "\r\n",
		"no LF":     strings.TrimSuffix(valid, "\n"),
		"oversize":  strings.Repeat(" ", maxBrokerFrame) + valid,
		"empty":     "\n",
	} {
		t.Run(name, func(t *testing.T) {
			var got brokerClaim
			err := readBrokerFrame(strings.NewReader(frame), &got)
			if err == nil || strings.Contains(err.Error(), "private-marker") {
				t.Fatal("invalid frame accepted or exposed", err)
			}
		})
	}
	for _, mutate := range []func(*brokerClaim){
		func(c *brokerClaim) { c.Peer.UID = 0 }, func(c *brokerClaim) { c.Peer.PID = 0 },
		func(c *brokerClaim) { c.Release.Version = "dev" }, func(c *brokerClaim) { c.Nonce = strings.Repeat("A", 32) },
		func(c *brokerClaim) { c.Request.Action = "incus.install" }, func(c *brokerClaim) { c.Request.Parameters = json.RawMessage(`{"command":"private-marker"}`) },
	} {
		c := brokerClaimFixture()
		mutate(&c)
		if validBrokerClaim(c) {
			t.Fatal("invalid binding claim accepted")
		}
	}
}

func TestBrokerHandshakeRetainsProcessBeyondSocketCompletion(t *testing.T) {
	remote, session, process := brokerServerFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	journal := &memoryAudit{}
	done := make(chan error, 1)
	go func() { done <- session.Serve(ctx, installedRelease(), brokerRunBinding(), journal) }()
	calls := 0
	err := remote.WithHostInvocation(ctx, testRequest(), installedRelease(), remote.peer, func(context.Context) error { calls++; return nil })
	if err != nil || <-done != nil || calls != 1 {
		t.Fatal("valid exchange failed", err, calls)
	}
	if err := session.Close(); !errors.Is(err, ErrBrokerExecutorRunning) {
		t.Fatal("handshake was mistaken for exit", err)
	}
	if process.closed.Load() {
		t.Fatal("live process handle discarded")
	}
	if err := remote.WithHostInvocation(ctx, testRequest(), installedRelease(), remote.peer, func(context.Context) error { t.Fatal("replayed"); return nil }); err == nil {
		t.Fatal("replay accepted")
	}
	process.ended.Store(true)
	if err := session.WaitExecutor(ctx); err != nil {
		t.Fatal(err)
	}
	if err := session.Close(); err != nil || !process.closed.Load() {
		t.Fatal("terminated handle retained", err)
	}
}

func TestBrokerDeniesChangedIdentityOrAuthorityBeforeCallback(t *testing.T) {
	for _, name := range []string{"release", "peer", "authority"} {
		t.Run(name, func(t *testing.T) {
			remote, session, _ := brokerServerFixture(t)
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			release := installedRelease()
			binding := brokerRunBinding()
			if name == "release" {
				release.Commit = strings.Repeat("b", 40)
			}
			if name == "peer" {
				session.self.PID++
			}
			if name == "authority" {
				binding = bindingFixture(func(context.Context, actionabi.Request, ReleaseIdentity, PeerIdentity, func(context.Context) error) error {
					return errors.New("private-marker")
				})
			}
			journal := &memoryAudit{}
			done := make(chan error, 1)
			go func() { done <- session.Serve(ctx, release, binding, journal) }()
			err := remote.WithHostInvocation(ctx, testRequest(), installedRelease(), remote.peer, func(context.Context) error { t.Fatal("denied handler executed"); return nil })
			serverErr := <-done
			if err == nil || serverErr == nil || strings.Contains(serverErr.Error(), "private-marker") {
				t.Fatal(err, serverErr)
			}
			if len(journal.events) != 1 || journal.events[0].Type != "host_action_rejected" {
				t.Fatal("missing broker rejection audit")
			}
			if session.granted {
				t.Fatal("denial granted execution")
			}
		})
	}
}

func TestBrokerPostGrantFailureRetainsExecutor(t *testing.T) {
	for _, name := range []string{"callback failure", "authority changed", "disconnect", "owner exited"} {
		t.Run(name, func(t *testing.T) {
			remote, session, process := brokerServerFixture(t)
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			binding := brokerRunBinding()
			if name == "authority changed" {
				binding = bindingFixture(func(ctx context.Context, _ actionabi.Request, _ ReleaseIdentity, _ PeerIdentity, run func(context.Context) error) error {
					if err := run(ctx); err != nil {
						return err
					}
					return ErrDenied
				})
			}
			done := make(chan error, 1)
			go func() { done <- session.Serve(ctx, installedRelease(), binding, &memoryAudit{}) }()
			err := remote.WithHostInvocation(ctx, testRequest(), installedRelease(), remote.peer, func(context.Context) error {
				if name == "callback failure" {
					return errors.New("private-marker")
				}
				if name == "disconnect" {
					_ = remote.conn.CloseWrite()
				}
				if name == "owner exited" {
					remote.watch.(*brokerTestProcess).ended.Store(true)
				}
				return nil
			})
			_ = remote.Close() // End input after a callback/transport failure.
			if err == nil || <-done == nil {
				t.Fatal("uncertain exchange accepted", err)
			}
			if !session.granted || process.closed.Load() || !errors.Is(session.Close(), ErrBrokerExecutorRunning) {
				t.Fatal("uncertain grant lost its process pin")
			}
		})
	}
}

func TestBrokerRejectsTrailingFramesBeforeValidating(t *testing.T) {
	for _, tail := range []string{"extra", "wrong nonce"} {
		t.Run(tail, func(t *testing.T) {
			remote, session, _ := brokerServerFixture(t)
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			done := make(chan error, 1)
			go func() { done <- session.Serve(ctx, installedRelease(), brokerRunBinding(), &memoryAudit{}) }()
			c := brokerClaimFixture()
			if err := writeBrokerFrame(remote.conn, c); err != nil {
				t.Fatal(err)
			}
			if err := readBrokerStep(ctx, remote.conn, c.Nonce, "bound"); err != nil {
				t.Fatal(err)
			}
			nonce := c.Nonce
			if tail == "wrong nonce" {
				nonce = strings.Repeat("b", 32)
			}
			_ = writeBrokerStep(ctx, remote.conn, nonce, "finished")
			if tail == "extra" {
				_ = writeBrokerFrame(remote.conn, brokerStep{brokerSchema, nonce, "finished"})
			}
			_ = remote.conn.CloseWrite()
			if err := <-done; err == nil {
				t.Fatal("trailing/out-of-session response accepted")
			}
		})
	}
}

func TestBrokerReadCancellationIsBounded(t *testing.T) {
	_, session, _ := brokerServerFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- session.Serve(ctx, installedRelease(), brokerRunBinding(), &memoryAudit{}) }()
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("cancelled read succeeded")
		}
	case <-time.After(time.Second):
		t.Fatal("cancelled read hung")
	}
}

func TestBrokerRequiresOriginalCallerAliveAcrossSecondConnection(t *testing.T) {
	for _, when := range []string{"already dead", "before callback", "during callback", "after callback", "still alive"} {
		t.Run(when, func(t *testing.T) {
			original := &brokerTestProcess{}
			if when == "already dead" {
				original.ended.Store(true)
			}
			calls := 0
			second := bindingFixture(func(ctx context.Context, _ actionabi.Request, _ ReleaseIdentity, _ PeerIdentity, run func(context.Context) error) error {
				// The second peer's claimed numeric identity stays identical;
				// only the first socket's handle can disambiguate reuse.
				if when == "before callback" {
					original.ended.Store(true)
				}
				err := run(ctx)
				if when == "after callback" {
					original.ended.Store(true)
				}
				return err
			})
			err := invokeOriginBound(context.Background(), testRequest(), installedRelease(), brokerClaimFixture().Peer.peer(), original, second, func(context.Context) error {
				calls++
				if when == "during callback" {
					original.ended.Store(true)
				}
				return nil
			})
			if (err == nil) != (when == "still alive") {
				t.Fatal("lost original process identity", err)
			}
			if (when == "already dead" || when == "before callback") && calls != 0 {
				t.Fatal("replacement process authorized original work")
			}
		})
	}
}
