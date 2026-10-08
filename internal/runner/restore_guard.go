package runner

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"
)

const dataRestoreGuardKey = "data_restore_source"

// Keep restoration uncertainty in the existing deployment failure record.
// This guard is workspace-wide because active.yml changes at the final commit.
func dataRestoreGuards(base string) ([]deploymentState, error) {
	paths, err := filepath.Glob(filepath.Join(base, "state", "deployments", "*.yml"))
	if err != nil {
		return nil, err
	}
	var guarded []deploymentState
	for _, path := range paths {
		var state deploymentState
		if err := readYAML(path, &state); err != nil {
			return nil, err
		}
		if source, _ := state.FailureDetail[dataRestoreGuardKey].(string); source != "" {
			guarded = append(guarded, state)
		}
	}
	return guarded, nil
}

func beginDataRestoreGuard(base, targetID, source string) error {
	active, err := loadActiveState(base)
	if err != nil {
		return err
	}
	id := active.ActiveDeployment
	if id == "" {
		id = targetID
	}
	state, err := loadDeploymentState(base, id)
	if err != nil {
		return err
	}
	if state.FailureDetail == nil {
		state.FailureDetail = map[string]any{}
	}
	state.FailureDetail[dataRestoreGuardKey] = source
	state.Failure = "data restore pending: " + source
	if err := saveDeploymentState(base, state); err != nil {
		return err
	}
	if active.ActiveDeployment != "" {
		active.RuntimeStatus = "stopped"
		return saveActiveState(base, active)
	}
	return nil
}

// State and active selection commit only after every data tree and metadata
// operation succeeded. A crash anywhere through this commit still finds a
// guard, including after active.yml has switched to the restored deployment.
func finishDataRestore(base, stateSource, targetID, source string) error {
	var restored deploymentState
	if err := readYAML(stateSource, &restored); err != nil {
		return err
	}
	if restored.ID != targetID {
		return fmt.Errorf("restored deployment state belongs to another artifact")
	}
	if restored.FailureDetail == nil {
		restored.FailureDetail = map[string]any{}
	}
	restored.FailureDetail[dataRestoreGuardKey] = source
	if err := saveDeploymentState(base, restored); err != nil {
		return err
	}
	now := time.Now().UTC().Format(time.RFC3339)
	if err := saveActiveState(base, &activeDeploymentState{APIVersion: activeStateVersion, ActiveDeployment: targetID,
		RuntimeStatus: "stopped", ActivatedAt: now, VerifiedAt: now}); err != nil {
		return err
	}
	if err := rebuildDeploymentIndex(base); err != nil {
		return err
	}
	guards, err := dataRestoreGuards(base)
	if err != nil {
		return err
	}
	for _, state := range guards {
		delete(state.FailureDetail, dataRestoreGuardKey)
		if strings.HasPrefix(state.Failure, "data restore pending: ") {
			state.Failure = ""
		}
		if err := saveDeploymentState(base, state); err != nil {
			return err
		}
	}
	return nil
}
