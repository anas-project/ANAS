package jobexecutor

import (
	"context"
	"errors"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/anas-project/ANAS/internal/consolejobs"
)

// ModuleActionWorkerStore is the SAME store and execution lease used by the
// ordinary console executor. A worker must not open an entrypoint-specific
// journal or recover running actions without independent process evidence.
type ModuleActionWorkerStore interface {
	ModuleActionStore
	List(context.Context) ([]consolejobs.Job, error)
	RequestActionCancelObserved(context.Context, string, string, string, consolejobs.JobCommitObserver) (consolejobs.Job, error)
}

// ModuleActionCancelAuthorizer re-resolves the current canceling actor and
// returns an audit observer bound to that actor, not the original job creator.
// This callback belongs to the trusted application adapter, never a request DTO.
// It is also re-run under jobs.lock immediately before the cancellation commit;
// neither the authorizer nor its observer may reenter the same Store.
type ModuleActionCancelAuthorizer func(context.Context, string, consolejobs.Job) (consolejobs.JobCommitObserver, error)

type ModuleActionWorkerOptions struct {
	Registry        *ModuleActionRegistry
	Store           ModuleActionWorkerStore
	Lease           *consolejobs.ExecutionLease
	Workspaces      []string
	Observer        consolejobs.JobCommitObserver
	AuthorizeCancel ModuleActionCancelAuthorizer
	// Optional additional entrypoint policy, rechecked before preparation.
	// Registry.Check remains mandatory and validates current deployment roles.
	CheckQueued  func(context.Context, consolejobs.Job) error
	PollInterval time.Duration
}

type moduleActionControl struct {
	invocation string
	signal     chan struct{}
	notified   bool
}

// ModuleActionWorker serializes the currently registered Module actions. The
// application still supplies its declared workspace/module locks, and the
// shared store enforces capacity and compensation barriers across both workers.
// Construct it only after execution-owner recovery, with an explicitly migrated
// registry. No existing Module Command or HTTP route opts in automatically.
type ModuleActionWorker struct {
	registry        *ModuleActionRegistry
	store           ModuleActionWorkerStore
	lease           *consolejobs.ExecutionLease
	workspaces      map[string]bool
	observer        consolejobs.JobCommitObserver
	authorizeCancel ModuleActionCancelAuthorizer
	checkQueued     func(context.Context, consolejobs.Job) error
	pollInterval    time.Duration
	wake            chan struct{}
	started         atomic.Bool
	mu              sync.Mutex
	running         map[string]*moduleActionControl
}

func NewModuleActionWorker(options ModuleActionWorkerOptions) (*ModuleActionWorker, error) {
	if options.Registry == nil || options.Store == nil || options.Lease == nil || options.Observer == nil ||
		options.AuthorizeCancel == nil || len(options.Workspaces) == 0 || len(options.Workspaces) > 1024 || options.PollInterval < 0 || options.PollInterval > time.Minute {
		return nil, ErrModuleActionUnavailable
	}
	if options.PollInterval == 0 {
		options.PollInterval = defaultPollInterval
	}
	workspaces := make(map[string]bool, len(options.Workspaces))
	for _, workspace := range options.Workspaces {
		if workspace == "" || len(workspace) > 256 || strings.TrimSpace(workspace) != workspace || workspaces[workspace] {
			return nil, ErrModuleActionUnavailable
		}
		workspaces[workspace] = true
	}
	return &ModuleActionWorker{
		registry: options.Registry, store: options.Store, lease: options.Lease, workspaces: workspaces,
		observer: options.Observer, authorizeCancel: options.AuthorizeCancel, checkQueued: options.CheckQueued, pollInterval: options.PollInterval,
		wake: make(chan struct{}, 1), running: make(map[string]*moduleActionControl),
	}, nil
}

// Notify is only an optimization. Missed notifications are recovered by a
// durable-store poll; request cancellation/disconnection never cancels Run.
func (worker *ModuleActionWorker) Notify() {
	if worker == nil {
		return
	}
	select {
	case worker.wake <- struct{}{}:
	default:
	}
}

// Run belongs to the daemon lifetime, not an invoke or attach request. It may
// be called once. On containment/persistence uncertainty it stops admission;
// callers must stop the execution owner, not recreate a worker and retry.
func (worker *ModuleActionWorker) Run(ctx context.Context) error {
	if worker == nil || ctx == nil || worker.started.Swap(true) {
		return ErrModuleActionUnavailable
	}
	ticker := time.NewTicker(worker.pollInterval)
	defer ticker.Stop()
	for ctx.Err() == nil {
		if worker.registry.poisoned.Load() {
			return ErrModuleActionContainment
		}
		readContext, readCancel := context.WithTimeout(ctx, terminalWriteTimeout)
		jobs, err := worker.store.List(readContext)
		readCancel()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return ErrModuleActionExecution
		}
		// No action is executing in this worker while it polls. A still-running
		// receipt therefore belongs to an unresolved execution owner, not a
		// claimable job. Never infer cleanup from this process holding a lease.
		for _, job := range jobs {
			if job.Action != nil && (job.Status == consolejobs.StatusRunning ||
				consolejobs.ActionContainmentLost(job) ||
				(job.Status == consolejobs.StatusInterrupted && job.Error != nil && job.Error.Code == "daemon_restarted")) {
				return ErrModuleActionContainment
			}
		}
		sort.Slice(jobs, func(i, j int) bool {
			if jobs[i].CreatedAt.Equal(jobs[j].CreatedAt) {
				return jobs[i].ID < jobs[j].ID
			}
			return jobs[i].CreatedAt.Before(jobs[j].CreatedAt)
		})
		advanced := false
		blocked := make(map[string]bool)
		for _, job := range jobs {
			if ctx.Err() != nil {
				return nil
			}
			if job.Action == nil || job.Status != consolejobs.StatusQueued || !worker.workspaces[job.WorkspaceID] ||
				blocked[job.WorkspaceID] || !strings.HasPrefix(job.Action.Name, "module.") {
				continue
			}
			if worker.checkQueued != nil {
				if err := worker.checkQueued(ctx, job); err != nil {
					if ctx.Err() != nil {
						return nil
					}
					if _, err := worker.store.RejectQueuedActionObserved(ctx, job.ID, job.Action.InvocationID, worker.observer); err != nil {
						if !errors.Is(err, consolejobs.ErrConflict) {
							return ErrModuleActionExecution
						}
						// An authorized queued cancellation may win this race.
						// Only a confirmed terminal is safe to advance past.
						latest, readErr := worker.store.Get(ctx, job.ID)
						if readErr != nil || !moduleActionTerminal(latest.Status) {
							return ErrModuleActionContainment
						}
					}
					advanced = true
					break // Refresh durable state after the preflight rejection.
				}
			}
			definition, found := worker.registry.lookup(job.Action.Name)
			control := &moduleActionControl{invocation: job.Action.InvocationID}
			if found && definition.Cancellable != "false" {
				control.signal = make(chan struct{})
			}
			worker.mu.Lock()
			worker.running[job.ID] = control
			worker.mu.Unlock()
			result, runErr := worker.registry.Run(ctx, worker.store, worker.lease, job.ID, control.signal, worker.observer)
			worker.mu.Lock()
			delete(worker.running, job.ID)
			worker.mu.Unlock()
			if errors.Is(runErr, ErrModuleActionContainment) || errors.Is(runErr, consolejobs.ErrActionExecutionBlocked) {
				return ErrModuleActionContainment
			}
			if ctx.Err() != nil {
				return nil
			}
			if moduleActionQueueBlocked(runErr) {
				blocked[job.WorkspaceID] = true
				continue
			}
			if errors.Is(runErr, consolejobs.ErrConflict) {
				// A queued cancellation may beat Start. Re-read once; a running
				// or missing result is not proof that this call can be retried.
				readContext, readCancel := context.WithTimeout(ctx, terminalWriteTimeout)
				result, err = worker.store.Get(readContext, job.ID)
				readCancel()
				if err != nil {
					return ErrModuleActionExecution
				}
			}
			if result.ID != job.ID || !moduleActionTerminal(result.Status) {
				return ErrModuleActionExecution
			}
			advanced = true
			break // Refresh durable state after every completed or rejected invocation.
		}
		if advanced {
			continue
		}
		select {
		case <-ctx.Done():
			return nil
		case <-worker.wake:
		case <-ticker.C:
		}
	}
	return nil
}

// Cancel persists authorized intent before notifying the owned process. The
// job may win the completion race and succeed; accepting a request is never a
// promise of a cancelled outcome. Unsupported cancellation fails closed.
func (worker *ModuleActionWorker) Cancel(ctx context.Context, actor, jobID string) (consolejobs.Job, error) {
	if worker == nil || ctx == nil || actor == "" || worker.registry.poisoned.Load() {
		return consolejobs.Job{}, ErrModuleActionUnavailable
	}
	ctx, cancel := context.WithTimeout(ctx, terminalWriteTimeout)
	defer cancel()
	worker.mu.Lock()
	defer worker.mu.Unlock()
	for attempt := 0; attempt < 2; attempt++ {
		job, err := worker.store.Get(ctx, jobID)
		if err != nil {
			return consolejobs.Job{}, err
		}
		if job.Action == nil || !worker.workspaces[job.WorkspaceID] || moduleActionTerminal(job.Status) {
			return consolejobs.Job{}, consolejobs.ErrConflict
		}
		definition, found := worker.registry.lookup(job.Action.Name)
		if !found || definition.Cancellable == "false" {
			return consolejobs.Job{}, ErrModuleActionDenied
		}
		if _, err := storedModuleActionParameters(job, definition); err != nil {
			return consolejobs.Job{}, ErrModuleActionDenied
		}
		observer, err := worker.authorizeCancel(ctx, actor, job)
		if err != nil || observer == nil {
			return consolejobs.Job{}, ErrModuleActionDenied
		}
		observer = consolejobs.JobCommitObserverFunc(func(commitContext context.Context, intent consolejobs.JobCommitIntent) error {
			if intent.Previous == nil || intent.Previous.ID != job.ID || intent.Previous.Action == nil ||
				intent.Previous.Action.InvocationID != job.Action.InvocationID || intent.Previous.Action.Name != job.Action.Name {
				return ErrModuleActionDenied
			}
			current, err := worker.authorizeCancel(commitContext, actor, *intent.Previous)
			if err != nil || current == nil {
				return ErrModuleActionDenied
			}
			return current.BeforeJobCommit(commitContext, intent)
		})
		if job.Status == consolejobs.StatusQueued {
			result, err := worker.store.CancelQueuedActionObserved(ctx, job.ID, job.Action.InvocationID, observer)
			if errors.Is(err, consolejobs.ErrConflict) {
				continue
			}
			return result, err
		}
		control := worker.running[job.ID]
		if job.Status != consolejobs.StatusRunning || control == nil || control.signal == nil || control.invocation != job.Action.InvocationID {
			return consolejobs.Job{}, ErrModuleActionUnavailable
		}
		result, err := worker.store.RequestActionCancelObserved(ctx, job.ID, job.Action.InvocationID, actor, observer)
		if err != nil {
			return consolejobs.Job{}, err
		}
		if !control.notified {
			close(control.signal)
			control.notified = true
		}
		return result, nil
	}
	return consolejobs.Job{}, consolejobs.ErrConflict
}

// Only a declared queue wait is retryable. A reject-policy conflict or an
// ambiguous start conflict must still take the fail-closed result path.
func moduleActionQueueBlocked(err error) bool {
	var active *consolejobs.ActionInFlightError
	return errors.Is(err, consolejobs.ErrCapacity) || errors.Is(err, consolejobs.ErrWorkspaceBusy) ||
		errors.Is(err, consolejobs.ErrCompensationRequired) ||
		(errors.As(err, &active) && active != nil && active.Policy == consolejobs.ActionQueue)
}

func moduleActionTerminal(status consolejobs.Status) bool {
	return status == consolejobs.StatusSucceeded || status == consolejobs.StatusFailed ||
		status == consolejobs.StatusCanceled || status == consolejobs.StatusInterrupted
}
