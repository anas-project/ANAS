package jobexecutor

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/anas-project/ANAS/internal/actionabi"
	"github.com/anas-project/ANAS/internal/computeingress"
	"github.com/anas-project/ANAS/internal/computeingressruntime"
	"github.com/anas-project/ANAS/internal/consolejobs"
	"github.com/anas-project/ANAS/internal/hostaction"
	"github.com/anas-project/ANAS/internal/hostconfirmation"
	"github.com/anas-project/ANAS/internal/incusprovision"
)

// Real queue, confirmation ledger, controller, journal and file lock. Kernel,
// Traefik, and the supervised root executor are explicitly synthetic adapters.
type ingressQueueEffects struct {
	target  computeingressruntime.PublicationTarget
	remove  func(context.Context) error
	closed  atomic.Int32
	removes atomic.Int32
}

func (f *ingressQueueEffects) ReadDesired(ctx context.Context, _ computeingressruntime.Journal) (computeingressruntime.DesiredSnapshot, error) {
	return computeingressruntime.DesiredSnapshot{Epoch: f.target.Epoch, Targets: []computeingressruntime.PublicationTarget{f.target}}, ctx.Err()
}
func (*ingressQueueEffects) AfterRetirement() {}
func (*ingressQueueEffects) ValidateAuthorization(ctx context.Context, _ computeingressruntime.PublicationTarget) error {
	return ctx.Err()
}
func (*ingressQueueEffects) ValidateTarget(ctx context.Context, _ computeingressruntime.PublicationTarget) error {
	return ctx.Err()
}
func (*ingressQueueEffects) CheckHTTPArtifacts(ctx context.Context, _ []computeingressruntime.PublicationTarget) error {
	return ctx.Err()
}
func (*ingressQueueEffects) HoldAddress(ctx context.Context, _ computeingressruntime.PublicationTarget) error {
	return ctx.Err()
}
func (*ingressQueueEffects) EnsureGuestRoute(ctx context.Context, _ computeingressruntime.PublicationTarget) error {
	return ctx.Err()
}
func (*ingressQueueEffects) EnsureHTTPPermit(ctx context.Context, _ computeingressruntime.PublicationTarget) error {
	return ctx.Err()
}
func (*ingressQueueEffects) RemoveHTTPPermit(ctx context.Context, _ computeingressruntime.PublicationTarget) error {
	return ctx.Err()
}
func (*ingressQueueEffects) CloseHTTPConnections(ctx context.Context, _ computeingressruntime.PublicationTarget) error {
	return ctx.Err()
}
func (*ingressQueueEffects) RemoveGuestRoute(ctx context.Context, _ computeingressruntime.PublicationTarget) error {
	return ctx.Err()
}
func (*ingressQueueEffects) ReleaseAddress(ctx context.Context, _ computeingressruntime.PublicationTarget) error {
	return ctx.Err()
}
func (*ingressQueueEffects) PublishHTTP(ctx context.Context, _ computeingressruntime.PublicationTarget) error {
	return ctx.Err()
}
func (*ingressQueueEffects) ProbeHTTP(ctx context.Context, _ computeingressruntime.PublicationTarget) error {
	return ctx.Err()
}
func (f *ingressQueueEffects) RemoveHTTP(ctx context.Context, _ computeingressruntime.PublicationTarget) error {
	f.removes.Add(1)
	if f.remove != nil {
		return f.remove(ctx)
	}
	return ctx.Err()
}
func (f *ingressQueueEffects) Close() error { f.closed.Add(1); return nil }

func startIngressQueueController(t *testing.T, s *HostActionService, workspace string, effects *ingressQueueEffects) (*computeingressruntime.ControllerService, computeingressruntime.FileStateStore) {
	t.Helper()
	effects.target = computeingressruntime.PublicationTarget{Epoch: strings.Repeat("a", 64), Incarnation: strings.Repeat("b", 64), NICMAC: "00:16:3e:01:02:03",
		Publication: computeingress.Publication{Reservation: strings.Repeat("c", 32) + ":1", Deployment: "deployment-one", Lease: computeingress.Lease{Consumer: "forgejo", Resource: "runners"},
			InstanceID: "anas-fj-job1", InstanceUUID: "11111111-1111-4111-8111-111111111111", WorkloadID: "job:123", GuestPort: 7000, Host: "ci.example.test", GuestIP: "10.42.0.2", Auth: "none"}}
	store := computeingressruntime.FileStateStore{Directory: t.TempDir()}
	if err := os.Chmod(store.Directory, 0700); err != nil {
		t.Fatal(err)
	}
	c := computeingressruntime.Controller{Executor: computeingressruntime.Executor{Observer: effects, Host: effects, Probe: effects, Renderer: effects, Store: store, Authority: effects},
		Source: effects, Interval: time.Hour, OperationTimeout: time.Second, CleanupTimeout: 5 * time.Second}
	owner, err := computeingressruntime.NewControllerService(c, effects)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.options.IngressCoordinator.Start(context.Background(), workspace, owner); err != nil {
		t.Fatal(err)
	}
	select {
	case <-owner.Ready():
	case <-time.After(5 * time.Second):
		t.Fatal("controller not ready")
	}
	return owner, store
}

func queueConfirmedObserverChange(t *testing.T, s *HostActionService, store *consolejobs.Store, lease *consolejobs.ExecutionLease) consolejobs.Job {
	t.Helper()
	ctx := context.Background()
	ledger, err := hostconfirmation.OpenForTesting(ctx, filepath.Join(t.TempDir(), "confirm"), s.options.Journal)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ledger.Close() })
	s.options.Confirmations = ledger
	s.available = true
	r := incusprovision.ObserverConfigurationRequest{Schema: incusprovision.ObserverConfigurationSchema, WorkspaceID: "main", Operation: "disable"}
	body, _ := json.Marshal(r)
	created, err := s.InvokePlan(ctx, "alice", "main", hostaction.ActionObserverPlan, body, "plan-barrier")
	if err != nil {
		t.Fatal(err)
	}
	started, err := store.StartActionObserved(ctx, created.Job.ID, lease, s.observer())
	if err != nil {
		t.Fatal(err)
	}
	p := incusprovision.ObserverConfigurationPlan{Schema: r.Schema, WorkspaceID: r.WorkspaceID, Operation: r.Operation, StateDigest: strings.Repeat("a", 64)}
	encoded, _ := json.Marshal(p)
	p.Digest = fmt.Sprintf("%x", sha256.Sum256(encoded))
	params := hostaction.ObserverApplyParameters{Schema: "anas.host-action.incus/v1", Request: r, Binding: incusprovision.ObserverConfigurationBinding{Schema: r.Schema, WorkspaceID: r.WorkspaceID, PlanDigest: p.Digest, StateDigest: p.StateDigest}}
	public, _ := json.Marshal(params)
	_, frozen, err := hostaction.FrozenRequest(hostaction.ActionObserverApply, s.options.Release, public)
	if err != nil {
		t.Fatal(err)
	}
	value := map[string]any{"schema": "anas.host-action.incus/v1", "action": hostaction.ActionObserverApply, "plan": p, "parameters": params, "action_confirmation": map[string]string{
		"parameters_digest": consolejobs.DigestRequest(frozen), "state_digest": p.StateDigest, "summary_digest": p.Digest, "release_digest": consolejobs.DigestRequest([]byte(s.options.Release.Version + "/" + s.options.Release.Commit))}}
	encoded, _ = json.Marshal(value)
	changed := false
	frame, err := hostaction.ProjectActionEvent(hostaction.ActionObserverPlan, actionabi.Event{ABI: actionabi.Version, JobID: started.ID, InvocationID: started.Action.InvocationID, Type: "result", Result: &actionabi.Result{Outcome: actionabi.Succeeded, Changed: &changed, Value: encoded}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.CompleteActionObserved(ctx, lease, frame, s.observer()); err != nil {
		t.Fatal(err)
	}
	proof, err := s.IssueConfirmation(ctx, "alice", "main", started.ID, hostaction.ActionObserverApply)
	if err != nil {
		t.Fatal(err)
	}
	applied, err := s.InvokeConfirmed(ctx, "alice", "main", hostaction.ActionObserverApply, started.ID, public, proof.Token, "apply-barrier")
	if err != nil {
		t.Fatal(err)
	}
	return applied.Job
}

func TestHostConfigurationDrainKeepsSharedQueueAvailableAndFenced(t *testing.T) {
	s, r, store, lease := serviceFixture(t, nil)
	write := queueConfirmedObserverChange(t, s, store, lease)
	if s.checkIngressChange(write) == nil {
		t.Fatal("mutation authorized without drain barrier")
	}
	var reads, writes atomic.Int32
	effects := &ingressQueueEffects{}
	effects.remove = func(ctx context.Context) error {
		created, err := s.InvokePreflight(ctx, "alice", "main", "cleanup-read")
		if err != nil {
			return err
		}
		for {
			job, err := store.Get(ctx, created.Job.ID)
			if err != nil {
				return err
			}
			if job.Status == consolejobs.StatusSucceeded {
				return nil
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(time.Millisecond):
			}
		}
	}
	owner, journal := startIngressQueueController(t, s, "main", effects)
	r.execute = func(ctx context.Context, id string, o consolejobs.JobCommitObserver) (consolejobs.Job, error) {
		job, err := store.Get(ctx, id)
		if err != nil {
			return job, err
		}
		changed := job.Mutating
		if changed {
			writes.Add(1)
			if effects.closed.Load() != 1 || owner.Phase() != computeingressruntime.ControllerStopped || s.checkIngressChange(job) != nil {
				t.Error("write preceded drain or lost gate")
			}
			stateErr := journal.WithExclusive(ctx, func(j computeingressruntime.Journal) error {
				state, err := j.Load(ctx)
				if err != nil {
					return err
				}
				if len(state.Publications) != 0 {
					return errors.New("still published")
				}
				return nil
			})
			if stateErr != nil {
				t.Error("state not retired", stateErr)
			}
			if _, err := s.options.IngressCoordinator.BeginChange(ctx, []string{"main"}); !errors.Is(err, computeingressruntime.ErrControllerChange) {
				t.Error("fence released before job terminal")
			}
		} else {
			reads.Add(1)
		}
		return store.CompleteActionObserved(ctx, lease, actionabi.Event{ABI: actionabi.Version, JobID: id, InvocationID: job.Action.InvocationID, Type: "result", Result: &actionabi.Result{Outcome: actionabi.Succeeded, Changed: &changed, Value: json.RawMessage(`{}`)}}, o)
	}
	stop, done := runServiceFixture(t, s)
	finished := awaitServiceJob(t, store, write.ID)
	stop()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if finished.Status != consolejobs.StatusSucceeded || writes.Load() != 1 || reads.Load() != 1 || effects.removes.Load() != 1 {
		t.Fatal("shared queue stalled or reordered", finished.Status, writes.Load(), reads.Load(), effects.removes.Load())
	}
	if s.checkIngressChange(finished) == nil {
		t.Fatal("completed job retained reusable proof")
	}
}

func TestHostConfigurationRejectsFailedDrainWithoutExecutingOrDroppingOwner(t *testing.T) {
	s, r, store, lease := serviceFixture(t, nil)
	write := queueConfirmedObserverChange(t, s, store, lease)
	var failure atomic.Bool
	failure.Store(true)
	effects := &ingressQueueEffects{remove: func(context.Context) error {
		if failure.Load() {
			return errors.New("private-adapter-error")
		}
		return nil
	}}
	owner, journal := startIngressQueueController(t, s, "main", effects)
	var writes atomic.Int32
	r.execute = func(context.Context, string, consolejobs.JobCommitObserver) (consolejobs.Job, error) {
		writes.Add(1)
		return consolejobs.Job{}, errors.New("unexpected execution")
	}
	stop, done := runServiceFixture(t, s)
	finished := awaitServiceJob(t, store, write.ID)
	stop()
	if finished.Status != consolejobs.StatusFailed || finished.StartedAt != nil || writes.Load() != 0 || effects.closed.Load() != 0 || owner.Phase() != computeingressruntime.ControllerDrainFailed {
		t.Fatal("failed drain permitted effect or lost ownership")
	}
	if err := journal.WithExclusive(context.Background(), func(computeingressruntime.Journal) error { return nil }); !errors.Is(err, computeingressruntime.ErrExecutorBusy) {
		t.Fatal("failed owner lock lost", err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		s.mu.Lock()
		closing := s.closing
		s.mu.Unlock()
		if closing {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("shutdown did not begin")
		}
		time.Sleep(time.Millisecond)
	}
	shutdown, err := s.options.IngressCoordinator.BeginShutdown(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	wait, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err = shutdown.Wait(wait); !errors.Is(err, computeingressruntime.ErrControllerDrain) {
		t.Fatal("shutdown retried failed drain implicitly", err)
	}
	select {
	case <-done:
		t.Fatal("failed shutdown released dependencies")
	default:
	}
	if r.closed.Load() || !errors.Is(lease.Close(), consolejobs.ErrExecutionRetained) {
		t.Fatal("runtime or execution lease released before drain")
	}
	failure.Store(false)
	if err = s.RetryIngressShutdown(wait); err != nil {
		t.Fatal("explicit recovery", err)
	}
	select {
	case err = <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-wait.Done():
		t.Fatal("shutdown did not finish after retry")
	}
	if effects.closed.Load() != 1 {
		t.Fatal("original cleanup resources not released")
	}
}

func TestHostServiceShutdownDrainsBeforeClosingQueueAndRejectsNewWrites(t *testing.T) {
	s, r, store, _ := serviceFixture(t, nil)
	entered, release := make(chan struct{}), make(chan struct{})
	effects := &ingressQueueEffects{remove: func(ctx context.Context) error {
		close(entered)
		select {
		case <-release:
		case <-ctx.Done():
			return ctx.Err()
		}
		created, err := s.InvokePreflight(ctx, "alice", "main", "shutdown-cleanup-read")
		if err != nil {
			return err
		}
		for {
			job, err := store.Get(ctx, created.Job.ID)
			if err != nil {
				return err
			}
			if job.Status == consolejobs.StatusSucceeded {
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
	stop, done := runServiceFixture(t, s)
	stop()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("shutdown did not drain")
	}
	if r.closed.Load() {
		t.Fatal("host dependencies closed first")
	}
	if _, err := s.InvokePlan(context.Background(), "alice", "main", hostaction.ActionInstallPlan, json.RawMessage(`{"schema":"anas.host-action.incus/v1","request":{}}`), "late-plan"); !errors.Is(err, hostaction.ErrUnavailable) {
		t.Fatal("shutdown admitted new configuration", err)
	}
	if _, err := s.options.IngressCoordinator.BeginChange(context.Background(), []string{"main"}); !errors.Is(err, computeingressruntime.ErrControllerChange) {
		t.Fatal("shutdown reopened launch admission", err)
	}
	close(release)
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("queue closed before cleanup dependency completed")
	}
	if owner.Phase() != computeingressruntime.ControllerStopped || effects.closed.Load() != 1 || !r.closed.Load() {
		t.Fatal("incorrect shutdown ordering")
	}
}

func TestHostConfigurationRechecksRevokedActorAfterDrain(t *testing.T) {
	var denied atomic.Bool
	s, r, store, lease := serviceFixture(t, func(context.Context, string, string) error {
		if denied.Load() {
			return errors.New("revoked")
		}
		return nil
	})
	write := queueConfirmedObserverChange(t, s, store, lease)
	effects := &ingressQueueEffects{remove: func(context.Context) error { denied.Store(true); return nil }}
	owner, _ := startIngressQueueController(t, s, "main", effects)
	var runs atomic.Int32
	r.execute = func(context.Context, string, consolejobs.JobCommitObserver) (consolejobs.Job, error) {
		runs.Add(1)
		return consolejobs.Job{}, errors.New("unapproved")
	}
	stop, done := runServiceFixture(t, s)
	finished := awaitServiceJob(t, store, write.ID)
	stop()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if finished.StartedAt != nil || finished.Status != consolejobs.StatusFailed || runs.Load() != 0 || owner.Phase() != computeingressruntime.ControllerStopped {
		t.Fatal("revoked actor executed after drain")
	}
}

func TestHostConfigurationUnknownExecutionRetainsFence(t *testing.T) {
	s, _, store, lease := serviceFixture(t, nil)
	job := queueConfirmedObserverChange(t, s, store, lease)
	ready, err := s.prepareIngressChange(context.Background(), job)
	if !ready || err != nil {
		t.Fatal(err)
	}
	if s.checkIngressChange(job) != nil {
		t.Fatal("prepared gate absent")
	}
	copy := job
	action := *job.Action
	copy.Action = &action
	copy.Status = consolejobs.StatusInterrupted
	copy.Action.Outcome = actionabi.Unknown
	if err = s.retireIngressChanges([]consolejobs.Job{copy}); !errors.Is(err, consolejobs.ErrActionContainment) {
		t.Fatal("unknown execution treated as terminal evidence", err)
	}
	if _, err = s.options.IngressCoordinator.BeginChange(context.Background(), []string{"main"}); !errors.Is(err, computeingressruntime.ErrControllerChange) {
		t.Fatal("unknown effect released fence", err)
	}
}
