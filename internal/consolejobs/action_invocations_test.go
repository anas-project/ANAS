package consolejobs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/anas-project/ANAS/internal/actionabi"
)

const invocationTestAction = "module.example.inspect"

func invocationTestObserver() JobCommitObserver {
	return JobCommitObserverFunc(func(context.Context, JobCommitIntent) error { return nil })
}

func invocationTestSpec(key, actor, value string) CreateSpec {
	return CreateSpec{
		WorkspaceID: "workspace", Request: map[string]any{"value": value},
		Idempotency: IdempotencyInput{Principal: actor, Key: key, Method: "POST", CanonicalPath: "/api/invoke", RequestDigest: strings.Repeat("f", 64)},
	}
}

func invocationTestCreate(t *testing.T, store *Store, spec CreateSpec, invocation string, policy ActionConcurrency) CreateResult {
	t.Helper()
	result, err := store.CreateActionWithPolicyObserved(context.Background(), spec, invocationTestAction, invocation, policy, invocationTestObserver())
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func TestActionInvocationKeyIdentityIgnoresActorAndTransport(t *testing.T) {
	store, _ := openStoreForTest(t, Options{})
	first := invocationTestCreate(t, store, invocationTestSpec("same-key", "alice", "same"), "call-first", ActionReject)
	spec := invocationTestSpec("same-key", "bob", "same")
	spec.Idempotency.Method, spec.Idempotency.CanonicalPath = "CLI", "/different/entry"
	spec.Idempotency.RequestDigest = "untrusted-digest-is-ignored"
	retry := invocationTestCreate(t, store, spec, "call-retry", ActionReject)
	if !retry.Existing || retry.Job.ID != first.Job.ID || retry.Job.CreatedBy != "alice" || retry.Job.Action.InvocationID != "call-first" {
		t.Fatal("retry identity was partitioned by actor or entrypoint")
	}
	spec.Request["value"] = "different"
	_, err := store.CreateActionWithPolicyObserved(context.Background(), spec, invocationTestAction, "call-conflict", ActionReject, invocationTestObserver())
	var conflict *IdempotencyConflictError
	if !errors.As(err, &conflict) || conflict.ExistingJobID != first.Job.ID {
		t.Fatalf("different parameters reused a key: %v", err)
	}
	spec.Request["value"], spec.WorkspaceID = "same", "other-workspace"
	_, err = store.CreateActionWithPolicyObserved(context.Background(), spec, invocationTestAction, "call-workspace", ActionReject, invocationTestObserver())
	if !errors.As(err, &conflict) {
		t.Fatalf("workspace change did not conflict: %v", err)
	}
	spec.WorkspaceID = "workspace"
	other, err := store.CreateActionWithPolicyObserved(context.Background(), spec, "module.example.status", "call-other-action", ActionReject, invocationTestObserver())
	if err != nil || other.Existing || other.Job.ID == first.Job.ID {
		t.Fatalf("different actions shared a key namespace: %v", err)
	}
}

func TestActionCoalescePersistsNewKeyOnlyAfterAudit(t *testing.T) {
	store, _ := openStoreForTest(t, Options{})
	first := invocationTestCreate(t, store, invocationTestSpec("", "alice", "same"), "call-keyless", ActionCoalesce)
	fail := true
	observer := JobCommitObserverFunc(func(_ context.Context, intent JobCommitIntent) error {
		if intent.Operation != JobCommitActionJoin || intent.Actor != "bob" || intent.Previous == nil || intent.Next.CreatedBy != "alice" {
			t.Error("join audit lost caller or immutable creator")
		}
		if fail {
			return errors.New("audit unavailable")
		}
		return nil
	})
	spec := invocationTestSpec("joined-key", "bob", "same")
	_, err := store.CreateActionWithPolicyObserved(context.Background(), spec, invocationTestAction, "call-denied", ActionCoalesce, observer)
	if err == nil {
		t.Fatal("new retry binding bypassed audit")
	}
	unchanged, err := store.Get(context.Background(), first.Job.ID)
	if err != nil || len(unchanged.Action.Policy.RetryKeys) != 0 || unchanged.Revision != first.Job.Revision {
		t.Fatalf("failed audit changed bindings: %v", err)
	}
	fail = false
	joined, err := store.CreateActionWithPolicyObserved(context.Background(), spec, invocationTestAction, "call-joined", ActionCoalesce, observer)
	if err != nil || !joined.Existing || joined.Job.ID != first.Job.ID || len(joined.Job.Action.Policy.RetryKeys) != 1 || joined.Job.Action.LastSeq != 0 {
		t.Fatalf("coalescing created another execution: %v", err)
	}
	retry := invocationTestCreate(t, store, invocationTestSpec("joined-key", "carol", "same"), "call-lost-response", ActionCoalesce)
	if retry.Job.ID != first.Job.ID || retry.Job.Revision != joined.Job.Revision {
		t.Fatal("joined retry key was not durable")
	}
}

func TestActionRetryKeyExpiresAtTerminalPlusOneHour(t *testing.T) {
	store, _ := openStoreForTest(t, Options{})
	now := time.Date(2026, 9, 18, 10, 0, 0, 0, time.UTC)
	store.now = func() time.Time { return now }
	spec := invocationTestSpec("reusable", "alice", "same")
	first := invocationTestCreate(t, store, spec, "call-first", ActionCoalesce)
	now = now.Add(2 * time.Hour)
	pendingRetry := invocationTestCreate(t, store, spec, "call-still-pending", ActionCoalesce)
	if pendingRetry.Job.ID != first.Job.ID {
		t.Fatal("pending key expired before a terminal")
	}
	terminal, err := store.CancelQueuedActionObserved(context.Background(), first.Job.ID, first.Job.Action.InvocationID, invocationTestObserver())
	if err != nil {
		t.Fatal(err)
	}
	now = terminal.FinishedAt.Add(ActionKeyRetention - time.Nanosecond)
	before := invocationTestCreate(t, store, spec, "call-before-deadline", ActionCoalesce)
	if !before.Existing || before.Job.ID != first.Job.ID {
		t.Fatal("key expired early")
	}
	now = terminal.FinishedAt.Add(ActionKeyRetention)
	after := invocationTestCreate(t, store, spec, "call-at-deadline", ActionCoalesce)
	if after.Existing || after.Job.ID == first.Job.ID {
		t.Fatal("key did not expire at the deadline")
	}
	// An old owner must not resurrect after a key has been rebound.
	now = terminal.FinishedAt.Add(time.Minute)
	rollback := invocationTestCreate(t, store, spec, "call-clock-rollback", ActionCoalesce)
	if !rollback.Existing || rollback.Job.ID != after.Job.ID {
		t.Fatal("clock rollback resurrected a retired key owner")
	}
}

func TestActionKeyCanRebindToAnOlderCoalescedJob(t *testing.T) {
	store, _ := openStoreForTest(t, Options{})
	now := time.Date(2026, 9, 18, 10, 0, 0, 0, time.UTC)
	store.now = func() time.Time { return now }
	older := invocationTestCreate(t, store, invocationTestSpec("", "alice", "older"), "call-older", ActionCoalesce)
	now = now.Add(time.Second)
	younger := invocationTestCreate(t, store, invocationTestSpec("transferable", "alice", "younger"), "call-younger", ActionCoalesce)
	if _, err := store.CancelQueuedActionObserved(context.Background(), younger.Job.ID, younger.Job.Action.InvocationID, invocationTestObserver()); err != nil {
		t.Fatal(err)
	}
	now = now.Add(ActionKeyRetention)
	spec := invocationTestSpec("transferable", "bob", "older")
	joined := invocationTestCreate(t, store, spec, "call-transfer", ActionCoalesce)
	retry := invocationTestCreate(t, store, spec, "call-transfer-retry", ActionCoalesce)
	if !joined.Existing || joined.Job.ID != older.Job.ID || retry.Job.ID != older.Job.ID {
		t.Fatal("retry ownership was inferred from job creation time instead of binding time")
	}
}

func TestActionCoalesceConcurrentInvocationsUseOneJob(t *testing.T) {
	store, _ := openStoreForTest(t, Options{})
	const calls = 12
	type result struct {
		job CreateResult
		err error
	}
	results := make(chan result, calls)
	var workers sync.WaitGroup
	for index := 0; index < calls; index++ {
		workers.Add(1)
		go func(index int) {
			defer workers.Done()
			spec := invocationTestSpec(fmt.Sprintf("key-%d", index), fmt.Sprintf("actor-%d", index), "same")
			job, err := store.CreateActionWithPolicyObserved(context.Background(), spec, invocationTestAction, fmt.Sprintf("call-%d", index), ActionCoalesce, invocationTestObserver())
			results <- result{job, err}
		}(index)
	}
	workers.Wait()
	close(results)
	id := ""
	created := 0
	for result := range results {
		if result.err != nil {
			t.Fatal(result.err)
		}
		if id == "" {
			id = result.job.Job.ID
		}
		if result.job.Job.ID != id {
			t.Fatal("concurrent invocations created multiple jobs")
		}
		if !result.job.Existing {
			created++
		}
	}
	job, err := store.Get(context.Background(), id)
	if err != nil || created != 1 || len(job.Action.Policy.RetryKeys) != calls {
		t.Fatalf("concurrent retry bindings were lost: %v", err)
	}
}

func TestActionRejectReportsInFlightButAllowsSameKeyRetry(t *testing.T) {
	store, _ := openStoreForTest(t, Options{})
	first := invocationTestCreate(t, store, invocationTestSpec("first", "alice", "same"), "call-first", ActionReject)
	_, err := store.CreateActionWithPolicyObserved(context.Background(), invocationTestSpec("second", "bob", "same"), invocationTestAction, "call-second", ActionReject, invocationTestObserver())
	var inFlight *ActionInFlightError
	if !errors.As(err, &inFlight) || inFlight.ExistingJobID != first.Job.ID || inFlight.Policy != ActionReject {
		t.Fatalf("reject policy did not identify the active job: %v", err)
	}
	retry := invocationTestCreate(t, store, invocationTestSpec("first", "bob", "same"), "call-retry", ActionReject)
	if !retry.Existing || retry.Job.ID != first.Job.ID {
		t.Fatal("reject policy overrode a valid retry key")
	}
}

func TestActionQueueSerializesEquivalentReadOnlyJobs(t *testing.T) {
	store, directory := openStoreForTest(t, Options{})
	lease, err := AcquireExecutionLease(context.Background(), directory)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = lease.Close() })
	now := time.Date(2026, 9, 18, 10, 0, 0, 0, time.UTC)
	store.now = func() time.Time { return now }
	first := invocationTestCreate(t, store, invocationTestSpec("first", "alice", "same"), "call-first", ActionQueue)
	now = now.Add(time.Second)
	second := invocationTestCreate(t, store, invocationTestSpec("second", "bob", "same"), "call-second", ActionQueue)
	if first.Job.Mutating || second.Job.Mutating || second.Existing {
		t.Fatal("fixture must create distinct read-only jobs")
	}
	var inFlight *ActionInFlightError
	if _, err := store.StartActionObserved(context.Background(), second.Job.ID, lease, invocationTestObserver()); !errors.As(err, &inFlight) || inFlight.ExistingJobID != first.Job.ID {
		t.Fatalf("later queued job overtook its predecessor: %v", err)
	}
	if _, err := store.StartActionObserved(context.Background(), first.Job.ID, lease, invocationTestObserver()); err != nil {
		t.Fatal(err)
	}
	if _, err := store.StartActionObserved(context.Background(), second.Job.ID, lease, invocationTestObserver()); !errors.As(err, &inFlight) {
		t.Fatalf("read-only queue ran equivalent actions together: %v", err)
	}
	changed := false
	if _, err := store.CompleteActionObserved(context.Background(), lease, actionabi.Event{
		ABI: actionabi.Version, JobID: first.Job.ID, InvocationID: first.Job.Action.InvocationID, Type: "result",
		Result: &actionabi.Result{Outcome: actionabi.Succeeded, Changed: &changed, Value: json.RawMessage(`{}`)},
	}, invocationTestObserver()); err != nil {
		t.Fatal(err)
	}
	if _, err := store.StartActionObserved(context.Background(), second.Job.ID, lease, invocationTestObserver()); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CompleteActionObserved(context.Background(), lease, actionabi.Event{
		ABI: actionabi.Version, JobID: second.Job.ID, InvocationID: second.Job.Action.InvocationID, Type: "result",
		Result: &actionabi.Result{Outcome: actionabi.Succeeded, Changed: &changed, Value: json.RawMessage(`{}`)},
	}, invocationTestObserver()); err != nil {
		t.Fatal(err)
	}
}

func TestActionRetryBindingsSurviveCompactionAndReopen(t *testing.T) {
	store, directory := openStoreForTest(t, Options{})
	now := time.Date(2026, 9, 18, 10, 0, 0, 0, time.UTC)
	store.now = func() time.Time { return now }
	first := invocationTestCreate(t, store, invocationTestSpec("first", "alice", "same"), "call-first", ActionCoalesce)
	joined := invocationTestCreate(t, store, invocationTestSpec("second", "bob", "same"), "call-second", ActionCoalesce)
	now = now.Add(time.Second)
	if _, err := store.CancelQueuedActionObserved(context.Background(), first.Job.ID, first.Job.Action.InvocationID, invocationTestObserver()); err != nil {
		t.Fatal(err)
	}
	if err := store.Compact(context.Background()); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(directory, Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	reopened.now = func() time.Time { return now }
	retry := invocationTestCreate(t, reopened, invocationTestSpec("second", "carol", "same"), "call-reopened", ActionCoalesce)
	if !retry.Existing || retry.Job.ID != joined.Job.ID || len(retry.Job.Action.Policy.RetryKeys) != 2 || retry.Job.Action.LastSeq != 1 {
		t.Fatal("compaction lost retry aliases or changed event sequences")
	}
}

func TestActionRetryBindingCapacityIsExplicitAndCopiesAreDetached(t *testing.T) {
	store, _ := openStoreForTest(t, Options{})
	first := invocationTestCreate(t, store, invocationTestSpec("key-0", "alice", "same"), "call-0", ActionCoalesce)
	for index := 1; index < MaxActionRetryKeys; index++ {
		invocationTestCreate(t, store, invocationTestSpec(fmt.Sprintf("key-%d", index), "alice", "same"), fmt.Sprintf("call-%d", index), ActionCoalesce)
	}
	_, err := store.CreateActionWithPolicyObserved(context.Background(), invocationTestSpec("overflow", "alice", "same"), invocationTestAction, "call-overflow", ActionCoalesce, invocationTestObserver())
	if !errors.Is(err, ErrCapacity) {
		t.Fatalf("retry aliases were silently discarded: %v", err)
	}
	retry := invocationTestCreate(t, store, invocationTestSpec("key-0", "bob", "same"), "call-earliest-retry", ActionCoalesce)
	if retry.Job.ID != first.Job.ID {
		t.Fatal("oldest retry alias was lost")
	}
	retry.Job.Action.Policy.RetryKeys[0].Digest = strings.Repeat("0", 64)
	stored, err := store.Get(context.Background(), first.Job.ID)
	if err != nil || stored.Action.Policy.RetryKeys[0].Digest != DigestRequest([]byte("key-0")) {
		t.Fatalf("returned policy mutated durable state: %v", err)
	}
}

func TestActionInvocationRejectsNonPublicParameters(t *testing.T) {
	store, _ := openStoreForTest(t, Options{})
	spec := invocationTestSpec("key", "alice", "same")
	spec.Request = map[string]any{"token": "private-material-must-not-persist"}
	_, err := store.CreateActionWithPolicyObserved(context.Background(), spec, invocationTestAction, "call-private", ActionCoalesce, invocationTestObserver())
	if err == nil || strings.Contains(err.Error(), "private-material") {
		t.Fatal("generic redaction silently changed execution parameters or exposed them")
	}
	jobs, err := store.List(context.Background())
	if err != nil || len(jobs) != 0 {
		t.Fatalf("non-public parameters created a job: %v", err)
	}
}

func TestActionInvocationCanonicalizesObjectOrderBeforeHashing(t *testing.T) {
	store, _ := openStoreForTest(t, Options{})
	spec := invocationTestSpec("key", "alice", "same")
	spec.Request["value"] = struct {
		Z string `json:"z"`
		A string `json:"a"`
	}{Z: "last", A: "first"}
	first := invocationTestCreate(t, store, spec, "call-struct", ActionCoalesce)
	spec.Request["value"] = map[string]any{"a": "first", "z": "last"}
	retry := invocationTestCreate(t, store, spec, "call-map", ActionCoalesce)
	if !retry.Existing || retry.Job.ID != first.Job.ID {
		t.Fatal("equivalent objects depended on serialization order")
	}
}

func TestActionRetrySnapshotRejectsOverlappingOwners(t *testing.T) {
	store, _ := openStoreForTest(t, Options{})
	first := invocationTestCreate(t, store, invocationTestSpec("one", "alice", "one"), "call-one", ActionCoalesce)
	second := invocationTestCreate(t, store, invocationTestSpec("two", "alice", "two"), "call-two", ActionCoalesce)
	state := store.state.clone()
	forged := state.jobs[second.Job.ID]
	forged.Action.Policy.RetryKeys[0].Digest = first.Job.Action.Policy.RetryKeys[0].Digest
	state.jobs[forged.ID] = forged
	if err := state.validateActionRetryHistory(); err == nil {
		t.Fatal("snapshot accepted two still-live owners of one action key")
	}
}

func TestActionInvocationPreservesExactJSONIntegers(t *testing.T) {
	store, _ := openStoreForTest(t, Options{})
	spec := invocationTestSpec("key", "alice", "same")
	spec.Request["value"] = json.Number("18446744073709551615")
	first := invocationTestCreate(t, store, spec, "call-integer", ActionCoalesce)
	retry := invocationTestCreate(t, store, spec, "call-integer-retry", ActionCoalesce)
	if retry.Job.ID != first.Job.ID || retry.Job.Request["value"] != json.Number("18446744073709551615") {
		t.Fatal("retry normalization rounded a uint64 value")
	}
}
