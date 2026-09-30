package jobexecutor

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"reflect"
	"sync"
	"time"

	"github.com/anas-project/ANAS/internal/actionabi"
	"github.com/anas-project/ANAS/internal/consolejobs"
	"github.com/anas-project/ANAS/internal/hostaction"
)

// HostJobRequest freezes the installed release alongside public action input.
// This helper does not create/start/authorize a job or confer root privileges.
func HostJobRequest(release hostaction.ReleaseIdentity) (map[string]any, error) {
	return HostActionRequest(hostaction.ActionStatus, release, json.RawMessage(`{}`))
}

func HostActionRequest(action string, release hostaction.ReleaseIdentity, parameters json.RawMessage) (map[string]any, error) {
	request, _, err := hostaction.FrozenRequest(action, release, parameters)
	return request, err
}

type hostStream interface {
	io.Reader
	Close() error
}

// hostActionRunner is the job owner's side of the host action channel. It
// sends a running job's frozen request to the socket-activated hostd, records
// the streamed events through the shared ActionRecorder, and settles a stream
// that ended without a clean terminal from hostd's own invocation ledger.
// There is no callback listener, broker or service-manager query: anasd and
// hostd are both root, so proving which root process sits at the other end of
// the root-only socket would not add a boundary (simplification review item 4).
type hostActionRunner struct {
	store     *consolejobs.Store
	lease     *consolejobs.ExecutionLease
	release   hostaction.ReleaseIdentity
	authorize func(context.Context, consolejobs.Job) error
	dial      func(context.Context, actionabi.Request) (hostStream, error)
	query     func(context.Context, string, string) (hostaction.InvocationStatus, error)
	poll      time.Duration
	absent    time.Duration
	ready     chan struct{}
	once      sync.Once
}

func newHostActionRunner(store *consolejobs.Store, lease *consolejobs.ExecutionLease, release hostaction.ReleaseIdentity, authorize func(context.Context, consolejobs.Job) error) (*hostActionRunner, error) {
	if store == nil || lease == nil || authorize == nil || release.Validate() != nil {
		return nil, hostaction.ErrUnavailable
	}
	return &hostActionRunner{
		store: store, lease: lease, release: release, authorize: authorize,
		dial: func(ctx context.Context, request actionabi.Request) (hostStream, error) {
			stream, err := hostaction.DialHostAction(ctx, request)
			if err != nil {
				return nil, err
			}
			return stream, nil
		},
		query: hostaction.QueryHostInvocation,
		poll:  2 * time.Second,
		// hostd reads its request within three seconds or gives up, so a record
		// that is still absent after this window never began.
		absent: 6 * time.Second,
		ready:  make(chan struct{}),
	}, nil
}

func (r *hostActionRunner) Run(ctx context.Context) error {
	r.once.Do(func() { close(r.ready) })
	<-ctx.Done()
	return nil
}

func (r *hostActionRunner) Ready() <-chan struct{} { return r.ready }
func (r *hostActionRunner) Stop()                  {}
func (r *hostActionRunner) Close() error           { return nil }

// runnable binds the immutable execution: a started, uncancelled action job
// whose stored request still equals the frozen request for this release.
func (r *hostActionRunner) runnable(bound, job consolejobs.Job) bool {
	if job.Action == nil || job.Action.ABI != actionabi.Version || job.Action.Outcome != "" || job.Action.Cancellation != nil ||
		job.Status != consolejobs.StatusRunning || job.CreatedBy == "" || job.WorkspaceID == "" || job.StartedAt == nil {
		return false
	}
	spec, ok := hostaction.LookupAction(job.Action.Name)
	if !ok || job.Mutating != spec.Mutating {
		return false
	}
	parameters, err := publicParametersFromStoredRequest(job.Action.Name, job.Request)
	if err != nil {
		return false
	}
	want, err := HostActionRequest(job.Action.Name, r.release, parameters)
	if err != nil {
		return false
	}
	stored := job.Request
	if spec.RequiresConfirm {
		stored = publicStoredRequest(job.Request)
	}
	a, errA := json.Marshal(stored)
	b, errB := json.Marshal(want)
	return errA == nil && errB == nil && string(a) == string(b) && job.ID == bound.ID && job.Kind == bound.Kind &&
		job.CreatedBy == bound.CreatedBy && job.WorkspaceID == bound.WorkspaceID &&
		job.Action.InvocationID == bound.Action.InvocationID && reflect.DeepEqual(job.StartedAt, bound.StartedAt)
}

// ExecutePreflight belongs to the daemon execution owner, never a subscriber.
// The job is already running in the one shared store; the shared recorder or
// hostd's ledger alone decides its terminal.
func (r *hostActionRunner) ExecutePreflight(owner context.Context, jobID string, observer consolejobs.JobCommitObserver) (consolejobs.Job, error) {
	if r == nil || owner == nil || observer == nil {
		return consolejobs.Job{}, hostaction.ErrUnavailable
	}
	current, err := r.store.Get(owner, jobID)
	if err != nil || current.Action == nil {
		return consolejobs.Job{}, hostaction.ErrDenied
	}
	job, unretain, err := r.store.RetainActionExecution(owner, r.lease, jobID, current.Action.InvocationID)
	if err != nil {
		return consolejobs.Job{}, hostaction.ErrDenied
	}
	if !r.runnable(job, job) {
		unretain()
		return consolejobs.Job{}, hostaction.ErrDenied
	}
	finished, contained, err := r.execute(owner, job, observer)
	if !contained {
		// An unconfirmed execution keeps the lease retained: that durable
		// barrier needs explicit recovery, never a quiet release.
		unretain()
	}
	return finished, err
}

func (r *hostActionRunner) execute(owner context.Context, job consolejobs.Job, observer consolejobs.JobCommitObserver) (consolejobs.Job, bool, error) {
	commit := r.commitObserver(job, observer)
	if r.authorize(owner, job) != nil {
		finished, err := r.complete(owner, job, failure(job, actionabi.Failed, "job_authorization_revoked", "Host action was not started"), commit)
		return finished, false, err
	}
	parameters, err := parametersForExecution(job)
	if err != nil {
		return consolejobs.Job{}, false, hostaction.ErrDenied
	}
	request := actionabi.Request{ABI: actionabi.Version, JobID: job.ID, InvocationID: job.Action.InvocationID, Action: job.Action.Name, Parameters: parameters}
	stream, err := r.dial(owner, request)
	if errors.Is(err, hostaction.ErrNoExecution) {
		finished, err := r.complete(owner, job, failure(job, actionabi.Failed, "host_action_unavailable",
			"anas-hostd is not installed or does not accept this daemon; install ANAS as a system service to install it"), commit)
		return finished, false, err
	}
	if err != nil {
		// The request may have reached hostd: its ledger decides.
		return r.settle(owner, job, commit)
	}
	terminal := ""
	recorder, err := NewActionRecorder(ActionRecorderOptions{Store: r.store, Lease: r.lease, Job: job, Output: stream,
		Project: func(event actionabi.Event) (actionabi.Event, error) {
			public, err := hostaction.ProjectActionEvent(job.Action.Name, event)
			if err == nil && (event.Type == "result" || event.Type == "error") {
				terminal = event.Type
			}
			return public, err
		}, Observer: commit})
	if err != nil {
		_ = stream.Close()
		return r.settle(owner, job, commit)
	}
	stop := context.AfterFunc(owner, func() { _ = stream.Close() })
	for err == nil {
		err = recorder.Next(owner)
	}
	stop()
	closeErr := stream.Close()
	if err != io.EOF || terminal == "" || owner.Err() != nil {
		return r.settle(owner, job, commit)
	}
	// hostd records the terminal in its ledger before it sends the frame, so
	// a clean terminal followed by EOF is that record, not a provisional claim.
	exit := actionabi.ExitState{ProcessExited: true, StreamEOF: closeErr == nil}
	if terminal == "error" {
		exit.ExitCode = 1
	}
	cleanup, cancel := context.WithTimeout(context.WithoutCancel(owner), terminalWriteTimeout)
	defer cancel()
	finished, err := recorder.Finish(cleanup, exit)
	return finished, false, err
}

// settle reads hostd's ledger for an invocation whose stream ended without a
// clean terminal. A running invocation is polled until its compiled budget has
// passed; a record that never appears means hostd refused the request before
// it began, so nothing ran; a lost record is an unconfirmed execution.
func (r *hostActionRunner) settle(owner context.Context, job consolejobs.Job, commit consolejobs.JobCommitObserver) (consolejobs.Job, bool, error) {
	start := time.Now()
	deadline := start.Add(hostaction.ExecutionTimeout(job.Action.Name) + r.absent)
	failures := 0
	for {
		query, cancel := context.WithTimeout(context.WithoutCancel(owner), 30*time.Second)
		status, err := r.query(query, job.ID, job.Action.InvocationID)
		cancel()
		if err != nil {
			// hostd that cannot answer from its own record cannot settle
			// this invocation later either; do not wait out the budget.
			if failures++; failures >= 5 {
				return r.containment(owner, job, commit)
			}
		} else {
			failures = 0
			switch status.State {
			case hostaction.InvocationFinished:
				public, projectErr := hostaction.ProjectActionEvent(job.Action.Name, *status.Terminal)
				if projectErr != nil {
					return r.containment(owner, job, commit)
				}
				finished, err := r.complete(owner, job, public, commit)
				return finished, false, err
			case hostaction.InvocationLost:
				return r.containment(owner, job, commit)
			case hostaction.InvocationAbsent:
				if time.Since(start) >= r.absent {
					finished, err := r.complete(owner, job, failure(job, actionabi.Failed, "host_action_not_started", "Host action was not started"), commit)
					return finished, false, err
				}
			}
		}
		if owner.Err() != nil || time.Now().After(deadline) {
			return r.containment(owner, job, commit)
		}
		timer := time.NewTimer(r.poll)
		select {
		case <-owner.Done():
			timer.Stop()
		case <-timer.C:
		}
	}
}

func failure(job consolejobs.Job, outcome actionabi.Outcome, code, message string) actionabi.Event {
	return actionabi.Event{ABI: actionabi.Version, JobID: job.ID, InvocationID: job.Action.InvocationID, Type: "error",
		Error: &actionabi.Failure{Outcome: outcome, Code: code, Message: message}}
}

func (r *hostActionRunner) complete(owner context.Context, job consolejobs.Job, terminal actionabi.Event, commit consolejobs.JobCommitObserver) (consolejobs.Job, error) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(owner), terminalWriteTimeout)
	defer cancel()
	return r.store.CompleteActionObserved(ctx, r.lease, terminal, commit)
}

func (r *hostActionRunner) containment(owner context.Context, job consolejobs.Job, commit consolejobs.JobCommitObserver) (consolejobs.Job, bool, error) {
	finished, err := r.complete(owner, job, failure(job, actionabi.Unknown, consolejobs.ActionContainmentCode,
		"Host action execution could not be confirmed"), commit)
	return finished, true, errors.Join(consolejobs.ErrActionContainment, err)
}

// commitObserver runs under jobs.lock and never reenters Store. A success may
// only replace the exact running execution this runner started.
func (r *hostActionRunner) commitObserver(job consolejobs.Job, observer consolejobs.JobCommitObserver) consolejobs.JobCommitObserver {
	return consolejobs.JobCommitObserverFunc(func(ctx context.Context, intent consolejobs.JobCommitIntent) error {
		if intent.Next.Action != nil && intent.Next.Action.Outcome == actionabi.Succeeded &&
			(intent.Previous == nil || !r.runnable(job, *intent.Previous)) {
			return hostaction.ErrDenied
		}
		return observer.BeforeJobCommit(ctx, intent)
	})
}
