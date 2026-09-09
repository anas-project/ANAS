package runner

import (
	"errors"
	"fmt"
	"github.com/anas-project/ANAS/internal/compose"
	"strings"
)

// Persist the primary failure before any recovery command can fail or hang.
// The legacy failure text remains readable; structured detail never replaces it.
func activationFailure(base, id, code string, cause error, candidate *app, candidateRoot string, previous *app, previousRoot string, jsonMode bool) *CLIError {
	return recordActivationFailure(base, id, code, cause, func() []map[string]any {
		outcomes := []map[string]any{}
		if candidate != nil {
			outcomes = append(outcomes, recoveryResult("candidate_stop", candidate.stopRelease(candidateRoot, jsonMode)))
		}
		if previous != nil {
			outcomes = append(outcomes, recoveryResult("previous_restore", startDeployment(previous, previousRoot, previous.order, jsonMode)))
		}
		return outcomes
	})
}
func recoveryResult(phase string, err error) map[string]any {
	r := map[string]any{"phase": phase, "status": "succeeded"}
	if err != nil {
		r["status"] = "failed"
		r["message"] = err.Error()
	}
	return r
}
func recordActivationFailure(base, id, code string, cause error, recover func() []map[string]any) *CLIError {
	primary := map[string]any{"code": code, "message": cause.Error()}
	var command *compose.CommandFailure
	if errors.As(cause, &command) {
		primary["command"] = command
	}
	detail := map[string]any{"primary": primary, "recovery": []map[string]any{}}
	state, readErr := loadDeploymentState(base, id)
	var persistErr error
	if readErr != nil {
		persistErr = readErr
	} else {
		state.Status = "failed"
		state.Failure = cause.Error()
		state.FailureDetail = detail
		persistErr = saveDeploymentState(base, state)
	}
	outcomes := recover()
	if persistErr != nil {
		outcomes = append(outcomes, recoveryResult("primary_persist", persistErr))
	}
	detail["recovery"] = outcomes
	if readErr == nil {
		if err := saveDeploymentState(base, state); err != nil {
			outcomes = append(outcomes, recoveryResult("recovery_persist", err))
			detail["recovery"] = outcomes
		}
	}
	parts := []string{cause.Error()}
	for _, r := range outcomes {
		parts = append(parts, fmt.Sprintf("recovery %s: %s", r["phase"], r["status"]))
	}
	return &CLIError{Code: code, Message: strings.Join(parts, "; "), Detail: detail, Exit: exitFailure}
}
