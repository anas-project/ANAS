package runner

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/anas-project/ANAS/internal/application"
	"github.com/anas-project/ANAS/internal/modulestore"
)

func TestModuleManagementEnableDisableUsesManagedConfigCAS(t *testing.T) {
	workspace := t.TempDir()
	if err := ensureRuntimeLayout(stateDir(workspace)); err != nil {
		t.Fatal(err)
	}
	body := []byte(`module_source: official
modules:
  traefik:
    config: {}
global:
  base_domain: nas.test
  email: admin@nas.test
  timezone: UTC
`)
	if err := os.WriteFile(workspaceConfigPath(workspace), body, 0o600); err != nil {
		t.Fatal(err)
	}
	state, err := managedConfigStateBytes(body, "test")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(managedConfigStatePath(stateDir(workspace)), state, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := saveWorkspaceModuleView(workspace, modulestore.View{
		APIVersion: "anas.module-view/v1", Digest: "sha256:test", ModuleRoot: repoRoot(t) + "/modules",
	}); err != nil {
		t.Fatal(err)
	}

	service := NewWorkspaceModuleManagementService(workspace, application.NopEventSink{})
	listed, err := service.ListModules(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	observer := &configApplicationTestObserver{}
	enabled, err := service.SetModuleEnabled(context.Background(), application.ModuleEnabledRequest{
		Module: "lego", Enabled: true, ExpectedConfigValidator: listed.ConfigValidator,
		OperationID: "cfg-0123456789abcdef0123456789abcdef",
	}, observer)
	if err != nil {
		t.Fatal(err)
	}
	if enabled.PreviousValidator != listed.ConfigValidator || enabled.ConfigValidator == listed.ConfigValidator || len(observer.intents) != 1 {
		t.Fatalf("enable result=%#v intents=%#v", enabled, observer.intents)
	}
	afterEnable, err := service.ListModules(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if state := moduleConfigurationState(afterEnable.Modules, "lego"); state != "selected" {
		t.Fatalf("lego state after enable = %q", state)
	}

	disabled, err := service.SetModuleEnabled(context.Background(), application.ModuleEnabledRequest{
		Module: "lego", Enabled: false, ExpectedConfigValidator: enabled.ConfigValidator,
		OperationID: "cfg-fedcba9876543210fedcba9876543210",
	}, &configApplicationTestObserver{})
	if err != nil {
		t.Fatal(err)
	}
	if disabled.ConfigValidator == enabled.ConfigValidator || disabled.Enabled {
		t.Fatalf("disable result = %#v", disabled)
	}
	afterDisable, err := service.ListModules(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if state := moduleConfigurationState(afterDisable.Modules, "lego"); state != "available" {
		t.Fatalf("lego state after disable = %q", state)
	}

	_, err = service.SetModuleEnabled(context.Background(), application.ModuleEnabledRequest{
		Module: "lego", Enabled: true, ExpectedConfigValidator: listed.ConfigValidator,
		OperationID: "cfg-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
	}, &configApplicationTestObserver{})
	assertDeploymentApplicationCode(t, err, application.ErrorKindFailedPrecondition, "config_precondition_failed")
}

func TestFreezeManagementSurfacesPublishesOnlySafeHTTPAddresses(t *testing.T) {
	module := Module{Name: "demo", ManagementSurfaces: []ManagementSurface{{ID: "admin", URIFrom: "DEMO_ADMIN_URL", Authentication: "local"}}}
	surfaces, err := freezeManagementSurfaces(module, map[string]string{"DEMO_ADMIN_URL": "https://demo.example.test/admin"})
	if err != nil {
		t.Fatal(err)
	}
	if len(surfaces) != 1 || surfaces[0].ID != "admin" || surfaces[0].URI != "https://demo.example.test/admin" || surfaces[0].Authentication != "local" {
		t.Fatalf("frozen surfaces = %#v", surfaces)
	}
	for _, uri := range []string{"/private/admin", "file:///private/admin", "https://owner:secret@demo.example.test/admin"} {
		if _, err := freezeManagementSurfaces(module, map[string]string{"DEMO_ADMIN_URL": uri}); err == nil || !strings.Contains(err.Error(), "public HTTP(S) URI") {
			t.Fatalf("unsafe URI %q was accepted: %v", uri, err)
		}
	}
}

func moduleConfigurationState(modules []application.ModuleState, name string) string {
	for _, module := range modules {
		if module.Name == name {
			return module.ConfigurationState
		}
	}
	return ""
}
