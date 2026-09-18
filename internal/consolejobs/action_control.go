package consolejobs

import (
	"context"
	"errors"
	"reflect"
	"time"

	"github.com/anas-project/ANAS/internal/actionabi"
)

const (
	actionNotStartedCode    = "action_not_started"
	actionNotStartedMessage = "Action preflight failed; execution was not started"
)

// ActionCancellation records an authorized request, not an outcome. The first
// actor/time is immutable and survives event-tail truncation and compaction.
// It is never derived from an executor's warning text.
type ActionCancellation struct {
	Actor       string    `json:"actor"`
	RequestedAt time.Time `json:"requested_at"`
}

// ActionContainmentLost identifies the existing durable execution barrier.
// Full job validation separately checks the status/outcome binding. There is
// no second fence, reset flag or store: action_admission.go owns admission.
func ActionContainmentLost(job Job) bool {
	return job.Action != nil && job.Status == StatusInterrupted && job.Error != nil &&
		job.Error.Code == "execution_containment_lost"
}

// RequestActionCancelObserved persists intent BEFORE the worker notifies the
// executor. The trusted application must authorize the current canceling actor
// and provide its actor-bound audit observer. A duplicate keeps the first
// actor/time and appends no record. This method does not own or signal a child.
func (store *Store) RequestActionCancelObserved(ctx context.Context, jobID, invocationID, actor string, observer JobCommitObserver) (Job, error) {
	if store == nil || observer == nil || ctx == nil {
		return Job{}, ErrUnavailable
	}
	if validateIdentifier("cancel actor", actor, 256) != nil || sanitizeText(actor) != actor {
		return Job{}, invalidError("invalid action cancellation actor")
	}
	var result Job
	err := store.withState(ctx, func() error {
		job, exists := store.state.jobs[jobID]
		if !exists {
			return ErrNotFound
		}
		if job.Action == nil || job.Action.InvocationID != invocationID || job.Status != StatusRunning {
			return ErrConflict
		}
		if job.Action.Cancellation != nil {
			result = cloneJob(job)
			return nil
		}
		if job.Revision == ^uint64(0) {
			return invalidError("action job revision exhausted")
		}
		now := store.now().UTC()
		next := cloneJob(job)
		next.Revision++
		next.Action.Cancellation = &ActionCancellation{Actor: actor, RequestedAt: now}
		previous := cloneJob(job)
		if err := observeJobCommit(ctx, observer, JobCommitIntent{
			Operation: JobCommitCancelRequest, Previous: &previous, Next: cloneJob(next),
		}); err != nil {
			return err
		}
		if err := store.persistRecords(ctx, []journalRecord{{Kind: recordJobUpdated, RecordedAt: now, Job: &next}}); err != nil {
			return err
		}
		result = cloneJob(next)
		return nil
	})
	return result, err
}

// RejectQueuedActionObserved closes a call which failed preflight without
// ever launching. Race with Start is decided by jobs.lock. This must NOT be
// used to turn an uncertain running execution into a harmless failed job.
// All rejection reasons have a fixed public projection; raw authorization,
// descriptor, path and executable errors never become persisted messages.
func (store *Store) RejectQueuedActionObserved(ctx context.Context, jobID, invocationID string, observer JobCommitObserver) (Job, error) {
	if store == nil || observer == nil || ctx == nil {
		return Job{}, ErrUnavailable
	}
	return store.writeActionEvent(ctx, nil, actionabi.Event{
		ABI: actionabi.Version, JobID: jobID, InvocationID: invocationID, Type: "error",
		Error: &actionabi.Failure{Outcome: actionabi.Failed, Code: actionNotStartedCode, Message: actionNotStartedMessage},
	}, observer, true)
}

func isActionNotStarted(event actionabi.Event) bool {
	return event.Type == "error" && event.Error != nil && event.Error.Outcome == actionabi.Failed &&
		event.Error.Code == actionNotStartedCode && event.Error.Message == actionNotStartedMessage
}

// Check evidence in both ordinary replay and compacted snapshots. A terminal
// without StartedAt is a queued cancellation or preflight rejection, never an
// execution success/unknown. Running cancellation requires persisted intent in
// addition to the supervisor's independent EOF/exit/acknowledgement checks.
func validateActionControl(job Job) error {
	if cancellation := job.Action.Cancellation; cancellation != nil {
		if job.StartedAt == nil || cancellation.RequestedAt.IsZero() ||
			validateIdentifier("cancel actor", cancellation.Actor, 256) != nil || sanitizeText(cancellation.Actor) != cancellation.Actor {
			return errors.New("invalid persisted action cancellation")
		}
	}
	if !job.Status.terminal() {
		return nil
	}
	if job.StartedAt == nil {
		if job.NeedsCompensationCheck || (job.Status != StatusCanceled &&
			(job.Status != StatusFailed || job.Error == nil || job.Error.Code != actionNotStartedCode ||
				job.Error.Message != actionNotStartedMessage)) {
			return errors.New("unstarted action has an execution outcome")
		}
	} else if (job.Error != nil && job.Error.Code == actionNotStartedCode) ||
		(job.Status == StatusCanceled && job.Action.Cancellation == nil) {
		return errors.New("running action completion lacks control evidence")
	}
	return nil
}

// Ordinary job_updated records may start an action, acknowledge compensation,
// or add exactly one cancel intent. They cannot replace the atomic event path
// for outcomes or rewrite the first cancellation actor/time or retry policy.
func validActionJobUpdate(previous, next Job) bool {
	if previous.Action == nil || next.Action == nil {
		return false
	}
	if reflect.DeepEqual(previous.Action, next.Action) {
		return previous.Status == next.Status || (previous.Status == StatusQueued && next.Status == StatusRunning)
	}
	if previous.Status != StatusRunning || next.Status != StatusRunning ||
		previous.Action.Cancellation != nil || next.Action.Cancellation == nil {
		return false
	}
	expected := cloneJob(previous)
	expected.Revision = next.Revision
	cancellation := *next.Action.Cancellation
	expected.Action.Cancellation = &cancellation
	return reflect.DeepEqual(expected, next)
}
