package runner

import (
	"errors"
	"testing"
)

func TestActivationFailurePersistsPrimaryBeforeRecoveryAndKeepsAllOutcomes(t *testing.T) {
	base := t.TempDir()
	err := recordActivationFailure(base, "candidate", "start_failed", errors.New("primary"), func() []map[string]any {
		state, err := loadDeploymentState(base, "candidate")
		if err != nil || state.Failure != "primary" || state.Status != "failed" || state.FailureDetail["primary"] == nil {
			t.Fatalf("primary not durable before recovery: %#v %v", state, err)
		}
		return []map[string]any{recoveryResult("candidate_stop", errors.New("stop error")), recoveryResult("previous_restore", errors.New("restore error"))}
	})
	if err.Code != "start_failed" {
		t.Fatalf("primary code changed: %v", err)
	}
	state, readErr := loadDeploymentState(base, "candidate")
	if readErr != nil || state.Failure != "primary" {
		t.Fatalf("primary overwritten: %#v %v", state, readErr)
	}
	results, ok := state.FailureDetail["recovery"].([]interface{})
	if !ok || len(results) != 2 {
		t.Fatalf("lost recovery: %#v", state.FailureDetail)
	}
}

func TestFailedCandidateCleanupCannotActivatePreviousDeployment(t *testing.T) {
	candidate, candidateRoot, _ := stopBarrierFixture(t, true)
	previous, previousRoot, _ := stopBarrierFixture(t, false)
	err := activationFailure(t.TempDir(), "candidate", "start_failed", errors.New("primary failure"),
		candidate, candidateRoot, previous, previousRoot, false)
	results, ok := err.Detail["recovery"].([]map[string]any)
	if !ok || len(results) != 2 || results[0]["phase"] != "candidate_stop" || results[0]["status"] != "failed" ||
		results[1]["phase"] != "previous_restore" || results[1]["status"] != "failed" ||
		results[1]["message"] != "not attempted: module cleanup remains unconfirmed" {
		t.Fatal("an unconfirmed candidate was replaced by an automatic recovery activation", err.Detail)
	}
}
