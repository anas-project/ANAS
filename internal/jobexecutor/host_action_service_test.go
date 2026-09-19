package jobexecutor

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/anas-project/ANAS/internal/actionabi"
	"github.com/anas-project/ANAS/internal/audit"
	"github.com/anas-project/ANAS/internal/consolejobs"
	"github.com/anas-project/ANAS/internal/hostaction"
)

// A private runtime seam, not a socket/systemd acceptance test. The journal,
// action policy, execution lease, authorization and queue are the real ones.
type serviceTestRuntime struct {
	ready   chan struct{}
	startup error
	execute func(context.Context, string, consolejobs.JobCommitObserver) (consolejobs.Job, error)
	closed  atomic.Bool
}

func (r *serviceTestRuntime) Ready() <-chan struct{} { return r.ready }
func (r *serviceTestRuntime) Run(ctx context.Context) error {
	if r.startup != nil {
		return r.startup
	}
	close(r.ready)
	<-ctx.Done()
	return nil
}
func (r *serviceTestRuntime) ExecutePreflight(ctx context.Context, id string, o consolejobs.JobCommitObserver) (consolejobs.Job, error) {
	return r.execute(ctx, id, o)
}
func (r *serviceTestRuntime) Stop()        {}
func (r *serviceTestRuntime) Close() error { r.closed.Store(true); return nil }

func serviceFixture(t *testing.T, authorize func(context.Context, string, string) error) (*HostActionService, *serviceTestRuntime, *consolejobs.Store, *consolejobs.ExecutionLease) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "jobs")
	store, err := consolejobs.Open(dir, consolejobs.Options{})
	if err != nil {
		t.Fatal(err)
	}
	lease, err := consolejobs.AcquireExecutionLease(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	journal, err := audit.Open(filepath.Join(t.TempDir(), "audit"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := lease.Close(); err != nil {
			t.Error(err)
		}
		_ = store.Close()
		_ = journal.Close()
	})
	if authorize == nil {
		authorize = func(context.Context, string, string) error { return nil }
	}
	s, err := NewHostActionService(HostActionServiceOptions{Store: store, Lease: lease, Journal: journal, Release: hostaction.ReleaseIdentity{Version: "0.1.1", Commit: strings.Repeat("a", 40)}, Workspaces: []string{"main", "other"}, Authorize: authorize})
	if err != nil {
		t.Fatal(err)
	}
	r := &serviceTestRuntime{ready: make(chan struct{})}
	r.execute = func(ctx context.Context, id string, o consolejobs.JobCommitObserver) (consolejobs.Job, error) {
		job, err := store.Get(ctx, id)
		if err != nil {
			return job, err
		}
		changed := false
		return store.CompleteActionObserved(ctx, lease, actionabi.Event{ABI: actionabi.Version, JobID: id, InvocationID: job.Action.InvocationID, Type: "result", Result: &actionabi.Result{Outcome: actionabi.Succeeded, Changed: &changed, Value: json.RawMessage(`{}`)}}, o)
	}
	s.runtime = r
	return s, r, store, lease
}
func runServiceFixture(t *testing.T, s *HostActionService) (context.CancelFunc, <-chan error) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.Run(ctx) }()
	select {
	case <-s.Ready():
	case err := <-done:
		cancel()
		t.Fatal(err)
	case <-time.After(3 * time.Second):
		cancel()
		t.Fatal("service did not become ready")
	}
	t.Cleanup(cancel)
	return cancel, done
}
func awaitServiceJob(t *testing.T, store *consolejobs.Store, id string) consolejobs.Job {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		j, e := store.Get(context.Background(), id)
		if e != nil {
			t.Fatal(e)
		}
		if moduleActionTerminal(j.Status) {
			return j
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("job did not finish")
	return consolejobs.Job{}
}

func TestHostServiceQueueOutlivesRequestAndCoalesces(t *testing.T) {
	s, r, store, lease := serviceFixture(t, nil)
	entered, release := make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	t.Cleanup(func() { releaseOnce.Do(func() { close(release) }) })
	original := r.execute
	var runs atomic.Int32
	r.execute = func(ctx context.Context, id string, o consolejobs.JobCommitObserver) (consolejobs.Job, error) {
		runs.Add(1)
		close(entered)
		select {
		case <-release:
		case <-ctx.Done():
			return consolejobs.Job{}, ctx.Err()
		}
		return original(ctx, id, o)
	}
	stop, done := runServiceFixture(t, s)
	request, cancelRequest := context.WithCancel(context.Background())
	created, err := s.InvokePreflight(request, "alice", "main", "first")
	if err != nil {
		t.Fatal(err)
	}
	<-entered
	cancelRequest()
	if !errors.Is(lease.Close(), consolejobs.ErrExecutionRetained) {
		t.Fatal("service lost execution ownership")
	}
	retry, err := s.InvokePreflight(context.Background(), "bob", "main", "second")
	if err != nil || !retry.Existing || retry.Job.ID != created.Job.ID {
		t.Fatal("in-flight call was not coalesced", err)
	}
	_, err = s.InvokePreflight(context.Background(), "alice", "other", "first")
	if !errors.Is(err, consolejobs.ErrConflict) || strings.Contains(err.Error(), created.Job.ID) {
		t.Fatal("cross-workspace conflict leaked an id", err)
	}
	releaseOnce.Do(func() { close(release) })
	job := awaitServiceJob(t, store, created.Job.ID)
	if job.Status != consolejobs.StatusSucceeded || job.CreatedBy != "alice" || runs.Load() != 1 {
		t.Fatalf("unexpected completion: status=%s actor=%s runs=%d error=%+v", job.Status, job.CreatedBy, runs.Load(), job.Error)
	}
	again, err := s.InvokePreflight(context.Background(), "bob", "main", "first")
	if err != nil || again.Job.ID != job.ID {
		t.Fatal("terminal retry was not stable", err)
	}
	stop()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if !r.closed.Load() {
		t.Fatal("runtime cleanup was not awaited")
	}
	if _, err := s.InvokePreflight(context.Background(), "alice", "main", "later"); err == nil {
		t.Fatal("stopped service accepted work")
	}
}

func TestHostServiceRejectsStaleAndUnauthorizedQueuedWork(t *testing.T) {
	for _, which := range []string{"version", "permission"} {
		t.Run(which, func(t *testing.T) {
			var denied atomic.Bool
			s, r, store, _ := serviceFixture(t, func(context.Context, string, string) error {
				if denied.Load() {
					return errors.New("private-authority")
				}
				return nil
			})
			request, _ := HostJobRequest(s.options.Release)
			if which == "version" {
				request["release_commit"] = strings.Repeat("b", 40)
			} else {
				denied.Store(true)
			}
			o := consolejobs.JobCommitObserverFunc(func(context.Context, consolejobs.JobCommitIntent) error { return nil })
			created, err := store.CreateActionWithPolicyObserved(context.Background(), consolejobs.CreateSpec{WorkspaceID: "main", Request: request, Idempotency: consolejobs.IdempotencyInput{Principal: "alice", Method: "POST", CanonicalPath: "/test"}}, "incus.status", "stale-call", consolejobs.ActionCoalesce, o)
			if err != nil {
				t.Fatal(err)
			}
			r.execute = func(context.Context, string, consolejobs.JobCommitObserver) (consolejobs.Job, error) {
				t.Error("stale/denied task executed")
				return consolejobs.Job{}, nil
			}
			stop, done := runServiceFixture(t, s)
			job := awaitServiceJob(t, store, created.Job.ID)
			stop()
			if err := <-done; err != nil {
				t.Fatalf("service stop after queued rejection: %v", err)
			}
			if job.Status != consolejobs.StatusFailed || job.StartedAt != nil || job.Error == nil || job.Error.Code != "action_not_started" {
				t.Fatal("queued rejection was not distinct from execution")
			}
		})
	}
}

func TestHostServiceUnconfirmedExecutionCreatesDurableBarrier(t *testing.T) {
	s, r, store, _ := serviceFixture(t, nil)
	r.execute = func(context.Context, string, consolejobs.JobCommitObserver) (consolejobs.Job, error) {
		return consolejobs.Job{}, errors.New("private-runtime")
	}
	_, done := runServiceFixture(t, s)
	created, err := s.InvokePreflight(context.Background(), "alice", "main", "uncertain")
	if err != nil {
		t.Fatal(err)
	}
	if err := <-done; !errors.Is(err, consolejobs.ErrActionContainment) || strings.Contains(err.Error(), "private-runtime") {
		t.Fatal(err)
	}
	j, _ := store.Get(context.Background(), created.Job.ID)
	if !consolejobs.ActionContainmentLost(j) {
		t.Fatal("lost durable containment barrier")
	}
	request, _ := HostJobRequest(s.options.Release)
	_, err = store.CreateActionWithPolicyObserved(context.Background(), consolejobs.CreateSpec{WorkspaceID: "main", Request: request, Idempotency: consolejobs.IdempotencyInput{Principal: "alice", Method: "POST", CanonicalPath: "/test"}}, "incus.status", "later-call", consolejobs.ActionCoalesce, s.observer())
	if !errors.Is(err, consolejobs.ErrActionExecutionBlocked) {
		t.Fatal("store accepted new work after uncertainty", err)
	}
}

func TestHostServiceStartupFailureNeverAdmits(t *testing.T) {
	s, r, _, _ := serviceFixture(t, nil)
	r.startup = errors.New("private-listener")
	if err := s.Run(context.Background()); err == nil {
		t.Fatal("startup succeeded")
	}
	select {
	case <-s.Ready():
		t.Fatal("failure published readiness")
	default:
	}
	if !r.closed.Load() {
		t.Fatal("failed runtime not closed")
	}
	if _, err := s.InvokePreflight(context.Background(), "alice", "main", "key"); err == nil {
		t.Fatal("unavailable service accepted work")
	}
}

func TestHostServiceQueuedCancellationWinsWithoutStoppingOwner(t *testing.T) {
	s, _, store, _ := serviceFixture(t, nil)
	request, _ := HostJobRequest(s.options.Release)
	o := consolejobs.JobCommitObserverFunc(func(context.Context, consolejobs.JobCommitIntent) error { return nil })
	created, err := store.CreateActionWithPolicyObserved(context.Background(), consolejobs.CreateSpec{WorkspaceID: "main", Request: request, Idempotency: consolejobs.IdempotencyInput{Principal: "alice", Method: "POST", CanonicalPath: "/test"}}, "incus.status", "cancel-call", consolejobs.ActionCoalesce, o)
	if err != nil {
		t.Fatal(err)
	}
	var once sync.Once
	s.options.Authorize = func(ctx context.Context, _, _ string) error {
		once.Do(func() {
			_, err = store.CancelQueuedActionObserved(ctx, created.Job.ID, created.Job.Action.InvocationID, o)
		})
		return err
	}
	stop, done := runServiceFixture(t, s)
	job := awaitServiceJob(t, store, created.Job.ID)
	if job.Status != consolejobs.StatusCanceled || job.StartedAt != nil {
		t.Fatal("cancellation ran a job")
	}
	// Wait for the next poll: the normal cancellation must not kill admission.
	time.Sleep(2 * defaultPollInterval)
	if !s.admission() {
		t.Fatal("queue cancellation stopped the execution owner")
	}
	stop()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestHostServiceRecoversRunningJobsEvenWhenDisabled(t *testing.T) {
	s, _, store, lease := serviceFixture(t, nil)
	o := consolejobs.JobCommitObserverFunc(func(context.Context, consolejobs.JobCommitIntent) error { return nil })
	req, _ := HostJobRequest(s.options.Release)
	j, e := store.CreateActionWithPolicyObserved(context.Background(), consolejobs.CreateSpec{WorkspaceID: "main", Request: req, Idempotency: consolejobs.IdempotencyInput{Principal: "alice", Method: "POST", CanonicalPath: "/test"}}, "incus.status", "recover-call", consolejobs.ActionCoalesce, o)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = store.StartActionObserved(context.Background(), j.Job.ID, lease, o); e != nil {
		t.Fatal(e)
	}
	if e = store.RecoverInterruptedJobsObserved(context.Background(), lease, HostActionRecoveryObserver(s.options.Journal)); e != nil {
		t.Fatal(e)
	}
	job, _ := store.Get(context.Background(), j.Job.ID)
	if job.Status != consolejobs.StatusInterrupted || job.Error == nil || job.Error.Code != "daemon_restarted" {
		t.Fatal("recovery lost barrier")
	}
}
