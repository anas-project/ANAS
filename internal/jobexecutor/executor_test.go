package jobexecutor

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/anas-project/ANAS/internal/application"
	"github.com/anas-project/ANAS/internal/consolejobs"
	"github.com/anas-project/ANAS/internal/deploymentaudit"
)

type fakeDeploymentService struct {
	apply func(context.Context, application.ApplyRequest) (application.ApplyResult, error)
}

type recordingDeploymentAudit struct {
	mu     sync.Mutex
	events []deploymentaudit.Event
	err    error
}

func (sink *recordingDeploymentAudit) RecordDeploymentEvent(_ context.Context, event deploymentaudit.Event) error {
	sink.mu.Lock()
	defer sink.mu.Unlock()
	sink.events = append(sink.events, event)
	return sink.err
}

func (sink *recordingDeploymentAudit) snapshot() []deploymentaudit.Event {
	sink.mu.Lock()
	defer sink.mu.Unlock()
	return append([]deploymentaudit.Event(nil), sink.events...)
}

func (*fakeDeploymentService) Plan(context.Context, application.PlanRequest) (application.PlanResult, error) {
	return application.PlanResult{}, nil
}

func (service *fakeDeploymentService) Apply(ctx context.Context, request application.ApplyRequest) (application.ApplyResult, error) {
	return service.apply(ctx, request)
}

func TestExecutorRunsApplyFromDaemonContextAndPersistsEventsBeforeSuccess(t *testing.T) {
	store := openExecutorStore(t)
	request := application.ApplyRequest{
		ExpectedConfigValidator: "cfgv-0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
		ExpectedPlanDigest:      "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
		Snapshot:                true,
	}
	job := createApplyJob(t, store, "main", "first", request)

	requestContext, cancelRequest := context.WithCancel(context.Background())
	cancelRequest()
	_ = requestContext // A canceled enqueue/request context must not own execution.

	var gotRequest application.ApplyRequest
	auditSink := &recordingDeploymentAudit{}
	executor, err := New(Options{
		Store: store, Audit: auditSink,
		Workspaces: []Workspace{{ID: "main", Path: "/registered/main"}}, PollInterval: 5 * time.Millisecond,
		DeploymentFactory: func(path string, events application.EventSink) application.DeploymentService {
			if path != "/registered/main" {
				t.Fatalf("factory path = %q", path)
			}
			return &fakeDeploymentService{apply: func(ctx context.Context, incoming application.ApplyRequest) (application.ApplyResult, error) {
				gotRequest = incoming
				if err := ctx.Err(); err != nil {
					t.Fatalf("job context inherited canceled request: %v", err)
				}
				events.Progress(application.ProgressEvent{Phase: "activate", Current: 1, Total: 2, Unit: "modules"})
				events.Warning(application.WarningEvent{Code: "test_warning", Message: "safe warning"})
				return application.ApplyResult{
					Workspace: "/registered/main", DeploymentID: "dep-1", ActivatedAt: "2026-08-31T00:00:00Z",
					DeploymentPath: "/registered/main/.anas/deployments/dep-1",
				}, nil
			}}
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- executor.Run(ctx) }()
	executor.Notify("main")

	completed := waitForJobStatus(t, store, job.ID, consolejobs.StatusSucceeded)
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if !gotRequest.Confirmed || gotRequest.ExpectedPlanDigest != request.ExpectedPlanDigest || !gotRequest.Snapshot {
		t.Fatalf("executor request = %#v", gotRequest)
	}
	if completed.Progress != 100 || completed.Result["deployment_id"] != "dep-1" {
		t.Fatalf("completed job = %#v", completed)
	}
	if _, exposed := completed.Result["deployment_path"]; exposed {
		t.Fatalf("job result exposed host deployment path: %#v", completed.Result)
	}
	if len(completed.Warnings) != 1 || completed.Warnings[0] != "safe warning" {
		t.Fatalf("job warnings = %#v", completed.Warnings)
	}
	page, err := store.Replay(context.Background(), job.ID, consolejobs.ReplayOptions{Limit: 20})
	if err != nil {
		t.Fatal(err)
	}
	wantKinds := []string{"started", "progress", "warning", "succeeded"}
	if len(page.Events) != len(wantKinds) {
		t.Fatalf("event count = %d, want %d: %#v", len(page.Events), len(wantKinds), page.Events)
	}
	for index, want := range wantKinds {
		if page.Events[index].Kind != want {
			t.Fatalf("event %d kind = %q, want %q", index, page.Events[index].Kind, want)
		}
	}
	auditEvents := auditSink.snapshot()
	if len(auditEvents) != 2 || auditEvents[0].Stage != deploymentaudit.StageJobStartAuthorized || auditEvents[1].Stage != deploymentaudit.StageJobSucceededAuthorized {
		t.Fatalf("deployment audit events = %#v", auditEvents)
	}
	for _, event := range auditEvents {
		if event.Action != deploymentaudit.ActionApply || event.Actor != "local-owner" || event.IdentitySource != "local" || event.WorkspaceID != "main" || event.JobID != job.ID || event.PlanJobID == "" || event.ConfigValidator != request.ExpectedConfigValidator || event.PlanDigest != request.ExpectedPlanDigest {
			t.Fatalf("deployment audit binding = %#v", event)
		}
	}
}

func TestExecutorFailsClosedBeforeClaimWhenAuditUnavailable(t *testing.T) {
	store := openExecutorStore(t)
	job := createApplyJob(t, store, "main", "audit-down", application.ApplyRequest{})
	auditCalled := make(chan struct{})
	var auditOnce sync.Once
	var applyCalls atomic.Int32
	executor, err := New(Options{
		Store: store,
		Audit: deploymentaudit.SinkFunc(func(context.Context, deploymentaudit.Event) error {
			auditOnce.Do(func() { close(auditCalled) })
			return errors.New("audit unavailable")
		}),
		Workspaces:   []Workspace{{ID: "main", Path: "/main"}},
		PollInterval: 5 * time.Millisecond,
		DeploymentFactory: func(string, application.EventSink) application.DeploymentService {
			return &fakeDeploymentService{apply: func(context.Context, application.ApplyRequest) (application.ApplyResult, error) {
				applyCalls.Add(1)
				return application.ApplyResult{}, nil
			}}
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- executor.Run(ctx) }()
	executor.Notify("main")
	select {
	case <-auditCalled:
	case <-time.After(2 * time.Second):
		t.Fatal("executor did not attempt pre-claim audit")
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	stored, err := store.Get(context.Background(), job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Status != consolejobs.StatusQueued || applyCalls.Load() != 0 {
		t.Fatalf("job after audit failure = %#v, apply calls = %d", stored, applyCalls.Load())
	}
}

func TestExecutorAuditsExecutionFailureBeforeTerminalState(t *testing.T) {
	store := openExecutorStore(t)
	job := createApplyJob(t, store, "main", "operation-fails", application.ApplyRequest{})
	auditSink := &recordingDeploymentAudit{}
	executor, err := New(Options{
		Store: store, Audit: auditSink, Workspaces: []Workspace{{ID: "main", Path: "/main"}}, PollInterval: 5 * time.Millisecond,
		DeploymentFactory: func(string, application.EventSink) application.DeploymentService {
			return &fakeDeploymentService{apply: func(context.Context, application.ApplyRequest) (application.ApplyResult, error) {
				return application.ApplyResult{}, &application.Error{
					Kind: application.ErrorKindFailedPrecondition, Code: "plan_changed", Message: "plan changed",
				}
			}}
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- executor.Run(ctx) }()
	executor.Notify("main")
	failed := waitForJobStatus(t, store, job.ID, consolejobs.StatusFailed)
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if failed.Error == nil || failed.Error.Code != "plan_changed" {
		t.Fatalf("failed job = %#v", failed)
	}
	events := auditSink.snapshot()
	if len(events) != 2 || events[0].Stage != deploymentaudit.StageJobStartAuthorized ||
		events[1].Stage != deploymentaudit.StageJobFailedAuthorized || events[1].FailureCode != "plan_changed" {
		t.Fatalf("failure audit events = %#v", events)
	}
}

func TestPublicJobErrorProjectsOnlyGuardedChangeBlockers(t *testing.T) {
	blockers := []string{
		"global.base_domain (immutable; migrate-service-domain)",
		"samba_dc.application_dns_mode (data_migrate; migrate-application-dns-zone)",
	}
	projected := publicJobError(&application.Error{
		Kind: application.ErrorKindFailedPrecondition, Code: "guarded_changes", Message: "private runner message",
		Detail: map[string]any{"blocked": blockers, "private_path": "/srv/anas"},
	})
	if projected.Code != "guarded_changes" || projected.Message != "deployment apply failed" || projected.Detail == nil {
		t.Fatalf("projected job error = %#v", projected)
	}
	if !reflect.DeepEqual(projected.Detail.Blocked, blockers) {
		t.Fatalf("projected blockers = %#v, want %#v", projected.Detail.Blocked, blockers)
	}
	body, err := json.Marshal(projected)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(body, []byte("private_path")) || bytes.Contains(body, []byte("/srv/anas")) || bytes.Contains(body, []byte("private runner")) {
		t.Fatalf("projected job error leaked non-allowlisted detail: %s", body)
	}

	other := publicJobError(&application.Error{
		Kind: application.ErrorKindFailedPrecondition, Code: "plan_changed", Message: "changed",
		Detail: map[string]any{"blocked": blockers},
	})
	if other.Detail != nil {
		t.Fatalf("non-guarded error exposed blocker detail: %#v", other)
	}
}

func TestExecutorSerializesWorkspaceMutations(t *testing.T) {
	store := openExecutorStore(t)
	first := createApplyJob(t, store, "main", "first", application.ApplyRequest{})
	second := createApplyJob(t, store, "main", "second", application.ApplyRequest{})
	var active atomic.Int32
	var maximum atomic.Int32
	executor, err := New(Options{
		Store: store, Audit: deploymentaudit.SinkFunc(func(context.Context, deploymentaudit.Event) error { return nil }),
		Workspaces: []Workspace{{ID: "main", Path: "/main"}}, PollInterval: 5 * time.Millisecond,
		DeploymentFactory: func(string, application.EventSink) application.DeploymentService {
			return &fakeDeploymentService{apply: func(context.Context, application.ApplyRequest) (application.ApplyResult, error) {
				current := active.Add(1)
				for {
					observed := maximum.Load()
					if current <= observed || maximum.CompareAndSwap(observed, current) {
						break
					}
				}
				time.Sleep(20 * time.Millisecond)
				active.Add(-1)
				return application.ApplyResult{DeploymentID: "dep"}, nil
			}}
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- executor.Run(ctx) }()
	executor.Notify("main")
	waitForJobStatus(t, store, first.ID, consolejobs.StatusSucceeded)
	waitForJobStatus(t, store, second.ID, consolejobs.StatusSucceeded)
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if maximum.Load() != 1 {
		t.Fatalf("maximum concurrent workspace applies = %d, want 1", maximum.Load())
	}
}

// CONSOLE-R-048 requires serialization to be scoped to a workspace rather
// than turning the daemon into one global mutation queue. This complements
// TestExecutorSerializesWorkspaceMutations by proving that two registered
// workspaces can both enter their job-owned Apply operation before either one
// is released.
func TestExecutorRunsDifferentWorkspaceMutationsConcurrently(t *testing.T) {
	store := openExecutorStore(t)
	mainJob := createApplyJob(t, store, "main", "main-apply", application.ApplyRequest{})
	labJob := createApplyJob(t, store, "lab", "lab-apply", application.ApplyRequest{})
	started := make(chan string, 2)
	release := make(chan struct{})
	executor, err := New(Options{
		Store: store, Audit: deploymentaudit.SinkFunc(func(context.Context, deploymentaudit.Event) error { return nil }),
		Workspaces: []Workspace{{ID: "main", Path: "/main"}, {ID: "lab", Path: "/lab"}}, PollInterval: 5 * time.Millisecond,
		DeploymentFactory: func(path string, _ application.EventSink) application.DeploymentService {
			return &fakeDeploymentService{apply: func(ctx context.Context, _ application.ApplyRequest) (application.ApplyResult, error) {
				select {
				case started <- path:
				case <-ctx.Done():
					return application.ApplyResult{}, ctx.Err()
				}
				select {
				case <-release:
					return application.ApplyResult{DeploymentID: "dep-" + path[1:]}, nil
				case <-ctx.Done():
					return application.ApplyResult{}, ctx.Err()
				}
			}}
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- executor.Run(ctx) }()
	executor.Notify("main")
	executor.Notify("lab")

	seen := map[string]bool{}
	for len(seen) < 2 {
		select {
		case path := <-started:
			seen[path] = true
		case <-time.After(2 * time.Second):
			cancel()
			<-done
			t.Fatalf("different workspace applies did not overlap; started paths = %#v", seen)
		}
	}
	if !seen["/main"] || !seen["/lab"] {
		t.Fatalf("started paths = %#v", seen)
	}
	close(release)
	waitForJobStatus(t, store, mainJob.ID, consolejobs.StatusSucceeded)
	waitForJobStatus(t, store, labJob.ID, consolejobs.StatusSucceeded)
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestExecutorMarksDaemonCanceledApplyInterrupted(t *testing.T) {
	store := openExecutorStore(t)
	job := createApplyJob(t, store, "main", "cancel", application.ApplyRequest{})
	started := make(chan struct{})
	var once sync.Once
	executor, err := New(Options{
		Store: store, Audit: deploymentaudit.SinkFunc(func(context.Context, deploymentaudit.Event) error { return nil }),
		Workspaces: []Workspace{{ID: "main", Path: "/main"}}, PollInterval: 5 * time.Millisecond,
		DeploymentFactory: func(string, application.EventSink) application.DeploymentService {
			return &fakeDeploymentService{apply: func(ctx context.Context, _ application.ApplyRequest) (application.ApplyResult, error) {
				once.Do(func() { close(started) })
				<-ctx.Done()
				return application.ApplyResult{}, ctx.Err()
			}}
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- executor.Run(ctx) }()
	executor.Notify("main")
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("apply did not start")
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	interrupted := waitForJobStatus(t, store, job.ID, consolejobs.StatusInterrupted)
	if !interrupted.NeedsCompensationCheck || interrupted.Error == nil || interrupted.Error.Code != "daemon_shutdown" {
		t.Fatalf("interrupted job = %#v", interrupted)
	}
}

func openExecutorStore(t *testing.T) *consolejobs.Store {
	t.Helper()
	store, err := consolejobs.Open(filepath.Join(t.TempDir(), "console-store"), consolejobs.Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Error(err)
		}
	})
	return store
}

func createApplyJob(t *testing.T, store *consolejobs.Store, workspaceID, key string, request application.ApplyRequest) consolejobs.Job {
	t.Helper()
	if request.ExpectedConfigValidator == "" {
		request.ExpectedConfigValidator = "cfgv-0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	}
	if request.ExpectedPlanDigest == "" {
		request.ExpectedPlanDigest = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	}
	requestMap, err := jsonObject(request)
	if err != nil {
		t.Fatal(err)
	}
	body := []byte(fmt.Sprintf("%s:%s", workspaceID, key))
	token := "cnf_" + consolejobs.DigestRequest([]byte("confirmation-token-"+key))
	planCreated, err := store.CreateOrGet(context.Background(), consolejobs.CreateSpec{
		Kind: "deployment.plan", WorkspaceID: workspaceID, Mutating: false,
		Idempotency: consolejobs.IdempotencyInput{
			Principal: "local-owner", Method: "POST", CanonicalPath: "/api/v1/workspaces/" + workspaceID + "/plans",
			Key: "plan-" + key, RequestDigest: consolejobs.DigestRequest([]byte("plan:" + key)),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Start(context.Background(), planCreated.Job.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Transition(context.Background(), planCreated.Job.ID, consolejobs.StatusSucceeded, consolejobs.TransitionInput{
		Result: map[string]any{"confirmation": map[string]any{
			"proof_digest": consolejobs.DigestRequest([]byte(token)), "actor": "local-owner", "identity_source": "local",
			"transaction_id": "", "action": "deployment.apply", "workspace_id": workspaceID,
			"config_validator": request.ExpectedConfigValidator, "plan_digest": request.ExpectedPlanDigest,
			"expires_at": time.Now().UTC().Add(time.Minute).Format(time.RFC3339Nano),
		}},
	}); err != nil {
		t.Fatal(err)
	}
	created, err := store.CreateOrGetConfirmed(context.Background(), consolejobs.CreateSpec{
		Kind: KindDeploymentApply, WorkspaceID: workspaceID, Mutating: true, Request: requestMap,
		Idempotency: consolejobs.IdempotencyInput{
			Principal: "local-owner", Method: "POST", CanonicalPath: "/api/v1/workspaces/" + workspaceID + "/actions/apply",
			Key: key, RequestDigest: consolejobs.DigestRequest(body),
		},
	}, consolejobs.ConfirmationInput{
		PlanJobID: planCreated.Job.ID, PlanKind: "deployment.plan", Token: token,
		Actor: "local-owner", IdentitySource: "local", Action: "deployment.apply", WorkspaceID: workspaceID,
		ConfigValidator: request.ExpectedConfigValidator, PlanDigest: request.ExpectedPlanDigest,
	})
	if err != nil {
		t.Fatal(err)
	}
	return created.Job
}

func waitForJobStatus(t *testing.T, store *consolejobs.Store, jobID string, status consolejobs.Status) consolejobs.Job {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		job, err := store.Get(context.Background(), jobID)
		if err != nil {
			t.Fatal(err)
		}
		if job.Status == status {
			return job
		}
		if job.Status == consolejobs.StatusFailed && status != consolejobs.StatusFailed {
			t.Fatalf("job failed while waiting for %s: %#v", status, job)
		}
		time.Sleep(5 * time.Millisecond)
	}
	job, err := store.Get(context.Background(), jobID)
	if err != nil && !errors.Is(err, consolejobs.ErrNotFound) {
		t.Fatal(err)
	}
	t.Fatalf("job = %#v, want status %s", job, status)
	return consolejobs.Job{}
}
