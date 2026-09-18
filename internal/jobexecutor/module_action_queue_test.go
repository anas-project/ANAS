package jobexecutor

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/anas-project/ANAS/internal/actionabi"
	"github.com/anas-project/ANAS/internal/consolejobs"
)

func moduleActionQueueFixture(t *testing.T) (*consolejobs.Store, *consolejobs.ExecutionLease, *ModuleActionRegistry, consolejobs.Job) {
	t.Helper()
	directory := filepath.Join(t.TempDir(), "store")
	store, err := consolejobs.Open(directory, consolejobs.Options{})
	if err != nil {
		t.Fatal(err)
	}
	lease, err := consolejobs.AcquireExecutionLease(context.Background(), directory)
	if err != nil {
		_ = store.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := lease.Close(); err != nil {
			t.Error(err)
		}
		if err := store.Close(); err != nil {
			t.Error(err)
		}
	})
	definition := ModuleActionDefinition{
		Name: "module.example.inspect", Module: "example", DeploymentID: "test-deployment",
		DescriptorDigest: strings.Repeat("1", 64), ExecutableDigest: strings.Repeat("2", 64),
		ModuleRoot: t.TempDir(), Executable: "executor", Risk: "normal", Cancellable: "true",
		Timeout: time.Second, CancelGrace: time.Second,
		Normalize: func(raw json.RawMessage) (json.RawMessage, error) { return raw, nil },
		Check:     func(context.Context, ModuleActionCall) error { return nil },
		Acquire:   func(context.Context, ModuleActionCall) (func(), error) { return func() {}, nil },
		// Only trusted frames constructed by these tests use identity projection.
		Project: func(event actionabi.Event) (actionabi.Event, error) { return event, nil },
	}
	registry, err := NewModuleActionRegistry([]ModuleActionDefinition{definition})
	if err != nil {
		t.Fatal(err)
	}
	created, err := registry.Create(context.Background(), store, consolejobs.CreateSpec{
		WorkspaceID: "test-workspace", Request: map[string]any{"mode": "inspect"},
		Idempotency: consolejobs.IdempotencyInput{Principal: "creator", Method: "POST", CanonicalPath: "/actions/invoke", Key: "test-key"},
	}, definition.Name, moduleActionAllowCommit())
	if err != nil {
		t.Fatal(err)
	}
	return store, lease, registry, created.Job
}

func moduleActionAllowCommit() consolejobs.JobCommitObserver {
	return consolejobs.JobCommitObserverFunc(func(context.Context, consolejobs.JobCommitIntent) error { return nil })
}

func moduleActionWorkerFixture(t *testing.T, store *consolejobs.Store, lease *consolejobs.ExecutionLease, registry *ModuleActionRegistry,
	authorize func(context.Context, string, consolejobs.Job) error, audit func(context.Context, string, consolejobs.JobCommitIntent) error) *ModuleActionWorker {
	t.Helper()
	worker, err := NewModuleActionWorker(ModuleActionWorkerOptions{
		Registry: registry, Store: store, Lease: lease, Workspaces: []string{"test-workspace"},
		Observer: moduleActionAllowCommit(),
		AuthorizeCancel: func(ctx context.Context, actor string, job consolejobs.Job) (consolejobs.JobCommitObserver, error) {
			if err := authorize(ctx, actor, job); err != nil {
				return nil, err
			}
			return consolejobs.JobCommitObserverFunc(func(ctx context.Context, intent consolejobs.JobCommitIntent) error {
				if intent.Previous == nil {
					return errors.New("missing cancellation binding")
				}
				if err := authorize(ctx, actor, *intent.Previous); err != nil {
					return err
				}
				return audit(ctx, actor, intent)
			}), nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return worker
}

func TestModuleActionQueuedCancelIsAuditedAndDoesNotStart(t *testing.T) {
	store, lease, registry, job := moduleActionQueueFixture(t)
	auditFailed := true
	worker := moduleActionWorkerFixture(t, store, lease, registry,
		func(_ context.Context, actor string, current consolejobs.Job) error {
			if actor != "other-admin" || current.ID != job.ID {
				return errors.New("denied")
			}
			return nil
		}, func(_ context.Context, actor string, intent consolejobs.JobCommitIntent) error {
			if actor != "other-admin" || intent.Previous == nil || intent.Previous.Status != consolejobs.StatusQueued {
				t.Error("cancel audit lost actor or original state")
			}
			if auditFailed {
				return errors.New("audit unavailable")
			}
			return nil
		})
	if _, err := worker.Cancel(context.Background(), "other-admin", job.ID); err == nil {
		t.Fatal("cancel succeeded without audit")
	}
	current, err := store.Get(context.Background(), job.ID)
	if err != nil || current.Status != consolejobs.StatusQueued || current.Action.LastSeq != 0 {
		t.Fatalf("audit failure changed queued job: %v, %s", err, current.Status)
	}
	auditFailed = false
	current, err = worker.Cancel(context.Background(), "other-admin", job.ID)
	if err != nil || current.Status != consolejobs.StatusCanceled || current.StartedAt != nil || current.Action.Outcome != actionabi.Cancelled {
		t.Fatalf("queued cancellation: %v, %s", err, current.Status)
	}
	if _, err := store.StartActionObserved(context.Background(), job.ID, lease, moduleActionAllowCommit()); !errors.Is(err, consolejobs.ErrConflict) {
		t.Fatalf("canceled job started: %v", err)
	}
}

func TestModuleActionRunningCancelPersistsIntentBeforeSignal(t *testing.T) {
	store, lease, registry, job := moduleActionQueueFixture(t)
	if _, err := store.StartActionObserved(context.Background(), job.ID, lease, moduleActionAllowCommit()); err != nil {
		t.Fatal(err)
	}
	control := &moduleActionControl{invocation: job.Action.InvocationID, signal: make(chan struct{})}
	auditFailed, authorized := true, true
	worker := moduleActionWorkerFixture(t, store, lease, registry,
		func(context.Context, string, consolejobs.Job) error {
			if !authorized {
				return errors.New("revoked")
			}
			return nil
		}, func(_ context.Context, _ string, intent consolejobs.JobCommitIntent) error {
			select {
			case <-control.signal:
				t.Error("notification preceded audit")
			default:
			}
			if intent.Operation != consolejobs.JobCommitCancelRequest || intent.Next.Status != consolejobs.StatusRunning {
				t.Error("cancel request was represented as completion")
			}
			if auditFailed {
				return errors.New("unavailable")
			}
			return nil
		})
	worker.running[job.ID] = control
	if _, err := worker.Cancel(context.Background(), "admin", job.ID); err == nil {
		t.Fatal("cancel ignored audit failure")
	}
	select {
	case <-control.signal:
		t.Fatal("cancel notified despite failed commit")
	default:
	}
	auditFailed = false
	current, err := worker.Cancel(context.Background(), "admin", job.ID)
	if err != nil || current.Status != consolejobs.StatusRunning || current.Action.LastSeq != 0 || current.Action.Outcome != "" || current.Action.Cancellation == nil || current.Action.Cancellation.Actor != "admin" {
		t.Fatalf("cancel intent changed outcome: %v", err)
	}
	select {
	case <-control.signal:
	default:
		t.Fatal("durable cancellation did not notify")
	}
	persisted, err := store.Get(context.Background(), job.ID)
	if err != nil || persisted.Action.Cancellation == nil || persisted.Action.Cancellation.RequestedAt.IsZero() {
		t.Fatalf("missing durable cancel intent: %v", err)
	}
	duplicate, err := worker.Cancel(context.Background(), "admin", job.ID)
	if err != nil || duplicate.Action.LastSeq != current.Action.LastSeq || duplicate.Revision != current.Revision {
		t.Fatalf("duplicate cancel changed sequence: %v", err)
	}
	authorized = false
	if _, err := worker.Cancel(context.Background(), "admin", job.ID); !errors.Is(err, ErrModuleActionDenied) {
		t.Fatalf("revoked actor could repeat cancel: %v", err)
	}
}

func TestModuleActionPermissionIsRecheckedBeforeExecution(t *testing.T) {
	store, lease, registry, job := moduleActionQueueFixture(t)
	definition, _ := registry.lookup(job.Action.Name)
	definition.Check = func(context.Context, ModuleActionCall) error { return errors.New("permission revoked") }
	deniedRegistry, err := NewModuleActionRegistry([]ModuleActionDefinition{definition})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := deniedRegistry.Run(context.Background(), store, lease, job.ID, nil, moduleActionAllowCommit()); !errors.Is(err, ErrModuleActionDenied) {
		t.Fatalf("expected current permission denial: %v", err)
	}
	current, err := store.Get(context.Background(), job.ID)
	if err != nil || current.Status != consolejobs.StatusFailed || current.StartedAt != nil || current.Error == nil || current.Error.Code != "action_not_started" {
		t.Fatalf("denial did not persist a preflight rejection: %v", err)
	}
}

func TestModuleActionWorkerRefusesUnreconciledRunningJob(t *testing.T) {
	store, lease, registry, job := moduleActionQueueFixture(t)
	if _, err := store.StartActionObserved(context.Background(), job.ID, lease, moduleActionAllowCommit()); err != nil {
		t.Fatal(err)
	}
	worker := moduleActionWorkerFixture(t, store, lease, registry,
		func(context.Context, string, consolejobs.Job) error { return nil },
		func(context.Context, string, consolejobs.JobCommitIntent) error { return nil })
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := worker.Run(ctx); !errors.Is(err, ErrModuleActionContainment) {
		t.Fatalf("unreconciled invocation was resumed: %v", err)
	}
	if err := worker.Run(ctx); !errors.Is(err, ErrModuleActionUnavailable) {
		t.Fatalf("worker allowed automatic restart: %v", err)
	}
}

func TestModuleActionRecorderRequiresEOFAndExitEvidence(t *testing.T) {
	for _, test := range []struct {
		name                                                            string
		trailing                                                        string
		readEOF, forced, cancelResult, cancelRequested, projectionFails bool
		want                                                            actionabi.Outcome
	}{
		{name: "success", readEOF: true, want: actionabi.Succeeded},
		{name: "missing-eof", want: actionabi.Unknown},
		{name: "trailing-data", readEOF: true, trailing: "garbage\n", want: actionabi.Unknown},
		{name: "forced", readEOF: true, forced: true, want: actionabi.Unknown},
		{name: "unsolicited-cancel", readEOF: true, cancelResult: true, want: actionabi.Unknown},
		{name: "confirmed-cancel", readEOF: true, cancelResult: true, cancelRequested: true, want: actionabi.Cancelled},
		{name: "projection-rejected", readEOF: true, projectionFails: true, want: actionabi.Unknown},
	} {
		t.Run(test.name, func(t *testing.T) {
			store, lease, registry, job := moduleActionQueueFixture(t)
			var err error
			job, err = store.StartActionObserved(context.Background(), job.ID, lease, moduleActionAllowCommit())
			if err != nil {
				t.Fatal(err)
			}
			if test.cancelRequested {
				job, err = store.RequestActionCancelObserved(context.Background(), job.ID, job.Action.InvocationID, "admin", moduleActionAllowCommit())
				if err != nil {
					t.Fatal(err)
				}
			}
			changed := false
			result := &actionabi.Result{Outcome: actionabi.Succeeded, Changed: &changed, Value: json.RawMessage(`{}`)}
			if test.cancelResult {
				result = &actionabi.Result{Outcome: actionabi.Cancelled}
			}
			frame, err := actionabi.EncodeExecutorEvent(actionabi.Event{
				ABI: actionabi.Version, JobID: job.ID, InvocationID: job.Action.InvocationID, Type: "result", Result: result,
			})
			if err != nil {
				t.Fatal(err)
			}
			definition, _ := registry.lookup(job.Action.Name)
			project := definition.Project
			if test.projectionFails {
				project = func(actionabi.Event) (actionabi.Event, error) {
					return actionabi.Event{}, errors.New("private-output-must-not-persist")
				}
			}
			recorder, err := NewActionRecorder(ActionRecorderOptions{
				Store: store, Lease: lease, Job: job, Output: bytes.NewReader(append(frame, []byte(test.trailing)...)),
				Project: project, Observer: moduleActionAllowCommit(),
			})
			if err != nil {
				t.Fatal(err)
			}
			firstErr := recorder.Next(context.Background())
			if firstErr == nil && test.readEOF {
				for {
					if err := recorder.Next(context.Background()); err != nil {
						if test.trailing == "" && err != io.EOF {
							t.Fatal(err)
						}
						break
					}
				}
			}
			finished, finishErr := recorder.Finish(context.Background(), actionabi.ExitState{
				ProcessExited: true, ExitCode: 0, Forced: test.forced, CancelRequested: test.cancelRequested,
			})
			if finished.Action == nil || finished.Action.Outcome != test.want || (test.want == actionabi.Unknown && finishErr == nil) {
				t.Fatalf("wrong terminal outcome, error %v", finishErr)
			}
			page, err := store.ReplayAction(context.Background(), job.ID, 0, 10)
			if err != nil || len(page.Events) != 1 {
				t.Fatalf("terminal was not committed exactly once: %v", err)
			}
			if finished.Error != nil && strings.Contains(finished.Error.Message, "private-output") {
				t.Fatal("projection error leaked")
			}
		})
	}
}
