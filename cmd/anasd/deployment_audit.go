package main

import (
	"context"
	"encoding/hex"
	"errors"
	"log"
	"strings"

	"github.com/anas-project/ANAS/internal/audit"
	"github.com/anas-project/ANAS/internal/consoleaudit"
	"github.com/anas-project/ANAS/internal/deploymentaudit"
)

type deploymentAuditSink struct {
	writer consoleaudit.Appender
	logger *log.Logger
}

func (sink deploymentAuditSink) RecordDeploymentEvent(ctx context.Context, event deploymentaudit.Event) error {
	if sink.writer == nil || !validDeploymentAuditEvent(event) {
		sink.reportFailure(event, audit.ErrUnavailable)
		return audit.ErrUnavailable
	}
	details := map[string]any{
		"stage":  event.Stage,
		"action": event.Action,
		"job_id": event.JobID,
	}
	for key, value := range map[string]string{
		"identity_source":            event.IdentitySource,
		"identity_issuer":            event.IdentityIssuer,
		"identity_subject":           event.IdentitySubject,
		"semantic_role":              event.SemanticRole,
		"directory_group":            event.DirectoryGroup,
		"transaction_id":             event.TransactionID,
		"plan_job_id":                event.PlanJobID,
		"config_validator":           event.ConfigValidator,
		"plan_digest":                event.PlanDigest,
		"failure_code":               event.FailureCode,
		"target_id":                  event.TargetID,
		"operation_id":               event.OperationID,
		"candidate_config_validator": event.CandidateConfigValidator,
	} {
		if value != "" {
			details[key] = value
		}
	}
	_, err := sink.writer.AppendContext(ctx, audit.Event{
		Type: "workspace.deployment", Actor: event.Actor, WorkspaceID: event.WorkspaceID,
		Outcome: string(event.Stage), Details: details,
	})
	if err != nil {
		failure := errors.Join(audit.ErrUnavailable, err)
		sink.reportFailure(event, failure)
		return failure
	}
	return nil
}

func validDeploymentAuditEvent(event deploymentaudit.Event) bool {
	if event.Actor == "" || event.WorkspaceID == "" || event.JobID == "" ||
		len(event.Actor) > 512 || len(event.WorkspaceID) > 256 || len(event.JobID) > 256 ||
		len(event.IdentitySource) > 512 || len(event.IdentityIssuer) > 1024 || len(event.IdentitySubject) > 512 ||
		len(event.SemanticRole) > 128 || len(event.DirectoryGroup) > 512 || len(event.TransactionID) > 256 || len(event.PlanJobID) > 256 || len(event.FailureCode) > 128 ||
		len(event.TargetID) > 256 || len(event.OperationID) > 256 {
		return false
	}
	if !validDeploymentAuditAction(event.Action) {
		return false
	}
	switch event.Stage {
	case deploymentaudit.StageJobCreateAuthorized,
		deploymentaudit.StageJobStartAuthorized,
		deploymentaudit.StageJobSucceededAuthorized:
	case deploymentaudit.StageJobFailedAuthorized:
		if event.FailureCode == "" {
			return false
		}
	case deploymentaudit.StageJobInterruptedAuthorized:
	case deploymentaudit.StageJobCanceledAuthorized:
	case deploymentaudit.StageConfirmationIssueAuthorized:
		if event.Action != deploymentaudit.ActionPlan || event.PlanJobID == "" || event.ConfigValidator == "" || event.PlanDigest == "" {
			return false
		}
	case deploymentaudit.StageConfirmationConsumeAndCreateAuthorized:
		if event.Action != deploymentaudit.ActionApply || event.PlanJobID == "" || event.ConfigValidator == "" || event.PlanDigest == "" {
			return false
		}
	case deploymentaudit.StageModuleConfigCommitAuthorized:
		if event.Action != deploymentaudit.ActionModuleEnable && event.Action != deploymentaudit.ActionModuleDisable ||
			event.TargetID == "" || !validConfigAuditOperationID(event.OperationID) ||
			event.ConfigValidator == "" || event.CandidateConfigValidator == "" {
			return false
		}
	default:
		return false
	}
	if event.ConfigValidator != "" && !validDeploymentConfigValidator(event.ConfigValidator) {
		return false
	}
	if event.CandidateConfigValidator != "" && !validDeploymentConfigValidator(event.CandidateConfigValidator) {
		return false
	}
	if event.OperationID != "" && !validConfigAuditOperationID(event.OperationID) {
		return false
	}
	if (event.Action == deploymentaudit.ActionModuleEnable || event.Action == deploymentaudit.ActionModuleDisable) && event.TargetID == "" {
		return false
	}
	return event.PlanDigest == "" || validLowerHexDigest(event.PlanDigest)
}

func validDeploymentAuditAction(action string) bool {
	switch action {
	case deploymentaudit.ActionPlan,
		deploymentaudit.ActionApply,
		deploymentaudit.ActionStart,
		deploymentaudit.ActionStop,
		deploymentaudit.ActionRestart,
		deploymentaudit.ActionRollback,
		deploymentaudit.ActionModuleSync,
		deploymentaudit.ActionModuleUpdate,
		deploymentaudit.ActionModuleEnable,
		deploymentaudit.ActionModuleDisable:
		return true
	default:
		return false
	}
}

func validDeploymentConfigValidator(value string) bool {
	digest, ok := strings.CutPrefix(value, "cfgv-")
	return ok && validLowerHexDigest(digest)
}

func validLowerHexDigest(value string) bool {
	if len(value) != 64 || value != strings.ToLower(value) {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func (sink deploymentAuditSink) reportFailure(event deploymentaudit.Event, err error) {
	if sink.logger != nil {
		sink.logger.Printf("deployment audit append failed: stage=%q action=%q job=%q workspace=%q: %v",
			event.Stage, event.Action, event.JobID, event.WorkspaceID, err)
	}
}
