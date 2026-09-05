package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
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

func TestDeploymentAuditAcceptsLifecycleAndModuleCommitBindings(t *testing.T) {
	appender := &recordingConfigAuditAppender{}
	sink := deploymentAuditSink{writer: appender}
	lifecycle := deploymentaudit.Event{
		Stage: deploymentaudit.StageJobSucceededAuthorized, Action: deploymentaudit.ActionRestart,
		Actor: "local-owner", WorkspaceID: "default", JobID: "job-restart",
	}
	if err := sink.RecordDeploymentEvent(context.Background(), lifecycle); err != nil {
		t.Fatalf("lifecycle audit: %v", err)
	}
	module := deploymentaudit.Event{
		Stage: deploymentaudit.StageModuleConfigCommitAuthorized, Action: deploymentaudit.ActionModuleEnable,
		Actor: "local-owner", WorkspaceID: "default", JobID: "job-enable", TargetID: "demo",
		OperationID:              "cfg-0123456789abcdef0123456789abcdef",
		ConfigValidator:          "cfgv-" + "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
		CandidateConfigValidator: "cfgv-" + "abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789",
	}
	if err := sink.RecordDeploymentEvent(context.Background(), module); err != nil {
		t.Fatalf("Module commit audit: %v", err)
	}
	if len(appender.events) != 2 || appender.events[1].Details["target_id"] != "demo" ||
		appender.events[1].Details["candidate_config_validator"] != module.CandidateConfigValidator {
		t.Fatalf("persisted Module audit = %#v", appender.events)
	}
}

// The audit sink fails closed on an action it does not recognise, and job
// creation treats a failed audit as a refusal. So an action a route can issue
// but this allowlist omits makes that route return 503 in a real daemon while
// every unit test that stubs the sink still passes -- which is how the M4
// snapshot and local-admin actions and the Module command invoke action all
// shipped unaccepted.
//
// Binding the allowlist to the declared action set removes the chance to add
// one without the other.
func TestAuditAllowlistAcceptsEveryDeclaredAction(t *testing.T) {
	declared := []string{
		deploymentaudit.ActionPlan,
		deploymentaudit.ActionApply,
		deploymentaudit.ActionStart,
		deploymentaudit.ActionStop,
		deploymentaudit.ActionRestart,
		deploymentaudit.ActionRollback,
		deploymentaudit.ActionModuleSync,
		deploymentaudit.ActionModuleUpdate,
		deploymentaudit.ActionModuleEnable,
		deploymentaudit.ActionModuleDisable,
		deploymentaudit.ActionSnapshotCreate,
		deploymentaudit.ActionSnapshotPin,
		deploymentaudit.ActionSnapshotUnpin,
		deploymentaudit.ActionSnapshotVerify,
		deploymentaudit.ActionLocalAdminRotate,
		deploymentaudit.ActionLocalAdminReveal,
		deploymentaudit.ActionModuleCommandInvoke,
	}
	for _, action := range declared {
		if !validDeploymentAuditAction(action) {
			t.Errorf("the audit sink rejects %q, so every route issuing it returns 503;\n"+
				"add it to validDeploymentAuditAction", action)
		}
	}
	if validDeploymentAuditAction("deployment.not-an-action") {
		t.Error("the allowlist accepts an undeclared action")
	}
	// Every constant this package declares must appear above, so a new action
	// cannot be added to deploymentaudit without being considered here.
	if len(declared) != declaredDeploymentAuditActionCount(t) {
		t.Fatalf("this test covers %d actions but internal/deploymentaudit declares %d; add the new one",
			len(declared), declaredDeploymentAuditActionCount(t))
	}
}

func declaredDeploymentAuditActionCount(t *testing.T) int {
	t.Helper()
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate test source")
	}
	path := filepath.Clean(filepath.Join(filepath.Dir(source), "..", "..", "internal", "deploymentaudit", "audit.go"))
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return len(regexp.MustCompile(`(?m)^\s*Action[A-Za-z]+\s*=\s*"`).FindAllString(string(body), -1))
}
