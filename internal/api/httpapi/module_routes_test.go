package httpapi

import (
	"context"
	"crypto/tls"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/anas-project/ANAS/internal/application"
	"github.com/anas-project/ANAS/internal/consolejobs"
	"github.com/anas-project/ANAS/internal/deploymentaudit"
)

type fakeModuleManagementService struct {
	listResult    application.ModuleListResult
	catalogResult application.ModuleCatalogResult
	listErr       error
	catalogErr    error
}

func (service *fakeModuleManagementService) ListModules(context.Context) (application.ModuleListResult, error) {
	return service.listResult, service.listErr
}

func (service *fakeModuleManagementService) CatalogModules(context.Context, application.ModuleCatalogRequest) (application.ModuleCatalogResult, error) {
	return service.catalogResult, service.catalogErr
}

func (*fakeModuleManagementService) SyncModules(context.Context, application.ModuleSyncRequest) (application.ModuleSyncResult, error) {
	return application.ModuleSyncResult{}, nil
}

func (*fakeModuleManagementService) UpdateModules(context.Context, application.ModuleUpdateRequest) (application.ModuleUpdateResult, error) {
	return application.ModuleUpdateResult{}, nil
}

func (*fakeModuleManagementService) SetModuleEnabled(context.Context, application.ModuleEnabledRequest, application.ConfigCommitObserver) (application.ModuleEnabledResult, error) {
	return application.ModuleEnabledResult{}, nil
}

func TestModuleReadRoutesExposePublicStateAndConfigETag(t *testing.T) {
	registry, paths := testRegistry(t, "main")
	store := openHTTPJobStore(t, consolejobs.Options{})
	validator := "cfgv-" + strings.Repeat("a", 64)
	service := &fakeModuleManagementService{
		listResult: application.ModuleListResult{
			Workspace: paths[0], ConfigValidator: validator, Modules: []application.ModuleState{{
				Name: "demo", ConfigurationState: "selected", Runtime: "running", Health: "healthy", Containers: 2,
				Dependencies: []string{}, EntryPoints: []application.ModuleManagementSurface{{ID: "admin", URI: "https://demo.example.test/", Authentication: "owner"}},
			}},
		},
		catalogResult: application.ModuleCatalogResult{
			Source: "official", CatalogReference: "oci://registry.example/catalog@sha256:" + strings.Repeat("b", 64),
			CatalogDigest: "sha256:" + strings.Repeat("b", 64), SourceCommit: "abc123",
			Modules: []application.ModuleCatalogEntry{{Module: "demo", Release: "1.2.3-4", Repository: "example/demo", Platforms: []string{"linux/amd64"}}},
		},
	}
	handler := newModuleRouteHandler(t, registry, store, service, nil)

	listed := fullDeploymentRequest(handler, http.MethodGet, "/api/v1/workspaces/main/modules", "", "")
	if listed.Code != http.StatusOK || listed.Header().Get("ETag") != `"`+validator+`"` {
		t.Fatalf("module list = %d etag=%q body=%s", listed.Code, listed.Header().Get("ETag"), listed.Body.String())
	}
	if strings.Contains(listed.Body.String(), paths[0]) || !strings.Contains(listed.Body.String(), `"workspace_id":"main"`) || !strings.Contains(listed.Body.String(), `"uri":"https://demo.example.test/"`) {
		t.Fatalf("module list public projection = %s", listed.Body.String())
	}

	catalog := fullDeploymentRequest(handler, http.MethodGet, "/api/v1/catalog/modules?workspace_id=main", "", "")
	if catalog.Code != http.StatusOK || !strings.Contains(catalog.Body.String(), `"module":"demo"`) || strings.Contains(catalog.Body.String(), paths[0]) {
		t.Fatalf("module catalog = %d, %s", catalog.Code, catalog.Body.String())
	}
}

func TestModuleMutationRoutesCreateIdempotentDurableJobs(t *testing.T) {
	registry, _ := testRegistry(t, "main")
	store := openHTTPJobStore(t, consolejobs.Options{})
	auditSink := &recordingDeploymentAuditSink{}
	notified := []string{}
	handler := newModuleRouteHandler(t, registry, store, &fakeModuleManagementService{}, func(workspace string) {
		notified = append(notified, workspace)
	})
	// Replace the helper-created sink so this test can inspect fail-closed job
	// creation bindings.
	stateHandler, err := NewHandlerWithDeployment(registry, func(string) QueryService { return &fakeQueryService{} }, SecurityOptions{
		InitialState: StateFull, HostAllowed: func(*http.Request) bool { return true }, Listener: ListenerDirect,
		Authorize: func(*http.Request, AuthorizationRequest) (Principal, error) {
			return Principal{ID: consolejobs.PrincipalLocalOwner, Role: "owner", Source: "local"}, nil
		},
	}, DeploymentOptions{
		PlanFactory: func(string) application.DeploymentPlanService { return &fakeDeploymentPlanService{} },
		ModuleFactory: func(string, application.EventSink) application.ModuleManagementService {
			return &fakeModuleManagementService{}
		},
		Store: store, Audit: auditSink, Notify: func(workspace string) { notified = append(notified, workspace) },
	})
	if err != nil {
		t.Fatal(err)
	}
	handler = stateHandler

	updated := fullDeploymentRequest(handler, http.MethodPost, "/api/v1/workspaces/main/actions/update-modules", `{"mode":"update","modules":["demo"]}`, "update-1")
	if updated.Code != http.StatusAccepted {
		t.Fatalf("module update = %d, %s", updated.Code, updated.Body.String())
	}

	enabled := fullModuleConfigRequest(handler, "/api/v1/workspaces/main/modules/demo/actions/enable", "enable-1", `"cfgv-`+strings.Repeat("c", 64)+`"`)
	if enabled.Code != http.StatusAccepted {
		t.Fatalf("module enable = %d, %s", enabled.Code, enabled.Body.String())
	}
	replayed := fullModuleConfigRequest(handler, "/api/v1/workspaces/main/modules/demo/actions/enable", "enable-1", `"cfgv-`+strings.Repeat("c", 64)+`"`)
	if replayed.Code != http.StatusAccepted || !strings.Contains(replayed.Body.String(), `"existing":true`) {
		t.Fatalf("module enable replay = %d, %s", replayed.Code, replayed.Body.String())
	}
	conflict := fullModuleConfigRequest(handler, "/api/v1/workspaces/main/modules/demo/actions/enable", "enable-1", `"cfgv-`+strings.Repeat("d", 64)+`"`)
	if conflict.Code != http.StatusConflict {
		t.Fatalf("module enable precondition idempotency conflict = %d, %s", conflict.Code, conflict.Body.String())
	}

	jobs, err := store.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 2 || jobs[0].Kind != deploymentaudit.ActionModuleUpdate || jobs[1].Kind != deploymentaudit.ActionModuleEnable {
		t.Fatalf("module jobs = %#v", jobs)
	}
	if jobs[1].Request["module"] != "demo" || jobs[1].Request["expected_config_validator"] != "cfgv-"+strings.Repeat("c", 64) || jobs[1].Request["operation_id"] == "" {
		t.Fatalf("module enable request = %#v", jobs[1].Request)
	}
	if len(notified) != 2 {
		t.Fatalf("notifications = %v", notified)
	}
	if len(auditSink.events) != 2 || auditSink.events[1].TargetID != "demo" || auditSink.events[1].OperationID == "" {
		t.Fatalf("module audit events = %#v", auditSink.events)
	}
}

func newModuleRouteHandler(t *testing.T, registry *Registry, store *consolejobs.Store, service application.ModuleManagementService, notify func(string)) http.Handler {
	t.Helper()
	if notify == nil {
		notify = func(string) {}
	}
	handler, err := NewHandlerWithDeployment(registry, func(string) QueryService { return &fakeQueryService{} }, SecurityOptions{
		InitialState: StateFull, HostAllowed: func(*http.Request) bool { return true }, Listener: ListenerDirect,
		Authorize: func(*http.Request, AuthorizationRequest) (Principal, error) {
			return Principal{ID: consolejobs.PrincipalLocalOwner, Role: "owner", Source: "local"}, nil
		},
	}, DeploymentOptions{
		PlanFactory:   func(string) application.DeploymentPlanService { return &fakeDeploymentPlanService{} },
		ModuleFactory: func(string, application.EventSink) application.ModuleManagementService { return service },
		Store:         store, Audit: &recordingDeploymentAuditSink{}, Notify: notify,
	})
	if err != nil {
		t.Fatal(err)
	}
	return handler
}

func fullModuleConfigRequest(handler http.Handler, path, idempotencyKey, validator string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodPost, "https://nas.example"+path, strings.NewReader(`{}`))
	request.TLS = &tls.ConnectionState{}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Origin", "https://nas.example")
	request.Header.Set(csrfHeaderName, "csrf-token")
	request.Header.Set("Idempotency-Key", idempotencyKey)
	request.Header.Set("If-Match", validator)
	request.AddCookie(&http.Cookie{Name: localSessionCookie, Value: "local-session-token"})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}
