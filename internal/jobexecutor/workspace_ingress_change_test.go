package jobexecutor

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/anas-project/ANAS/internal/application"
	"github.com/anas-project/ANAS/internal/computeingressruntime"
	"github.com/anas-project/ANAS/internal/consolejobs"
	"github.com/anas-project/ANAS/internal/deploymentaudit"
)

// The owner, file lock, journal and both queues are real. Network effects,
// the application operation and the supervised root executor are fixtures.
func TestWorkspaceMutationWaitsQueuedForIngressDrain(t *testing.T) {
	s, _, store, _ := serviceFixture(t, nil)
	entered, allow := make(chan struct{}), make(chan struct{})
	var once, release sync.Once
	effects := &ingressQueueEffects{remove: func(ctx context.Context) error {
		once.Do(func() { close(entered) })
		select {
		case <-allow:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}}
	owner, _ := startIngressQueueController(t, s, "main", effects)
	job := createApplyJob(t, store, "main", "ingress-apply", application.ApplyRequest{})
	var calls atomic.Int32
	e, err := New(Options{Store: store, Audit: &recordingDeploymentAudit{},
		Workspaces:         []Workspace{{ID: "main", Path: "/main"}, {ID: "other", Path: "/other"}},
		IngressCoordinator: s.options.IngressCoordinator, PollInterval: 5 * time.Millisecond,
		DeploymentFactory: func(string, application.EventSink) application.DeploymentService {
			return &fakeDeploymentService{apply: func(context.Context, application.ApplyRequest) (application.ApplyResult, error) {
				calls.Add(1)
				if effects.closed.Load() != 1 {
					return application.ApplyResult{}, errors.New("operation preceded ingress drain")
				}
				return application.ApplyResult{}, nil
			}}
		}})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- e.Run(ctx) }()
	t.Cleanup(func() {
		release.Do(func() { close(allow) })
		cancel()
		<-done
		cleanup, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		_ = owner.Stop(cleanup)
	})
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("deployment executed without beginning ingress drain")
	}
	current, err := store.Get(context.Background(), job.ID)
	if err != nil || current.Status != consolejobs.StatusQueued || current.StartedAt != nil || calls.Load() != 0 {
		t.Fatal("waiting drain claimed mutation", current.Status, err)
	}
	release.Do(func() { close(allow) })
	finished := awaitServiceJob(t, store, job.ID)
	if finished.Status != consolejobs.StatusSucceeded || calls.Load() != 1 || owner.Phase() != computeingressruntime.ControllerStopped {
		t.Fatal("drained mutation did not finish", finished.Status)
	}
}

func newIngressWorkspaceExecutor(t *testing.T, s *HostActionService, store Store, factory DeploymentFactory) *Executor {
	t.Helper()
	if factory == nil {
		factory = func(string, application.EventSink) application.DeploymentService {
			return &fakeDeploymentService{apply: func(context.Context, application.ApplyRequest) (application.ApplyResult, error) {
				return application.ApplyResult{}, nil
			}}
		}
	}
	e, err := New(Options{Store: store, Audit: &recordingDeploymentAudit{}, Workspaces: []Workspace{{ID: "main", Path: "/main"}, {ID: "other", Path: "/other"}}, IngressCoordinator: s.options.IngressCoordinator, DeploymentFactory: factory, PollInterval: 5 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	return e
}

func driveWorkspaceUntil(t *testing.T, e *Executor, condition func(consolejobs.Job, bool) bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		job, found, err := e.claimWorkspaceJob(context.Background(), "main")
		if err != nil && !errors.Is(err, errWorkspaceIngressPending) {
			t.Fatal("claim failed", err)
		}
		if condition(job, found) {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("workspace coordination did not finish")
}

func TestWorkspaceDrainCanUseSameHostQueueBeforeClaim(t *testing.T) {
	s, r, store, _ := serviceFixture(t, nil)
	job := createApplyJob(t, store, "main", "shared-queue-apply", application.ApplyRequest{})
	var reads atomic.Int32
	original := r.execute
	r.execute = func(ctx context.Context, id string, o consolejobs.JobCommitObserver) (consolejobs.Job, error) {
		current, err := store.Get(ctx, job.ID)
		if err != nil || current.Status != consolejobs.StatusQueued || current.StartedAt != nil {
			t.Error("cleanup read blocked by running mutation", err)
		}
		reads.Add(1)
		return original(ctx, id, o)
	}
	effects := &ingressQueueEffects{remove: func(ctx context.Context) error {
		read, err := s.InvokePreflight(ctx, "alice", "main", "legacy-drain-read")
		if err != nil {
			return err
		}
		for {
			j, err := store.Get(ctx, read.Job.ID)
			if err != nil {
				return err
			}
			if j.Status == consolejobs.StatusSucceeded {
				return nil
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(time.Millisecond):
			}
		}
	}}
	owner, _ := startIngressQueueController(t, s, "main", effects)
	stopHost, hostDone := runServiceFixture(t, s)
	t.Cleanup(func() { stopHost(); <-hostDone })
	e := newIngressWorkspaceExecutor(t, s, store, func(string, application.EventSink) application.DeploymentService {
		return &fakeDeploymentService{apply: func(ctx context.Context, _ application.ApplyRequest) (application.ApplyResult, error) {
			if effects.closed.Load() != 1 || owner.Phase() != computeingressruntime.ControllerStopped || reads.Load() != 1 {
				t.Error("application preceded complete drain")
			}
			if _, err := s.options.IngressCoordinator.BeginChange(ctx, []string{"main"}); !errors.Is(err, computeingressruntime.ErrControllerChange) {
				t.Error("application lost original fence", err)
			}
			return application.ApplyResult{}, nil
		}}
	})
	driveWorkspaceUntil(t, e, func(job consolejobs.Job, found bool) bool {
		if !found {
			return false
		}
		e.execute(context.Background(), "/main", job)
		return true
	})
	finished, err := store.Get(context.Background(), job.ID)
	if err != nil || finished.Status != consolejobs.StatusSucceeded {
		t.Fatal("shared queue flow failed", finished.Status, err)
	}
	if err = e.retireWorkspaceChange(context.Background(), "main"); err != nil {
		t.Fatal(err)
	}
}

func TestWorkspaceFailedDrainRejectsUnstartedJobAndRetainsOwner(t *testing.T) {
	s, _, store, _ := serviceFixture(t, nil)
	var failed atomic.Bool
	failed.Store(true)
	effects := &ingressQueueEffects{remove: func(ctx context.Context) error {
		if failed.Load() {
			return errors.New("private fixture cleanup failure")
		}
		return ctx.Err()
	}}
	owner, journal := startIngressQueueController(t, s, "main", effects)
	t.Cleanup(func() {
		failed.Store(false)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = owner.RetryDrain(ctx)
	})
	job := createApplyJob(t, store, "main", "failed-drain", application.ApplyRequest{})
	e := newIngressWorkspaceExecutor(t, s, store, nil)
	driveWorkspaceUntil(t, e, func(_ consolejobs.Job, found bool) bool {
		if found {
			t.Fatal("failed drain claimed a job")
		}
		j, _ := store.Get(context.Background(), job.ID)
		return j.Status == consolejobs.StatusFailed
	})
	finished, _ := store.Get(context.Background(), job.ID)
	if finished.StartedAt != nil || finished.NeedsCompensationCheck || finished.Error == nil || finished.Error.Code != "ingress_drain_failed" || effects.closed.Load() != 0 {
		t.Fatal("unstarted failure misreported", finished)
	}
	if err := journal.WithExclusive(context.Background(), func(computeingressruntime.Journal) error { return nil }); !errors.Is(err, computeingressruntime.ErrExecutorBusy) {
		t.Fatal("failed drain released original journal", err)
	}
	if err := e.retireWorkspaceChange(context.Background(), "main"); err != nil {
		t.Fatal(err)
	}
	if effects.removes.Load() != 1 {
		t.Fatal("queue polling retried cleanup")
	}
}

func TestWorkspaceCancellationDoesNotLetNextJobStealDrain(t *testing.T) {
	s, _, store, _ := serviceFixture(t, nil)
	allow := make(chan struct{})
	var release sync.Once
	effects := &ingressQueueEffects{remove: func(ctx context.Context) error {
		select {
		case <-allow:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}}
	owner, _ := startIngressQueueController(t, s, "main", effects)
	t.Cleanup(func() {
		release.Do(func() { close(allow) })
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = owner.Stop(ctx)
	})
	first := createApplyJob(t, store, "main", "cancel-first", application.ApplyRequest{})
	second := createApplyJob(t, store, "main", "cancel-second", application.ApplyRequest{})
	e := newIngressWorkspaceExecutor(t, s, store, nil)
	if _, found, err := e.claimWorkspaceJob(context.Background(), "main"); found || !errors.Is(err, errWorkspaceIngressPending) {
		t.Fatal(found, err)
	}
	if _, err := e.Cancel(context.Background(), first.ID); err != nil {
		t.Fatal(err)
	}
	for range 3 {
		if _, found, err := e.claimWorkspaceJob(context.Background(), "main"); found || !errors.Is(err, errWorkspaceIngressPending) {
			t.Fatal("next job stole pending drain", found, err)
		}
	}
	release.Do(func() { close(allow) })
	driveWorkspaceUntil(t, e, func(job consolejobs.Job, found bool) bool {
		if !found {
			return false
		}
		if job.ID != second.ID {
			t.Fatal("canceled job revived")
		}
		e.execute(context.Background(), "/main", job)
		return true
	})
	if err := e.retireWorkspaceChange(context.Background(), "main"); err != nil {
		t.Fatal(err)
	}
}

func TestWorkspaceAuditAndRevokedActorVetoDrainAndExecution(t *testing.T) {
	s, _, store, _ := serviceFixture(t, nil)
	allow := make(chan struct{})
	var release sync.Once
	effects := &ingressQueueEffects{remove: func(ctx context.Context) error {
		select {
		case <-allow:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}}
	owner, _ := startIngressQueueController(t, s, "main", effects)
	t.Cleanup(func() {
		release.Do(func() { close(allow) })
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = owner.Stop(ctx)
	})
	job := createApplyJob(t, store, "main", "revoked-apply", application.ApplyRequest{})
	e := newIngressWorkspaceExecutor(t, s, store, nil)
	fault := errors.New("fixture audit denied")
	e.audit = deploymentaudit.SinkFunc(func(context.Context, deploymentaudit.Event) error { return fault })
	if _, found, err := e.claimWorkspaceJob(context.Background(), "main"); found || !errors.Is(err, fault) {
		t.Fatal(found, err)
	}
	if owner.Phase() != computeingressruntime.ControllerRunning || e.workspaceChange("main") != nil {
		t.Fatal("audit failure stopped controller")
	}
	e.audit = &recordingDeploymentAudit{}
	if _, found, err := e.claimWorkspaceJob(context.Background(), "main"); found || !errors.Is(err, errWorkspaceIngressPending) {
		t.Fatal(found, err)
	}
	e.authorize = func(context.Context, consolejobs.Job) error { return errors.New("private revoked identity") }
	if _, found, err := e.claimWorkspaceJob(context.Background(), "main"); found || err != nil {
		t.Fatal(found, err)
	}
	failed, _ := store.Get(context.Background(), job.ID)
	if failed.Status != consolejobs.StatusFailed || failed.StartedAt != nil || failed.Error.Code != "job_authorization_revoked" {
		t.Fatal("revocation after drain not enforced")
	}
}

func TestWorkspaceClaimIdentityAndCompensationRetainFence(t *testing.T) {
	s, _, store, _ := serviceFixture(t, nil)
	job := createApplyJob(t, store, "main", "changed-claim", application.ApplyRequest{})
	var calls atomic.Int32
	e := newIngressWorkspaceExecutor(t, s, store, func(string, application.EventSink) application.DeploymentService { calls.Add(1); return nil })
	var claimed consolejobs.Job
	driveWorkspaceUntil(t, e, func(j consolejobs.Job, found bool) bool { claimed = j; return found })
	claimed.Request = map[string]any{"changed": true}
	e.execute(context.Background(), "/main", claimed)
	if calls.Load() != 0 {
		t.Fatal("changed request reached factory")
	}
	if _, err := s.options.IngressCoordinator.BeginChange(context.Background(), []string{"main"}); !errors.Is(err, computeingressruntime.ErrControllerChange) {
		t.Fatal("unknown execution lost fence", err)
	}
	_, err := store.TransitionObserved(context.Background(), job.ID, consolejobs.StatusInterrupted, consolejobs.TransitionInput{NeedsCompensationCheck: true}, e.jobAuditObserver(deploymentaudit.StageJobInterruptedAuthorized, "test"))
	if err != nil {
		t.Fatal(err)
	}
	if err = e.retireWorkspaceChange(context.Background(), "main"); !errors.Is(err, consolejobs.ErrCompensationRequired) {
		t.Fatal("unacknowledged interruption released fence", err)
	}
	if _, err = store.AcknowledgeCompensation(context.Background(), job.ID, "fixture explicit compensation check"); err != nil {
		t.Fatal(err)
	}
	if err = e.retireWorkspaceChange(context.Background(), "main"); err != nil {
		t.Fatal(err)
	}
}

func TestAllLegacyMutationsIncludingRotationUseDrainBeforeClaim(t *testing.T) {
	for _, kind := range []string{KindDeploymentApply, KindDeploymentStart, KindDeploymentStop, KindDeploymentRestart, KindDeploymentRollback, KindLocalAdminRotate, KindModuleUpdate, KindModuleCommandInvoke, KindSnapshotCreate} {
		t.Run(kind, func(t *testing.T) {
			s, _, store, _ := serviceFixture(t, nil)
			allow := make(chan struct{})
			var release sync.Once
			effects := &ingressQueueEffects{remove: func(ctx context.Context) error {
				select {
				case <-allow:
					return nil
				case <-ctx.Done():
					return ctx.Err()
				}
			}}
			owner, _ := startIngressQueueController(t, s, "main", effects)
			t.Cleanup(func() {
				release.Do(func() { close(allow) })
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				_ = owner.Stop(ctx)
			})
			created, err := store.CreateOrGet(context.Background(), consolejobs.CreateSpec{Kind: kind, WorkspaceID: "main", Mutating: true, Request: map[string]any{}, Idempotency: consolejobs.IdempotencyInput{Principal: "local-owner", Method: "POST", CanonicalPath: "/test", Key: kind, RequestDigest: consolejobs.DigestRequest([]byte("{}"))}})
			if err != nil {
				t.Fatal(err)
			}
			e := newIngressWorkspaceExecutor(t, s, store, nil)
			if _, found, err := e.claimWorkspaceJob(context.Background(), "main"); found || !errors.Is(err, errWorkspaceIngressPending) {
				t.Fatal("mutation bypassed drain", found, err)
			}
			current, _ := store.Get(context.Background(), created.Job.ID)
			if current.Status != consolejobs.StatusQueued || current.StartedAt != nil {
				t.Fatal("mutation claimed before drain")
			}
		})
	}
}

type ingressRotationMaintenance struct {
	fakeMaintenanceService
	rotate func(context.Context, application.LocalAdminTarget) (application.LocalAdminRecord, error)
}

func (s *ingressRotationMaintenance) RotateLocalAdmin(ctx context.Context, r application.LocalAdminTarget) (application.LocalAdminRecord, error) {
	return s.rotate(ctx, r)
}

func TestLocalAdminRotationUsesDrainedOriginalScope(t *testing.T) {
	s, _, store, _ := serviceFixture(t, nil)
	effects := &ingressQueueEffects{}
	owner, _ := startIngressQueueController(t, s, "main", effects)
	request := map[string]any{"module": "traefik", "account": "admin"}
	digest, _ := workspaceJobDigest(consolejobs.Job{Request: request})
	created, err := store.CreateOrGet(context.Background(), consolejobs.CreateSpec{Kind: KindLocalAdminRotate, WorkspaceID: "main", Mutating: true, Request: request,
		Idempotency: consolejobs.IdempotencyInput{Principal: "local-owner", Method: "POST", CanonicalPath: "/test/rotation", Key: "rotation", RequestDigest: digest}})
	if err != nil {
		t.Fatal(err)
	}
	e := newIngressWorkspaceExecutor(t, s, store, nil)
	rotations := 0
	e.maintenanceFactory = func(path string, _ application.EventSink) application.MaintenanceService {
		return &ingressRotationMaintenance{rotate: func(ctx context.Context, r application.LocalAdminTarget) (application.LocalAdminRecord, error) {
			rotations++
			if path != "/main" || r.Module != "traefik" || r.Account != "admin" || owner.Phase() != computeingressruntime.ControllerStopped || effects.closed.Load() != 1 {
				t.Fatal("rotation identity or drain lost")
			}
			if _, err := s.options.IngressCoordinator.BeginChange(ctx, []string{"main"}); !errors.Is(err, computeingressruntime.ErrControllerChange) {
				t.Fatal("rotation did not hold fence", err)
			}
			return application.LocalAdminRecord{}, nil
		}}
	}
	driveWorkspaceUntil(t, e, func(job consolejobs.Job, found bool) bool {
		if !found {
			return false
		}
		e.execute(context.Background(), "/main", job)
		return true
	})
	finished, _ := store.Get(context.Background(), created.Job.ID)
	if rotations != 1 || finished.Status != consolejobs.StatusSucceeded {
		t.Fatal("rotation did not finish", rotations, finished.Status)
	}
	if err = e.retireWorkspaceChange(context.Background(), "main"); err != nil {
		t.Fatal(err)
	}
}

func TestWorkspaceRevocationAfterClaimCannotInvokeFactory(t *testing.T) {
	s, _, store, _ := serviceFixture(t, nil)
	job := createApplyJob(t, store, "main", "revoked-after-claim", application.ApplyRequest{})
	calls := 0
	e := newIngressWorkspaceExecutor(t, s, store, func(string, application.EventSink) application.DeploymentService { calls++; return nil })
	var running consolejobs.Job
	driveWorkspaceUntil(t, e, func(j consolejobs.Job, found bool) bool { running = j; return found })
	e.authorize = func(context.Context, consolejobs.Job) error { return errors.New("fixture lost owner") }
	e.execute(context.Background(), "/main", running)
	finished, err := store.Get(context.Background(), job.ID)
	if err != nil || calls != 0 || finished.Status != consolejobs.StatusFailed || finished.NeedsCompensationCheck || finished.Error.Code != "job_authorization_revoked" {
		t.Fatal("late revocation executed or lost terminal", err)
	}
	if err = e.retireWorkspaceChange(context.Background(), "main"); err != nil {
		t.Fatal(err)
	}
}

type ingressTerminalFaultStore struct {
	Store
	fail bool
}

func (s *ingressTerminalFaultStore) TransitionObserved(ctx context.Context, id string, status consolejobs.Status, in consolejobs.TransitionInput, o consolejobs.JobCommitObserver) (consolejobs.Job, error) {
	if s.fail {
		return consolejobs.Job{}, errors.New("fixture terminal persistence unavailable")
	}
	return s.Store.TransitionObserved(ctx, id, status, in, o)
}

func TestWorkspaceLostTerminalCommitCannotReleaseLaunchFence(t *testing.T) {
	s, _, store, _ := serviceFixture(t, nil)
	job := createApplyJob(t, store, "main", "lost-terminal", application.ApplyRequest{})
	fault := &ingressTerminalFaultStore{Store: store, fail: true}
	e := newIngressWorkspaceExecutor(t, s, fault, nil)
	driveWorkspaceUntil(t, e, func(j consolejobs.Job, found bool) bool {
		if !found {
			return false
		}
		e.execute(context.Background(), "/main", j)
		return true
	})
	current, _ := store.Get(context.Background(), job.ID)
	if current.Status != consolejobs.StatusRunning {
		t.Fatal("fault did not leave uncertain terminal")
	}
	if err := e.retireWorkspaceChange(context.Background(), "main"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.options.IngressCoordinator.BeginChange(context.Background(), []string{"main"}); !errors.Is(err, computeingressruntime.ErrControllerChange) {
		t.Fatal("success event substituted for committed terminal", err)
	}
	fault.fail = false
	if _, err := store.TransitionObserved(context.Background(), job.ID, consolejobs.StatusSucceeded, consolejobs.TransitionInput{}, e.jobAuditObserver(deploymentaudit.StageJobSucceededAuthorized, "")); err != nil {
		t.Fatal(err)
	}
	if err := e.retireWorkspaceChange(context.Background(), "main"); err != nil {
		t.Fatal(err)
	}
}

func TestWorkspaceExecutorRejectsMismatchedCoordinator(t *testing.T) {
	s, _, store, _ := serviceFixture(t, nil)
	_, err := New(Options{Store: store, Audit: &recordingDeploymentAudit{}, Workspaces: []Workspace{{ID: "main", Path: "/main"}}, IngressCoordinator: s.options.IngressCoordinator,
		DeploymentFactory: func(string, application.EventSink) application.DeploymentService { return nil }})
	if err == nil {
		t.Fatal("different launcher and worker workspace sets accepted")
	}
}
