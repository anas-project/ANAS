package jobexecutor

import (
	"context"
	"errors"
	"net"
	"sync"

	"github.com/anas-project/ANAS/internal/actionabi"
	"github.com/anas-project/ANAS/internal/consolejobs"
	"github.com/anas-project/ANAS/internal/hostaction"
)

const (
	maxHostBrokerRegistrations = 32
	maxHostBrokerConnections   = 8
)

type HostJobBrokerOptions struct {
	Store     *consolejobs.Store
	Lease     *consolejobs.ExecutionLease
	Release   hostaction.ReleaseIdentity
	Journal   hostaction.AuditJournal
	Authorize HostJobAuthorizer
}

type hostBrokerListener interface {
	Accept(context.Context) (*net.UnixConn, error)
	Check() error
	Close() error
}

type hostBrokerAcceptor func(context.Context, *net.UnixConn, hostaction.AuditJournal) (hostBrokerSession, error)

// HostJobBroker routes kernel-authenticated connections to previously bound
// running jobs in ONE existing Store/ExecutionLease. It is neither a job queue
// nor a second executor registry: the only handler remains compiled incus.status.
// Register belongs to the trusted supervisor BEFORE it initiates activation;
// socket input cannot create, start, register or recover a job.
//
// Run must share that supervisor's installed service process, not an HTTP subscription.
// No service/UID migration or root binary is implicitly installed by this API.
type HostJobBroker struct {
	mu       sync.Mutex
	options  HostJobBrokerOptions
	bindings map[string]*HostJobBinding
	orphans  []hostBrokerSession
	ready    chan struct{}
	started  bool
	finished bool
	stopped  bool
	closed   bool
	failure  error
	stop     context.CancelFunc
}

func NewHostJobBroker(options HostJobBrokerOptions) (*HostJobBroker, error) {
	if options.Store == nil || options.Lease == nil || options.Journal == nil || options.Authorize == nil || options.Release.Validate() != nil {
		return nil, hostaction.ErrUnavailable
	}
	return &HostJobBroker{options: options, bindings: make(map[string]*HostJobBinding), ready: make(chan struct{})}, nil
}

// Ready closes only when the fixed listener has actually opened and passed
// validation. Startup failure is returned by Run, never represented as readiness.
func (b *HostJobBroker) Ready() <-chan struct{} {
	if b == nil {
		return nil
	}
	return b.ready
}

func (b *HostJobBroker) Register(ctx context.Context, jobID string) (*HostJobBinding, error) {
	if b == nil || ctx == nil || jobID == "" {
		return nil, hostaction.ErrUnavailable
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.stopped || b.closed || b.failure != nil || b.bindings == nil || b.options.Store == nil ||
		b.bindings[jobID] != nil || len(b.bindings) >= maxHostBrokerRegistrations {
		return nil, hostaction.ErrDenied
	}
	bound, err := NewHostJobBinding(ctx, b.options.Store, b.options.Lease, jobID, b.options.Release, b.options.Authorize)
	if err != nil {
		return nil, err
	}
	if ctx.Err() != nil {
		_ = bound.Close()
		return nil, ctx.Err()
	}
	b.bindings[jobID] = bound
	return bound, nil
}

// Retire requires durable terminal state AND confirmed remote cleanup. Merely
// closing a socket/binding never frees a running job for re-registration.
// No TTL, eviction or automatic reconstruction of a registration is provided.
func (b *HostJobBroker) Retire(ctx context.Context, jobID string) error {
	if b == nil || ctx == nil {
		return hostaction.ErrUnavailable
	}
	b.mu.Lock()
	bound := b.bindings[jobID]
	b.mu.Unlock()
	if bound == nil {
		return hostaction.ErrDenied
	}
	job, err := b.options.Store.Get(ctx, jobID)
	if err != nil || !moduleActionTerminal(job.Status) || job.Action == nil || job.Action.InvocationID != bound.job.Action.InvocationID {
		return hostaction.ErrDenied
	}
	if err := bound.Close(); err != nil {
		return err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.bindings[jobID] != bound {
		return hostaction.ErrDenied
	}
	delete(b.bindings, jobID)
	return nil
}

func (b *HostJobBroker) Run(owner context.Context) error {
	return b.run(owner, func(ctx context.Context) (hostBrokerListener, error) {
		return hostaction.OpenJobBrokerListener(ctx)
	}, func(ctx context.Context, c *net.UnixConn, journal hostaction.AuditJournal) (hostBrokerSession, error) {
		return hostaction.AcceptJobBrokerObserved(ctx, c, journal)
	})
}

// Private seams allow the lifecycle to be tested without root, /run writes,
// a fake production authenticator, or a second on-disk job store.
func (b *HostJobBroker) run(owner context.Context, open func(context.Context) (hostBrokerListener, error), accept hostBrokerAcceptor) (result error) {
	if b == nil || owner == nil || open == nil || accept == nil {
		return hostaction.ErrUnavailable
	}
	b.mu.Lock()
	if b.started || b.stopped || b.closed || b.options.Lease == nil || b.ready == nil {
		b.mu.Unlock()
		return hostaction.ErrUnavailable
	}
	ctx, cancel := context.WithCancel(owner)
	b.started, b.stop = true, cancel
	b.mu.Unlock()
	defer func() {
		cancel()
		b.mu.Lock()
		b.finished, b.stopped = true, true
		if result != nil && b.failure == nil {
			b.failure = hostaction.ErrUnavailable
		}
		b.mu.Unlock()
	}()
	unretain, err := b.options.Lease.Retain()
	if err != nil {
		return hostaction.ErrUnavailable
	}
	defer unretain() // Per-job guards remain until their own remote cleanup.
	listener, err := open(ctx)
	if err != nil || listener == nil {
		return hostaction.ErrUnavailable
	}
	defer func() {
		if listener.Close() != nil {
			result = hostaction.ErrUnavailable
		}
	}()
	if listener.Check() != nil || ctx.Err() != nil {
		return hostaction.ErrUnavailable
	}
	close(b.ready)
	var handlers sync.WaitGroup
	defer handlers.Wait() // No accept worker may outlive execution ownership.
	slots := make(chan struct{}, maxHostBrokerConnections)
	for ctx.Err() == nil {
		select {
		case slots <- struct{}{}:
		case <-ctx.Done():
			continue
		}
		connection, err := listener.Accept(ctx)
		if err != nil {
			<-slots
			if ctx.Err() == nil {
				b.fail()
			}
			break
		}
		handlers.Add(1)
		go func() {
			defer handlers.Done()
			defer func() { <-slots }()
			b.serveConnection(ctx, listener, connection, accept)
		}()
	}
	handlers.Wait()
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.failure
}

func (b *HostJobBroker) fail() {
	b.mu.Lock()
	b.failure = hostaction.ErrUnavailable
	b.stopped = true
	stop := b.stop
	b.mu.Unlock()
	if stop != nil {
		stop()
	}
}

func (b *HostJobBroker) serveConnection(ctx context.Context, listener hostBrokerListener, c *net.UnixConn, accept hostBrokerAcceptor) {
	session, err := accept(ctx, c, b.options.Journal)
	if err != nil {
		_ = c.Close()
		if errors.Is(err, hostaction.ErrAudit) {
			b.fail()
		}
		return
	}
	route := &hostBrokerRoute{broker: b, session: session, check: listener.Check}
	err = session.Serve(ctx, b.options.Release, route, b.options.Journal)
	if route.bound != nil {
		route.bound.brokerFinished(err)
		// The supervisor, not the accept loop, observes exit and closes this
		// session. In particular, do not discard an already-exited pidfd before
		// WaitBrokerExecutor can observe it and finalization can use the pin.
		if errors.Is(err, hostaction.ErrAudit) || (err != nil && session.GrantPossible()) {
			b.fail()
		}
		return
	}
	if session.Close() != nil {
		// Defensive retention: even an unexpected session implementation must
		// not lose a possible grant's process handle to a garbage collector.
		b.mu.Lock()
		b.orphans = append(b.orphans, session)
		b.mu.Unlock()
		b.fail()
	}
	if errors.Is(err, hostaction.ErrAudit) {
		b.fail()
	}
}

type hostBrokerRoute struct {
	broker  *HostJobBroker
	session hostBrokerSession
	check   func() error
	bound   *HostJobBinding
}

// The session calls this only after validating peer identity and the complete
// canonical claim. Lookup selects an existing binding; it never creates one.
func (r *hostBrokerRoute) WithHostInvocation(ctx context.Context, request actionabi.Request, release hostaction.ReleaseIdentity, peer hostaction.PeerIdentity, run func(context.Context) error) error {
	if ctx == nil || run == nil || r.check() != nil {
		return hostaction.ErrDenied
	}
	b := r.broker
	b.mu.Lock()
	bound := b.bindings[request.JobID]
	if b.stopped || b.closed || b.failure != nil || release != b.options.Release || bound == nil || bound.job.Action.InvocationID != request.InvocationID {
		b.mu.Unlock()
		return hostaction.ErrDenied
	}
	// Attach while registry admission is locked, so shutdown cannot miss a
	// session that has just gained authority. Binding lock is not held in Serve.
	err := bound.attachBroker(r.session)
	if err == nil {
		r.bound = bound
	}
	b.mu.Unlock()
	if err != nil {
		return err
	}
	return bound.WithHostInvocation(ctx, request, release, peer, func(owner context.Context) error {
		if r.check() != nil || owner.Err() != nil {
			return hostaction.ErrDenied
		}
		if err := run(owner); err != nil {
			return err
		}
		if r.check() != nil || owner.Err() != nil {
			return hostaction.ErrUnavailable
		}
		return nil
	})
}

// Stop closes admission via the OWNER context, not a subscriber. Active
// exchanges are bounded by their existing protocol deadlines and retain grants.
func (b *HostJobBroker) Stop() {
	if b == nil {
		return
	}
	b.mu.Lock()
	b.stopped = true
	stop := b.stop
	b.mu.Unlock()
	if stop != nil {
		stop()
	}
}

// Close is retryable after exact remote exit observation, but never reopens
// admission. Run must have returned first. Failure preserves all bound objects
// and their lease guards; there is no reset/recovery-by-reconstruction path.
func (b *HostJobBroker) Close() error {
	if b == nil {
		return nil
	}
	b.Stop()
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.started && !b.finished {
		return hostaction.ErrBrokerExecutorRunning
	}
	if b.closed {
		return nil
	}
	var failures []error
	for _, bound := range b.bindings {
		if err := bound.Close(); err != nil {
			failures = append(failures, err)
		}
	}
	for _, session := range b.orphans {
		if err := session.Close(); err != nil {
			failures = append(failures, err)
		}
	}
	if len(failures) > 0 {
		return errors.Join(failures...)
	}
	b.closed = true
	return nil
}
