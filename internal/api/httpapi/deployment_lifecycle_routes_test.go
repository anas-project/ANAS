package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/anas-project/ANAS/internal/application"
	"github.com/anas-project/ANAS/internal/consolejobs"
	"github.com/anas-project/ANAS/internal/deploymentaudit"
)

type fakeDeploymentService struct {
	lifecyclePreview application.LifecyclePreviewResult
	rollbackPreview  application.RollbackPreviewResult
}

func (*fakeDeploymentService) Plan(context.Context, application.PlanRequest) (application.PlanResult, error) {
	return application.PlanResult{}, nil
}
func (*fakeDeploymentService) Apply(context.Context, application.ApplyRequest) (application.ApplyResult, error) {
	return application.ApplyResult{}, nil
}
func (service *fakeDeploymentService) PreviewLifecycle(context.Context, application.LifecyclePreviewRequest) (application.LifecyclePreviewResult, error) {
	return service.lifecyclePreview, nil
}
func (*fakeDeploymentService) ExecuteLifecycle(context.Context, application.LifecycleRequest) (application.LifecycleResult, error) {
	return application.LifecycleResult{}, nil
}
func (service *fakeDeploymentService) PreviewRollback(context.Context, application.RollbackPreviewRequest) (application.RollbackPreviewResult, error) {
	return service.rollbackPreview, nil
}
func (*fakeDeploymentService) Rollback(context.Context, application.RollbackRequest) (application.RollbackResult, error) {
	return application.RollbackResult{}, nil
}
func (*fakeDeploymentService) CheckCompensation(context.Context) error { return nil }

func TestLifecyclePreviewMustConfirmRunnerExpandedChainBeforeJobCreation(t *testing.T) {
	registry, workspacePaths := testRegistry(t, "main")
	store := openHTTPJobStore(t, consolejobs.Options{})
	service := &fakeDeploymentService{lifecyclePreview: application.LifecyclePreviewResult{
		Workspace: workspacePaths[0], DeploymentID: "dep-active", Action: application.LifecycleRestart,
		RequestedModules: []string{"app"}, AffectedModules: []string{"postgres", "app"}, Digest: deploymentTestDigest,
	}}
	notified := 0
	handler := newLifecycleRouteHandler(t, registry, store, service, func(string) { notified++ })

	previewResponse := fullDeploymentRequest(handler, http.MethodPost,
		"/api/v1/workspaces/main/modules/actions/restart", `{"modules":["app"]}`, "")
	if previewResponse.Code != http.StatusOK {
		t.Fatalf("preview = %d, %s", previewResponse.Code, previewResponse.Body.String())
	}
	var preview lifecyclePreviewResponse
	decodeResponse(t, previewResponse, &preview)
	if len(preview.Preview.AffectedModules) != 2 || preview.Preview.AffectedModules[0] != "postgres" || preview.Preview.AffectedModules[1] != "app" {
		t.Fatalf("preview chain = %#v", preview.Preview.AffectedModules)
	}

	wrong := fullDeploymentRequest(handler, http.MethodPost,
		"/api/v1/workspaces/main/modules/actions/restart",
		`{"modules":["app"],"expected_deployment_id":"dep-active","expected_digest":"`+deploymentTestDigest+`","confirmed_modules":["app"]}`,
		"lifecycle-restart")
	if wrong.Code != http.StatusConflict {
		t.Fatalf("wrong confirmation = %d, %s", wrong.Code, wrong.Body.String())
	}

	confirmed := fullDeploymentRequest(handler, http.MethodPost,
		"/api/v1/workspaces/main/modules/actions/restart",
		`{"modules":["app"],"expected_deployment_id":"dep-active","expected_digest":"`+deploymentTestDigest+`","confirmed_modules":["postgres","app"]}`,
		"lifecycle-restart")
	if confirmed.Code != http.StatusAccepted {
		t.Fatalf("confirmed = %d, %s", confirmed.Code, confirmed.Body.String())
	}
	jobs, err := store.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 1 || jobs[0].Kind != deploymentaudit.ActionRestart || notified != 1 {
		t.Fatalf("jobs = %#v, notified = %d", jobs, notified)
	}
	encoded, _ := json.Marshal(jobs[0].Request)
	if string(encoded) != `{"action":"restart","expected_deployment_id":"dep-active","expected_digest":"`+deploymentTestDigest+`","expected_modules":["postgres","app"],"modules":["app"]}` {
		t.Fatalf("stored lifecycle request = %s", encoded)
	}
	service.lifecyclePreview.DeploymentID = "dep-after-job"
	retry := fullDeploymentRequest(handler, http.MethodPost,
		"/api/v1/workspaces/main/modules/actions/restart",
		`{"modules":["app"],"expected_deployment_id":"dep-active","expected_digest":"`+deploymentTestDigest+`","confirmed_modules":["postgres","app"]}`,
		"lifecycle-restart")
	if retry.Code != http.StatusAccepted || !strings.Contains(retry.Body.String(), `"existing":true`) || notified != 1 {
		t.Fatalf("idempotent retry after runtime drift = %d, %s, notified=%d", retry.Code, retry.Body.String(), notified)
	}
}

func TestRollbackRequiresExplicitTargetAndExactImpactConfirmation(t *testing.T) {
	registry, workspacePaths := testRegistry(t, "main")
	store := openHTTPJobStore(t, consolejobs.Options{})
	service := &fakeDeploymentService{rollbackPreview: application.RollbackPreviewResult{
		Workspace: workspacePaths[0], ActiveDeployment: "dep-current", TargetDeployment: "dep-previous",
		GuardedChanges: []string{"global.base_domain (immutable; migrate-service-domain)"}, DataTouched: false, Digest: deploymentTestDigest,
	}}
	handler := newLifecycleRouteHandler(t, registry, store, service, func(string) {})

	preview := fullDeploymentRequest(handler, http.MethodPost, "/api/v1/workspaces/main/actions/rollback", `{"deployment_id":"dep-previous"}`, "")
	if preview.Code != http.StatusOK || !json.Valid(preview.Body.Bytes()) {
		t.Fatalf("rollback preview = %d, %s", preview.Code, preview.Body.String())
	}
	confirmed := fullDeploymentRequest(handler, http.MethodPost, "/api/v1/workspaces/main/actions/rollback",
		`{"deployment_id":"dep-previous","expected_active_deployment":"dep-current","expected_digest":"`+deploymentTestDigest+`","allow_risky":true,"confirmed_deployment_id":"dep-previous","confirmed_guarded_changes":["global.base_domain (immutable; migrate-service-domain)"]}`,
		"rollback")
	if confirmed.Code != http.StatusAccepted {
		t.Fatalf("rollback confirmed = %d, %s", confirmed.Code, confirmed.Body.String())
	}
	jobs, err := store.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 1 || jobs[0].Kind != deploymentaudit.ActionRollback || jobs[0].Request["deployment_id"] != "dep-previous" {
		t.Fatalf("rollback jobs = %#v", jobs)
	}
}

func newLifecycleRouteHandler(t *testing.T, registry *Registry, store *consolejobs.Store, service application.DeploymentService, notify func(string)) http.Handler {
	t.Helper()
	handler, err := NewHandlerWithDeployment(registry, func(string) QueryService { return &fakeQueryService{} }, SecurityOptions{
		InitialState: StateFull, HostAllowed: func(*http.Request) bool { return true }, Listener: ListenerDirect,
		Authorize: func(*http.Request, AuthorizationRequest) (Principal, error) {
			return Principal{ID: consolejobs.PrincipalLocalOwner, Role: "owner", Source: "local"}, nil
		},
	}, DeploymentOptions{
		PlanFactory:    func(string) application.DeploymentPlanService { return service },
		ServiceFactory: func(string) application.DeploymentService { return service },
		Store:          store, Audit: &recordingDeploymentAuditSink{}, Notify: notify,
	})
	if err != nil {
		t.Fatal(err)
	}
	return handler
}
