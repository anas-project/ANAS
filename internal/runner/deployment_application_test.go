package runner

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/anas-project/ANAS/internal/application"
	"github.com/anas-project/ANAS/internal/config"
	"github.com/anas-project/ANAS/internal/modulestore"
)

func TestWorkspaceDeploymentPlanUsesPersistedViewAndOpaqueValidatorDigest(t *testing.T) {
	workspace := newWorkspace(t)
	seedDeploymentPlanModuleView(t, workspace)
	service := NewWorkspaceDeploymentPlanService(workspace)

	// Daemon planning must not inherit the CLI discovery escape hatch.
	t.Setenv("ANAS_MODULE_ROOT", filepath.Join(t.TempDir(), "attacker-controlled"))
	first, err := service.Plan(context.Background(), application.PlanRequest{})
	if err != nil {
		t.Fatal(err)
	}
	second, err := service.Plan(context.Background(), application.PlanRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if first.Digest == "" || first.Digest != second.Digest {
		t.Fatalf("stable plan digests = %q and %q", first.Digest, second.Digest)
	}
	if !validManagedConfigValidator(first.ConfigValidator) {
		t.Fatalf("config validator = %q", first.ConfigValidator)
	}
	managed, err := readManagedConfigSnapshot(workspace)
	if err != nil {
		t.Fatal(err)
	}
	state := managed.state
	if first.ConfigValidator != state.Validator {
		t.Fatalf("plan validator = %q, state validator = %q", first.ConfigValidator, state.Validator)
	}
	if first.Digest == state.ContentDigest || strings.Contains(first.Digest, state.ContentDigest) {
		t.Fatal("plan digest exposed or reused the internal config content digest")
	}
	body, err := json.Marshal(first)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), first.ConfigPath) || strings.Contains(string(body), first.ModuleRoot) {
		t.Fatalf("application JSON exposed host paths: %s", body)
	}

	if err := writeManagedConfigState(workspace, "test-validator-rotation"); err != nil {
		t.Fatal(err)
	}
	rotated, err := service.Plan(context.Background(), application.PlanRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if rotated.ConfigValidator == first.ConfigValidator || rotated.Digest == first.Digest {
		t.Fatalf("validator rotation did not invalidate plan: before=%q/%q after=%q/%q",
			first.ConfigValidator, first.Digest, rotated.ConfigValidator, rotated.Digest)
	}
}

func TestWorkspaceDeploymentPlanLockWaitIsCancelable(t *testing.T) {
	workspace := newWorkspace(t)
	seedDeploymentPlanModuleView(t, workspace)
	unlock, err := acquireRuntimeLock(stateDir(workspace))
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, err = NewWorkspaceDeploymentPlanService(workspace).Plan(ctx, application.PlanRequest{})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("plan lock wait error = %v, want deadline exceeded", err)
	}
	appErr, ok := application.ErrorOf(err)
	if !ok || appErr.Code != "runtime_lock_unavailable" {
		t.Fatalf("plan lock error = %#v, want runtime_lock_unavailable", appErr)
	}
}

func TestDeploymentPlanCLIRenderingPreservesExistingProjection(t *testing.T) {
	provider := "samba_dc"
	result := application.PlanResult{
		ConfigValidator: "cfgv-not-for-cli",
		Digest:          "plan-digest-not-for-cli",
		ConfigPath:      "/workspace/config.yml",
		ModuleRoot:      "/modules",
		Modules:         []string{"samba_dc", "lego", "ddns_go"},
		IAM: application.PlanIAM{Provider: &provider, Consumers: []application.PlanIAMConsumer{
			{Module: "app", Interface: "oidc"},
		}},
		ModulePlans: map[string]map[string]string{
			"zeta": {"zone": "zeta.test", "mode": "auto"},
		},
		CapabilityBindings: map[string]map[string]string{"app": {"iam": "samba_dc"}},
		DNSPlatforms:       map[string]string{"lego": "cloudflare", "ddns_go": "cloudflare"},
		DNSCredentialCompatibility: []application.PlanDNSCredentialCompatibility{
			{Left: "ddns_go", Right: "lego", Platform: "cloudflare", Compatibility: "shared"},
		},
		DynamicDNS:       application.PlanDynamicDNS{Provider: &provider, SelfManaged: []string{"ddns_go"}, Automatic: true},
		ModuleLifecycles: []application.PlanModuleLifecycle{{Module: "lego", Status: "developing"}},
	}
	wantText := "samba_dc\nlego\nddns_go\n" +
		"\niam provider: samba_dc\n  app -> samba_dc/oidc\n" +
		"\ndns platforms:\n  ddns_go -> cloudflare\n  lego -> cloudflare\n  ddns_go/lego credentials: shared\n" +
		"\ndynamic dns: samba_dc (auto)\n  ddns_go runs with its own configuration\n" +
		"module lifecycle: lego=developing (not release quality)\n" +
		"module plan: zeta mode=auto zone=zeta.test\n"
	if got := planResultCLIText(result); got != wantText {
		t.Fatalf("plan CLI text = %q, want %q", got, wantText)
	}
	body, err := json.Marshal(planResultCLIMap(result))
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"config_validator", "plan-digest-not-for-cli", "dns_credential_compatibility", "module_lifecycles", "automatic"} {
		if strings.Contains(string(body), forbidden) {
			t.Fatalf("legacy CLI JSON gained %q: %s", forbidden, body)
		}
	}
}

func TestDeploymentApplicationErrorPreservesRunnerDetailForSafeAdapterProjection(t *testing.T) {
	err := deploymentApplicationErrorFromCLI(&CLIError{
		Code: "guarded_changes", Message: "guarded", Exit: exitPrecondition,
		Detail: map[string]any{"blocked": []string{"global.base_domain (immutable; migrate-service-domain)"}},
	})
	applicationError, ok := application.ErrorOf(err)
	if !ok {
		t.Fatalf("application error = %T", err)
	}
	blocked, ok := applicationError.Detail["blocked"].([]string)
	if !ok || len(blocked) != 1 || blocked[0] != "global.base_domain (immutable; migrate-service-domain)" {
		t.Fatalf("application error detail = %#v", applicationError.Detail)
	}
}

func TestWorkspaceDeploymentApplyRequiresConfirmationAndRejectsPlanDriftBeforeMaterializing(t *testing.T) {
	workspace := newWorkspace(t)
	seedDeploymentPlanModuleView(t, workspace)
	service := NewWorkspaceDeploymentService(workspace)
	plan, err := service.Plan(context.Background(), application.PlanRequest{})
	if err != nil {
		t.Fatal(err)
	}
	_, err = service.Apply(context.Background(), application.ApplyRequest{
		ExpectedConfigValidator: plan.ConfigValidator,
		ExpectedPlanDigest:      plan.Digest,
	})
	assertDeploymentApplicationCode(t, err, application.ErrorKindPreconditionRequired, "confirmation_required")

	if err := writeManagedConfigState(workspace, "apply-drift-test"); err != nil {
		t.Fatal(err)
	}
	_, err = service.Apply(context.Background(), application.ApplyRequest{
		ExpectedConfigValidator: plan.ConfigValidator,
		ExpectedPlanDigest:      plan.Digest,
		Confirmed:               true,
	})
	assertDeploymentApplicationCode(t, err, application.ErrorKindFailedPrecondition, "plan_changed")
	if entries, readErr := filepath.Glob(filepath.Join(stateDir(workspace), "deployments", "*")); readErr != nil {
		t.Fatal(readErr)
	} else if len(entries) != 0 {
		t.Fatalf("plan drift materialized deployments: %v", entries)
	}
}

func TestWorkspaceDeploymentApplyRejectsNetworkNamespaceBeforeMaterializing(t *testing.T) {
	workspace := newWorkspace(t)
	seedDeploymentPlanModuleView(t, workspace)
	if err := config.SetString(workspaceConfigPath(workspace), []string{"env", "NETWORK_NAMESPACE_PATH"}, "/run/netns/anas-test"); err != nil {
		t.Fatal(err)
	}
	if err := writeManagedConfigState(workspace, "network-namespace-test"); err != nil {
		t.Fatal(err)
	}
	service := NewWorkspaceDeploymentService(workspace)
	plan, err := service.Plan(context.Background(), application.PlanRequest{})
	if err != nil {
		t.Fatal(err)
	}
	_, err = service.Apply(context.Background(), application.ApplyRequest{
		ExpectedConfigValidator: plan.ConfigValidator,
		ExpectedPlanDigest:      plan.Digest,
		Confirmed:               true,
	})
	assertDeploymentApplicationCode(t, err, application.ErrorKindFailedPrecondition, "network_namespace_requires_tty")
	if entries, readErr := filepath.Glob(filepath.Join(stateDir(workspace), "deployments", "*")); readErr != nil {
		t.Fatal(readErr)
	} else if len(entries) != 0 {
		t.Fatalf("namespace rejection materialized deployments: %v", entries)
	}
}

func assertDeploymentApplicationCode(t *testing.T, err error, kind application.ErrorKind, code string) {
	t.Helper()
	appErr, ok := application.ErrorOf(err)
	if !ok || appErr.Kind != kind || appErr.Code != code {
		t.Fatalf("application error = %#v (%v), want %s/%s", appErr, err, kind, code)
	}
}

func seedDeploymentPlanModuleView(t *testing.T, workspace string) {
	t.Helper()
	if err := saveWorkspaceModuleView(workspace, modulestore.View{
		APIVersion: "anas.module-view/v1",
		Digest:     "sha256:test",
		ModuleRoot: filepath.Join(repoRoot(t), "modules"),
	}); err != nil {
		t.Fatal(err)
	}
}
