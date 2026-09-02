package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/anas-project/ANAS/internal/application"
	"github.com/anas-project/ANAS/internal/configschema"
	"github.com/anas-project/ANAS/internal/deployment"
)

type fakeQueryService struct {
	version       application.VersionResult
	versionErr    error
	status        application.StatusResult
	statusErr     error
	list          application.ListDeploymentsResult
	listErr       error
	inspect       application.InspectDeploymentResult
	inspectErr    error
	commands      application.ListModuleCommandsResult
	commandsErr   error
	command       application.EffectiveModuleCommand
	commandErr    error
	listRequests  []application.ListDeploymentsRequest
	inspectIDs    []string
	statusCalls   int
	versionCalls  int
	inspectCalls  int
	deploymentIDs []string
	commandLists  []application.ListModuleCommandsRequest
	commandGets   []application.GetModuleCommandRequest
}

func (service *fakeQueryService) Version(context.Context) (application.VersionResult, error) {
	service.versionCalls++
	return service.version, service.versionErr
}

func (service *fakeQueryService) Status(context.Context) (application.StatusResult, error) {
	service.statusCalls++
	return service.status, service.statusErr
}

func (service *fakeQueryService) ListDeployments(_ context.Context, request application.ListDeploymentsRequest) (application.ListDeploymentsResult, error) {
	service.listRequests = append(service.listRequests, request)
	return service.list, service.listErr
}

func (service *fakeQueryService) InspectDeployment(_ context.Context, request application.InspectDeploymentRequest) (application.InspectDeploymentResult, error) {
	service.inspectCalls++
	service.inspectIDs = append(service.inspectIDs, request.DeploymentID)
	return service.inspect, service.inspectErr
}

func (service *fakeQueryService) ListModuleCommands(_ context.Context, request application.ListModuleCommandsRequest) (application.ListModuleCommandsResult, error) {
	service.commandLists = append(service.commandLists, request)
	return service.commands, service.commandsErr
}

func (service *fakeQueryService) GetModuleCommand(_ context.Context, request application.GetModuleCommandRequest) (application.EffectiveModuleCommand, error) {
	service.commandGets = append(service.commandGets, request)
	return service.command, service.commandErr
}

func TestHealthzDoesNotConstructAService(t *testing.T) {
	registry, _ := testRegistry(t, "main")
	factoryCalls := 0
	handler := NewHandler(registry, func(string) QueryService {
		factoryCalls++
		return &fakeQueryService{}
	})

	response := serveRequest(handler, http.MethodGet, "/healthz")
	if response.Code != http.StatusOK || response.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("healthz = %d, %s", response.Code, response.Body.String())
	}
	if factoryCalls != 0 {
		t.Fatalf("healthz constructed %d services", factoryCalls)
	}
	var body map[string]any
	decodeResponse(t, response, &body)
	if body["status"] != "ok" || len(body) != 1 {
		t.Fatalf("healthz body = %#v", body)
	}
}

func TestHandlerAcceptsOnlyNumericLoopbackHosts(t *testing.T) {
	registry, _ := testRegistry(t, "main")
	factoryCalls := 0
	handler := NewHandler(registry, func(string) QueryService {
		factoryCalls++
		return &fakeQueryService{}
	})
	for _, host := range []string{
		"127.0.0.1", "127.255.1.2:8080", "[::1]", "[::1]:8443", "::1", "[::1%lo0]", "[::1%lo0]:8080", "::1%lo0",
	} {
		request := httptest.NewRequest(http.MethodGet, "/healthz", nil)
		request.Host = host
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusOK {
			t.Errorf("Host %q = %d, %s", host, response.Code, response.Body.String())
		}
	}
	for _, host := range []string{
		"", "localhost", "localhost:8080", "example.com", "127.0.0.1.example.com", "0.0.0.0", "192.0.2.1:8080",
		"[::]", "[2001:db8::1]:8080", "127.0.0.1:http", "127.0.0.1:65536", "127.0.0.1%lo0", "[::1%bad/zone]:8080",
	} {
		request := httptest.NewRequest(http.MethodGet, "/healthz", nil)
		request.Host = host
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusBadRequest || response.Header().Get("Content-Type") != "application/problem+json" {
			t.Errorf("Host %q = %d, %s", host, response.Code, response.Body.String())
			continue
		}
		var document problem
		decodeResponse(t, response, &document)
		if document.Code != "invalid_host" || document.Status != http.StatusBadRequest || host != "" && strings.Contains(response.Body.String(), host) {
			t.Errorf("Host %q problem = %#v, raw %s", host, document, response.Body.String())
		}
	}
	if factoryCalls != 0 {
		t.Fatalf("Host validation constructed %d services", factoryCalls)
	}
}

func TestSystemReportsOnlyPublicBuildCapabilitiesCertificateAndWorkspaceIDs(t *testing.T) {
	registry, paths := testRegistry(t, "main", "lab")
	service := &fakeQueryService{version: application.VersionResult{Version: "1.2.3", Commit: "abc", Date: "2026-08-18"}}
	var factoryPaths []string
	handler := NewHandler(registry, func(path string) QueryService {
		factoryPaths = append(factoryPaths, path)
		return service
	})

	response := serveRequest(handler, http.MethodGet, "/api/v1/system")
	if response.Code != http.StatusOK {
		t.Fatalf("system = %d, %s", response.Code, response.Body.String())
	}
	body := response.Body.String()
	for _, path := range paths {
		if strings.Contains(body, path) {
			t.Fatalf("system response leaked workspace path %q: %s", path, body)
		}
	}
	var document struct {
		APIVersion string `json:"api_version"`
		Build      struct {
			Version string `json:"version"`
		} `json:"build"`
		Capabilities struct {
			ReadOnly bool `json:"read_only"`
		} `json:"capabilities"`
		WorkspaceIDs      []string          `json:"workspace_ids"`
		CertificateIssuer CertificateIssuer `json:"certificate_issuer"`
		ConsoleState      ConsoleState      `json:"console_state"`
	}
	decodeResponse(t, response, &document)
	if document.APIVersion != APIVersion || document.Build.Version != "1.2.3" || !document.Capabilities.ReadOnly || strings.Join(document.WorkspaceIDs, ",") != "main,lab" || document.CertificateIssuer != CertificateIssuerNone || document.ConsoleState != StateM0 {
		t.Fatalf("system body = %#v", document)
	}
	if len(factoryPaths) != 1 || factoryPaths[0] != "" {
		t.Fatalf("factory paths = %#v", factoryPaths)
	}
}

func TestStatusResolvesRegisteredIDWithoutReturningPath(t *testing.T) {
	registry, paths := testRegistry(t, "main")
	active, activated := "dep-2", "2026-08-18T01:00:00Z"
	runtime, probeError, healthy := "degraded", "runtime_probe_failed", false
	service := &fakeQueryService{status: application.StatusResult{
		Workspace: paths[0], ActiveDeployment: &active, ActivatedAt: &activated,
		RuntimeStatus: &runtime, RuntimeHealthy: &healthy, RuntimeProbeError: &probeError,
		ModuleRuntime:       []application.ModuleRuntimeStatus{{Module: "demo", Runtime: "running", Health: "unhealthy", Containers: 1}},
		PreviousDeployments: []string{"dep-1"},
	}}
	var gotPath string
	handler := NewHandler(registry, func(path string) QueryService { gotPath = path; return service })

	response := serveRequest(handler, http.MethodGet, "/api/v1/workspaces/main/status")
	if response.Code != http.StatusOK || gotPath != paths[0] {
		t.Fatalf("status = %d, path %q, body %s", response.Code, gotPath, response.Body.String())
	}
	if strings.Contains(response.Body.String(), paths[0]) {
		t.Fatalf("status response leaked a path: %s", response.Body.String())
	}
	var document statusResponse
	decodeResponse(t, response, &document)
	if document.WorkspaceID != "main" || document.ActiveDeployment == nil || *document.ActiveDeployment != active || document.VerifiedAt != nil || len(document.PreviousDeployments) != 1 ||
		document.RuntimeStatus == nil || *document.RuntimeStatus != "degraded" || document.RuntimeHealthy == nil || *document.RuntimeHealthy ||
		document.RuntimeProbeError == nil || *document.RuntimeProbeError != "runtime_probe_failed" || len(document.ModuleRuntime) != 1 {
		t.Fatalf("status response = %#v", document)
	}
}

func TestModuleCommandRoutesUseIndependentSafeHTTPDTOs(t *testing.T) {
	registry, paths := testRegistry(t, "main")
	active := "dep-commands"
	command := application.EffectiveModuleCommand{
		Module: "demo", Release: "1.2.3-r4", DeploymentID: active, Available: true,
		Command: application.ModuleCommandDescriptor{
			ID: "doctor", Title: "Inspect service", Description: "Read safe diagnostics.",
			Mode: "query", Risk: "normal", RuntimeState: "running", Lock: "module_read",
			TimeoutSeconds: 20, Cancellable: "true", Digest: "sha256:public-descriptor",
			Parameters: []application.ModuleCommandParameter{{
				Name: "verbose", Title: "Verbose", Description: "Include safe details.",
				Type: configschema.Parameter{Kind: "bool"}, Default: false,
			}},
		},
	}
	service := &fakeQueryService{
		commands: application.ListModuleCommandsResult{ActiveDeployment: &active, Commands: []application.EffectiveModuleCommand{command}},
		command:  command,
	}
	handler := NewHandler(registry, func(string) QueryService { return service })

	list := serveRequest(handler, http.MethodGet, "/api/v1/workspaces/main/modules/demo/commands")
	if list.Code != http.StatusOK {
		t.Fatalf("list = %d, %s", list.Code, list.Body.String())
	}
	var listDocument moduleCommandListResponse
	decodeResponse(t, list, &listDocument)
	if listDocument.WorkspaceID != "main" || listDocument.ActiveDeployment == nil || len(listDocument.Items) != 1 || listDocument.Items[0].ID != "doctor" {
		t.Fatalf("list document = %#v", listDocument)
	}

	detail := serveRequest(handler, http.MethodGet, "/api/v1/workspaces/main/modules/demo/commands/doctor")
	if detail.Code != http.StatusOK {
		t.Fatalf("detail = %d, %s", detail.Code, detail.Body.String())
	}
	var detailDocument moduleCommandDetailResponse
	decodeResponse(t, detail, &detailDocument)
	if detailDocument.Command.Module != "demo" || detailDocument.Command.Parameters[0].Type != "bool" {
		t.Fatalf("detail document = %#v", detailDocument)
	}
	for _, forbidden := range []string{paths[0], "handler", "executor", "env", "secret", ".command.bin"} {
		if strings.Contains(strings.ToLower(list.Body.String()+detail.Body.String()), strings.ToLower(forbidden)) {
			t.Fatalf("Module Command HTTP DTO exposed %q: list=%s detail=%s", forbidden, list.Body.String(), detail.Body.String())
		}
	}
	if len(service.commandLists) != 1 || service.commandLists[0].Module != "demo" || len(service.commandGets) != 1 || service.commandGets[0].Command != "doctor" {
		t.Fatalf("application requests = %#v / %#v", service.commandLists, service.commandGets)
	}
}

func TestHandlerReadsARegisteredWorkspaceThroughApplicationService(t *testing.T) {
	workspace := t.TempDir()
	statePath := filepath.Join(workspace, ".anas", "state", "active.yml")
	if err := os.MkdirAll(filepath.Dir(statePath), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(statePath, []byte("api_version: anas.state/v2\nactive_deployment: dep-real\nprevious_deployments: []\nactivated_at: 2026-08-18T01:00:00Z\n"), 0600); err != nil {
		t.Fatal(err)
	}
	registry, err := NewRegistry([]Workspace{{ID: "real", Path: workspace}})
	if err != nil {
		t.Fatal(err)
	}
	handler := NewHandler(registry, func(path string) QueryService { return application.NewService(path) })

	response := serveRequest(handler, http.MethodGet, "/api/v1/workspaces/real/status")
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, %s", response.Code, response.Body.String())
	}
	var document statusResponse
	decodeResponse(t, response, &document)
	if document.ActiveDeployment == nil || *document.ActiveDeployment != "dep-real" || document.WorkspaceID != "real" {
		t.Fatalf("status = %#v", document)
	}
}

func TestHandlerListsAndInspectsARegisteredWorkspaceThroughApplicationService(t *testing.T) {
	workspace := t.TempDir()
	if err := os.Mkdir(filepath.Join(workspace, ".anas"), 0700); err != nil {
		t.Fatal(err)
	}
	registry, err := NewRegistry([]Workspace{{ID: "real", Path: workspace}})
	if err != nil {
		t.Fatal(err)
	}
	registeredPath, ok := registry.Resolve("real")
	if !ok {
		t.Fatal("registered workspace was not resolved")
	}

	manifest := deployment.Manifest{
		APIVersion:        deployment.ManifestAPIVersion,
		ID:                "dep-real",
		CreatedAt:         "2026-08-18T01:00:00Z",
		ConfigFingerprint: "sha256:config-secret",
		ModuleOrder:       []string{"app"},
		Modules: map[string]deployment.Module{
			"app": {
				Name:               "app",
				Version:            "1.0.0",
				Revision:           1,
				Lifecycle:          "managed",
				ArtifactDeployment: "dep-real",
				RenderDigest:       "sha256:render-secret",
				RuntimeType:        "compose",
				ComposeFile:        filepath.Join(registeredPath, "modules", "app", "compose.yml"),
				Hook: deployment.HookConfig{
					Command: []string{filepath.Join(registeredPath, "modules", "app", "hook.sh")},
				},
			},
		},
		Settings: map[string]deployment.Setting{
			"app.token": {
				Fingerprint: "sha256:setting-secret",
				Module:      "app",
				Parameter:   "token",
				Effect:      "restart",
			},
		},
		Resources: []deployment.Resource{
			{
				Consumer:        "app",
				ID:              "database",
				Contract:        "database",
				ContractVersion: "1",
				Provider:        "postgres",
				Interface:       "tcp",
				Spec: map[string]any{
					"socket": filepath.Join(registeredPath, "private", "database.sock"),
					"dsn":    "password=manifest-secret",
				},
				SecretKey: "secret-store-key", CredentialSecretKey: "object-secret-store-key",
			},
		},
		Snapshot: deployment.SnapshotPolicy{
			Source: filepath.Join(registeredPath, "private", "data"),
			Root:   filepath.Join(registeredPath, "private", "snapshots"),
		},
	}
	state := deployment.State{
		APIVersion: deployment.StateAPIVersion,
		ID:         "dep-real",
		Status:     "failed",
		CreatedAt:  "2026-08-18T01:00:00Z",
		Failure:    "failure-secret at " + filepath.Join(registeredPath, "private", "state.log"),
	}
	writeHandlerFixture(t, filepath.Join(registeredPath, ".anas", "deployments", "dep-real", "deployment.yml"), manifest)
	writeHandlerFixture(t, filepath.Join(registeredPath, ".anas", "state", "deployments", "dep-real.yml"), state)

	var factoryPaths []string
	handler := NewHandler(registry, func(path string) QueryService {
		factoryPaths = append(factoryPaths, path)
		return application.NewService(path)
	})

	listResponse := serveRequest(handler, http.MethodGet, "/api/v1/workspaces/real/deployments?limit=1")
	if listResponse.Code != http.StatusOK {
		t.Fatalf("list = %d, %s", listResponse.Code, listResponse.Body.String())
	}
	var listDocument deploymentListResponse
	decodeResponse(t, listResponse, &listDocument)
	if listDocument.WorkspaceID != "real" || len(listDocument.Items) != 1 || listDocument.Items[0].ID != "dep-real" {
		t.Fatalf("list response = %#v", listDocument)
	}
	if listDocument.Items[0].Failure == nil || *listDocument.Items[0].Failure != "deployment failed; inspect host logs for details" {
		t.Fatalf("list failure was not redacted: %#v", listDocument.Items[0].Failure)
	}

	detailResponse := serveRequest(handler, http.MethodGet, "/api/v1/workspaces/real/deployments/dep-real")
	if detailResponse.Code != http.StatusOK {
		t.Fatalf("detail = %d, %s", detailResponse.Code, detailResponse.Body.String())
	}
	detailBody := detailResponse.Body.String()
	for _, forbidden := range []string{
		registeredPath,
		"config_fingerprint", "sha256:config-secret",
		"render_digest", "sha256:render-secret",
		`"fingerprint"`, "sha256:setting-secret",
		`"password_secret"`, `"credential_secret"`, "secret-store-key", "object-secret-store-key",
		`"spec"`, "password=manifest-secret",
		"failure-secret",
	} {
		if strings.Contains(detailBody, forbidden) {
			t.Errorf("detail contains %q: %s", forbidden, detailBody)
		}
	}
	var detailDocument deploymentDetailResponse
	decodeResponse(t, detailResponse, &detailDocument)
	if detailDocument.WorkspaceID != "real" || detailDocument.Deployment.ID != "dep-real" || detailDocument.Deployment.Modules["app"].Version != "1.0.0" {
		t.Fatalf("detail response = %#v", detailDocument)
	}
	if detailDocument.State.Failure == nil || *detailDocument.State.Failure != "deployment failed; inspect host logs for details" {
		t.Fatalf("detail failure was not redacted: %#v", detailDocument.State.Failure)
	}
	if len(factoryPaths) != 2 || factoryPaths[0] != registeredPath || factoryPaths[1] != registeredPath {
		t.Fatalf("factory paths = %#v, want two calls with %q", factoryPaths, registeredPath)
	}
}

func TestRoutesRejectUnsupportedOrMalformedQueries(t *testing.T) {
	registry, _ := testRegistry(t, "main")
	service := &fakeQueryService{version: application.VersionResult{Version: "dev"}}
	handler := NewHandler(registry, func(string) QueryService { return service })
	paths := []string{
		"/healthz?verbose=true",
		"/api/v1/system?workspace=/tmp/outside",
		"/api/v1/workspaces/main/status?workspace=/tmp/outside",
		"/api/v1/workspaces/main/deployments?workspace=/tmp/outside",
		"/api/v1/workspaces/main/deployments/dep-1?path=/tmp/outside",
		"/api/v1/workspaces/main/modules/demo/commands?workspace=/tmp/outside",
		"/api/v1/workspaces/main/modules/demo/commands/doctor?path=/tmp/outside",
		"/api/v1/workspaces/main/deployments?limit=1;a=2",
	}
	for _, path := range paths {
		response := serveRequest(handler, http.MethodGet, path)
		if response.Code != http.StatusBadRequest || response.Header().Get("Content-Type") != "application/problem+json" || strings.Contains(response.Body.String(), "/tmp/outside") {
			t.Errorf("%s = %d, %s", path, response.Code, response.Body.String())
		}
	}
	if service.versionCalls != 0 || service.statusCalls != 0 || len(service.listRequests) != 0 || service.inspectCalls != 0 || len(service.commandLists) != 0 || len(service.commandGets) != 0 {
		t.Fatalf("unsupported query reached application: %#v", service)
	}
}

func TestDeploymentListPaginationAndNullStateFields(t *testing.T) {
	registry, _ := testRegistry(t, "main")
	next := "next-page"
	service := &fakeQueryService{list: application.ListDeploymentsResult{
		Deployments: []deployment.State{{ID: "dep-1", Status: "active", CreatedAt: "2026-08-18T01:00:00Z"}},
		NextCursor:  &next,
	}}
	handler := NewHandler(registry, func(string) QueryService { return service })

	response := serveRequest(handler, http.MethodGet, "/api/v1/workspaces/main/deployments?limit=7&cursor=current")
	if response.Code != http.StatusOK {
		t.Fatalf("list = %d, %s", response.Code, response.Body.String())
	}
	if len(service.listRequests) != 1 || service.listRequests[0].Limit != 7 || service.listRequests[0].Cursor != "current" {
		t.Fatalf("list request = %#v", service.listRequests)
	}
	var document deploymentListResponse
	decodeResponse(t, response, &document)
	if document.WorkspaceID != "main" || len(document.Items) != 1 || document.Items[0].ActivatedAt != nil || document.NextCursor == nil || *document.NextCursor != next {
		t.Fatalf("list response = %#v", document)
	}

	defaultResponse := serveRequest(handler, http.MethodGet, "/api/v1/workspaces/main/deployments")
	if defaultResponse.Code != http.StatusOK || len(service.listRequests) != 2 || service.listRequests[1].Limit != defaultPageLimit {
		t.Fatalf("default pagination = %#v", service.listRequests)
	}
}

func TestDeploymentListRejectsInvalidPaginationBeforeApplicationCall(t *testing.T) {
	registry, _ := testRegistry(t, "main")
	service := &fakeQueryService{}
	handler := NewHandler(registry, func(string) QueryService { return service })
	tooLong := strings.Repeat("a", maximumCursorLen+1)
	paths := []string{
		"/api/v1/workspaces/main/deployments?limit=0",
		"/api/v1/workspaces/main/deployments?limit=101",
		"/api/v1/workspaces/main/deployments?limit=abc",
		"/api/v1/workspaces/main/deployments?limit=1&limit=2",
		"/api/v1/workspaces/main/deployments?cursor=",
		"/api/v1/workspaces/main/deployments?cursor=a&cursor=b",
		"/api/v1/workspaces/main/deployments?cursor=" + tooLong,
	}
	for _, path := range paths {
		response := serveRequest(handler, http.MethodGet, path)
		if response.Code != http.StatusBadRequest || response.Header().Get("Content-Type") != "application/problem+json" {
			t.Errorf("%s = %d, %s", path, response.Code, response.Body.String())
		}
	}
	if len(service.listRequests) != 0 {
		t.Fatalf("invalid pagination reached service: %#v", service.listRequests)
	}
}

func TestDeploymentDetailUsesSafeProjection(t *testing.T) {
	registry, paths := testRegistry(t, "main")
	dataBreaking := []string{}
	service := &fakeQueryService{inspect: application.InspectDeploymentResult{
		Workspace: paths[0], DeploymentPath: filepath.Join(paths[0], ".anas", "deployments", "dep-1"),
		Deployment: &deployment.Manifest{
			APIVersion: deployment.ManifestAPIVersion, ID: "dep-1", CreatedAt: "2026-08-18T01:00:00Z",
			ConfigFingerprint: "sha256:abc", ModuleOrder: []string{"app"},
			Modules: map[string]deployment.Module{"app": {
				Name: "app", Version: "1.0.0", Revision: 2, ArtifactDeployment: "dep-1", RuntimeType: "compose",
				RenderDigest: "sha256:render-secret",
				ComposeFile:  filepath.Join(paths[0], "docker-compose.yml"), Hook: deployment.HookConfig{Command: []string{filepath.Join(paths[0], "hook.sh")}},
				DataBreaking: &dataBreaking,
			}},
			Settings:  map[string]deployment.Setting{"app.token": {Fingerprint: "sha256:value", Module: "app", Parameter: "token", Effect: "restart"}},
			Resources: []deployment.Resource{{Consumer: "app", ID: "db", Contract: "database", Provider: "postgres", Spec: map[string]any{"socket": filepath.Join(paths[0], "db.sock")}, SecretKey: "super-secret-key", CredentialSecretKey: "object-secret-key"}},
			Snapshot:  deployment.SnapshotPolicy{Backend: "btrfs", Source: filepath.Join(paths[0], "data"), Root: "/snapshots/private", KeepAuto: 3},
		},
		State: deployment.State{ID: "dep-1", Status: "failed", CreatedAt: "2026-08-18T01:00:00Z", Failure: "password=secret at " + filepath.Join(paths[0], "state")},
	}}
	handler := NewHandler(registry, func(string) QueryService { return service })

	response := serveRequest(handler, http.MethodGet, "/api/v1/workspaces/main/deployments/dep-1")
	if response.Code != http.StatusOK {
		t.Fatalf("detail = %d, %s", response.Code, response.Body.String())
	}
	body := response.Body.String()
	for _, forbidden := range []string{
		paths[0], "/snapshots/private", "super-secret-key", "object-secret-key", "password=secret", "compose_file", "\"hook\"", "\"spec\"", "password_secret", "credential_secret", "deployment_path",
		"config_fingerprint", "render_digest", "\"fingerprint\":", "sha256:abc", "sha256:render-secret", "sha256:value",
	} {
		if strings.Contains(body, forbidden) {
			t.Errorf("detail contains %q: %s", forbidden, body)
		}
	}
	var document deploymentDetailResponse
	decodeResponse(t, response, &document)
	module := document.Deployment.Modules["app"]
	if document.WorkspaceID != "main" || module.DataBreaking == nil || *module.DataBreaking == nil || document.Deployment.Snapshot.KeepAuto != 3 {
		t.Fatalf("detail response = %#v", document)
	}
	if document.State.Failure == nil || *document.State.Failure != "deployment failed; inspect host logs for details" {
		t.Fatalf("failure was not redacted: %#v", document.State.Failure)
	}
}

func TestApplicationErrorsBecomeProblemsWithoutLeakingCauses(t *testing.T) {
	registry, _ := testRegistry(t, "main")
	tests := []struct {
		name   string
		err    error
		status int
		code   string
	}{
		{name: "invalid", err: &application.Error{Kind: application.ErrorKindInvalidArgument, Code: "invalid_deployment_id", Message: "bad /private/path"}, status: 400, code: "invalid_deployment_id"},
		{name: "missing", err: &application.Error{Kind: application.ErrorKindNotFound, Code: "deployment_missing", Message: "open /private/path"}, status: 404, code: "deployment_missing"},
		{name: "precondition", err: &application.Error{Kind: application.ErrorKindFailedPrecondition, Code: "deployment_missing", Message: "decode /private/path"}, status: 412, code: "deployment_missing"},
		{name: "internal", err: &application.Error{Kind: application.ErrorKindInternal, Code: "state_unreadable", Message: "read /private/path"}, status: 500, code: "state_unreadable"},
		{name: "unknown", err: errors.New("secret /private/path"), status: 500, code: "internal_error"},
		{name: "deadline", err: context.DeadlineExceeded, status: 504, code: "deadline_exceeded"},
		{name: "canceled", err: context.Canceled, status: 408, code: "request_canceled"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			service := &fakeQueryService{statusErr: test.err}
			handler := NewHandler(registry, func(string) QueryService { return service })
			response := serveRequest(handler, http.MethodGet, "/api/v1/workspaces/main/status")
			if response.Code != test.status || response.Header().Get("Content-Type") != "application/problem+json" {
				t.Fatalf("response = %d, %s", response.Code, response.Body.String())
			}
			var document problem
			decodeResponse(t, response, &document)
			if document.APIVersion != APIVersion || document.Code != test.code || document.Status != test.status || strings.Contains(response.Body.String(), "/private/path") || strings.Contains(response.Body.String(), "secret") {
				t.Fatalf("problem = %#v, raw %s", document, response.Body.String())
			}
		})
	}
}

func TestRoutingRejectsUnknownWorkspacesTraversalAndMethods(t *testing.T) {
	registry, _ := testRegistry(t, "main")
	service := &fakeQueryService{}
	handler := NewHandler(registry, func(string) QueryService { return service })
	tests := []struct {
		method string
		path   string
		status int
		allow  string
	}{
		{method: http.MethodGet, path: "/api/v1/workspaces/missing/status", status: 404},
		{method: http.MethodGet, path: "/api/v1/workspaces/%2e%2e/status", status: 404},
		{method: http.MethodGet, path: "/api/v1/workspaces/main/deployments/../outside", status: 404},
		{method: http.MethodGet, path: "/api/v1/workspaces/main/unknown", status: 404},
		{method: http.MethodPost, path: "/api/v1/workspaces/main/status", status: 405, allow: "GET"},
		{method: http.MethodPost, path: "/api/v1/workspaces/main/modules/demo/commands/doctor", status: 405, allow: "GET"},
		{method: http.MethodHead, path: "/healthz", status: 405, allow: "GET"},
	}
	for _, test := range tests {
		response := serveRequest(handler, test.method, test.path)
		if response.Code != test.status || response.Header().Get("Content-Type") != "application/problem+json" || response.Header().Get("Allow") != test.allow {
			t.Errorf("%s %s = %d, allow %q, body %s", test.method, test.path, response.Code, response.Header().Get("Allow"), response.Body.String())
		}
	}
	if service.inspectCalls != 0 || service.statusCalls != 0 || len(service.commandLists) != 0 || len(service.commandGets) != 0 {
		t.Fatalf("rejected routes reached application: %#v", service)
	}
}

func TestDeploymentIDLengthIsEnforcedBeforeApplicationCall(t *testing.T) {
	registry, _ := testRegistry(t, "main")
	service := &fakeQueryService{}
	handler := NewHandler(registry, func(string) QueryService { return service })
	response := serveRequest(handler, http.MethodGet, "/api/v1/workspaces/main/deployments/"+strings.Repeat("x", 256))
	if response.Code != http.StatusBadRequest || service.inspectCalls != 0 {
		t.Fatalf("response = %d, inspect calls = %d, body %s", response.Code, service.inspectCalls, response.Body.String())
	}
}

func TestNilRegistryAndFactoryFailClosed(t *testing.T) {
	handler := NewHandler(nil, nil)
	system := serveRequest(handler, http.MethodGet, "/api/v1/system")
	if system.Code != http.StatusInternalServerError {
		t.Fatalf("system = %d, %s", system.Code, system.Body.String())
	}
	workspace := serveRequest(handler, http.MethodGet, "/api/v1/workspaces/main/status")
	if workspace.Code != http.StatusNotFound {
		t.Fatalf("workspace = %d, %s", workspace.Code, workspace.Body.String())
	}
}

func testRegistry(t *testing.T, ids ...string) (*Registry, []string) {
	t.Helper()
	workspaces := make([]Workspace, 0, len(ids))
	paths := make([]string, 0, len(ids))
	for _, id := range ids {
		path := t.TempDir()
		if err := os.Mkdir(filepath.Join(path, ".anas"), 0700); err != nil {
			t.Fatal(err)
		}
		canonical, err := filepath.EvalSymlinks(path)
		if err != nil {
			t.Fatal(err)
		}
		workspaces = append(workspaces, Workspace{ID: id, Path: path})
		paths = append(paths, canonical)
	}
	registry, err := NewRegistry(workspaces)
	if err != nil {
		t.Fatal(err)
	}
	return registry, paths
}

func serveRequest(handler http.Handler, method, target string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, target, nil)
	request.Host = "127.0.0.1"
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func decodeResponse(t *testing.T, response *httptest.ResponseRecorder, destination any) {
	t.Helper()
	if err := json.Unmarshal(response.Body.Bytes(), destination); err != nil {
		t.Fatalf("decode response: %v\n%s", err, response.Body.String())
	}
}

func writeHandlerFixture(t *testing.T, path string, value any) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, body, 0600); err != nil {
		t.Fatal(err)
	}
}
