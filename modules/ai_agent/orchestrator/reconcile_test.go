package main

import (
	"context"
	"errors"
	"testing"
	"time"
)

func testReconciler(t *testing.T) (*Reconciler, *fakeAdmin, *MemoryStore) {
	t.Helper()
	ingress, store, _ := testIngress(t)
	admin := newFakeAdmin()
	return &Reconciler{
		Config: ingress.Config, Admin: admin, Store: store, Ingress: ingress,
		Log: func(string) {},
	}, admin, store
}

// AGENT-R-013: an event the webhook never delivered is recovered by the sweep.
func TestReconciliationRecoversAMissedEvent(t *testing.T) {
	reconciler, admin, store := testReconciler(t)
	ctx := context.Background()
	admin.addIssue(testRepo(t), 12, "alice", time.Date(2026, 9, 5, 10, 0, 0, 0, time.UTC))

	recovered, err := reconciler.Sweep(ctx)
	if err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if recovered != 1 {
		t.Fatalf("recovered = %d, want 1", recovered)
	}
	pending, _ := store.PendingEvents(ctx, 10)
	if len(pending) != 1 || pending[0].Source != "reconcile" {
		t.Fatalf("inbox = %+v, want one event marked as recovered by reconciliation", pending)
	}
}

// AGENT-R-013: sweeping repeatedly over unchanged issues must not manufacture
// new events. The delivery id is derived from the issue's own identity, so the
// second sweep deduplicates against the first.
func TestRepeatedSweepsDoNotDuplicate(t *testing.T) {
	reconciler, admin, store := testReconciler(t)
	ctx := context.Background()
	updated := time.Date(2026, 9, 5, 10, 0, 0, 0, time.UTC)
	admin.addIssue(testRepo(t), 12, "alice", updated)

	for sweep := 0; sweep < 5; sweep++ {
		if _, err := reconciler.Sweep(ctx); err != nil {
			t.Fatalf("sweep %d: %v", sweep, err)
		}
	}
	pending, _ := store.PendingEvents(ctx, 10)
	if len(pending) != 1 {
		t.Fatalf("inbox holds %d events after five sweeps, want 1", len(pending))
	}
}

// The cursor only moves to a timestamp Forgejo reported, never to "now":
// advancing past the sweep would skip anything that changed while it ran.
func TestCursorAdvancesOnlyToAReportedTimestamp(t *testing.T) {
	reconciler, admin, store := testReconciler(t)
	ctx := context.Background()
	updated := time.Date(2026, 9, 5, 10, 0, 0, 0, time.UTC)
	admin.addIssue(testRepo(t), 12, "alice", updated)
	if _, err := reconciler.Sweep(ctx); err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	cursor, err := store.Cursor(ctx, testRepo(t))
	if err != nil {
		t.Fatalf("Cursor: %v", err)
	}
	if !cursor.Equal(updated) {
		t.Fatalf("cursor = %v, want %v", cursor, updated)
	}
}

// The sweep goes through the same filters as a webhook, so an agent's own
// issue update does not come back in through the reconciliation door.
func TestSweepAppliesTheSameSelfTriggerFilter(t *testing.T) {
	reconciler, admin, store := testReconciler(t)
	ctx := context.Background()
	admin.addIssue(testRepo(t), 12, "agent-codex", time.Date(2026, 9, 5, 10, 0, 0, 0, time.UTC))
	if _, err := reconciler.Sweep(ctx); err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	pending, _ := store.PendingEvents(ctx, 10)
	if len(pending) != 0 {
		t.Fatalf("inbox holds %d event(s) caused by our own agent", len(pending))
	}
}

func TestSweepReportsAdminFailures(t *testing.T) {
	reconciler, admin, _ := testReconciler(t)
	admin.failOn["IssuesUpdatedSince"] = errors.New("forgejo is down")
	if _, err := reconciler.Sweep(context.Background()); err == nil {
		t.Fatal("Sweep succeeded while Forgejo was unreachable")
	}
}

func TestReconcileDeliveryIDIsStable(t *testing.T) {
	updated := time.Date(2026, 9, 5, 10, 0, 0, 0, time.UTC)
	first := ReconcileDeliveryID(testRepo(t), 12, updated)
	second := ReconcileDeliveryID(testRepo(t), 12, updated.In(time.FixedZone("elsewhere", 3600)))
	if first != second {
		t.Fatalf("delivery ids differ across zones: %q vs %q", first, second)
	}
	if changed := ReconcileDeliveryID(testRepo(t), 12, updated.Add(time.Second)); changed == first {
		t.Fatal("a later update produced the same delivery id, so a real change would be deduplicated away")
	}
}

// AGENT-R-044 and AGENT-R-013 together: the same change arriving once by
// webhook and once by reconciliation must produce one external write. The inbox
// cannot do this on its own -- the two paths carry different delivery ids -- so
// the outbox key is what collapses them.
func TestOutboxCollapsesTwoPathsIntoOneWrite(t *testing.T) {
	store := NewMemoryStore()
	outbox := &Outbox{Store: store}
	ctx := context.Background()
	key := WriteKey(testRepo(t), 12, "status_comment", "v1")

	writes := 0
	for _, path := range []string{"webhook", "reconcile"} {
		did, err := outbox.Do(ctx, key, "run-1", "status_comment", "anas-project/ANAS#12",
			func(context.Context) error { writes++; return nil })
		if err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		if path == "webhook" && !did {
			t.Fatal("the first path did not perform the write")
		}
		if path == "reconcile" && did {
			t.Fatal("the second path performed the write again")
		}
	}
	if writes != 1 {
		t.Fatalf("external writes = %d, want exactly 1", writes)
	}
}

// A failed write leaves the key claimed but incomplete, so the failure is
// visible rather than silently retried into a duplicate.
func TestOutboxDoesNotCompleteAFailedWrite(t *testing.T) {
	store := NewMemoryStore()
	outbox := &Outbox{Store: store}
	ctx := context.Background()
	key := WriteKey(testRepo(t), 12, "comment", "v1")

	if _, err := outbox.Do(ctx, key, "run-1", "comment", "t", func(context.Context) error {
		return errors.New("forgejo refused")
	}); err == nil {
		t.Fatal("Do returned no error for a failed write")
	}
	mine, err := store.WriteByRunID(ctx, "run-1")
	if err != nil {
		t.Fatalf("WriteByRunID: %v", err)
	}
	if !mine {
		t.Fatal("the reservation was rolled back; a retry could now write twice")
	}
}

func TestWriteKeyDistinguishesIntents(t *testing.T) {
	repo := testRepo(t)
	if WriteKey(repo, 12, "comment", "v1") == WriteKey(repo, 12, "comment", "v2") {
		t.Fatal("two plan versions share one idempotency key")
	}
	if WriteKey(repo, 12, "comment", "v1") == WriteKey(repo, 13, "comment", "v1") {
		t.Fatal("two issues share one idempotency key")
	}
}
