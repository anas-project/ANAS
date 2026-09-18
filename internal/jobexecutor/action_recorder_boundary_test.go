package jobexecutor

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/anas-project/ANAS/internal/actionabi"
	"github.com/anas-project/ANAS/internal/consolejobs"
)

// These are recorder-boundary tests, not real process/host acceptance. The
// identity projector is used only with test-owned frames, never in production.
func TestActionRecorderDefersTerminalUntilRealEOF(t *testing.T) {
	store := &recorderBoundaryStore{}
	frame := recorderBoundaryTerminal()
	recorder := newBoundaryRecorder(t, store, []actionabi.Event{frame}, func(event actionabi.Event) (actionabi.Event, error) {
		event.Result.Value = json.RawMessage(`{"public":true}`)
		return event, nil
	})
	ctx := context.Background()
	if err := recorder.Next(ctx); err != nil {
		t.Fatal(err)
	}
	if len(store.events) != 0 || store.completions != 0 {
		t.Fatal("provisional success was persisted")
	}
	if err := recorder.Next(ctx); err != io.EOF {
		t.Fatalf("expected actual EOF: %v", err)
	}
	if store.completions != 0 {
		t.Fatal("EOF without exit evidence committed a terminal")
	}
	if _, err := recorder.Finish(ctx, actionabi.ExitState{ProcessExited: true, ExitCode: 0}); err != nil {
		t.Fatal(err)
	}
	if store.completions != 1 || len(store.events) != 1 || store.events[0].Result == nil {
		t.Fatal("missing atomic completion")
	}
	if string(store.events[0].Result.Value) != `{"public":true}` {
		t.Fatal("public result projection was not used")
	}
	if _, err := recorder.Finish(ctx, actionabi.ExitState{ProcessExited: true}); !errors.Is(err, actionabi.ErrProtocol) || store.completions != 1 {
		t.Fatal("terminal could be committed twice")
	}
}

func TestActionRecorderRejectsAlteredSemanticsAndPrivateErrors(t *testing.T) {
	const secret = "private-recorder-sentinel"
	cases := []struct {
		name      string
		project   ActionPublicProjection
		appendErr error
	}{
		{"projector_error", func(actionabi.Event) (actionabi.Event, error) { return actionabi.Event{}, errors.New(secret) }, nil},
		{"counter_rewrite", func(event actionabi.Event) (actionabi.Event, error) { *event.Progress.Current = 0; return event, nil }, nil},
		{"identity_rewrite", func(event actionabi.Event) (actionabi.Event, error) {
			event.InvocationID = "other-call"
			return event, nil
		}, nil},
		{"append_failure", func(event actionabi.Event) (actionabi.Event, error) { return event, nil }, errors.New(secret)},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			store := &recorderBoundaryStore{appendErr: test.appendErr}
			current := uint64(1)
			progress := actionabi.Event{ABI: actionabi.Version, JobID: "job-1", InvocationID: "call-1", Type: "progress", Progress: &actionabi.Progress{Phase: "checking", Current: &current, Unit: "items"}}
			recorder := newBoundaryRecorder(t, store, []actionabi.Event{progress}, test.project)
			if err := recorder.Next(context.Background()); err == nil {
				t.Fatal("invalid projection/persistence was accepted")
			}
			_, err := recorder.Finish(context.Background(), actionabi.ExitState{ProcessExited: true, StreamEOF: true, ExitCode: 0})
			if !errors.Is(err, actionabi.ErrUnknownOutcome) {
				t.Fatalf("execution was not unknown: %v", err)
			}
			if store.completions != 1 || len(store.events) != 1 || store.events[0].Error == nil || store.events[0].Error.Outcome != actionabi.Unknown {
				t.Fatal("missing unknown terminal")
			}
			body, marshalErr := json.Marshal(store.events)
			if marshalErr != nil || strings.Contains(string(body), secret) || strings.Contains(err.Error(), secret) {
				t.Fatal("private error reached the terminal")
			}
		})
	}
}

func TestActionRecorderCannotConfirmSuccessWithTrailingEvent(t *testing.T) {
	store := &recorderBoundaryStore{}
	terminal := recorderBoundaryTerminal()
	recorder := newBoundaryRecorder(t, store, []actionabi.Event{terminal, terminal}, func(event actionabi.Event) (actionabi.Event, error) { return event, nil })
	if err := recorder.Next(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := recorder.Next(context.Background()); !errors.Is(err, actionabi.ErrProtocol) {
		t.Fatal("duplicate terminal accepted")
	}
	_, err := recorder.Finish(context.Background(), actionabi.ExitState{ProcessExited: true, StreamEOF: true, ExitCode: 0})
	if !errors.Is(err, actionabi.ErrUnknownOutcome) || len(store.events) != 1 || store.events[0].Error == nil || store.events[0].Error.Outcome != actionabi.Unknown {
		t.Fatal("trailing output became success")
	}
}

func TestActionRecorderRejectsMissingPublicProjection(t *testing.T) {
	options := recorderBoundaryOptions(&recorderBoundaryStore{}, bytes.NewReader(nil), nil)
	if _, err := NewActionRecorder(options); err == nil {
		t.Fatal("recorder accepted absent public projection")
	}
}

type recorderBoundaryStore struct {
	events      []actionabi.Event
	completions int
	appendErr   error
}

func (store *recorderBoundaryStore) AppendActionEvent(_ context.Context, _ *consolejobs.ExecutionLease, event actionabi.Event) (consolejobs.Job, error) {
	if store.appendErr != nil {
		return consolejobs.Job{}, store.appendErr
	}
	store.events = append(store.events, event)
	return consolejobs.Job{}, nil
}

func (store *recorderBoundaryStore) CompleteActionObserved(_ context.Context, _ *consolejobs.ExecutionLease, event actionabi.Event, _ consolejobs.JobCommitObserver) (consolejobs.Job, error) {
	store.completions++
	store.events = append(store.events, event)
	return consolejobs.Job{}, nil
}

func recorderBoundaryTerminal() actionabi.Event {
	changed := false
	return actionabi.Event{ABI: actionabi.Version, JobID: "job-1", InvocationID: "call-1", Type: "result", Result: &actionabi.Result{Outcome: actionabi.Succeeded, Changed: &changed, Value: json.RawMessage(`{"private":"not-for-persistence"}`)}}
}

func recorderBoundaryOptions(store ActionEventStore, output io.Reader, project ActionPublicProjection) ActionRecorderOptions {
	return ActionRecorderOptions{
		Store:    store,
		Lease:    &consolejobs.ExecutionLease{},
		Job:      consolejobs.Job{ID: "job-1", Kind: consolejobs.ActionJobKind, Status: consolejobs.StatusRunning, Action: &consolejobs.ActionState{ABI: actionabi.Version, Name: "module.incus.status", InvocationID: "call-1"}},
		Output:   output,
		Project:  project,
		Observer: consolejobs.JobCommitObserverFunc(func(context.Context, consolejobs.JobCommitIntent) error { return nil }),
	}
}

func newBoundaryRecorder(t *testing.T, store ActionEventStore, frames []actionabi.Event, project ActionPublicProjection) *ActionRecorder {
	t.Helper()
	var output bytes.Buffer
	for _, frame := range frames {
		body, err := actionabi.EncodeExecutorEvent(frame)
		if err != nil {
			t.Fatal(err)
		}
		output.Write(body)
	}
	recorder, err := NewActionRecorder(recorderBoundaryOptions(store, &output, project))
	if err != nil {
		t.Fatal(err)
	}
	return recorder
}
