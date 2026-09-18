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

// This fixture tests the recorder, not the journal's durability or locks.
// Its identity projection is deliberately restricted to synthetic test frames.
type recorderRegressionStore struct {
	job        consolejobs.Job
	events     []actionabi.Event
	terminals  []actionabi.Event
	appendFail bool
}

func (store *recorderRegressionStore) AppendActionEvent(_ context.Context, _ *consolejobs.ExecutionLease, event actionabi.Event) (consolejobs.Job, error) {
	if store.appendFail {
		return consolejobs.Job{}, errors.New("private-storage-failure")
	}
	store.events = append(store.events, event)
	return store.job, nil
}

func (store *recorderRegressionStore) CompleteActionObserved(_ context.Context, _ *consolejobs.ExecutionLease, event actionabi.Event, _ consolejobs.JobCommitObserver) (consolejobs.Job, error) {
	store.terminals = append(store.terminals, event)
	if event.Result != nil {
		store.job.Action.Outcome = event.Result.Outcome
		store.job.Status = consolejobs.StatusSucceeded
		if event.Result.Outcome == actionabi.Cancelled {
			store.job.Status = consolejobs.StatusCanceled
		}
	} else if event.Error != nil {
		store.job.Action.Outcome = event.Error.Outcome
		store.job.Status = consolejobs.StatusFailed
		if event.Error.Outcome == actionabi.Unknown {
			store.job.Status = consolejobs.StatusInterrupted
		}
		store.job.Error = &consolejobs.JobError{Code: event.Error.Code, Message: event.Error.Message}
	}
	return store.job, nil
}

func recorderRegressionJob() consolejobs.Job {
	return consolejobs.Job{ID: "job-recorder", Kind: consolejobs.ActionJobKind, Status: consolejobs.StatusRunning,
		Action: &consolejobs.ActionState{ABI: actionabi.Version, Name: "module.sample.status", InvocationID: "call-recorder"}}
}

func recorderRegressionSuccess() actionabi.Event {
	changed := true
	return actionabi.Event{ABI: actionabi.Version, JobID: "job-recorder", InvocationID: "call-recorder", Type: "result",
		Result: &actionabi.Result{Outcome: actionabi.Succeeded, Changed: &changed, Value: json.RawMessage(`{"safe":true}`)}}
}

func recorderRegressionProgress() actionabi.Event {
	current := uint64(9007199254740993)
	return actionabi.Event{ABI: actionabi.Version, JobID: "job-recorder", InvocationID: "call-recorder", Type: "progress",
		Progress: &actionabi.Progress{Phase: "copying", Current: &current, Unit: "bytes"}}
}

func recorderRegressionFrames(t *testing.T, events ...actionabi.Event) []byte {
	t.Helper()
	var output []byte
	for _, event := range events {
		frame, err := actionabi.EncodeExecutorEvent(event)
		if err != nil {
			t.Fatal(err)
		}
		output = append(output, frame...)
	}
	return output
}

func recorderRegressionOptions(store *recorderRegressionStore, output []byte) ActionRecorderOptions {
	return ActionRecorderOptions{Store: store, Lease: &consolejobs.ExecutionLease{}, Job: store.job, Output: bytes.NewReader(output),
		Project:  func(event actionabi.Event) (actionabi.Event, error) { return event, nil },
		Observer: consolejobs.JobCommitObserverFunc(func(context.Context, consolejobs.JobCommitIntent) error { return nil })}
}

func TestActionRecorderDefersTerminalUntilEOFAndExit(t *testing.T) {
	store := &recorderRegressionStore{job: recorderRegressionJob()}
	recorder, err := NewActionRecorder(recorderRegressionOptions(store, recorderRegressionFrames(t, recorderRegressionProgress(), recorderRegressionSuccess())))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := recorder.Next(ctx); err != nil {
		t.Fatal(err)
	}
	if len(store.events) != 1 || *store.events[0].Progress.Current != 9007199254740993 {
		t.Fatal("progress was dropped or rounded")
	}
	if err := recorder.Next(ctx); err != nil {
		t.Fatal(err)
	}
	if len(store.terminals) != 0 {
		t.Fatal("success persisted before actual EOF and exit")
	}
	if err := recorder.Next(ctx); err != io.EOF {
		t.Fatalf("expected EOF, got %v", err)
	}
	if _, err := recorder.Finish(ctx, actionabi.ExitState{ProcessExited: true, ExitCode: 0}); err != nil {
		t.Fatal(err)
	}
	if len(store.terminals) != 1 || store.terminals[0].Result == nil || store.terminals[0].Result.Outcome != actionabi.Succeeded {
		t.Fatal("missing confirmed success")
	}
	if _, err := recorder.Finish(ctx, actionabi.ExitState{ProcessExited: true}); !errors.Is(err, actionabi.ErrProtocol) || len(store.terminals) != 1 {
		t.Fatalf("duplicate terminal was allowed: %v", err)
	}
}

func TestActionRecorderProjectsBeforePersistenceAndDetachesResult(t *testing.T) {
	store := &recorderRegressionStore{job: recorderRegressionJob()}
	event := recorderRegressionSuccess()
	event.Result.Value = json.RawMessage(`{"safe":true,"unapproved":"private-fixture-value"}`)
	options := recorderRegressionOptions(store, recorderRegressionFrames(t, event))
	var retained json.RawMessage
	options.Project = func(event actionabi.Event) (actionabi.Event, error) {
		event.Result.Value = json.RawMessage(`{"safe":true}`)
		retained = event.Result.Value
		return event, nil
	}
	recorder, err := NewActionRecorder(options)
	if err != nil {
		t.Fatal(err)
	}
	if err := recorder.Next(context.Background()); err != nil {
		t.Fatal(err)
	}
	// The registry must not be able to alter a provisional result after Next.
	for index := range retained {
		retained[index] = 'x'
	}
	if err := recorder.Next(context.Background()); err != io.EOF {
		t.Fatal(err)
	}
	if _, err := recorder.Finish(context.Background(), actionabi.ExitState{ProcessExited: true, ExitCode: 0}); err != nil {
		t.Fatal(err)
	}
	if len(store.terminals) != 1 || string(store.terminals[0].Result.Value) != `{"safe":true}` {
		t.Fatal("unapproved or retained mutable result reached persistence")
	}
}

func TestActionRecorderRejectsSemanticProjectionChanges(t *testing.T) {
	tests := []struct {
		name    string
		event   actionabi.Event
		project ActionPublicProjection
	}{
		{"identity", recorderRegressionSuccess(), func(event actionabi.Event) (actionabi.Event, error) { event.JobID = "other"; return event, nil }},
		{"changed", recorderRegressionSuccess(), func(event actionabi.Event) (actionabi.Event, error) { *event.Result.Changed = false; return event, nil }},
		{"counter", recorderRegressionProgress(), func(event actionabi.Event) (actionabi.Event, error) { *event.Progress.Current = 0; return event, nil }},
		{"unit", recorderRegressionProgress(), func(event actionabi.Event) (actionabi.Event, error) { event.Progress.Unit = "files"; return event, nil }},
		{"project failure", recorderRegressionSuccess(), func(actionabi.Event) (actionabi.Event, error) {
			return actionabi.Event{}, errors.New("private-projection-failure")
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store := &recorderRegressionStore{job: recorderRegressionJob()}
			options := recorderRegressionOptions(store, recorderRegressionFrames(t, test.event))
			options.Project = test.project
			recorder, err := NewActionRecorder(options)
			if err != nil {
				t.Fatal(err)
			}
			if err := recorder.Next(context.Background()); err == nil || strings.Contains(err.Error(), "private-projection-failure") {
				t.Fatalf("invalid projection was accepted or exposed: %v", err)
			}
			if len(store.events) != 0 || len(store.terminals) != 0 {
				t.Fatal("unvalidated event was persisted")
			}
			_, err = recorder.Finish(context.Background(), actionabi.ExitState{ProcessExited: true, ExitCode: 0})
			if !errors.Is(err, actionabi.ErrUnknownOutcome) || len(store.terminals) != 1 || store.terminals[0].Error == nil || store.terminals[0].Error.Outcome != actionabi.Unknown {
				t.Fatalf("projection failure did not become unknown: %v", err)
			}
		})
	}
}

func TestActionRecorderAppendFailureNeverPersistsProvisionalSuccess(t *testing.T) {
	store := &recorderRegressionStore{job: recorderRegressionJob(), appendFail: true}
	recorder, err := NewActionRecorder(recorderRegressionOptions(store, recorderRegressionFrames(t, recorderRegressionProgress(), recorderRegressionSuccess())))
	if err != nil {
		t.Fatal(err)
	}
	if err := recorder.Next(context.Background()); err == nil {
		t.Fatal("expected store failure")
	}
	_, err = recorder.Finish(context.Background(), actionabi.ExitState{ProcessExited: true, ExitCode: 0})
	if !errors.Is(err, actionabi.ErrUnknownOutcome) || len(store.terminals) != 1 || store.terminals[0].Error == nil || store.terminals[0].Error.Code != "execution_unconfirmed" {
		t.Fatalf("store failure did not produce stable unknown: %v", err)
	}
}
