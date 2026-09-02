package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/anas-project/ANAS/internal/application"
	"github.com/anas-project/ANAS/internal/consoleauth"
	"github.com/anas-project/ANAS/internal/consolejobs"
	"github.com/anas-project/ANAS/internal/deploymentaudit"
)

type fakeMaintenanceService struct {
	snapshots        application.SnapshotListResult
	backupPlan       application.BackupPlanResult
	backups          application.BackupListResult
	admins           application.LocalAdminListResult
	credential       application.LocalAdminCredential
	descriptor       application.TerminalActionDescriptor
	stateDigest      string
	previewRequest   application.TerminalActionRequest
	revealCalls      int
	stepUpDigestCall int
}

func (service *fakeMaintenanceService) ListSnapshots(context.Context) (application.SnapshotListResult, error) {
	return service.snapshots, nil
}
func (*fakeMaintenanceService) CreateSnapshot(context.Context, application.SnapshotCreateRequest) (application.SnapshotRecord, error) {
	return application.SnapshotRecord{}, nil
}
func (*fakeMaintenanceService) SetSnapshotPinned(context.Context, application.SnapshotPinRequest) (application.SnapshotRecord, error) {
	return application.SnapshotRecord{}, nil
}
func (*fakeMaintenanceService) VerifySnapshots(context.Context, application.SnapshotVerifyRequest) (application.SnapshotVerifyResult, error) {
	return application.SnapshotVerifyResult{}, nil
}
func (service *fakeMaintenanceService) PlanBackup(context.Context, application.BackupPlanRequest) (application.BackupPlanResult, error) {
	return service.backupPlan, nil
}
func (service *fakeMaintenanceService) ListBackups(context.Context, application.BackupListRequest) (application.BackupListResult, error) {
	return service.backups, nil
}
func (service *fakeMaintenanceService) ListLocalAdmins(context.Context) (application.LocalAdminListResult, error) {
	return service.admins, nil
}
func (*fakeMaintenanceService) RotateLocalAdmin(context.Context, application.LocalAdminTarget) (application.LocalAdminRecord, error) {
	return application.LocalAdminRecord{}, nil
}
func (service *fakeMaintenanceService) RevealLocalAdmin(context.Context, application.LocalAdminTarget) (application.LocalAdminCredential, error) {
	service.revealCalls++
	return service.credential, nil
}
func (service *fakeMaintenanceService) PreviewTerminalAction(_ context.Context, request application.TerminalActionRequest) (application.TerminalActionDescriptor, error) {
	service.previewRequest = request
	return service.descriptor, nil
}
func (service *fakeMaintenanceService) StepUpStateDigest(context.Context, string, string) (string, error) {
	service.stepUpDigestCall++
	return service.stateDigest, nil
}

func TestMaintenanceReadAndTerminalPreviewExposeOnlyPublicProjection(t *testing.T) {
	service := &fakeMaintenanceService{
		snapshots: application.SnapshotListResult{Workspace: "/private/workspace", KeepAuto: 3, Snapshots: []application.SnapshotRecord{{ID: "snap-1", Modules: map[string]string{}}}},
		descriptor: application.TerminalActionDescriptor{
			Operation: "snapshot.restore", WorkspaceID: "/private/workspace",
			Target:  application.TerminalActionTarget{SnapshotID: "snap-1"},
			Impact:  application.TerminalActionImpact{Data: true, Reversible: true},
			Argv:    []string{"anas", "snapshot", "restore", "snap-1", "-w", "/registered/workspace"},
			Display: "anas snapshot restore snap-1 -w /registered/workspace", CLIContract: "docs/reference/contracts/snapshot.md#anas-snapshot-restore-id",
		},
	}
	auditSink := &recordingDeploymentAuditSink{}
	handler, _ := newMaintenanceRouteHandler(t, service, auditSink, &fakeDeploymentStepUp{})

	listed := fullDeploymentRequest(handler, http.MethodGet, "/api/v1/workspaces/main/snapshots", "", "")
	if listed.Code != http.StatusOK || strings.Contains(listed.Body.String(), "/private/workspace") || !strings.Contains(listed.Body.String(), `"workspace_id":"main"`) {
		t.Fatalf("snapshot list = %d, %s", listed.Code, listed.Body.String())
	}
	preview := fullDeploymentRequest(handler, http.MethodPost, "/api/v1/workspaces/main/terminal-action-previews", `{"operation":"snapshot.restore","snapshot":{"id":"snap-1","restore_userdata":false}}`, "")
	if preview.Code != http.StatusOK || strings.Contains(preview.Body.String(), "/private/workspace") || !strings.Contains(preview.Body.String(), `"argv":["anas","snapshot","restore"`) {
		t.Fatalf("terminal preview = %d, %s", preview.Code, preview.Body.String())
	}
	if service.previewRequest.Operation != "snapshot.restore" || len(auditSink.events) != 1 || auditSink.events[0].Stage != deploymentaudit.StageTerminalDescriptorReadyAuthorized {
		t.Fatalf("preview request/audit = %#v / %#v", service.previewRequest, auditSink.events)
	}
	unknown := fullDeploymentRequest(handler, http.MethodPost, "/api/v1/workspaces/main/terminal-action-previews", `{"operation":"snapshot.restore","path":"/tmp/evil","snapshot":{"id":"snap-1"}}`, "")
	if unknown.Code != http.StatusBadRequest {
		t.Fatalf("arbitrary path = %d, %s", unknown.Code, unknown.Body.String())
	}
}

func TestMaintenanceMutationsQueueTypedJobsAndExposeNoExecutionRoutes(t *testing.T) {
	service := &fakeMaintenanceService{}
	handler, store := newMaintenanceRouteHandler(t, service, &recordingDeploymentAuditSink{}, &fakeDeploymentStepUp{})

	created := fullDeploymentRequest(handler, http.MethodPost, "/api/v1/workspaces/main/snapshots", `{"label":"before upgrade","include_userdata":false}`, "snapshot-1")
	if created.Code != http.StatusAccepted {
		t.Fatalf("snapshot create = %d, %s", created.Code, created.Body.String())
	}
	password := fullDeploymentRequest(handler, http.MethodPost, "/api/v1/workspaces/main/local-admins/demo/primary/actions/rotate", `{"password":"chosen"}`, "rotate-invalid")
	if password.Code != http.StatusBadRequest {
		t.Fatalf("caller password accepted = %d, %s", password.Code, password.Body.String())
	}
	rotated := fullDeploymentRequest(handler, http.MethodPost, "/api/v1/workspaces/main/local-admins/demo/primary/actions/rotate", `{}`, "rotate-1")
	if rotated.Code != http.StatusAccepted {
		t.Fatalf("random rotate = %d, %s", rotated.Code, rotated.Body.String())
	}
	jobs, err := store.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 2 || jobs[0].Kind != deploymentaudit.ActionSnapshotCreate || jobs[1].Kind != deploymentaudit.ActionLocalAdminRotate {
		t.Fatalf("jobs = %#v", jobs)
	}
	encoded, _ := json.Marshal(jobs[1].Request)
	if strings.Contains(string(encoded), "password") || string(encoded) != `{"account":"primary","module":"demo"}` {
		t.Fatalf("rotation job request = %s", encoded)
	}
	for _, request := range []struct {
		method string
		path   string
		want   int
	}{
		{http.MethodDelete, "/api/v1/workspaces/main/snapshots/snap-1", http.StatusNotFound},
		{http.MethodPost, "/api/v1/workspaces/main/snapshots/snap-1/actions/restore", http.StatusNotFound},
		{http.MethodPost, "/api/v1/workspaces/main/backups", http.StatusMethodNotAllowed},
		{http.MethodPost, "/api/v1/workspaces/main/backups/backup-1/actions/restore", http.StatusNotFound},
	} {
		response := fullDeploymentRequest(handler, request.method, request.path, `{}`, "absent-route")
		if response.Code != request.want {
			t.Errorf("execution route %s %s = %d, %s", request.method, request.path, response.Code, response.Body.String())
		}
	}
}

func TestLocalAdminRevealConsumesStepUpBeforeNoStoreResponseAndAudit(t *testing.T) {
	targetID := application.LocalAdminTargetID("demo", "primary")
	stateDigest := strings.Repeat("d", 64)
	service := &fakeMaintenanceService{
		admins:      application.LocalAdminListResult{Accounts: []application.LocalAdminRecord{{TargetID: targetID, Module: "demo", Account: "primary", Username: "admin"}}},
		credential:  application.LocalAdminCredential{LocalAdminRecord: application.LocalAdminRecord{TargetID: targetID, Module: "demo", Account: "primary", Username: "admin"}, Password: "one-time-secret"},
		stateDigest: stateDigest,
	}
	stepUp := &fakeDeploymentStepUp{}
	credential, err := stepUp.IssueLocalStepUp(context.Background(), consoleauth.LocalStepUpRequest{
		SessionToken: "local-session-token", Action: deploymentaudit.ActionLocalAdminReveal,
		WorkspaceID: "main", TargetID: targetID, StateDigest: stateDigest,
	})
	if err != nil {
		t.Fatal(err)
	}
	auditSink := &recordingDeploymentAuditSink{}
	handler, _ := newMaintenanceRouteHandler(t, service, auditSink, stepUp)

	listed := fullDeploymentRequest(handler, http.MethodGet, "/api/v1/workspaces/main/local-admins", "", "")
	if listed.Code != http.StatusOK || strings.Contains(listed.Body.String(), "one-time-secret") || strings.Contains(listed.Body.String(), `"password"`) {
		t.Fatalf("local admin list = %d, %s", listed.Code, listed.Body.String())
	}
	revealed := fullDeploymentRequest(handler, http.MethodPost, "/api/v1/workspaces/main/local-admins/demo/primary/reveal", `{"step_up_proof":"`+credential.Token+`"}`, "")
	if revealed.Code != http.StatusOK || revealed.Header().Get("Cache-Control") != "no-store" || !strings.Contains(revealed.Body.String(), "one-time-secret") {
		t.Fatalf("reveal = %d cache=%q body=%s", revealed.Code, revealed.Header().Get("Cache-Control"), revealed.Body.String())
	}
	if service.revealCalls != 1 || service.stepUpDigestCall != 1 || len(auditSink.events) != 1 || auditSink.events[0].Stage != deploymentaudit.StageCredentialRevealAuthorized {
		t.Fatalf("reveal calls/audit = %d/%d %#v", service.revealCalls, service.stepUpDigestCall, auditSink.events)
	}
	reused := fullDeploymentRequest(handler, http.MethodPost, "/api/v1/workspaces/main/local-admins/demo/primary/reveal", `{"step_up_proof":"`+credential.Token+`"}`, "")
	if reused.Code != http.StatusConflict || service.revealCalls != 1 {
		t.Fatalf("reused reveal = %d, %s calls=%d", reused.Code, reused.Body.String(), service.revealCalls)
	}
}

func newMaintenanceRouteHandler(t *testing.T, service application.MaintenanceService, auditSink deploymentaudit.Sink, stepUp DeploymentStepUpAuthenticator) (http.Handler, *consolejobs.Store) {
	t.Helper()
	registry, _ := testRegistry(t, "main")
	store := openHTTPJobStore(t, consolejobs.Options{})
	handler, err := NewHandlerWithDeployment(registry, func(string) QueryService { return &fakeQueryService{} }, SecurityOptions{
		InitialState: StateFull, HostAllowed: func(*http.Request) bool { return true }, Listener: ListenerDirect,
		Authorize: func(*http.Request, AuthorizationRequest) (Principal, error) {
			return Principal{ID: consolejobs.PrincipalLocalOwner, Role: "owner", Source: "local"}, nil
		},
	}, DeploymentOptions{
		PlanFactory:        func(string) application.DeploymentPlanService { return &fakeDeploymentPlanService{} },
		MaintenanceFactory: func(string, application.EventSink) application.MaintenanceService { return service },
		Store:              store, Audit: auditSink, StepUp: stepUp, Notify: func(string) {},
	})
	if err != nil {
		t.Fatal(err)
	}
	return handler, store
}
