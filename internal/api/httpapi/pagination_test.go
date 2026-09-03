package httpapi

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/anas-project/ANAS/internal/application"
	"github.com/anas-project/ANAS/internal/consolejobs"
)

func TestWorkspaceListUsesOpaqueScopedCursor(t *testing.T) {
	registry, _ := testRegistry(t, "first", "second", "third")
	handler := NewHandler(registry, func(string) QueryService { return &fakeQueryService{} })

	first := serveRequest(handler, http.MethodGet, "/api/v1/workspaces?limit=2")
	if first.Code != http.StatusOK {
		t.Fatalf("first page = %d, %s", first.Code, first.Body.String())
	}
	var page workspaceListResponse
	decodeResponse(t, first, &page)
	if len(page.Items) != 2 || page.Items[0].ID != "first" || page.Items[1].ID != "second" || page.NextCursor == nil {
		t.Fatalf("first page = %#v", page)
	}
	if encoded, _ := json.Marshal(page); string(encoded) == "" || strings.Contains(string(encoded), "first=/") || strings.Contains(string(encoded), "/.anas") {
		t.Fatalf("workspace page leaked a path: %s", encoded)
	}

	second := serveRequest(handler, http.MethodGet, "/api/v1/workspaces?limit=2&cursor="+*page.NextCursor)
	if second.Code != http.StatusOK {
		t.Fatalf("second page = %d, %s", second.Code, second.Body.String())
	}
	decodeResponse(t, second, &page)
	if len(page.Items) != 1 || page.Items[0].ID != "third" || page.NextCursor != nil {
		t.Fatalf("second page = %#v", page)
	}

	invalid := serveRequest(handler, http.MethodGet, "/api/v1/workspaces?limit=2&cursor=not-a-cursor")
	if invalid.Code != http.StatusBadRequest || !strings.Contains(invalid.Body.String(), `"code":"invalid_cursor"`) {
		t.Fatalf("invalid cursor = %d, %s", invalid.Code, invalid.Body.String())
	}
}

func TestListCursorCannotCrossResourceScope(t *testing.T) {
	items := []string{"a", "b", "c"}
	_, cursor, err := paginateList(items, listPagination{Limit: 1}, "modules\x00main", func(item string) string { return item })
	if err != nil || cursor == nil {
		t.Fatalf("first page cursor = %v, %v", cursor, err)
	}
	if _, _, err := paginateList(items, listPagination{Limit: 1, Cursor: *cursor}, "modules\x00other", func(item string) string { return item }); err == nil {
		t.Fatal("cursor was accepted for another resource scope")
	}
}

func TestM5ListRoutesApplyPaginationAtHTTPBoundary(t *testing.T) {
	registry, _ := testRegistry(t, "main")
	validator := "cfgv-" + strings.Repeat("a", 64)
	moduleService := &fakeModuleManagementService{
		listResult:    application.ModuleListResult{ConfigValidator: validator, Modules: []application.ModuleState{{Name: "a"}, {Name: "b"}}},
		catalogResult: application.ModuleCatalogResult{Modules: []application.ModuleCatalogEntry{{Module: "a"}, {Module: "b"}}},
	}
	moduleHandler := newModuleRouteHandler(t, registry, openHTTPJobStore(t, consolejobs.Options{}), moduleService, nil)
	assertPaginatedField(t, fullDeploymentRequest(moduleHandler, http.MethodGet, "/api/v1/workspaces/main/modules?limit=1", "", ""), "modules")
	assertPaginatedField(t, fullDeploymentRequest(moduleHandler, http.MethodGet, "/api/v1/catalog/modules?workspace_id=main&limit=1", "", ""), "catalog.modules")

	queryService := &fakeQueryService{commands: application.ListModuleCommandsResult{Commands: []application.EffectiveModuleCommand{
		{Command: application.ModuleCommandDescriptor{ID: "a"}},
		{Command: application.ModuleCommandDescriptor{ID: "b"}},
	}}}
	queryHandler := NewHandler(registry, func(string) QueryService { return queryService })
	assertPaginatedField(t, serveRequest(queryHandler, http.MethodGet, "/api/v1/workspaces/main/modules/demo/commands?limit=1"), "items")

	maintenanceService := &fakeMaintenanceService{
		snapshots: application.SnapshotListResult{Snapshots: []application.SnapshotRecord{{ID: "a"}, {ID: "b"}}},
		backups:   application.BackupListResult{TargetID: "target", Backups: []application.BackupRecord{{ID: "a"}, {ID: "b"}}},
		admins:    application.LocalAdminListResult{Accounts: []application.LocalAdminRecord{{TargetID: "a"}, {TargetID: "b"}}},
	}
	maintenanceHandler, _ := newMaintenanceRouteHandler(t, maintenanceService, &recordingDeploymentAuditSink{}, &fakeDeploymentStepUp{})
	assertPaginatedField(t, fullDeploymentRequest(maintenanceHandler, http.MethodGet, "/api/v1/workspaces/main/snapshots?limit=1", "", ""), "snapshots")
	assertPaginatedField(t, fullDeploymentRequest(maintenanceHandler, http.MethodGet, "/api/v1/workspaces/main/backups?target_id=target&limit=1", "", ""), "backups")
	assertPaginatedField(t, fullDeploymentRequest(maintenanceHandler, http.MethodGet, "/api/v1/workspaces/main/local-admins?limit=1", "", ""), "accounts")
}

func assertPaginatedField(t *testing.T, response interface {
	Result() *http.Response
}, field string) {
	t.Helper()
	result := response.Result()
	defer result.Body.Close()
	if result.StatusCode != http.StatusOK {
		t.Fatalf("%s status = %d", field, result.StatusCode)
	}
	var body map[string]any
	if err := json.NewDecoder(result.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	container := body
	name := field
	if prefix, suffix, found := strings.Cut(field, "."); found {
		var ok bool
		container, ok = body[prefix].(map[string]any)
		if !ok {
			t.Fatalf("%s container = %#v", field, body[prefix])
		}
		name = suffix
	}
	items, ok := container[name].([]any)
	if !ok || len(items) != 1 {
		t.Fatalf("%s = %#v", field, container[name])
	}
	if cursor, ok := body["next_cursor"].(string); !ok || cursor == "" {
		t.Fatalf("%s next_cursor = %#v", field, body["next_cursor"])
	}
}
