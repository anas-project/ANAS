package consolejobs

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

func TestQueuedRejectionIsAtomicAuditedAndReopensWithoutClaim(t *testing.T) {
	ctx := context.Background()
	store, dir := openStoreForTest(t, Options{})
	created, err := store.CreateOrGet(ctx, testCreateSpec("reject-ingress", "workspace-a", true))
	if err != nil {
		t.Fatal(err)
	}
	input := TransitionInput{Error: &JobError{Code: "ingress_drain_failed", Message: "workspace operation was not started"}}
	event := EventInput{Kind: "rejected", Data: map[string]any{"code": "ingress_drain_failed"}}
	auditErr := errors.New("fixture audit unavailable")
	if _, err = store.RejectQueuedObserved(ctx, created.Job.ID, input, event, JobCommitObserverFunc(func(context.Context, JobCommitIntent) error { return auditErr })); !errors.Is(err, auditErr) {
		t.Fatal(err)
	}
	unchanged, err := store.Get(ctx, created.Job.ID)
	if err != nil || !reflect.DeepEqual(unchanged, created.Job) {
		t.Fatal("failed audit changed queued state", err)
	}
	rejected, err := store.RejectQueuedObserved(ctx, created.Job.ID, input, event, JobCommitObserverFunc(func(_ context.Context, i JobCommitIntent) error {
		if i.Previous == nil || i.Previous.Status != StatusQueued || i.Next.Status != StatusFailed || i.Next.StartedAt != nil || i.Next.NeedsCompensationCheck {
			t.Fatal("invalid rejection intent")
		}
		return nil
	}))
	if err != nil || rejected.Status != StatusFailed || rejected.StartedAt != nil || rejected.FinishedAt == nil {
		t.Fatal(rejected, err)
	}
	if err = store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(dir, Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	actual, err := reopened.Get(ctx, created.Job.ID)
	if err != nil || !reflect.DeepEqual(actual, rejected) {
		t.Fatal("replay lost rejection", err)
	}
	if _, err = reopened.RejectQueuedObserved(ctx, created.Job.ID, input, event, JobCommitObserverFunc(func(context.Context, JobCommitIntent) error { return nil })); !errors.Is(err, ErrConflict) {
		t.Fatal("terminal job rejected twice", err)
	}
}

func TestQueuedRejectionCannotFabricateExecutionOrChangeRunningJob(t *testing.T) {
	ctx := context.Background()
	store, _ := openStoreForTest(t, Options{})
	created, err := store.CreateOrGet(ctx, testCreateSpec("rejection-boundary", "workspace-a", true))
	if err != nil {
		t.Fatal(err)
	}
	observer := JobCommitObserverFunc(func(context.Context, JobCommitIntent) error { return nil })
	for _, input := range []TransitionInput{{}, {Error: &JobError{Code: "no", Message: "no"}, NeedsCompensationCheck: true}, {Error: &JobError{Code: "no", Message: "no"}, Result: map[string]any{"executed": true}}} {
		if _, err = store.RejectQueuedObserved(ctx, created.Job.ID, input, EventInput{Kind: "rejected"}, observer); err == nil {
			t.Fatal("invalid rejection accepted")
		}
	}
	if _, err = store.Start(ctx, created.Job.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = store.RejectQueuedObserved(ctx, created.Job.ID, TransitionInput{Error: &JobError{Code: "no", Message: "no"}}, EventInput{Kind: "rejected"}, observer); !errors.Is(err, ErrConflict) {
		t.Fatal("running operation treated as unstarted", err)
	}
	previous := created.Job
	next := previous
	next.Status = StatusFailed
	next.Error = &JobError{Code: "no", Message: "no"}
	next.StartedAt = created.Job.FinishedAt
	if !validPersistedJobUpdate(previous, next) {
		t.Fatal("unstarted rejection not accepted by replay")
	}
	next.NeedsCompensationCheck = true
	if validPersistedJobUpdate(previous, next) {
		t.Fatal("queued failure claimed execution effects")
	}
}
