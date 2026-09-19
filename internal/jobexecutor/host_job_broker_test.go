package jobexecutor

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/anas-project/ANAS/internal/actionabi"
	"github.com/anas-project/ANAS/internal/audit"
	"github.com/anas-project/ANAS/internal/consolejobs"
	"github.com/anas-project/ANAS/internal/hostaction"
)

// These fixtures exercise routing/lifetime against the real shared Store.
// Session injection is test-only, NOT kernel/root authentication evidence;
// hostaction's native gate tests those transports independently.
type ownerTestSession struct {
	request    actionabi.Request
	peer       hostaction.PeerIdentity
	connection *net.UnixConn
	callback   func(context.Context) error
	beforeRead func(context.Context) error
	done       chan struct{}
	result     error // Read only after done is closed.
	granted    atomic.Bool
	ended      atomic.Bool
	closed     atomic.Bool
	runs       atomic.Int32
}

func (s *ownerTestSession) Serve(ctx context.Context, release hostaction.ReleaseIdentity, binding hostaction.JobBinding, _ hostaction.AuditJournal) (result error) {
	defer func() {
		s.result = result
		_ = s.connection.Close()
		close(s.done)
	}()
	if s.beforeRead != nil {
		return s.beforeRead(ctx)
	}
	return binding.WithHostInvocation(ctx, s.request, release, s.peer, func(owner context.Context) error {
		s.granted.Store(true)
		s.runs.Add(1)
		if s.callback != nil {
			return s.callback(owner)
		}
		return nil
	})
}

func (s *ownerTestSession) GrantPossible() bool { return s.granted.Load() }
func (s *ownerTestSession) WaitExecutor(ctx context.Context) error {
	for !s.ended.Load() {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Millisecond):
		}
	}
	return nil
}
func (s *ownerTestSession) Close() error {
	if s.granted.Load() && !s.ended.Load() {
		return hostaction.ErrBrokerExecutorRunning
	}
	s.closed.Store(true)
	if s.connection != nil {
		_ = s.connection.Close()
	}
	return nil
}

type ownerTestListener struct {
	incoming chan *net.UnixConn
	closed   atomic.Bool
	bad      atomic.Bool
	badClose bool
}

func (l *ownerTestListener) Accept(ctx context.Context) (*net.UnixConn, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case c := <-l.incoming:
		return c, l.Check()
	}
}
func (l *ownerTestListener) Check() error {
	if l.bad.Load() || l.closed.Load() {
		return hostaction.ErrUnavailable
	}
	return nil
}
func (l *ownerTestListener) Close() error {
	l.closed.Store(true)
	if l.badClose {
		return errors.New("private-listener-path")
	}
	return nil
}

func ownerBrokerFixture(t *testing.T) (*HostJobBroker, *consolejobs.Store, *consolejobs.ExecutionLease, consolejobs.Job, consolejobs.JobCommitObserver) {
	t.Helper()
	store, lease, job, release, observer := hostBindingFixture(t, nil, true)
	journal, err := audit.Open(filepath.Join(t.TempDir(), "audit"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = journal.Close() })
	b, err := NewHostJobBroker(HostJobBrokerOptions{Store: store, Lease: lease, Release: release, Journal: journal,
		Authorize: func(context.Context, consolejobs.Job, hostaction.PeerIdentity) error { return nil }})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := b.Close(); err != nil {
			t.Error(err)
		}
	})
	return b, store, lease, job, observer
}

func ownerBrokerConnection(t *testing.T) *net.UnixConn {
	t.Helper()
	dir, err := os.MkdirTemp("", "anas-owner-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: filepath.Join(dir, "s"), Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
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
	return server
}

type ownerBrokerRun struct {
	done chan struct{}
	err  error
}

func startOwnerBroker(t *testing.T, b *HostJobBroker, l *ownerTestListener, accept hostBrokerAcceptor) *ownerBrokerRun {
	t.Helper()
	run := &ownerBrokerRun{done: make(chan struct{})}
	go func() {
		run.err = b.run(context.Background(), func(context.Context) (hostBrokerListener, error) { return l, nil }, accept)
		close(run.done)
	}()
	select {
	case <-b.Ready():
	case <-run.done:
		t.Fatal("startup failed", run.err)
	case <-time.After(3 * time.Second):
		t.Fatal("listener did not become ready")
	}
	t.Cleanup(func() { b.Stop(); waitOwnerBroker(t, run.done) })
	return run
}

func waitOwnerBroker(t *testing.T, done <-chan struct{}) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("owner worker did not stop")
	}
}

func TestHostBrokerRoutesOnlyRegisteredRunningJobs(t *testing.T) {
	b, store, lease, job, _ := ownerBrokerFixture(t)
	requestContext, disconnect := context.WithCancel(context.Background())
	bound, err := b.Register(requestContext, job.ID)
	disconnect() // Registration does not make request lifetime execution lifetime.
	if err != nil {
		t.Fatal(err)
	}
	if _, err := b.Register(context.Background(), job.ID); err == nil {
		t.Fatal("duplicate registration replaced the binding")
	}
	l := &ownerTestListener{incoming: make(chan *net.UnixConn, 2)}
	sessions := make(chan *ownerTestSession, 2)
	run := startOwnerBroker(t, b, l, func(_ context.Context, c *net.UnixConn, _ hostaction.AuditJournal) (hostBrokerSession, error) {
		s := <-sessions
		s.connection = c
		return s, nil
	})
	unknown := &ownerTestSession{request: hostWire(job), peer: hostPeer(), done: make(chan struct{})}
	unknown.request.JobID = "not-registered"
	sessions <- unknown
	l.incoming <- ownerBrokerConnection(t)
	waitOwnerBroker(t, unknown.done)
	if unknown.result == nil || unknown.runs.Load() != 0 {
		t.Fatal("a socket claim created a job/binding")
	}
	valid := &ownerTestSession{request: hostWire(job), peer: hostPeer(), done: make(chan struct{})}
	t.Cleanup(func() { valid.ended.Store(true) })
	sessions <- valid
	l.incoming <- ownerBrokerConnection(t)
	waitOwnerBroker(t, valid.done)
	if valid.result != nil || valid.runs.Load() != 1 {
		t.Fatal("registered job not dispatched", valid.result)
	}
	b.Stop()
	waitOwnerBroker(t, run.done)
	if run.err != nil || !l.closed.Load() {
		t.Fatal("normal owner shutdown failed", run.err)
	}
	if err := b.Close(); !errors.Is(err, hostaction.ErrBrokerExecutorRunning) || valid.closed.Load() {
		t.Fatal("listener shutdown discarded a live executor", err)
	}
	if err := lease.Close(); !errors.Is(err, consolejobs.ErrExecutionRetained) {
		t.Fatal("listener shutdown released the job execution lease", err)
	}
	if b.Retire(context.Background(), job.ID) == nil {
		t.Fatal("running job could be retired and rebound")
	}
	valid.ended.Store(true)
	if err := bound.WaitBrokerExecutor(context.Background()); err != nil {
		t.Fatal("exit pin was discarded by the accept worker", err)
	}
	if err := b.Close(); err != nil {
		t.Fatal(err)
	}
	actual, err := store.Get(context.Background(), job.ID)
	if err != nil || actual.Status != consolejobs.StatusRunning || actual.Action.LastSeq != 0 || actual.Action.Outcome != "" {
		t.Fatal("broker fabricated completion or job events", err)
	}
}

func TestHostBrokerStartupFailureNeverSignalsReadinessOrRetries(t *testing.T) {
	b, _, _, job, _ := ownerBrokerFixture(t)
	err := b.run(context.Background(), func(context.Context) (hostBrokerListener, error) {
		return nil, errors.New("private-installation-path")
	}, func(context.Context, *net.UnixConn, hostaction.AuditJournal) (hostBrokerSession, error) {
		t.Fatal("failed listener admitted a connection")
		return nil, nil
	})
	if !errors.Is(err, hostaction.ErrUnavailable) {
		t.Fatal("startup error was exposed or ignored", err)
	}
	select {
	case <-b.Ready():
		t.Fatal("startup failure signaled readiness")
	default:
	}
	if _, err := b.Register(context.Background(), job.ID); err == nil {
		t.Fatal("failed owner admitted work")
	}
	if err := b.Run(context.Background()); err == nil {
		t.Fatal("one-shot owner restarted itself")
	}
}

func TestHostBrokerRegistrationCapacityDoesNotEvict(t *testing.T) {
	b, _, _, job, _ := ownerBrokerFixture(t)
	bound, err := b.Register(context.Background(), job.ID)
	if err != nil {
		t.Fatal(err)
	}
	// Fill the index without creating fake runnable jobs. Capacity rejection
	// must precede any attempt to interpret an unregistered id as job authority.
	for i := 1; i < maxHostBrokerRegistrations; i++ {
		b.bindings[string(rune('a'+i))] = &HostJobBinding{}
	}
	if _, err := b.Register(context.Background(), "overflow"); err == nil || len(b.bindings) != maxHostBrokerRegistrations || b.bindings[job.ID] != bound {
		t.Fatal("capacity handling evicted/replaced a retained job")
	}
}

func TestHostBrokerConcurrentClaimsCannotReplaceSession(t *testing.T) {
	b, _, _, job, _ := ownerBrokerFixture(t)
	if _, err := b.Register(context.Background(), job.ID); err != nil {
		t.Fatal(err)
	}
	var ran atomic.Int32
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			session := &ownerTestSession{}
			session.ended.Store(true)
			route := &hostBrokerRoute{broker: b, session: session, check: func() error { return nil }}
			_ = route.WithHostInvocation(context.Background(), hostWire(job), b.options.Release, hostPeer(), func(context.Context) error { ran.Add(1); return nil })
		}()
	}
	wg.Wait()
	if ran.Load() != 1 {
		t.Fatal("multiple sessions executed the same registered job", ran.Load())
	}
}

func TestHostBrokerPostGrantFailureStopsAdmissionButRetainsOwnership(t *testing.T) {
	b, _, lease, job, _ := ownerBrokerFixture(t)
	if _, err := b.Register(context.Background(), job.ID); err != nil {
		t.Fatal(err)
	}
	s := &ownerTestSession{request: hostWire(job), peer: hostPeer(), done: make(chan struct{}), callback: func(context.Context) error { return hostaction.ErrUnavailable }}
	l := &ownerTestListener{incoming: make(chan *net.UnixConn, 1)}
	run := startOwnerBroker(t, b, l, func(_ context.Context, c *net.UnixConn, _ hostaction.AuditJournal) (hostBrokerSession, error) {
		s.connection = c
		return s, nil
	})
	t.Cleanup(func() { s.ended.Store(true) })
	l.incoming <- ownerBrokerConnection(t)
	waitOwnerBroker(t, run.done)
	if run.err == nil || !s.granted.Load() || s.closed.Load() {
		t.Fatal("uncertain execution did not stop admission with its pin intact", run.err)
	}
	if _, err := b.Register(context.Background(), job.ID); err == nil {
		t.Fatal("failed broker admitted another invocation")
	}
	if err := lease.Close(); !errors.Is(err, consolejobs.ErrExecutionRetained) {
		t.Fatal("uncertain execution lost ownership", err)
	}
}

func TestHostBrokerBoundsConnectionsAndStopsIdleReaders(t *testing.T) {
	b, _, _, _, _ := ownerBrokerFixture(t)
	l := &ownerTestListener{incoming: make(chan *net.UnixConn, 16)}
	var accepted atomic.Int32
	full := make(chan struct{})
	run := startOwnerBroker(t, b, l, func(_ context.Context, c *net.UnixConn, _ hostaction.AuditJournal) (hostBrokerSession, error) {
		s := &ownerTestSession{connection: c, done: make(chan struct{}), beforeRead: func(ctx context.Context) error {
			if accepted.Add(1) == maxHostBrokerConnections {
				close(full)
			}
			<-ctx.Done()
			return hostaction.ErrRequest
		}}
		return s, nil
	})
	for range maxHostBrokerConnections + 2 {
		l.incoming <- ownerBrokerConnection(t)
	}
	waitOwnerBroker(t, full)
	time.Sleep(30 * time.Millisecond)
	if accepted.Load() != maxHostBrokerConnections {
		t.Fatal("connection limit exceeded", accepted.Load())
	}
	b.Stop()
	waitOwnerBroker(t, run.done)
	if run.err != nil {
		t.Fatal("cancelled ungranted readers poisoned the owner", run.err)
	}
}

func TestHostBrokerAuditAndCleanupFailuresAreNotHidden(t *testing.T) {
	for _, kind := range []string{"audit", "cleanup"} {
		t.Run(kind, func(t *testing.T) {
			b, _, _, _, _ := ownerBrokerFixture(t)
			l := &ownerTestListener{incoming: make(chan *net.UnixConn, 1), badClose: kind == "cleanup"}
			run := startOwnerBroker(t, b, l, func(context.Context, *net.UnixConn, hostaction.AuditJournal) (hostBrokerSession, error) {
				return nil, hostaction.ErrAudit
			})
			if kind == "audit" {
				l.incoming <- ownerBrokerConnection(t)
			} else {
				b.Stop()
			}
			waitOwnerBroker(t, run.done)
			if !errors.Is(run.err, hostaction.ErrUnavailable) {
				t.Fatal("audit/cleanup failure reported clean shutdown", run.err)
			}
		})
	}
}

func TestHostBrokerRetirementNeedsTerminalAndRemoteExit(t *testing.T) {
	b, store, _, job, observer := ownerBrokerFixture(t)
	bound, err := b.Register(context.Background(), job.ID)
	if err != nil {
		t.Fatal(err)
	}
	s := &ownerTestSession{}
	s.granted.Store(true)
	t.Cleanup(func() { s.ended.Store(true) })
	if err := bound.attachBroker(s); err != nil {
		t.Fatal(err)
	}
	// Synthetic supervisor failure: no successful host execution is claimed.
	_, err = store.CompleteActionObserved(context.Background(), b.options.Lease, actionabi.Event{ABI: actionabi.Version,
		JobID: job.ID, InvocationID: job.Action.InvocationID, Type: "error", Error: &actionabi.Failure{
			Outcome: actionabi.Unknown, Code: "execution_unconfirmed", Message: "Fixture execution unconfirmed"}}, observer)
	if err != nil {
		t.Fatal(err)
	}
	if err := b.Retire(context.Background(), job.ID); !errors.Is(err, hostaction.ErrBrokerExecutorRunning) {
		t.Fatal("terminal state substituted for process exit", err)
	}
	s.ended.Store(true)
	if err := b.Retire(context.Background(), job.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Register(context.Background(), job.ID); err == nil {
		t.Fatal("retired terminal job was rebound")
	}
}

func TestHostBrokerValidatesRequestAfterRegistryLookup(t *testing.T) {
	for _, kind := range []string{"invocation", "release", "parameters", "permission", "drift"} {
		t.Run(kind, func(t *testing.T) {
			b, _, _, job, _ := ownerBrokerFixture(t)
			if kind == "permission" {
				b.options.Authorize = func(context.Context, consolejobs.Job, hostaction.PeerIdentity) error { return hostaction.ErrDenied }
			}
			if _, err := b.Register(context.Background(), job.ID); err != nil {
				t.Fatal(err)
			}
			r, release := hostWire(job), b.options.Release
			s := &ownerTestSession{}
			s.ended.Store(true)
			route := &hostBrokerRoute{broker: b, session: s, check: func() error {
				if kind == "drift" {
					return hostaction.ErrUnavailable
				}
				return nil
			}}
			switch kind {
			case "invocation":
				r.InvocationID = "other"
			case "release":
				release.Version = "9.9.9"
			case "parameters":
				r.Parameters = json.RawMessage(`{"command":"denied"}`)
			}
			if err := route.WithHostInvocation(context.Background(), r, release, hostPeer(), func(context.Context) error {
				t.Fatal("invalid request executed")
				return nil
			}); err == nil {
				t.Fatal("invalid request accepted")
			}
		})
	}
}
