package main

import (
	"context"
	"errors"
	"testing"

	"github.com/anas-project/ANAS/internal/audit"
	"github.com/anas-project/ANAS/internal/deploymentaudit"
)

func TestDeploymentAuditSinkPersistsValueFreeBindings(t *testing.T) {
	appender := &recordingConfigAuditAppender{}
	event := deploymentaudit.Event{
		Stage: deploymentaudit.StageConfirmationConsumeAndCreateAuthorized, Action: deploymentaudit.ActionApply,
		Actor: "bootstrap:transaction-a", IdentitySource: "bootstrap", TransactionID: "transaction-a",
		WorkspaceID: "default", JobID: "job-apply", PlanJobID: "job-plan",
		ConfigValidator: "cfgv-0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
		PlanDigest:      "abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789",
	}
	if err := (deploymentAuditSink{writer: appender}).RecordDeploymentEvent(context.Background(), event); err != nil {
		t.Fatal(err)
	}
	if len(appender.events) != 1 {
		t.Fatalf("audit event count = %d, want 1", len(appender.events))
	}
	persisted := appender.events[0]
	if persisted.Type != "workspace.deployment" || persisted.Outcome != string(event.Stage) || persisted.Actor != event.Actor || persisted.WorkspaceID != event.WorkspaceID {
		t.Fatalf("persisted audit event = %#v", persisted)
	}
	if persisted.Details["job_id"] != event.JobID || persisted.Details["plan_job_id"] != event.PlanJobID || persisted.Details["config_validator"] != event.ConfigValidator || persisted.Details["plan_digest"] != event.PlanDigest {
		t.Fatalf("persisted audit details = %#v", persisted.Details)
	}
	for _, forbidden := range []string{"token", "proof", "request", "path", "message"} {
		if _, exists := persisted.Details[forbidden]; exists {
			t.Fatalf("audit details contain forbidden field %q", forbidden)
		}
	}
}

func TestDeploymentAuditSinkFailsClosed(t *testing.T) {
	valid := deploymentaudit.Event{
		Stage: deploymentaudit.StageJobFailedAuthorized, Action: deploymentaudit.ActionApply,
		Actor: "operator", WorkspaceID: "default", JobID: "job-apply", FailureCode: "apply_failed",
	}
	if err := (deploymentAuditSink{}).RecordDeploymentEvent(context.Background(), valid); !errors.Is(err, audit.ErrUnavailable) {
		t.Fatalf("missing writer error = %v, want audit unavailable", err)
	}
	want := errors.New("disk full")
	if err := (deploymentAuditSink{writer: &recordingConfigAuditAppender{err: want}}).RecordDeploymentEvent(context.Background(), valid); !errors.Is(err, audit.ErrUnavailable) || !errors.Is(err, want) {
		t.Fatalf("append error = %v", err)
	}
	invalid := valid
	invalid.FailureCode = ""
	if err := (deploymentAuditSink{writer: &recordingConfigAuditAppender{}}).RecordDeploymentEvent(context.Background(), invalid); !errors.Is(err, audit.ErrUnavailable) {
		t.Fatalf("invalid event error = %v, want audit unavailable", err)
	}
}
