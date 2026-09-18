package consolejobs

import (
	"testing"
	"time"

	"github.com/anas-project/ANAS/internal/actionabi"
)

func TestActionStateCloneDetachesCancellationAndPolicy(t *testing.T) {
	now := time.Date(2026, 9, 18, 0, 0, 0, 0, time.UTC)
	original := Job{Action: &ActionState{
		ABI: actionabi.Version, Name: "module.incus.status", InvocationID: "call-1",
		Cancellation: &ActionCancellation{Actor: "administrator-a", RequestedAt: now},
		Policy:       &ActionInvocationPolicy{Concurrency: ActionCoalesce, RequestDigest: "request"},
	}}
	copy := cloneJob(original)
	copy.Action.Cancellation.Actor = "administrator-b"
	copy.Action.Cancellation.RequestedAt = now.Add(time.Hour)
	copy.Action.Policy.Concurrency = ActionQueue
	copy.Action.Policy.RequestDigest = "different-request"
	if original.Action.Cancellation.Actor != "administrator-a" || !original.Action.Cancellation.RequestedAt.Equal(now) {
		t.Fatal("mutating a returned job changed its persisted cancellation evidence")
	}
	if original.Action.Policy.Concurrency != ActionCoalesce || original.Action.Policy.RequestDigest != "request" {
		t.Fatal("mutating a returned job changed its persisted invocation policy")
	}
	if cloneActionState(nil) != nil {
		t.Fatal("nil action state was not preserved")
	}
}

func TestActionControlCancelIntentCannotBeRewritten(t *testing.T) {
	now := time.Date(2026, 9, 18, 0, 0, 0, 0, time.UTC)
	previous := Job{ID: "job-1", Kind: ActionJobKind, Status: StatusRunning, Revision: 2,
		StartedAt: &now, Action: &ActionState{ABI: actionabi.Version, Name: "module.incus.status", InvocationID: "call-1"}}
	next := cloneJob(previous)
	next.Revision++
	next.Action.Cancellation = &ActionCancellation{Actor: "administrator-a", RequestedAt: now}
	if !validActionJobUpdate(previous, next) {
		t.Fatal("first cancellation intent was rejected")
	}
	changed := cloneJob(next)
	changed.Revision++
	changed.Action.Cancellation.Actor = "administrator-b"
	if validActionJobUpdate(next, changed) {
		t.Fatal("first cancellation actor was overwritten")
	}
	changed = cloneJob(next)
	changed.Revision++
	changed.Action.Cancellation.RequestedAt = now.Add(time.Second)
	if validActionJobUpdate(next, changed) {
		t.Fatal("first cancellation time was overwritten")
	}
	changed = cloneJob(next)
	changed.Revision++
	changed.Action.Cancellation = nil
	if validActionJobUpdate(next, changed) {
		t.Fatal("cancellation intent was removed")
	}
}

func TestActionControlOutcomeRequiresStartAndCancellationEvidence(t *testing.T) {
	now := time.Date(2026, 9, 18, 0, 0, 0, 0, time.UTC)
	queuedCancel := Job{Status: StatusCanceled, Action: &ActionState{Outcome: actionabi.Cancelled}}
	if err := validateActionControl(queuedCancel); err != nil {
		t.Fatalf("cancelling a never-started job must not need executor evidence: %v", err)
	}
	runningCancel := cloneJob(queuedCancel)
	runningCancel.StartedAt = &now
	if err := validateActionControl(runningCancel); err == nil {
		t.Fatal("running action accepted cancelled without persisted request")
	}
	runningCancel.Action.Cancellation = &ActionCancellation{Actor: "administrator-a", RequestedAt: now}
	if err := validateActionControl(runningCancel); err != nil {
		t.Fatalf("started action with cancellation evidence rejected: %v", err)
	}
	unstartedSuccess := Job{Status: StatusSucceeded, Action: &ActionState{Outcome: actionabi.Succeeded}}
	if err := validateActionControl(unstartedSuccess); err == nil {
		t.Fatal("unstarted action accepted an execution success")
	}
}
