package jobexecutor

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/anas-project/ANAS/internal/actionabi"
	"github.com/anas-project/ANAS/internal/consolejobs"
)

// ModuleActionAccess is an application authorization request, not client proof.
// The application resolves Actor's CURRENT roles for every operation. Read must
// not be restricted merely to Job.CreatedBy: another authorized administrator
// must see the same job regardless of whether CLI or HTTP invoked it.
type ModuleActionAccess struct {
	Operation string // invoke, read, or cancel
	Actor     string
	Job       consolejobs.Job
}

type ModuleActionDispatcherStore interface {
	ModuleActionWorkerStore
	ReplayAction(context.Context, string, uint64, int) (consolejobs.ActionEventPage, error)
}

type ModuleActionDispatcherOptions struct {
	Registry   *ModuleActionRegistry
	Store      ModuleActionDispatcherStore
	Lease      *consolejobs.ExecutionLease
	Workspaces []string
	Observer   consolejobs.JobCommitObserver
	Authorize  func(context.Context, ModuleActionAccess) error
	// Both callbacks belong to the trusted application adapter. The observer
	// binds the cancelling actor; AuditCancel runs inside the worker's durable
	// cancellation-intent precommit, never after the process has been signaled.
	CancelObserver func(string) consolejobs.JobCommitObserver
	AuditCancel    func(context.Context, string, consolejobs.Job) error
	PollInterval   time.Duration
}

// ModuleActionDispatcher is an opt-in service facade for CLI/HTTP adapters. It
// delegates ALL execution, queueing and cancellation to ModuleActionWorker;
// it does not own another queue, execution lease, process map or journal. Invoke
// only enqueues and Attach never owns execution. Run belongs to the daemon.
//
// Construct only after execution-owner recovery, using explicitly migrated
// frozen descriptors. Existing HTTP/CLI Module Command routes and the main
// daemon are not implicitly migrated. The shared store owns action-key
// retention and coalescing; this facade never synthesizes a subscriber key.
type ModuleActionDispatcher struct {
	registry          *ModuleActionRegistry
	store             ModuleActionDispatcherStore
	worker            *ModuleActionWorker
	workspaces        map[string]bool
	observer          consolejobs.JobCommitObserver
	lifecycleObserver consolejobs.JobCommitObserver
	authorize         func(context.Context, ModuleActionAccess) error
	cancelObserver    func(string) consolejobs.JobCommitObserver
	auditCancel       func(context.Context, string, consolejobs.Job) error
	pollInterval      time.Duration

	mu      sync.Mutex
	started bool
	stopped bool
	fatal   error
}

func NewModuleActionDispatcher(options ModuleActionDispatcherOptions) (*ModuleActionDispatcher, error) {
	if options.Registry == nil || options.Store == nil || options.Lease == nil || options.Observer == nil ||
		options.Authorize == nil || options.CancelObserver == nil || options.AuditCancel == nil ||
		len(options.Workspaces) == 0 || options.PollInterval < 0 || options.PollInterval > time.Minute {
		return nil, ErrModuleActionUnavailable
	}
	workspaces := make(map[string]bool, len(options.Workspaces))
	for _, workspace := range options.Workspaces {
		if workspace == "" || workspaces[workspace] {
			return nil, ErrModuleActionUnavailable
		}
		workspaces[workspace] = true
	}
	if options.PollInterval == 0 {
		options.PollInterval = defaultPollInterval
	}
	dispatcher := &ModuleActionDispatcher{
		registry: options.Registry, store: options.Store, workspaces: workspaces,
		observer: options.Observer, authorize: options.Authorize, cancelObserver: options.CancelObserver,
		auditCancel: options.AuditCancel, pollInterval: options.PollInterval,
	}
	dispatcher.lifecycleObserver = consolejobs.JobCommitObserverFunc(func(ctx context.Context, intent consolejobs.JobCommitIntent) error {
		if intent.Operation == consolejobs.JobCommitCreate || intent.Operation == consolejobs.JobCommitStart || intent.Operation == consolejobs.JobCommitActionJoin {
			actor := intent.Next.CreatedBy
			if intent.Operation == consolejobs.JobCommitActionJoin {
				actor = intent.Actor
			}
			if actor == "" {
				return ErrModuleActionDenied
			}
			if err := dispatcher.authorize(ctx, ModuleActionAccess{Operation: "invoke", Actor: actor, Job: intent.Next}); err != nil {
				return ErrModuleActionDenied
			}
		}
		return dispatcher.observer.BeforeJobCommit(ctx, intent)
	})
	worker, err := NewModuleActionWorker(ModuleActionWorkerOptions{
		Registry: options.Registry, Store: options.Store, Lease: options.Lease, Workspaces: options.Workspaces,
		Observer: dispatcher.lifecycleObserver, PollInterval: options.PollInterval,
		CheckQueued: func(ctx context.Context, job consolejobs.Job) error {
			return dispatcher.authorize(ctx, ModuleActionAccess{Operation: "invoke", Actor: job.CreatedBy, Job: job})
		},
		AuthorizeCancel: func(ctx context.Context, actor string, job consolejobs.Job) (consolejobs.JobCommitObserver, error) {
			if err := dispatcher.authorize(ctx, ModuleActionAccess{Operation: "cancel", Actor: actor, Job: job}); err != nil {
				return nil, ErrModuleActionDenied
			}
			observer := dispatcher.cancelObserver(actor)
			if observer == nil {
				return nil, ErrModuleActionDenied
			}
			return consolejobs.JobCommitObserverFunc(func(ctx context.Context, intent consolejobs.JobCommitIntent) error {
				if err := dispatcher.authorize(ctx, ModuleActionAccess{Operation: "cancel", Actor: actor, Job: intent.Next}); err != nil {
					return ErrModuleActionDenied
				}
				if intent.Next.Status == consolejobs.StatusRunning {
					if err := dispatcher.auditCancel(ctx, actor, intent.Next); err != nil {
						return ErrModuleActionDenied
					}
				}
				return observer.BeforeJobCommit(ctx, intent)
			}), nil
		},
	})
	if err != nil {
		return nil, err
	}
	dispatcher.worker = worker
	return dispatcher, nil
}

func (dispatcher *ModuleActionDispatcher) admission() error {
	if dispatcher == nil {
		return ErrModuleActionUnavailable
	}
	dispatcher.mu.Lock()
	defer dispatcher.mu.Unlock()
	if dispatcher.fatal != nil {
		return dispatcher.fatal
	}
	if dispatcher.stopped {
		return ErrModuleActionUnavailable
	}
	return nil
}

// Invoke only enqueues. Its context may end immediately after this method
// returns without canceling the job. Both CLI and HTTP preserve the optional
// caller key. The store binds (action, key), checks the full frozen request and
// applies registry policy; neither actors nor transports partition that key.
func (dispatcher *ModuleActionDispatcher) Invoke(ctx context.Context, actor, workspace, name, key string, parameters map[string]any) (consolejobs.CreateResult, error) {
	if err := dispatcher.admission(); err != nil {
		return consolejobs.CreateResult{}, err
	}
	if ctx == nil || actor == "" || !dispatcher.workspaces[workspace] {
		return consolejobs.CreateResult{}, ErrModuleActionDenied
	}
	definition, found := dispatcher.registry.lookup(name)
	if !found {
		return consolejobs.CreateResult{}, ErrModuleActionUnavailable
	}
	if parameters == nil {
		parameters = map[string]any{}
	}
	candidate := consolejobs.Job{
		Kind: consolejobs.ActionJobKind, WorkspaceID: workspace, CreatedBy: actor,
		Mutating: definition.Mutating,
		Action:   &consolejobs.ActionState{ABI: actionabi.Version, Name: name},
	}
	if err := dispatcher.authorize(ctx, ModuleActionAccess{Operation: "invoke", Actor: actor, Job: candidate}); err != nil {
		return consolejobs.CreateResult{}, ErrModuleActionDenied
	}
	created, err := dispatcher.registry.Create(ctx, dispatcher.store, consolejobs.CreateSpec{
		WorkspaceID: workspace, Request: parameters,
		Idempotency: consolejobs.IdempotencyInput{
			Principal: actor, Method: "POST", CanonicalPath: "/actions/" + name, Key: key,
		},
	}, name, dispatcher.lifecycleObserver)
	if err != nil {
		// An action key is not scoped by workspace. A conflict may reference
		// another workspace or an older deployment: never disclose its job
		// ID unless the current caller can read that specific record.
		var retryConflict *consolejobs.IdempotencyConflictError
		var activeConflict *consolejobs.ActionInFlightError
		conflictingID := ""
		if errors.As(err, &retryConflict) {
			conflictingID = retryConflict.ExistingJobID
		} else if errors.As(err, &activeConflict) {
			conflictingID = activeConflict.ExistingJobID
		}
		if conflictingID != "" {
			if _, accessErr := dispatcher.Get(ctx, actor, conflictingID); accessErr != nil {
				return consolejobs.CreateResult{}, ErrModuleActionDenied
			}
		}
		return consolejobs.CreateResult{}, err
	}
	dispatcher.Notify()
	if err := dispatcher.authorize(ctx, ModuleActionAccess{Operation: "read", Actor: actor, Job: created.Job}); err != nil {
		return consolejobs.CreateResult{}, ErrModuleActionDenied
	}
	return created, nil
}

func (dispatcher *ModuleActionDispatcher) Notify() {
	if dispatcher != nil && dispatcher.worker != nil {
		dispatcher.worker.Notify()
	}
}

func (dispatcher *ModuleActionDispatcher) authorizedJob(ctx context.Context, actor, jobID, operation string) (consolejobs.Job, error) {
	if dispatcher == nil || ctx == nil || actor == "" {
		return consolejobs.Job{}, ErrModuleActionDenied
	}
	job, err := dispatcher.store.Get(ctx, jobID)
	if err != nil {
		return consolejobs.Job{}, err
	}
	if job.Action == nil || job.Kind != consolejobs.ActionJobKind || !dispatcher.workspaces[job.WorkspaceID] || !strings.HasPrefix(job.Action.Name, "module.") {
		return consolejobs.Job{}, ErrModuleActionDenied
	}
	if err := dispatcher.authorize(ctx, ModuleActionAccess{Operation: operation, Actor: actor, Job: job}); err != nil {
		return consolejobs.Job{}, ErrModuleActionDenied
	}
	return job, nil
}

func (dispatcher *ModuleActionDispatcher) Get(ctx context.Context, actor, jobID string) (consolejobs.Job, error) {
	return dispatcher.authorizedJob(ctx, actor, jobID, "read")
}

func (dispatcher *ModuleActionDispatcher) List(ctx context.Context, actor string) ([]consolejobs.Job, error) {
	if dispatcher == nil || ctx == nil || actor == "" {
		return nil, ErrModuleActionDenied
	}
	jobs, err := dispatcher.store.List(ctx)
	if err != nil {
		return nil, err
	}
	result := make([]consolejobs.Job, 0)
	for _, job := range jobs {
		if job.Action == nil || job.Kind != consolejobs.ActionJobKind || !dispatcher.workspaces[job.WorkspaceID] || !strings.HasPrefix(job.Action.Name, "module.") {
			continue
		}
		if err := dispatcher.authorize(ctx, ModuleActionAccess{Operation: "read", Actor: actor, Job: job}); err != nil {
			if errors.Is(err, ErrModuleActionDenied) {
				continue
			}
			return nil, ErrModuleActionUnavailable
		}
		result = append(result, job)
	}
	return result, nil
}

// Attach reauthorizes every page, preserves actual persisted sequence numbers
// and truncation markers, and never cancels execution on disconnect. emit must
// honor ctx and may not retain a transport owned by another subscriber.
func (dispatcher *ModuleActionDispatcher) Attach(ctx context.Context, actor, jobID string, fromSeq uint64, emit func(context.Context, actionabi.Event) error) error {
	if dispatcher == nil || ctx == nil || emit == nil {
		return ErrModuleActionUnavailable
	}
	ticker := time.NewTicker(dispatcher.pollInterval)
	defer ticker.Stop()
	for {
		if _, err := dispatcher.Get(ctx, actor, jobID); err != nil {
			return err
		}
		page, err := dispatcher.store.ReplayAction(ctx, jobID, fromSeq, 100)
		if err != nil {
			return err
		}
		if len(page.Events) == 0 && fromSeq < page.LatestSeq {
			return ErrModuleActionExecution
		}
		for _, event := range page.Events {
			if err := ctx.Err(); err != nil {
				return err
			}
			if err := emit(ctx, event); err != nil {
				return err
			}
			fromSeq = event.Seq
		}
		if fromSeq < page.LatestSeq {
			continue
		}
		if page.Outcome != "" {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

// Cancel uses the worker's atomic, audited intent and notification boundary.
// The returned record does NOT promise a cancelled outcome: only execution
// acknowledgement and complete exit evidence may establish that outcome.
func (dispatcher *ModuleActionDispatcher) Cancel(ctx context.Context, actor, jobID string) (consolejobs.Job, error) {
	if _, err := dispatcher.authorizedJob(ctx, actor, jobID, "cancel"); err != nil {
		return consolejobs.Job{}, err
	}
	return dispatcher.worker.Cancel(ctx, actor, jobID)
}

// Run delegates to the sole worker. The daemon owns its context and execution
// lease; a subscriber never calls Run. A fatal result stops this facade's
// admission, but reads remain available for diagnosis and reconciliation.
func (dispatcher *ModuleActionDispatcher) Run(ctx context.Context) (runErr error) {
	if dispatcher == nil || ctx == nil {
		return ErrModuleActionUnavailable
	}
	dispatcher.mu.Lock()
	if dispatcher.started || dispatcher.stopped {
		dispatcher.mu.Unlock()
		return ErrModuleActionUnavailable
	}
	dispatcher.started = true
	dispatcher.mu.Unlock()
	defer func() {
		dispatcher.mu.Lock()
		dispatcher.stopped = true
		if runErr != nil {
			dispatcher.fatal = ErrModuleActionUnavailable
			if errors.Is(runErr, ErrModuleActionContainment) {
				dispatcher.fatal = ErrModuleActionContainment
			}
		}
		dispatcher.mu.Unlock()
	}()
	return dispatcher.worker.Run(ctx)
}
