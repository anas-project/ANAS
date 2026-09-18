package consolejobs

import (
	"context"
	"errors"
	"testing"

	"github.com/anas-project/ANAS/internal/actionabi"
)

func TestActionCancellationIntentSurvivesTruncationCompactionAndReopen(t *testing.T) {
	ctx := context.Background()
	store, directory := openStoreForTest(t, Options{EventCapacity: 2})
	observer := JobCommitObserverFunc(func(context.Context, JobCommitIntent) error { return nil })
	lease, err := AcquireExecutionLease(ctx, directory)
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Close()
	created, err := store.CreateActionObserved(ctx, testCreateSpec("cancel-journal", "workspace-a", true), "module.incus.fixture", "cancel-call", observer)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.StartActionObserved(ctx, created.Job.ID, lease, observer); err != nil {
		t.Fatal(err)
	}
	audits := 0
	cancelObserver := JobCommitObserverFunc(func(_ context.Context, intent JobCommitIntent) error {
		audits++
		if intent.Operation != JobCommitCancelRequest || intent.Previous == nil || intent.Previous.Action.Cancellation != nil ||
			intent.Next.Action.Cancellation == nil || intent.Next.Action.Cancellation.Actor != "first-admin" {
			t.Error("invalid cancellation audit binding")
		}
		// The observer must not be able to replace the actor about to be
		// committed by modifying its otherwise mutable defensive snapshot.
		intent.Next.Action.Cancellation.Actor = "observer-overwrite"
		return nil
	})
	requested, err := store.RequestActionCancelObserved(ctx, created.Job.ID, created.Job.Action.InvocationID, "first-admin", cancelObserver)
	if err != nil {
		t.Fatal(err)
	}
	if requested.Status != StatusRunning || requested.Action.LastSeq != 0 || requested.Revision != 3 ||
		requested.Action.Outcome != "" || requested.Action.Cancellation == nil || requested.Action.Cancellation.Actor != "first-admin" {
		t.Fatalf("request was confused with completion or mutated: %+v", requested)
	}
	firstTime := requested.Action.Cancellation.RequestedAt
	requested.Action.Cancellation.Actor = "caller-overwrite"
	duplicate, err := store.RequestActionCancelObserved(ctx, created.Job.ID, created.Job.Action.InvocationID, "second-admin", observer)
	if err != nil || duplicate.Revision != 3 || duplicate.Action.Cancellation.Actor != "first-admin" ||
		!duplicate.Action.Cancellation.RequestedAt.Equal(firstTime) || audits != 1 {
		t.Fatalf("duplicate changed first cancellation: %+v, audits=%d, err=%v", duplicate, audits, err)
	}
	for index := 0; index < 5; index++ {
		if _, err := store.AppendActionEvent(ctx, lease, actionabi.Event{
			ABI: actionabi.Version, JobID: created.Job.ID, InvocationID: created.Job.Action.InvocationID, Type: "progress",
			Progress: &actionabi.Progress{Phase: "cleaning"},
		}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := store.CompleteActionObserved(ctx, lease, actionabi.Event{
		ABI: actionabi.Version, JobID: created.Job.ID, InvocationID: created.Job.Action.InvocationID, Type: "result",
		Result: &actionabi.Result{Outcome: actionabi.Cancelled},
	}, observer); err != nil {
		t.Fatal(err)
	}
	if err := store.Compact(ctx); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(directory, Options{EventCapacity: 2})
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	restored, err := reopened.Get(ctx, created.Job.ID)
	if err != nil || restored.Status != StatusCanceled || restored.Action.Cancellation == nil ||
		restored.Action.Cancellation.Actor != "first-admin" || !restored.Action.Cancellation.RequestedAt.Equal(firstTime) {
		t.Fatalf("cancellation evidence was lost: %+v, err=%v", restored, err)
	}
	page, err := reopened.ReplayAction(ctx, restored.ID, 0, 10)
	if err != nil || len(page.Events) != 2 || page.Events[0].Type != "truncated" || page.Outcome != actionabi.Cancelled {
		t.Fatalf("expected bounded tail and cancellation outcome: %+v, err=%v", page, err)
	}
}

func TestActionCancellationAuditFailureAndWrongInvocationDoNotWrite(t *testing.T) {
	ctx := context.Background()
	store, directory := openStoreForTest(t, Options{})
	observer := JobCommitObserverFunc(func(context.Context, JobCommitIntent) error { return nil })
	lease, err := AcquireExecutionLease(ctx, directory)
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Close()
	created, err := store.CreateActionObserved(ctx, testCreateSpec("cancel-denied", "workspace-a", true), "module.incus.fixture", "cancel-call", observer)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.StartActionObserved(ctx, created.Job.ID, lease, observer); err != nil {
		t.Fatal(err)
	}
	denied := JobCommitObserverFunc(func(context.Context, JobCommitIntent) error { return errors.New("audit unavailable") })
	for _, attempt := range []struct {
		name, invocation, actor string
		observer                JobCommitObserver
	}{
		{"nil-observer", "cancel-call", "administrator", nil},
		{"denied-observer", "cancel-call", "administrator", denied},
		{"wrong-invocation", "wrong-call", "administrator", observer},
		{"missing-actor", "cancel-call", "", observer},
	} {
		t.Run(attempt.name, func(t *testing.T) {
			if _, err := store.RequestActionCancelObserved(ctx, created.Job.ID, attempt.invocation, attempt.actor, attempt.observer); err == nil {
				t.Fatal("invalid cancellation was accepted")
			}
			job, err := store.Get(ctx, created.Job.ID)
			if err != nil || job.Revision != 2 || job.Action.Cancellation != nil || job.Action.LastSeq != 0 || job.Status != StatusRunning {
				t.Fatalf("invalid request changed the job: %+v, err=%v", job, err)
			}
		})
	}
}

func TestActionCancelledTerminalRequiresDurableRequest(t *testing.T) {
	ctx := context.Background()
	store, directory := openStoreForTest(t, Options{})
	observer := JobCommitObserverFunc(func(context.Context, JobCommitIntent) error { return nil })
	lease, err := AcquireExecutionLease(ctx, directory)
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Close()
	created, err := store.CreateActionObserved(ctx, testCreateSpec("cancel-proof", "workspace-a", true), "module.incus.fixture", "cancel-call", observer)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.StartActionObserved(ctx, created.Job.ID, lease, observer); err != nil {
		t.Fatal(err)
	}
	terminal := actionabi.Event{
		ABI: actionabi.Version, JobID: created.Job.ID, InvocationID: created.Job.Action.InvocationID, Type: "result",
		Result: &actionabi.Result{Outcome: actionabi.Cancelled},
	}
	if _, err := store.CompleteActionObserved(ctx, lease, terminal, observer); !errors.Is(err, ErrConflict) {
		t.Fatalf("unsolicited cancelled terminal accepted: %v", err)
	}
	if _, err := store.RequestActionCancelObserved(ctx, created.Job.ID, created.Job.Action.InvocationID, "administrator", observer); err != nil {
		t.Fatal(err)
	}
	completed, err := store.CompleteActionObserved(ctx, lease, terminal, observer)
	if err != nil || completed.Status != StatusCanceled || completed.Action.Outcome != actionabi.Cancelled {
		t.Fatalf("durable request did not permit confirmed cancellation: %+v, err=%v", completed, err)
	}
}
