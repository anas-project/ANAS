package jobexecutor

import (
	"context"
	"errors"
	"io"
	"reflect"

	"github.com/anas-project/ANAS/internal/actionabi"
	"github.com/anas-project/ANAS/internal/consolejobs"
)

// ActionEventStore is implemented by consolejobs.Store. It shares the journal
// and process-lifetime lease with the existing executor; it is not another job
// store or an action registry.
type ActionEventStore interface {
	AppendActionEvent(context.Context, *consolejobs.ExecutionLease, actionabi.Event) (consolejobs.Job, error)
	CompleteActionObserved(context.Context, *consolejobs.ExecutionLease, actionabi.Event, consolejobs.JobCommitObserver) (consolejobs.Job, error)
}

// ActionPublicProjection must be provided by the trusted action registry. It
// validates/projects public progress labels, warning/error text and result
// fields before ANY persistence. It may not change identity, event type, exact
// counters, outcome or changed. Never pass an identity function for untrusted
// executor output; the generic store sanitizer is only defense in depth.
type ActionPublicProjection func(actionabi.Event) (actionabi.Event, error)

type ActionRecorderOptions struct {
	Store    ActionEventStore
	Lease    *consolejobs.ExecutionLease
	Job      consolejobs.Job
	Output   io.Reader
	Project  ActionPublicProjection
	Observer consolejobs.JobCommitObserver
}

// ActionRecorder is the supervised-process adapter's durable event boundary.
// The adapter supplies a daemon-owned context, calls Next through actual EOF,
// reaps the process, and then calls Finish. On Next failure it must stop/drain
// and reap the executor with its bounded cleanup policy before Finish. This
// object never launches/kills a process and never uses subscriber disconnects
// as completion evidence. It is synchronous, not safe for concurrent callers.
type ActionRecorder struct {
	store      ActionEventStore
	lease      *consolejobs.ExecutionLease
	reader     *actionabi.ExecutionReader
	project    ActionPublicProjection
	observer   consolejobs.JobCommitObserver
	jobID      string
	invocation string
	pending    *actionabi.Event
	failed     bool
	finished   bool
}

func NewActionRecorder(options ActionRecorderOptions) (*ActionRecorder, error) {
	job := options.Job
	if options.Store == nil || options.Lease == nil || options.Project == nil || options.Observer == nil ||
		job.Action == nil || job.Kind != consolejobs.ActionJobKind || job.Status != consolejobs.StatusRunning ||
		job.Action.ABI != actionabi.Version || job.Action.LastSeq != 0 || job.Action.Outcome != "" {
		return nil, errors.New("action recorder requires a newly started, supervised action job")
	}
	reader, err := actionabi.NewExecutionReader(options.Output, job.ID, job.Action.InvocationID)
	if err != nil {
		return nil, err
	}
	return &ActionRecorder{
		store: options.Store, lease: options.Lease, reader: reader, project: options.Project, observer: options.Observer,
		jobID: job.ID, invocation: job.Action.InvocationID,
	}, nil
}

// Next persists only nonterminal public events. Even a valid success frame is
// held in memory until the real output EOF and process exit have been checked.
func (recorder *ActionRecorder) Next(ctx context.Context) error {
	if recorder == nil || recorder.failed || recorder.finished {
		return actionabi.ErrProtocol
	}
	frame, err := recorder.reader.Next()
	if err != nil {
		if err != io.EOF {
			recorder.failed = true
		}
		return err
	}
	// The projector gets a deep copy, so mutating it cannot also change the
	// reference used below to enforce identity and completion invariants.
	body, err := actionabi.EncodeExecutorEvent(frame)
	if err != nil {
		recorder.failed = true
		return actionabi.ErrProtocol
	}
	copy, err := actionabi.DecodeExecutorEvent(body)
	if err != nil {
		recorder.failed = true
		return actionabi.ErrProtocol
	}
	public, err := recorder.project(copy)
	if err != nil || !sameActionEventSemantics(frame, public) {
		recorder.failed = true
		return errors.New("action public event projection failed")
	}
	// Re-encode/re-decode both validates and detaches the projected result from
	// any memory still retained by the registry callback.
	body, err = actionabi.EncodeExecutorEvent(public)
	if err == nil {
		public, err = actionabi.DecodeExecutorEvent(body)
	}
	if err != nil {
		recorder.failed = true
		return errors.New("action public event projection failed")
	}
	if public.Type == "result" || public.Type == "error" {
		recorder.pending = &public
		return nil
	}
	_, err = recorder.store.AppendActionEvent(ctx, recorder.lease, public)
	if err != nil {
		recorder.failed = true
	}
	return err
}

// Finish must be called only after process cleanup. Missing/contradictory exit
// evidence, a forced kill, parse/projection/persistence failure, or trailing
// bytes replace any provisional terminal with a stable unknown event. The
// terminal write should have its own bounded daemon cleanup context.
func (recorder *ActionRecorder) Finish(ctx context.Context, exit actionabi.ExitState) (consolejobs.Job, error) {
	if recorder == nil || recorder.finished {
		return consolejobs.Job{}, actionabi.ErrProtocol
	}
	recorder.finished = true
	outcome, completionErr := recorder.reader.Finish(exit)
	if recorder.failed || recorder.pending == nil {
		outcome, completionErr = actionabi.Unknown, actionabi.ErrUnknownOutcome
	}
	terminal := recorder.pending
	if outcome == actionabi.Unknown {
		terminal = &actionabi.Event{
			ABI: actionabi.Version, JobID: recorder.jobID, InvocationID: recorder.invocation, Type: "error",
			Error: &actionabi.Failure{Outcome: actionabi.Unknown, Code: "execution_unconfirmed", Message: "Action completion could not be confirmed"},
		}
	}
	job, err := recorder.store.CompleteActionObserved(ctx, recorder.lease, *terminal, recorder.observer)
	return job, errors.Join(completionErr, err)
}

func sameActionEventSemantics(raw, public actionabi.Event) bool {
	if raw.ABI != public.ABI || raw.JobID != public.JobID || raw.InvocationID != public.InvocationID || raw.Seq != public.Seq || raw.Type != public.Type {
		return false
	}
	switch raw.Type {
	case "progress":
		return raw.Progress != nil && public.Progress != nil &&
			reflect.DeepEqual(raw.Progress.Current, public.Progress.Current) && reflect.DeepEqual(raw.Progress.Total, public.Progress.Total) &&
			reflect.DeepEqual(raw.Progress.TotalEstimated, public.Progress.TotalEstimated) && raw.Progress.Unit == public.Progress.Unit
	case "warning":
		return public.Warning != nil
	case "result":
		return raw.Result != nil && public.Result != nil && raw.Result.Outcome == public.Result.Outcome && reflect.DeepEqual(raw.Result.Changed, public.Result.Changed)
	case "error":
		return raw.Error != nil && public.Error != nil && raw.Error.Outcome == public.Error.Outcome
	default:
		return false
	}
}
