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

	"github.com/anas-project/ANAS/internal/actionabi"
	"github.com/anas-project/ANAS/internal/consolejobs"
	"github.com/anas-project/ANAS/internal/hostaction"
)

func hostBindingFixture(t *testing.T, mutate func(*consolejobs.CreateSpec), start bool) (*consolejobs.Store, *consolejobs.ExecutionLease, consolejobs.Job, hostaction.ReleaseIdentity, consolejobs.JobCommitObserver) {
	t.Helper()
	ctx := context.Background()
	dir := filepath.Join(t.TempDir(), "jobs")
	store, err := consolejobs.Open(dir, consolejobs.Options{})
	if err != nil {
		t.Fatal(err)
	}
	lease, err := consolejobs.AcquireExecutionLease(ctx, dir)
	if err != nil {
		store.Close()
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
	release := hostaction.ReleaseIdentity{Version: "0.1.1", Commit: strings.Repeat("a", 40)}
	request, err := HostJobRequest(release)
	if err != nil {
		t.Fatal(err)
	}
	spec := consolejobs.CreateSpec{WorkspaceID: "fixture-workspace", Request: request, Idempotency: consolejobs.IdempotencyInput{Principal: "fixture-actor", Method: "POST", CanonicalPath: "/host/actions"}}
	if mutate != nil {
		mutate(&spec)
	}
	observer := consolejobs.JobCommitObserverFunc(func(context.Context, consolejobs.JobCommitIntent) error { return nil })
	created, err := store.CreateActionWithPolicyObserved(ctx, spec, "incus.status", "host-call", consolejobs.ActionReject, observer)
	if err != nil {
		t.Fatal(err)
	}
	job := created.Job
	if start {
		job, err = store.StartActionObserved(ctx, job.ID, lease, observer)
		if err != nil {
			t.Fatal(err)
		}
	}
	return store, lease, job, release, observer
}

func hostWire(job consolejobs.Job) actionabi.Request {
	return actionabi.Request{ABI: actionabi.Version, JobID: job.ID, InvocationID: job.Action.InvocationID, Action: "incus.status", Parameters: json.RawMessage(`{}`)}
}
func hostPeer() hostaction.PeerIdentity {
	return hostaction.PeerIdentity{PID: 123, UID: 1001, GID: 1002}
}

func TestEmptyHostBindingIsInert(t *testing.T) {
	b := &HostJobBinding{}
	if err := b.WithHostInvocation(context.Background(), actionabi.Request{}, hostaction.ReleaseIdentity{}, hostPeer(), func(context.Context) error { t.Fatal("zero binding executed"); return nil }); err == nil {
		t.Fatal("zero binding accepted")
	}
	if err := b.Close(); err != nil {
		t.Fatal(err)
	}
	if err := b.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestHostBindingUsesRunningSharedJobAndRetainsOwner(t *testing.T) {
	store, lease, job, release, observer := hostBindingFixture(t, nil, true)
	ctx := context.Background()
	bind, err := NewHostJobBinding(ctx, store, lease, job.ID, release, func(_ context.Context, j consolejobs.Job, peer hostaction.PeerIdentity) error {
		if j.CreatedBy != "fixture-actor" || j.WorkspaceID != "fixture-workspace" || peer != hostPeer() {
			t.Fatal("lost trusted attribution")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	defer bind.Close()
	if err := lease.Close(); !errors.Is(err, consolejobs.ErrExecutionRetained) {
		t.Fatal("ownership escaped", err)
	}
	var ran atomic.Int32
	run := func(context.Context) error { ran.Add(1); return nil }
	var successes atomic.Int32
	var group sync.WaitGroup
	for range 8 {
		group.Add(1)
		go func() {
			defer group.Done()
			if bind.WithHostInvocation(ctx, hostWire(job), release, hostPeer(), run) == nil {
				successes.Add(1)
			}
		}()
	}
	group.Wait()
	if ran.Load() != 1 || successes.Load() != 1 {
		t.Fatal("invocation replayed", ran.Load(), successes.Load())
	}
	actual, err := store.Get(ctx, job.ID)
	if err != nil || actual.Status != consolejobs.StatusRunning || actual.Action.LastSeq != 0 {
		t.Fatal("binding invented completion", err)
	}
	// Only the common recorder may commit a terminal AFTER independent exit
	// evidence. This test controls a synthetic stream; no real host ran.
	changed := false
	wire, _ := actionabi.EncodeExecutorEvent(actionabi.Event{ABI: actionabi.Version, JobID: job.ID, InvocationID: job.Action.InvocationID, Type: "result", Result: &actionabi.Result{Outcome: actionabi.Succeeded, Changed: &changed, Value: json.RawMessage(`{}`)}})
	recorder, err := NewActionRecorder(ActionRecorderOptions{Store: store, Lease: lease, Job: job, Output: strings.NewReader(string(wire)), Project: func(e actionabi.Event) (actionabi.Event, error) { return e, nil }, Observer: observer})
	if err != nil {
		t.Fatal(err)
	}
	if err := recorder.Next(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := recorder.Finish(ctx, actionabi.ExitState{}); !errors.Is(err, actionabi.ErrUnknownOutcome) {
		t.Fatal("missing process exit accepted", err)
	}
	actual, _ = store.Get(ctx, job.ID)
	if actual.Action.Outcome != actionabi.Unknown {
		t.Fatal("socket stream fabricated success")
	}
}

func TestHostBindingAcceptsOwnProgressEventsBeforeCompletion(t *testing.T) {
	store, lease, job, release, observer := hostBindingFixture(t, nil, true)
	ctx := context.Background()
	wire, _ := actionabi.EncodeExecutorEvent(actionabi.Event{ABI: actionabi.Version, JobID: job.ID, InvocationID: job.Action.InvocationID, Type: "progress", Progress: &actionabi.Progress{Phase: "inspect"}})
	recorder, err := NewActionRecorder(ActionRecorderOptions{Store: store, Lease: lease, Job: job, Output: strings.NewReader(string(wire)), Project: func(e actionabi.Event) (actionabi.Event, error) { return e, nil }, Observer: observer})
	if err != nil {
		t.Fatal(err)
	}
	if err := recorder.Next(ctx); err != nil {
		t.Fatal(err)
	}
	withProgress, err := store.Get(ctx, job.ID)
	if err != nil || withProgress.Status != consolejobs.StatusRunning || withProgress.Action.LastSeq == 0 {
		t.Fatalf("progress was not persisted on running job: job=%+v err=%v", withProgress, err)
	}
	bind, err := NewHostJobBinding(ctx, store, lease, job.ID, release, func(context.Context, consolejobs.Job, hostaction.PeerIdentity) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	defer bind.Close()
	ran := false
	if err := bind.WithHostInvocation(ctx, hostWire(job), release, hostPeer(), func(context.Context) error { ran = true; return nil }); err != nil || !ran {
		t.Fatalf("binding rejected running job with prior progress: ran=%v err=%v", ran, err)
	}
}

func TestHostBindingRejectsQueuedChangedAndMutatingJobs(t *testing.T) {
	for _, name := range []string{"queued", "mutating", "release", "extra parameter", "wrong lease"} {
		t.Run(name, func(t *testing.T) {
			store, lease, job, release, _ := hostBindingFixture(t, func(s *consolejobs.CreateSpec) {
				if name == "mutating" {
					s.Mutating = true
				}
				if name == "release" {
					s.Request["release_commit"] = strings.Repeat("b", 40)
				}
				if name == "extra parameter" {
					s.Request["parameters"] = map[string]any{"path": "private-marker"}
				}
			}, name != "queued")
			if name == "wrong lease" {
				other, err := consolejobs.AcquireExecutionLease(context.Background(), filepath.Join(t.TempDir(), "other"))
				if err != nil {
					t.Fatal(err)
				}
				defer other.Close()
				lease = other
			}
			b, err := NewHostJobBinding(context.Background(), store, lease, job.ID, release, func(context.Context, consolejobs.Job, hostaction.PeerIdentity) error { return nil })
			if b != nil {
				b.Close()
			}
			if err == nil || strings.Contains(err.Error(), "private-marker") {
				t.Fatal("unsafe job accepted", err)
			}
		})
	}
}

func TestHostBindingRechecksAfterAuthorization(t *testing.T) {
	store, lease, job, release, observer := hostBindingFixture(t, nil, true)
	ctx := context.Background()
	b, err := NewHostJobBinding(ctx, store, lease, job.ID, release, func(ctx context.Context, _ consolejobs.Job, _ hostaction.PeerIdentity) error {
		_, err := store.RequestActionCancelObserved(ctx, job.ID, job.Action.InvocationID, "cancelling-actor", observer)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	ran := false
	err = b.WithHostInvocation(ctx, hostWire(job), release, hostPeer(), func(context.Context) error { ran = true; return nil })
	if err == nil || ran {
		t.Fatal("job change during authorization was ignored", err)
	}
}

func TestHostBindingReauthorizesAfterExecution(t *testing.T) {
	store, lease, job, release, _ := hostBindingFixture(t, nil, true)
	ctx := context.Background()
	allowed := true
	checks := 0
	b, err := NewHostJobBinding(ctx, store, lease, job.ID, release, func(context.Context, consolejobs.Job, hostaction.PeerIdentity) error {
		checks++
		if !allowed {
			return errors.New("private-revocation-reason")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	err = b.WithHostInvocation(ctx, hostWire(job), release, hostPeer(), func(context.Context) error { allowed = false; return nil })
	if !errors.Is(err, hostaction.ErrDenied) || checks != 2 || strings.Contains(err.Error(), "private-revocation-reason") {
		t.Fatal("revocation during execution ignored", err, checks)
	}
	current, err := store.Get(ctx, job.ID)
	if err != nil || current.Status != consolejobs.StatusRunning || current.Action.LastSeq != 0 {
		t.Fatal("binding invented a terminal", err)
	}
	if !errors.Is(lease.Close(), consolejobs.ErrExecutionRetained) {
		t.Fatal("failed final authorization discarded the execution lease")
	}
}

func TestHostBindingRejectsClaimedIdentityOrRelease(t *testing.T) {
	for _, name := range []string{"job", "invocation", "action", "parameters", "release", "peer", "permission"} {
		t.Run(name, func(t *testing.T) {
			store, lease, job, release, _ := hostBindingFixture(t, nil, true)
			b, err := NewHostJobBinding(context.Background(), store, lease, job.ID, release, func(context.Context, consolejobs.Job, hostaction.PeerIdentity) error {
				if name == "permission" {
					return errors.New("private-marker")
				}
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			defer b.Close()
			r, peer := hostWire(job), hostPeer()
			switch name {
			case "job":
				r.JobID = "other"
			case "invocation":
				r.InvocationID = "other"
			case "action":
				r.Action = "incus.install"
			case "parameters":
				r.Parameters = json.RawMessage(`{"path":"private-marker"}`)
			case "release":
				release.Commit = strings.Repeat("b", 40)
			case "peer":
				peer.PID = 0
			}
			ran := false
			err = b.WithHostInvocation(context.Background(), r, release, peer, func(context.Context) error { ran = true; return nil })
			if err == nil || ran || strings.Contains(err.Error(), "private-marker") {
				t.Fatal("invalid binding accepted", err)
			}
		})
	}
}
