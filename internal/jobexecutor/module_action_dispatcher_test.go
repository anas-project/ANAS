package jobexecutor

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/anas-project/ANAS/internal/actionabi"
	"github.com/anas-project/ANAS/internal/consolejobs"
)

// No Module process is launched by these tests. The identity projector is
// TEST-ONLY and sees only constant fixture events, never executor output.
func moduleDispatcherFixture(t *testing.T) (*ModuleActionDispatcher, *consolejobs.Store, *consolejobs.ExecutionLease) {
	t.Helper()
	directory := t.TempDir()
	// Match the production store contract independently of Go's TempDir mode
	// and the test runner's umask; do not relax the store's permission checks.
	if err := os.Chmod(directory, 0700); err != nil {
		t.Fatal(err)
	}
	store, err := consolejobs.Open(directory, consolejobs.Options{EventCapacity: 4})
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
		Name: "module.fixture.inspect", Module: "fixture", DeploymentID: "deployment-fixture",
		DescriptorDigest: strings.Repeat("a", 64), ExecutableDigest: strings.Repeat("b", 64),
		ModuleRoot: directory, Executable: "executor", Risk: "normal", Cancellable: "true",
		Timeout: time.Second, CancelGrace: time.Second,
		Normalize: func(raw json.RawMessage) (json.RawMessage, error) { return raw, nil },
		Check:     func(context.Context, ModuleActionCall) error { return nil },
		Acquire:   func(context.Context, ModuleActionCall) (func(), error) { return func() {}, nil },
		Project:   func(event actionabi.Event) (actionabi.Event, error) { return event, nil },
	}
	other := definition
	other.Name, other.Cancellable = "module.fixture.observe", "false"
	registry, err := NewModuleActionRegistry([]ModuleActionDefinition{definition, other})
	if err != nil {
		t.Fatal(err)
	}
	observer := consolejobs.JobCommitObserverFunc(func(context.Context, consolejobs.JobCommitIntent) error { return nil })
	dispatcher, err := NewModuleActionDispatcher(ModuleActionDispatcherOptions{
		Registry: registry, Store: store, Lease: lease, Workspaces: []string{"workspace-fixture"},
		Observer: observer, PollInterval: time.Millisecond,
		Authorize: func(_ context.Context, access ModuleActionAccess) error {
			if access.Actor == "blocked" {
				return ErrModuleActionDenied
			}
			return nil
		},
		CancelObserver: func(string) consolejobs.JobCommitObserver { return observer },
		AuditCancel:    func(context.Context, string, consolejobs.Job) error { return nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	return dispatcher, store, lease
}

func invokeModuleDispatcherFixture(t *testing.T, dispatcher *ModuleActionDispatcher, key string) consolejobs.Job {
	t.Helper()
	created, err := dispatcher.Invoke(context.Background(), "creator", "workspace-fixture", "module.fixture.inspect", key, nil)
	if err != nil {
		t.Fatal(err)
	}
	return created.Job
}

func moduleDispatcherProgress(job consolejobs.Job) actionabi.Event {
	return actionabi.Event{
		ABI: actionabi.Version, JobID: job.ID, InvocationID: job.Action.InvocationID, Type: "progress",
		Progress: &actionabi.Progress{Phase: "inspecting"},
	}
}

func TestModuleActionDispatcherEnqueueSurvivesDisconnectAndScopesRetries(t *testing.T) {
	dispatcher, store, _ := moduleDispatcherFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	created, err := dispatcher.Invoke(ctx, "creator", "workspace-fixture", "module.fixture.inspect", "retry-key", nil)
	cancel()
	if err != nil {
		t.Fatal(err)
	}
	job, err := store.Get(context.Background(), created.Job.ID)
	if err != nil || job.Status != consolejobs.StatusQueued {
		t.Fatalf("request disconnect changed job: %v, %v", job.Status, err)
	}
	retry, err := dispatcher.Invoke(context.Background(), "creator", "workspace-fixture", "module.fixture.inspect", "retry-key", nil)
	if err != nil || !retry.Existing || retry.Job.ID != job.ID {
		t.Fatalf("retry did not reuse durable job: %#v, %v", retry, err)
	}
	other, err := dispatcher.Invoke(context.Background(), "creator", "workspace-fixture", "module.fixture.observe", "retry-key", nil)
	if err != nil || other.Job.ID == job.ID {
		t.Fatalf("different action reused same job: %#v, %v", other, err)
	}
	_, err = dispatcher.Invoke(context.Background(), "creator", "workspace-fixture", "module.fixture.inspect", "retry-key", map[string]any{"changed": true})
	if !errors.Is(err, consolejobs.ErrIdempotencyConflict) {
		t.Fatalf("different parameters were not rejected: %v", err)
	}
}

func TestModuleActionDispatcherHistoricalJobsHaveSharedPermissionView(t *testing.T) {
	dispatcher, _, _ := moduleDispatcherFixture(t)
	job := invokeModuleDispatcherFixture(t, dispatcher, "history")
	empty, err := NewModuleActionRegistry(nil)
	if err != nil {
		t.Fatal(err)
	}
	dispatcher.registry = empty // New deployment no longer contains the action.
	view, err := dispatcher.Get(context.Background(), "another-admin", job.ID)
	if err != nil || view.ID != job.ID || view.CreatedBy != "creator" {
		t.Fatalf("authorized historical view failed: %#v, %v", view, err)
	}
	jobs, err := dispatcher.List(context.Background(), "another-admin")
	if err != nil || len(jobs) != 1 || jobs[0].ID != job.ID {
		t.Fatalf("shared list failed: %#v, %v", jobs, err)
	}
	if _, err := dispatcher.Get(context.Background(), "blocked", job.ID); !errors.Is(err, ErrModuleActionDenied) {
		t.Fatalf("unauthorized read succeeded: %v", err)
	}
	jobs, err = dispatcher.List(context.Background(), "blocked")
	if err != nil || len(jobs) != 0 {
		t.Fatalf("unauthorized jobs leaked: %#v, %v", jobs, err)
	}
}

func TestModuleActionDispatcherAttachDoesNotOwnExecution(t *testing.T) {
	for _, revoke := range []bool{false, true} {
		t.Run(map[bool]string{false: "disconnect", true: "permission-revoked"}[revoke], func(t *testing.T) {
			dispatcher, store, lease := moduleDispatcherFixture(t)
			job := invokeModuleDispatcherFixture(t, dispatcher, "attach")
			if _, err := store.StartActionObserved(context.Background(), job.ID, lease, dispatcher.observer); err != nil {
				t.Fatal(err)
			}
			if _, err := store.AppendActionEvent(context.Background(), lease, moduleDispatcherProgress(job)); err != nil {
				t.Fatal(err)
			}
			var revoked atomic.Bool
			dispatcher.authorize = func(context.Context, ModuleActionAccess) error {
				if revoked.Load() {
					return ErrModuleActionDenied
				}
				return nil
			}
			disconnect := errors.New("fixture disconnected")
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			err := dispatcher.Attach(ctx, "another-admin", job.ID, 0, func(context.Context, actionabi.Event) error {
				if revoke {
					revoked.Store(true)
					return nil
				}
				return disconnect
			})
			want := disconnect
			if revoke {
				want = ErrModuleActionDenied
			}
			if !errors.Is(err, want) {
				t.Fatalf("attach error = %v, want %v", err, want)
			}
			latest, err := store.Get(context.Background(), job.ID)
			if err != nil || latest.Status != consolejobs.StatusRunning || latest.Action.Outcome != "" {
				t.Fatalf("subscriber ended execution: %#v, %v", latest, err)
			}
		})
	}
}

func TestModuleActionDispatcherAttachPreservesTruncationAndTerminal(t *testing.T) {
	dispatcher, store, lease := moduleDispatcherFixture(t)
	job := invokeModuleDispatcherFixture(t, dispatcher, "truncation")
	if _, err := store.StartActionObserved(context.Background(), job.ID, lease, dispatcher.observer); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 6; i++ {
		if _, err := store.AppendActionEvent(context.Background(), lease, moduleDispatcherProgress(job)); err != nil {
			t.Fatal(err)
		}
	}
	changed := false
	completed, err := store.CompleteActionObserved(context.Background(), lease, actionabi.Event{
		ABI: actionabi.Version, JobID: job.ID, InvocationID: job.Action.InvocationID, Type: "result",
		Result: &actionabi.Result{Outcome: actionabi.Succeeded, Changed: &changed, Value: json.RawMessage(`{}`)},
	}, dispatcher.observer)
	if err != nil {
		t.Fatal(err)
	}
	var events []actionabi.Event
	err = dispatcher.Attach(context.Background(), "another-admin", job.ID, 0, func(_ context.Context, event actionabi.Event) error {
		events = append(events, event)
		return nil
	})
	if err != nil || len(events) < 2 || events[0].Type != "truncated" {
		t.Fatalf("missing actual marker: %#v, %v", events, err)
	}
	last := events[len(events)-1]
	if last.Seq != completed.Action.LastSeq || last.Result == nil || last.Result.Outcome != actionabi.Succeeded {
		t.Fatalf("terminal/sequence lost: %#v", last)
	}
}

func TestModuleActionDispatcherCancelAuditAndDuplicateSignal(t *testing.T) {
	dispatcher, store, lease := moduleDispatcherFixture(t)
	job := invokeModuleDispatcherFixture(t, dispatcher, "cancel")
	if _, err := store.StartActionObserved(context.Background(), job.ID, lease, dispatcher.observer); err != nil {
		t.Fatal(err)
	}
	active := &moduleActionControl{invocation: job.Action.InvocationID, signal: make(chan struct{})}
	dispatcher.worker.running[job.ID] = active
	dispatcher.auditCancel = func(context.Context, string, consolejobs.Job) error { return errors.New("audit unavailable") }
	if _, err := dispatcher.Cancel(context.Background(), "another-admin", job.ID); !errors.Is(err, ErrModuleActionDenied) {
		t.Fatalf("cancel without audit: %v", err)
	}
	select {
	case <-active.signal:
		t.Fatal("failed audit signaled execution")
	default:
	}
	calls := 0
	dispatcher.auditCancel = func(_ context.Context, actor string, recorded consolejobs.Job) error {
		calls++
		if actor != "another-admin" || recorded.ID != job.ID {
			t.Fatal("audit lost cancelling actor or job")
		}
		return nil
	}
	for i := 0; i < 2; i++ {
		latest, err := dispatcher.Cancel(context.Background(), "another-admin", job.ID)
		if err != nil || latest.Status != consolejobs.StatusRunning {
			t.Fatalf("request reported completed cancellation: %v, %v", latest.Status, err)
		}
	}
	if calls != 1 {
		t.Fatalf("duplicate signal/audit: %d", calls)
	}
	select {
	case <-active.signal:
	default:
		t.Fatal("authorized cancellation did not signal")
	}
}

func TestModuleActionDispatcherRejectsRevokedExecutionBeforeLaunch(t *testing.T) {
	dispatcher, store, _ := moduleDispatcherFixture(t)
	job := invokeModuleDispatcherFixture(t, dispatcher, "denied")
	dispatcher.authorize = func(context.Context, ModuleActionAccess) error { return ErrModuleActionDenied }
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	done := make(chan struct{})
	var runErr error
	go func() {
		runErr = dispatcher.Run(ctx)
		close(done)
	}()
	defer func() {
		cancel()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("worker did not stop after cancellation")
		}
	}()
	for {
		latest, err := store.Get(ctx, job.ID)
		if err != nil {
			t.Fatal(err)
		}
		if latest.Status == consolejobs.StatusFailed {
			if latest.StartedAt != nil || latest.Error == nil || latest.Error.Code != "action_not_started" {
				t.Fatalf("denied job reported execution: %#v", latest)
			}
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("revoked job was not rejected before execution")
		case <-done:
			t.Fatalf("worker stopped before preflight rejection: %v", runErr)
		case <-time.After(time.Millisecond):
		}
	}
	cancel()
	<-done
	if runErr != nil {
		t.Fatalf("worker returned error after confirmed preflight rejection: %v", runErr)
	}
	if _, err := dispatcher.Invoke(context.Background(), "creator", "workspace-fixture", "module.fixture.inspect", "new", nil); !errors.Is(err, ErrModuleActionUnavailable) {
		t.Fatalf("stopped dispatcher accepted work: %v", err)
	}
}

func TestModuleActionDispatcherQueuedCancelAndNoncancellablePolicy(t *testing.T) {
	dispatcher, _, _ := moduleDispatcherFixture(t)
	job := invokeModuleDispatcherFixture(t, dispatcher, "queued-cancel")
	canceled, err := dispatcher.Cancel(context.Background(), "another-admin", job.ID)
	if err != nil || canceled.Status != consolejobs.StatusCanceled || canceled.Action.Outcome != actionabi.Cancelled || canceled.StartedAt != nil {
		t.Fatalf("queued cancellation = %#v, %v", canceled, err)
	}
	created, err := dispatcher.Invoke(context.Background(), "creator", "workspace-fixture", "module.fixture.observe", "not-cancellable", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := dispatcher.Cancel(context.Background(), "another-admin", created.Job.ID); !errors.Is(err, ErrModuleActionDenied) {
		t.Fatalf("false cancellation policy ignored: %v", err)
	}
}

func TestModuleActionDispatcherDoesNotResumeUnrecoveredExecution(t *testing.T) {
	dispatcher, store, lease := moduleDispatcherFixture(t)
	job := invokeModuleDispatcherFixture(t, dispatcher, "unrecovered")
	if _, err := store.StartActionObserved(context.Background(), job.ID, lease, dispatcher.observer); err != nil {
		t.Fatal(err)
	}
	if err := dispatcher.Run(context.Background()); !errors.Is(err, ErrModuleActionContainment) {
		t.Fatalf("unrecovered invocation was resumed: %v", err)
	}
}

type moduleDispatcherListProbe struct {
	ModuleActionDispatcherStore
	onList func()
}

func (probe moduleDispatcherListProbe) List(ctx context.Context) ([]consolejobs.Job, error) {
	jobs, err := probe.ModuleActionDispatcherStore.List(ctx)
	probe.onList()
	return jobs, err
}

func TestModuleActionDispatcherDoesNotConsumeOrExposeHostActions(t *testing.T) {
	dispatcher, store, _ := moduleDispatcherFixture(t)
	created, err := store.CreateActionObserved(context.Background(), consolejobs.CreateSpec{
		WorkspaceID: "workspace-fixture", Request: map[string]any{},
		Idempotency: consolejobs.IdempotencyInput{
			Principal: "creator", Method: "POST", CanonicalPath: "/host/incus/status", Key: "host-action",
			RequestDigest: consolejobs.DigestRequest([]byte(`{}`)),
		},
	}, "incus.status", "fixture-host-call", dispatcher.observer)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := dispatcher.Get(context.Background(), "another-admin", created.Job.ID); !errors.Is(err, ErrModuleActionDenied) {
		t.Fatalf("Module facade exposed host action: %v", err)
	}
	jobs, err := dispatcher.List(context.Background(), "another-admin")
	if err != nil || len(jobs) != 0 {
		t.Fatalf("Module list exposed host action: %#v, %v", jobs, err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	polls := 0
	dispatcher.worker.store = moduleDispatcherListProbe{
		ModuleActionDispatcherStore: store,
		onList: func() {
			polls++
			if polls == 2 {
				cancel()
			}
		},
	}
	if err := dispatcher.Run(ctx); err != nil || polls < 2 {
		t.Fatalf("Module worker handled host queue: polls=%d, err=%v", polls, err)
	}
	latest, err := store.Get(context.Background(), created.Job.ID)
	if err != nil || latest.Status != consolejobs.StatusQueued || latest.StartedAt != nil {
		t.Fatalf("Module worker consumed/rejected host action: %#v, %v", latest, err)
	}
}
