package consolejobs

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/anas-project/ANAS/internal/actionabi"
)

func TestActionPreflightRejectionSurvivesCompactionAndReopen(t *testing.T) {
	ctx := context.Background()
	store, directory := openStoreForTest(t, Options{})
	observer := JobCommitObserverFunc(func(context.Context, JobCommitIntent) error { return nil })
	created, err := store.CreateActionObserved(ctx, testCreateSpec("preflight", "workspace-a", true), "module.incus.fixture", "preflight-call", observer)
	if err != nil {
		t.Fatal(err)
	}
	job, err := store.RejectQueuedActionObserved(ctx, created.Job.ID, created.Job.Action.InvocationID, observer)
	if err != nil {
		t.Fatal(err)
	}
	assertPreflight := func(store *Store, job Job) {
		t.Helper()
		if job.Status != StatusFailed || job.Action.Outcome != actionabi.Failed || job.Action.LastSeq != 1 ||
			job.StartedAt != nil || job.FinishedAt == nil || job.NeedsCompensationCheck || job.Revision != 2 ||
			job.Error == nil || job.Error.Code != actionNotStartedCode || job.Error.Message != actionNotStartedMessage {
			t.Fatalf("invalid preflight terminal: %+v", job)
		}
		page, err := store.ReplayAction(ctx, job.ID, 0, 10)
		if err != nil || len(page.Events) != 1 || page.LatestSeq != 1 || page.Outcome != actionabi.Failed ||
			!isActionNotStarted(page.Events[0]) {
			t.Fatalf("preflight replay: %+v, err=%v", page, err)
		}
	}
	assertPreflight(store, job)
	if err := store.Compact(ctx); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(directory, Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	job, err = reopened.Get(ctx, created.Job.ID)
	if err != nil {
		t.Fatal(err)
	}
	assertPreflight(reopened, job)

	// A refused mutation never owned an executor or touched external state.
	// It must not block a different job behind the compensation barrier.
	lease, err := AcquireExecutionLease(ctx, directory)
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Close()
	next, err := reopened.CreateActionObserved(ctx, testCreateSpec("after-preflight", "workspace-a", true), "module.incus.fixture", "next-call", observer)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := reopened.StartActionObserved(ctx, next.Job.ID, lease, observer); err != nil {
		t.Fatalf("preflight rejection blocked an independent job: %v", err)
	}
}

func TestActionPreflightRejectionAuditAndInvocationFailClosed(t *testing.T) {
	ctx := context.Background()
	store, _ := openStoreForTest(t, Options{})
	observer := JobCommitObserverFunc(func(context.Context, JobCommitIntent) error { return nil })
	created, err := store.CreateActionObserved(ctx, testCreateSpec("preflight-audit", "workspace-a", true), "module.incus.fixture", "expected-call", observer)
	if err != nil {
		t.Fatal(err)
	}
	denied := JobCommitObserverFunc(func(context.Context, JobCommitIntent) error {
		return errors.New("audit unavailable")
	})
	for _, attempt := range []struct {
		name       string
		invocation string
		observer   JobCommitObserver
	}{
		{"missing-audit", "expected-call", nil},
		{"denied-audit", "expected-call", denied},
		{"wrong-invocation", "another-call", observer},
	} {
		t.Run(attempt.name, func(t *testing.T) {
			if _, err := store.RejectQueuedActionObserved(ctx, created.Job.ID, attempt.invocation, attempt.observer); err == nil {
				t.Fatal("rejection without matching invocation and audit was accepted")
			}
			job, err := store.Get(ctx, created.Job.ID)
			if err != nil || job.Status != StatusQueued || job.Revision != 1 || job.Action.LastSeq != 0 {
				t.Fatalf("failed rejection changed job: %+v, err=%v", job, err)
			}
			page, err := store.ReplayAction(ctx, job.ID, 0, 10)
			if err != nil || len(page.Events) != 0 {
				t.Fatalf("failed rejection wrote an event: %+v, err=%v", page, err)
			}
		})
	}
}

func TestActionPreflightRejectionCannotRewriteRunningExecution(t *testing.T) {
	ctx := context.Background()
	store, directory := openStoreForTest(t, Options{})
	observer := JobCommitObserverFunc(func(context.Context, JobCommitIntent) error { return nil })
	lease, err := AcquireExecutionLease(ctx, directory)
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Close()
	created, err := store.CreateActionObserved(ctx, testCreateSpec("running-preflight", "workspace-a", true), "module.incus.fixture", "running-call", observer)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.StartActionObserved(ctx, created.Job.ID, lease, observer); err != nil {
		t.Fatal(err)
	}
	if _, err := store.RejectQueuedActionObserved(ctx, created.Job.ID, created.Job.Action.InvocationID, observer); !errors.Is(err, ErrConflict) {
		t.Fatalf("preflight rejected a running job: %v", err)
	}
	// Even a syntactically valid terminal from an executor must not be able
	// to claim that execution never happened and suppress compensation.
	if _, err := store.CompleteActionObserved(ctx, lease, actionabi.Event{
		ABI: actionabi.Version, JobID: created.Job.ID, InvocationID: created.Job.Action.InvocationID, Type: "error",
		Error: &actionabi.Failure{Outcome: actionabi.Failed, Code: actionNotStartedCode, Message: actionNotStartedMessage},
	}, observer); !errors.Is(err, ErrConflict) {
		t.Fatalf("executor forged preflight terminal: %v", err)
	}
	job, err := store.Get(ctx, created.Job.ID)
	if err != nil || job.Status != StatusRunning || job.Action.LastSeq != 0 || job.StartedAt == nil {
		t.Fatalf("running execution changed: %+v, err=%v", job, err)
	}
}

func TestActionPreflightRejectionRacesStartAtomically(t *testing.T) {
	for iteration := 0; iteration < 8; iteration++ {
		t.Run(fmt.Sprint(iteration), func(t *testing.T) {
			ctx := context.Background()
			store, directory := openStoreForTest(t, Options{})
			observer := JobCommitObserverFunc(func(context.Context, JobCommitIntent) error { return nil })
			lease, err := AcquireExecutionLease(ctx, directory)
			if err != nil {
				t.Fatal(err)
			}
			defer lease.Close()
			created, err := store.CreateActionObserved(ctx, testCreateSpec("racing-preflight", "workspace-a", true), "module.incus.fixture", "racing-call", observer)
			if err != nil {
				t.Fatal(err)
			}
			start := make(chan struct{})
			results := make(chan error, 2)
			go func() {
				<-start
				_, err := store.StartActionObserved(ctx, created.Job.ID, lease, observer)
				results <- err
			}()
			go func() {
				<-start
				_, err := store.RejectQueuedActionObserved(ctx, created.Job.ID, created.Job.Action.InvocationID, observer)
				results <- err
			}()
			close(start)
			first, second := <-results, <-results
			if (first == nil) == (second == nil) || (first != nil && !errors.Is(first, ErrConflict)) || (second != nil && !errors.Is(second, ErrConflict)) {
				t.Fatalf("start/rejection must have exactly one winner: %v, %v", first, second)
			}
			job, err := store.Get(ctx, created.Job.ID)
			if err != nil {
				t.Fatal(err)
			}
			if job.Status == StatusFailed {
				if job.StartedAt != nil || job.Action.LastSeq != 1 || job.NeedsCompensationCheck {
					t.Fatalf("rejected job acquired execution state: %+v", job)
				}
			} else if job.Status != StatusRunning || job.StartedAt == nil || job.Action.LastSeq != 0 {
				t.Fatalf("unexpected start/rejection winner: %+v", job)
			}
		})
	}
}
