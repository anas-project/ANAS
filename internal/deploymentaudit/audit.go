// Package deploymentaudit defines the value-free audit boundary shared by
// deployment HTTP handlers and the daemon-lifetime job executor.
package deploymentaudit

import (
	"context"
	"errors"

	"github.com/anas-project/ANAS/internal/consolejobs"
)

type Stage string

const (
	StageJobCreateAuthorized                    Stage = "job_create_authorized"
	StageJobStartAuthorized                     Stage = "job_start_authorized"
	StageJobSucceededAuthorized                 Stage = "job_succeeded_authorized"
	StageJobFailedAuthorized                    Stage = "job_failed_authorized"
	StageJobInterruptedAuthorized               Stage = "job_interrupted_authorized"
	StageJobCanceledAuthorized                  Stage = "job_canceled_authorized"
	StageConfirmationIssueAuthorized            Stage = "confirmation_issue_authorized"
	StageConfirmationConsumeAndCreateAuthorized Stage = "confirmation_consume_and_job_create_authorized"
	StageModuleConfigCommitAuthorized           Stage = "module_config_commit_authorized"
	StageTerminalDescriptorReadyAuthorized      Stage = "terminal_descriptor_ready_authorized"
	StageCredentialRevealAuthorized             Stage = "credential_reveal_authorized"
)

const (
	ActionPlan                = "deployment.plan"
	ActionApply               = "deployment.apply"
	ActionStart               = "deployment.start"
	ActionStop                = "deployment.stop"
	ActionRestart             = "deployment.restart"
	ActionRollback            = "deployment.rollback"
	ActionModuleSync          = "module.sync"
	ActionModuleUpdate        = "module.update"
	ActionModuleEnable        = "module.enable"
	ActionModuleDisable       = "module.disable"
	ActionSnapshotCreate      = "snapshot.create"
	ActionSnapshotPin         = "snapshot.pin"
	ActionSnapshotUnpin       = "snapshot.unpin"
	ActionSnapshotVerify      = "snapshot.verify"
	ActionLocalAdminRotate    = "local_admin.rotate"
	ActionLocalAdminReveal    = "local_admin.reveal"
	ActionModuleCommandInvoke = "module_command.invoke"
)

// Event contains bindings and digests only. Raw confirmation proofs, request
// values, paths, command output, and error text do not cross this boundary.
type Event struct {
	Stage                    Stage
	Action                   string
	Actor                    string
	IdentitySource           string
	IdentityIssuer           string
	IdentitySubject          string
	SemanticRole             string
	DirectoryGroup           string
	TransactionID            string
	WorkspaceID              string
	JobID                    string
	PlanJobID                string
	ConfigValidator          string
	PlanDigest               string
	FailureCode              string
	TargetID                 string
	OperationID              string
	CandidateConfigValidator string
}

type Sink interface {
	RecordDeploymentEvent(context.Context, Event) error
}

type SinkFunc func(context.Context, Event) error

func (record SinkFunc) RecordDeploymentEvent(ctx context.Context, event Event) error {
	return record(ctx, event)
}

// ObserveJobCommit derives stable job and confirmation bindings from a
// consolejobs pre-commit intent and overlays any request-scoped values supplied
// by template. This keeps startup recovery, HTTP, and executor audit records
// structurally identical.
func ObserveJobCommit(sink Sink, template Event) consolejobs.JobCommitObserver {
	return consolejobs.JobCommitObserverFunc(func(ctx context.Context, intent consolejobs.JobCommitIntent) error {
		if sink == nil {
			return errors.New("deployment audit sink is unavailable")
		}
		job := intent.Next
		event := template
		if event.Action == "" {
			event.Action = job.Kind
		}
		if event.Actor == "" {
			event.Actor = job.CreatedBy
		}
		if event.WorkspaceID == "" {
			event.WorkspaceID = job.WorkspaceID
		}
		if event.JobID == "" {
			event.JobID = job.ID
		}
		if event.PlanJobID == "" {
			event.PlanJobID, _ = job.Request[consolejobs.ConfirmationPlanJobRequestKey].(string)
			if event.PlanJobID == "" {
				event.PlanJobID = intent.PlanJobID
			}
		}
		if event.IdentitySource == "" {
			event.IdentitySource, _ = job.Request[consolejobs.ConfirmationIdentitySourceRequestKey].(string)
		}
		if event.TransactionID == "" {
			event.TransactionID, _ = job.Request[consolejobs.ConfirmationTransactionRequestKey].(string)
		}
		if event.ConfigValidator == "" {
			event.ConfigValidator, _ = job.Request["expected_config_validator"].(string)
		}
		if event.TargetID == "" {
			event.TargetID, _ = job.Request["module"].(string)
		}
		if event.OperationID == "" {
			event.OperationID, _ = job.Request["operation_id"].(string)
		}
		if event.PlanDigest == "" {
			event.PlanDigest, _ = job.Request["expected_plan_digest"].(string)
			if event.PlanDigest == "" {
				event.PlanDigest, _ = job.Request["expected_digest"].(string)
			}
		}
		return sink.RecordDeploymentEvent(ctx, event)
	})
}
