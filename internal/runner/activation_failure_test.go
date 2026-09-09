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
