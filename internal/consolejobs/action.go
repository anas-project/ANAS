package consolejobs

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"sort"
	"time"

	"github.com/anas-project/ANAS/internal/actionabi"
)

const ActionJobKind = "action.invoke"

// ActionState lives in the existing job journal. LastSeq is local to this job;
// it must never be confused with Event.ID, the console's global SSE cursor.
// Outcome preserves unknown/cancelled without changing legacy status spelling.
type ActionState struct {
	Cancellation *ActionCancellation     `json:"cancellation,omitempty"`
	ABI          string                  `json:"abi"`
	Name         string                  `json:"name"`
	InvocationID string                  `json:"invocation_id"`
	LastSeq      uint64                  `json:"last_seq"`
	Outcome      actionabi.Outcome       `json:"outcome,omitempty"`
	Policy       *ActionInvocationPolicy `json:"policy,omitempty"`
}

type ActionEventPage struct {
	Events    []actionabi.Event
	LatestSeq uint64
	Outcome   actionabi.Outcome
}

// CreateActionObserved is an internal dispatcher boundary, not a public invoke
// API. The caller authorizes the action and supplies its bounded public parameter
// projection in Request. It still uses the existing idempotency scope/retention;
// action-scoped coalescing and one-hour expiry are not implemented here.
func (store *Store) CreateActionObserved(ctx context.Context, spec CreateSpec, name, invocationID string, observer JobCommitObserver) (CreateResult, error) {
	if store == nil || observer == nil {
		return CreateResult{}, ErrUnavailable
	}
	if store.options.EventCapacity < 2 {
		return CreateResult{}, invalidError("action events require capacity of at least two")
	}
	body, err := json.Marshal(spec.Request)
	if err != nil {
		return CreateResult{}, invalidError("invalid action parameters")
	}
	if _, err := actionabi.EncodeRequest(actionabi.Request{
		ABI: actionabi.Version, JobID: "pending", InvocationID: invocationID, Action: name, Parameters: body,
	}); err != nil {
		return CreateResult{}, invalidError("invalid action invocation")
	}
	spec.Kind = ActionJobKind
	spec.action = &ActionState{ABI: actionabi.Version, Name: name, InvocationID: invocationID}
	return store.CreateOrGetObserved(ctx, spec, observer)
}

// StartActionObserved requires ownership of the same execution lease used for
// restart recovery. Legacy ClaimNext deliberately skips action jobs until a
// registered dispatcher can execute them. Hold lease until all children stop.
func (store *Store) StartActionObserved(ctx context.Context, jobID string, lease *ExecutionLease, observer JobCommitObserver) (Job, error) {
	if store == nil || observer == nil {
		return Job{}, ErrUnavailable
	}
	var result Job
	err := lease.withOwnership(store.directory, func() error {
		var err error
		result, err = store.startObserved(ctx, jobID, observer, true)
		return err
	})
	return result, err
}

// AppendActionEvent accepts only already-projected progress/warning events.
// Registry-specific public projection MUST precede this call: generic secret
// redaction cannot recognize arbitrary secrets in an otherwise ordinary string.
func (store *Store) AppendActionEvent(ctx context.Context, lease *ExecutionLease, event actionabi.Event) (Job, error) {
	if event.Type != "progress" && event.Type != "warning" {
		return Job{}, invalidError("action append requires progress or warning")
	}
	return store.writeActionEvent(ctx, lease, event, nil, false)
}

// CompleteActionObserved commits the public terminal and job state in ONE
// journal record. Only the trusted supervisor may call this, after draining the
// executor and validating its actual exit with actionabi.ExecutionReader.
func (store *Store) CompleteActionObserved(ctx context.Context, lease *ExecutionLease, event actionabi.Event, observer JobCommitObserver) (Job, error) {
	if observer == nil {
		return Job{}, ErrUnavailable
	}
	if event.Type != "result" && event.Type != "error" {
		return Job{}, invalidError("action completion requires a terminal event")
	}
	return store.writeActionEvent(ctx, lease, event, observer, false)
}

// CancelQueuedActionObserved races the claim under jobs.lock. It needs no
// executor acknowledgement because a still-queued job has never been launched.
func (store *Store) CancelQueuedActionObserved(ctx context.Context, jobID, invocationID string, observer JobCommitObserver) (Job, error) {
	if observer == nil {
		return Job{}, ErrUnavailable
	}
	return store.writeActionEvent(ctx, nil, actionabi.Event{
		ABI: actionabi.Version, JobID: jobID, InvocationID: invocationID, Type: "result",
		Result: &actionabi.Result{Outcome: actionabi.Cancelled},
	}, observer, true)
}

func (store *Store) writeActionEvent(ctx context.Context, lease *ExecutionLease, event actionabi.Event, observer JobCommitObserver, queued bool) (Job, error) {
	if store == nil {
		return Job{}, ErrUnavailable
	}
	public, err := sanitizeActionEvent(event)
	if err != nil {
		return Job{}, err
	}
	var result Job
	write := func() error {
		return store.withState(ctx, func() error {
			job, exists := store.state.jobs[public.JobID]
			if !exists {
				return ErrNotFound
			}
			if job.Action == nil || (queued && job.Status != StatusQueued) || (!queued && job.Status != StatusRunning) {
				return ErrConflict
			}
			record, err := store.actionEventRecord(store.state, job, public, store.now().UTC())
			if err != nil {
				return err
			}
			if record.Job.Status.terminal() {
				previous := cloneJob(job)
				if err := observeJobCommit(ctx, observer, JobCommitIntent{
					Operation: JobCommitTransition, Previous: &previous, Next: cloneJob(*record.Job),
				}); err != nil {
					return err
				}
			}
			if err := store.persistRecords(ctx, []journalRecord{record}); err != nil {
				return err
			}
			result = cloneJob(*record.Job)
			return nil
		})
	}
	if queued {
		err = write()
	} else {
		err = lease.withOwnership(store.directory, write)
	}
	return result, err
}

// ReplayAction returns the actual persisted marker when fromSeq has been
// pruned; it never synthesizes a subscriber-specific event or renumbers history.
func (store *Store) ReplayAction(ctx context.Context, jobID string, fromSeq uint64, limit int) (ActionEventPage, error) {
	if limit < 0 || limit > 1000 {
		return ActionEventPage{}, invalidError("action replay limit must be between 0 and 1000")
	}
	if limit == 0 {
		limit = 100
	}
	var page ActionEventPage
	err := store.withState(ctx, func() error {
		job, exists := store.state.jobs[jobID]
		if !exists {
			return ErrNotFound
		}
		if job.Action == nil || fromSeq > job.Action.LastSeq {
			return invalidError("invalid action replay cursor")
		}
		page.LatestSeq, page.Outcome = job.Action.LastSeq, job.Action.Outcome
		events := store.state.events[jobID]
		start := sort.Search(len(events), func(i int) bool { return events[i].Action.Seq > fromSeq })
		end := min(start+limit, len(events))
		page.Events = make([]actionabi.Event, 0, end-start)
		for _, event := range events[start:end] {
			page.Events = append(page.Events, *cloneActionEvent(event.Action))
		}
		return nil
	})
	return page, err
}

func sanitizeActionEvent(event actionabi.Event) (actionabi.Event, error) {
	body, err := actionabi.EncodeExecutorEvent(event)
	if err != nil {
		return actionabi.Event{}, invalidError("invalid action event")
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	var object map[string]any
	if err := decoder.Decode(&object); err != nil {
		return actionabi.Event{}, invalidError("invalid action event")
	}
	public, err := sanitizePayload(object)
	if err != nil {
		return actionabi.Event{}, err
	}
	body, err = json.Marshal(public)
	if err != nil {
		return actionabi.Event{}, invalidError("invalid public action event")
	}
	projected, err := actionabi.DecodeExecutorEvent(append(body, '\n'))
	if err != nil {
		return actionabi.Event{}, invalidError("invalid public action event")
	}
	return projected, nil
}

func cloneActionEvent(event *actionabi.Event) *actionabi.Event {
	body, err := json.Marshal(event)
	if err != nil {
		panic("consolejobs: action event became unencodable")
	}
	var result actionabi.Event
	if err := json.Unmarshal(body, &result); err != nil {
		panic("consolejobs: action event became undecodable")
	}
	return &result
}

func sameActionBinding(left, right *ActionState) bool {
	if left == nil || right == nil {
		return left == right
	}
	return left.ABI == right.ABI && left.Name == right.Name && left.InvocationID == right.InvocationID &&
		sameActionInvocationPolicy(left.Policy, right.Policy)
}

func sameActionCreate(job Job, spec CreateSpec) bool {
	if job.Action == nil || spec.action == nil {
		return job.Action == nil && spec.action == nil
	}
	// A retry can generate a fresh invocation ID but must return the original
	// job's binding. Never trust a caller-supplied digest to equate two actions.
	return job.Action.ABI == spec.action.ABI && job.Action.Name == spec.action.Name &&
		sameActionInvocationPolicy(job.Action.Policy, spec.action.Policy) && job.Mutating == spec.Mutating && job.WorkspaceID == spec.WorkspaceID && reflect.DeepEqual(job.Request, spec.Request)
}

func validateActionCreate(spec CreateSpec) error {
	body, err := json.Marshal(spec.Request)
	if err != nil {
		return invalidError("invalid public action parameters")
	}
	if _, err := actionabi.EncodeRequest(actionabi.Request{
		ABI: spec.action.ABI, JobID: "pending", InvocationID: spec.action.InvocationID, Action: spec.action.Name, Parameters: body,
	}); err != nil {
		return invalidError("invalid public action invocation")
	}
	return nil
}

func interruptedActionEvent(job Job) actionabi.Event {
	return actionabi.Event{
		ABI: actionabi.Version, JobID: job.ID, InvocationID: job.Action.InvocationID, Type: "error",
		Error: &actionabi.Failure{Outcome: actionabi.Unknown, Code: "daemon_restarted", Message: "Action execution state was lost during daemon restart"},
	}
}

func actionOutcome(event actionabi.Event) actionabi.Outcome {
	if event.Result != nil {
		return event.Result.Outcome
	}
	if event.Error != nil {
		return event.Error.Outcome
	}
	return ""
}

func actionJobAfterEvent(previous Job, event actionabi.Event, now time.Time) (Job, error) {
	if previous.Action == nil || event.JobID != previous.ID || event.InvocationID != previous.Action.InvocationID || previous.Status.terminal() || event.Type == "truncated" {
		return Job{}, ErrConflict
	}
	if previous.Status == StatusQueued {
		if actionOutcome(event) != actionabi.Cancelled && !isActionNotStarted(event) {
			return Job{}, ErrConflict
		}
	} else if previous.Status != StatusRunning || isActionNotStarted(event) ||
		(actionOutcome(event) == actionabi.Cancelled && previous.Action.Cancellation == nil) {
		return Job{}, ErrConflict
	}
	if previous.Revision == ^uint64(0) {
		return Job{}, invalidError("action job revision exhausted")
	}
	next := cloneJob(previous)
	next.Revision++
	next.Action.LastSeq = event.Seq
	next.Action.Outcome = actionOutcome(event)
	if next.Action.Outcome == "" {
		return next, nil
	}
	next.FinishedAt = cloneTime(&now)
	switch next.Action.Outcome {
	case actionabi.Succeeded:
		next.Status = StatusSucceeded
		next.Progress = 100
		decoder := json.NewDecoder(bytes.NewReader(event.Result.Value))
		decoder.UseNumber()
		var value map[string]any
		if err := decoder.Decode(&value); err != nil {
			return Job{}, invalidError("invalid action result")
		}
		next.Result = map[string]any{"changed": *event.Result.Changed, "value": value}
	case actionabi.Cancelled:
		next.Status = StatusCanceled
	case actionabi.Failed, actionabi.Unknown:
		next.Status = StatusFailed
		if next.Action.Outcome == actionabi.Unknown {
			next.Status = StatusInterrupted
		}
		next.Error = &JobError{Code: event.Error.Code, Message: event.Error.Message}
		next.NeedsCompensationCheck = next.Mutating && previous.Status == StatusRunning
	default:
		return Job{}, invalidError("invalid action outcome")
	}
	return next, nil
}

func validateActionJob(job Job) error {
	if job.Action == nil {
		if job.Kind == ActionJobKind {
			return errors.New("action job is missing its binding")
		}
		return nil
	}
	if job.Kind != ActionJobKind {
		return errors.New("action binding on a legacy job")
	}
	if err := validateActionInvocationPolicy(job); err != nil {
		return err
	}
	if err := validateActionControl(job); err != nil {
		return err
	}
	if !job.Status.terminal() && (job.Result != nil || job.Error != nil || job.NeedsCompensationCheck) {
		return errors.New("nonterminal action job has terminal fields")
	}
	if (job.Status == StatusSucceeded && job.Progress != 100) || (job.Status != StatusSucceeded && job.Progress != 0) {
		return errors.New("action counters must not be folded into legacy job progress")
	}
	body, err := json.Marshal(job.Request)
	if err != nil {
		return errors.New("invalid action request")
	}
	if _, err := actionabi.EncodeRequest(actionabi.Request{
		ABI: job.Action.ABI, JobID: job.ID, InvocationID: job.Action.InvocationID, Action: job.Action.Name, Parameters: body,
	}); err != nil {
		return errors.New("invalid action binding or parameters")
	}
	want := map[Status]actionabi.Outcome{
		StatusQueued: "", StatusRunning: "", StatusSucceeded: actionabi.Succeeded,
		StatusFailed: actionabi.Failed, StatusCanceled: actionabi.Cancelled, StatusInterrupted: actionabi.Unknown,
	}[job.Status]
	if job.Action.Outcome != want || (job.Status == StatusQueued && job.Action.LastSeq != 0) || (job.Status.terminal() && job.Action.LastSeq == 0) {
		return errors.New("action outcome does not match job state")
	}
	return nil
}

func validateActionEvent(event Event) error {
	if event.Action == nil {
		return nil
	}
	frame := event.Action
	if event.Data != nil || event.Kind != "action."+frame.Type || event.JobID != frame.JobID {
		return errors.New("invalid action event envelope")
	}
	if _, err := actionabi.EncodeJournalEvent(*frame); err != nil {
		return errors.New("invalid persisted action frame")
	}
	if frame.Type != "truncated" {
		candidate := *cloneActionEvent(frame)
		candidate.Seq = 0
		public, err := sanitizeActionEvent(candidate)
		if err != nil || !reflect.DeepEqual(public, candidate) {
			return errors.New("action event is not sanitized")
		}
	}
	return nil
}
