package runner

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestModuleValidationCollectsTrimmedPlanMetadata(t *testing.T) {
	response := moduleValidationPlanResponse(t, map[string]string{
		"requested_mode": " auto ",
		"resolved_mode":  " separate_zone\n",
		"zone":           " apps.example.net ",
	})
	mod := moduleValidationPlanTestModule(t, response)
	a := moduleValidationTestApp([]string{mod.Name}, map[string]Module{mod.Name: mod})

	if err := a.validateModules(); err != nil {
		t.Fatal(err)
	}
	want := map[string]map[string]string{
		"demo": {
			"requested_mode": "auto",
			"resolved_mode":  "separate_zone",
			"zone":           "apps.example.net",
		},
	}
	if !reflect.DeepEqual(a.modulePlans, want) {
		t.Fatalf("module plans = %#v, want %#v", a.modulePlans, want)
	}
}

func TestModuleValidationRejectsInvalidOrSensitivePlanMetadata(t *testing.T) {
	const secret = "classified-validation-value"
	tests := []struct {
		name      string
		plan      map[string]string
		sensitive bool
		want      string
	}{
		{
			name: "uppercase key",
			plan: map[string]string{"RequestedMode": "auto"},
			want: "plan key",
		},
		{
			name: "invalid key punctuation",
			plan: map[string]string{"requested-mode": "auto"},
			want: "plan key",
		},
		{
			name: "blank value",
			plan: map[string]string{"resolved_mode": " \t\n "},
			want: "must not be empty",
		},
		{
			name: "oversized value",
			plan: map[string]string{"zone": strings.Repeat("x", 1025)},
			want: "exceeds 1024 bytes",
		},
		{
			name:      "sensitive parameter key",
			plan:      map[string]string{"password": "redacted"},
			sensitive: true,
			want:      "sensitive parameter",
		},
		{
			name:      "sensitive environment key",
			plan:      map[string]string{"demo_password": "redacted"},
			sensitive: true,
			want:      "sensitive parameter",
		},
		{
			name:      "sensitive value",
			plan:      map[string]string{"resolved_mode": secret},
			sensitive: true,
			want:      "sensitive value",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			mod := moduleValidationPlanTestModule(t, moduleValidationPlanResponse(t, test.plan))
			a := moduleValidationTestApp([]string{mod.Name}, map[string]Module{mod.Name: mod})
			if test.sensitive {
				a.env["DEMO_PASSWORD"] = secret
				a.envOwner["DEMO_PASSWORD"] = mod.Name
				a.runnerSensitive = map[string]bool{"DEMO_PASSWORD": true}
			}

			err := a.validateModules()
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("invalid validation plan error = %v, want %q", err, test.want)
			}
			if strings.Contains(err.Error(), secret) {
				t.Fatalf("validation plan error exposed secret plaintext: %v", err)
			}
			if len(a.modulePlans) != 0 {
				t.Fatalf("rejected validation metadata was retained: %#v", a.modulePlans)
			}
		})
	}
}

func TestModuleValidationRejectsTrimmedSensitiveValue(t *testing.T) {
	const secret = "secret-with-padding"
	mod := moduleValidationPlanTestModule(t, moduleValidationPlanResponse(t, map[string]string{
		"resolved_mode": secret,
	}))
	a := moduleValidationTestApp([]string{mod.Name}, map[string]Module{mod.Name: mod})
	a.env["DEMO_PASSWORD"] = "  " + secret + "\n"
	a.envOwner["DEMO_PASSWORD"] = mod.Name
	a.runnerSensitive = map[string]bool{"DEMO_PASSWORD": true}

	err := a.validateModules()
	if err == nil || !strings.Contains(err.Error(), "sensitive value") {
		t.Fatalf("trimmed sensitive plan value error = %v", err)
	}
	if strings.Contains(err.Error(), secret) {
		t.Fatalf("validation error exposed secret plaintext: %v", err)
	}
}

func TestDeploymentManifestRoundTripsValidationPlan(t *testing.T) {
	const secret = "manifest-secret-must-not-appear"
	id := "validation-plan-deployment"
	root := filepath.Join(t.TempDir(), id)
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"requested_mode": "auto",
		"resolved_mode":  "separate_zone",
		"zone":           "apps.example.net",
	}
	manifest := &deploymentManifest{
		APIVersion: deploymentAPIVersion,
		ID:         id,
		ModuleOrder: []string{
			"demo",
		},
		Modules: map[string]deploymentModule{
			"demo": {Name: "demo", RuntimeType: "builtin", ValidationPlan: want},
		},
	}
	path := filepath.Join(root, "deployment.yml")
	if err := writeYAMLAtomic(path, manifest, 0o600); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, visible := range []string{"validation_plan:", "requested_mode: auto", "resolved_mode: separate_zone", "zone: apps.example.net"} {
		if !strings.Contains(string(body), visible) {
			t.Errorf("deployment manifest is missing %q:\n%s", visible, body)
		}
	}
	if strings.Contains(string(body), secret) {
		t.Fatalf("deployment manifest exposed secret plaintext: %s", body)
	}

	got, err := loadDeploymentManifest(root)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got.Modules["demo"].ValidationPlan, want) {
		t.Fatalf("round-tripped validation plan = %#v, want %#v", got.Modules["demo"].ValidationPlan, want)
	}
}

func TestModuleValidationPlanSummaryIsStableAndSecretFree(t *testing.T) {
	const secret = "plan-summary-secret-must-not-appear"
	a := &app{
		modulePlans: map[string]map[string]string{
			"zeta":  {"zone": "zeta.example.net"},
			"alpha": {"zone": "alpha.example.net", "resolved_mode": "ad_zone"},
		},
		env:             map[string]string{"DEMO_PASSWORD": secret},
		runnerSensitive: map[string]bool{"DEMO_PASSWORD": true},
	}
	want := "module plan: alpha resolved_mode=ad_zone zone=alpha.example.net\n" +
		"module plan: zeta zone=zeta.example.net\n"
	if got := a.moduleValidationPlanSummary(); got != want {
		t.Fatalf("module validation plan summary = %q, want %q", got, want)
	}
	body, err := json.Marshal(map[string]any{"module_plans": a.moduleValidationPlanDocument()})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), secret) {
		t.Fatalf("module plan document exposed secret plaintext: %s", body)
	}
	if !strings.Contains(string(body), `"module_plans"`) || !strings.Contains(string(body), `"resolved_mode":"ad_zone"`) {
		t.Fatalf("module plan document is missing validation metadata: %s", body)
	}
}

func TestRunPlanExposesModulePlansInJSONAndText(t *testing.T) {
	const secret = "run-plan-secret-must-not-appear"
	response := moduleValidationPlanResponse(t, map[string]string{
		"zone":           "apps.example.net",
		"requested_mode": "auto",
		"resolved_mode":  "separate_zone",
	})
	workspace, moduleRoot := moduleValidationPlanFixture(t, response, secret)
	args := []string{"-w", workspace, "--root", moduleRoot}

	jsonOutput, err := captureRunnerStdout(t, func() error { return runPlan(args, true) })
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err := json.Unmarshal([]byte(jsonOutput), &document); err != nil {
		t.Fatalf("decode plan JSON: %v; output=%q", err, jsonOutput)
	}
	plans, ok := document["module_plans"].(map[string]any)
	if !ok {
		t.Fatalf("plan JSON module_plans = %#v", document["module_plans"])
	}
	demo, ok := plans["demo"].(map[string]any)
	if !ok || demo["requested_mode"] != "auto" || demo["resolved_mode"] != "separate_zone" || demo["zone"] != "apps.example.net" {
		t.Fatalf("plan JSON demo metadata = %#v", plans["demo"])
	}
	if strings.Contains(jsonOutput, secret) {
		t.Fatalf("plan JSON exposed secret plaintext: %s", jsonOutput)
	}

	textOutput, err := captureRunnerStdout(t, func() error { return runPlan(args, false) })
	if err != nil {
		t.Fatal(err)
	}
	wantLine := "module plan: demo requested_mode=auto resolved_mode=separate_zone zone=apps.example.net\n"
	if !strings.Contains(textOutput, wantLine) {
		t.Fatalf("plan text is missing stable module metadata line %q; output=%q", wantLine, textOutput)
	}
	if strings.Contains(textOutput, secret) {
		t.Fatalf("plan text exposed secret plaintext: %s", textOutput)
	}
}

func TestUnpinnedValidationHooksWaitForExplicitLock(t *testing.T) {
	bundle := t.TempDir()
	moduleRoot := filepath.Join(bundle, "modules")
	moduleDir := filepath.Join(moduleRoot, "demo")
	if err := os.MkdirAll(moduleDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(bundle, "contracts"), 0o700); err != nil {
		t.Fatal(err)
	}
	sentinel := filepath.Join(t.TempDir(), "untrusted-hook-executed")
	command, err := json.Marshal(moduleValidationHookHelperCommand("capture", sentinel))
	if err != nil {
		t.Fatal(err)
	}
	manifest := fmt.Sprintf(`api_version: anas.module/v1
kind: Module
name: demo
version: 1.0.0
revision: 1
status: release
abi:
  supports: [anas.module-hook/v1]
runtime:
  type: builtin
config:
  env_prefix: DEMO
  types:
    target: string
logic:
  hook:
    command: %s
    phases: [validate]
`, command)
	if err := os.WriteFile(filepath.Join(moduleDir, "module.yml"), []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}
	reg, err := loadRegistryDir(moduleRoot)
	if err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(t.TempDir(), "config.yml")
	configBody := []byte("modules:\n  demo:\n    config:\n      target: before\nglobal:\n  base_domain: nas.test\n  email: admin@nas.test\n")
	if err := os.WriteFile(source, configBody, 0o600); err != nil {
		t.Fatal(err)
	}
	assertNotExecuted := func(boundary string) {
		t.Helper()
		if _, statErr := os.Stat(sentinel); !os.IsNotExist(statErr) {
			t.Fatalf("%s executed an unpinned validation Hook: %v", boundary, statErr)
		}
	}

	if err := validateConfigImportSource(source, reg); err != nil {
		t.Fatalf("import prevalidation: %v", err)
	}
	assertNotExecuted("import prevalidation")
	workspace := t.TempDir()
	if _, err := importConfigIntoWorkspace(workspace, source, reg); err != nil {
		t.Fatalf("config import: %v", err)
	}
	assertNotExecuted("config import")
	configPath := workspaceConfigPath(workspace)
	if err := setManagedConfigScalar(workspace, configPath,
		[]string{"modules", "demo", "config", "target"}, "after", true, reg); err != nil {
		t.Fatalf("config set: %v", err)
	}
	assertNotExecuted("config set")
	if err := reportConfigPlan(workspace, configPath, stateDir(workspace), reg, false); err != nil {
		t.Fatalf("config plan: %v", err)
	}
	assertNotExecuted("config plan")
	if _, err := captureRunnerStdout(t, func() error {
		return runPlan([]string{"-w", workspace, "--root", moduleRoot}, false)
	}); err != nil {
		t.Fatalf("deployment plan: %v", err)
	}
	assertNotExecuted("deployment plan")

	if _, err := captureRunnerStdout(t, func() error {
		return runLock([]string{"-w", workspace, "--root", moduleRoot}, false)
	}); err != nil {
		t.Fatalf("explicit lock: %v", err)
	}
	if _, err := os.Stat(sentinel); err != nil {
		t.Fatalf("explicit lock did not execute candidate validation Hook: %v", err)
	}
}

func TestConfigPlanRejectsGeneratedSecretInValidationMetadata(t *testing.T) {
	const secret = "generated-plan-secret"
	response := moduleValidationPlanResponse(t, map[string]string{"zone": secret})
	workspace, moduleRoot := moduleValidationPlanFixture(t, response, "  "+secret+"\n")
	reg, err := loadRegistryDir(moduleRoot)
	if err != nil {
		t.Fatal(err)
	}
	err = reportConfigPlan(workspace, workspaceConfigPath(workspace), stateDir(workspace), reg, false)
	if err == nil || !strings.Contains(err.Error(), "sensitive value") {
		t.Fatalf("config plan secret-filter error = %v", err)
	}
	if strings.Contains(err.Error(), secret) {
		t.Fatalf("config plan error exposed generated secret plaintext: %v", err)
	}
}

func TestPlanRejectsTamperedBundleBeforeExecutingValidateHook(t *testing.T) {
	bundle := t.TempDir()
	moduleRoot := filepath.Join(bundle, "modules")
	moduleDir := filepath.Join(moduleRoot, "demo")
	if err := os.MkdirAll(moduleDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(bundle, "contracts"), 0o700); err != nil {
		t.Fatal(err)
	}
	sentinel := filepath.Join(t.TempDir(), "hook-executed")
	command, err := json.Marshal(moduleValidationHookHelperCommand("capture", sentinel))
	if err != nil {
		t.Fatal(err)
	}
	manifestPath := filepath.Join(moduleDir, "module.yml")
	manifest := fmt.Sprintf(`api_version: anas.module/v1
kind: Module
name: demo
version: 1.0.0
revision: 1
status: release
abi:
  supports: [anas.module-hook/v1]
runtime:
  type: builtin
logic:
  hook:
    command: %s
    phases: [validate]
`, command)
	if err := os.WriteFile(manifestPath, []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}
	workspace := t.TempDir()
	if err := os.MkdirAll(stateDir(workspace), 0o700); err != nil {
		t.Fatal(err)
	}
	configPath := workspaceConfigPath(workspace)
	if err := os.WriteFile(configPath, []byte("modules:\n  demo: {}\nglobal:\n  base_domain: nas.test\n  email: admin@nas.test\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := writeManagedConfigState(workspace, "lock-trust-test"); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRunnerStdout(t, func() error {
		return runLock([]string{"-w", workspace, "--root", moduleRoot}, false)
	}); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(sentinel); err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	file, err := os.OpenFile(manifestPath, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteString("# tampered after lock\n"); err != nil {
		file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}

	for _, test := range []struct {
		name string
		run  func() error
	}{
		{
			name: "deployment plan",
			run: func() error {
				return runPlan([]string{"-w", workspace, "--root", moduleRoot}, false)
			},
		},
		{
			name: "config plan",
			run: func() error {
				reg, err := loadRegistryDir(moduleRoot)
				if err != nil {
					return err
				}
				return reportConfigPlan(workspace, configPath, stateDir(workspace), reg, false)
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := test.run()
			if err == nil || !strings.Contains(err.Error(), "bundle digest does not match") {
				t.Fatalf("tampered bundle error = %v", err)
			}
			if cliErr, ok := err.(*CLIError); !ok || cliErr.Code != "lock_stale" {
				t.Fatalf("tampered bundle code = %#v, want lock_stale", err)
			}
			if _, statErr := os.Stat(sentinel); !os.IsNotExist(statErr) {
				t.Fatalf("untrusted validate Hook executed before lock rejection: %v", statErr)
			}
		})
	}
}

func moduleValidationPlanTestModule(t *testing.T, response string) Module {
	t.Helper()
	return Module{
		Name: "demo", EnvPrefix: "DEMO", SourceDir: t.TempDir(),
		Changes: map[string]ChangePolicy{
			"password": {Sensitive: true},
		},
		Hook: HookConfig{
			Command: moduleValidationHookHelperCommand("respond", response),
			Phases:  []string{"validate"},
		},
	}
}

func moduleValidationPlanResponse(t *testing.T, plan map[string]string) string {
	t.Helper()
	body, err := json.Marshal(map[string]any{"plan": plan})
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}

func moduleValidationPlanFixture(t *testing.T, response, secret string) (string, string) {
	t.Helper()
	bundle := t.TempDir()
	moduleRoot := filepath.Join(bundle, "modules")
	moduleDir := filepath.Join(moduleRoot, "demo")
	if err := os.MkdirAll(moduleDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(bundle, "contracts"), 0o700); err != nil {
		t.Fatal(err)
	}
	command, err := json.Marshal(moduleValidationHookHelperCommand("respond", response))
	if err != nil {
		t.Fatal(err)
	}
	manifest := fmt.Sprintf(`api_version: anas.module/v1
kind: Module
name: demo
version: 1.0.0
revision: 1
status: release
abi:
  supports: [anas.module-hook/v1]
runtime:
  type: builtin
config:
  env_prefix: DEMO
logic:
  hook:
    command: %s
    phases: [validate]
`, command)
	if err := os.WriteFile(filepath.Join(moduleDir, "module.yml"), []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}

	workspace := t.TempDir()
	base := stateDir(workspace)
	if err := os.MkdirAll(base, 0o700); err != nil {
		t.Fatal(err)
	}
	configPath := workspaceConfigPath(workspace)
	configBody := "modules:\n  demo: {}\nglobal:\n  base_domain: nas.test\n  email: admin@nas.test\n"
	if err := os.WriteFile(configPath, []byte(configBody), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := writeManagedConfigState(workspace, "module-validation-plan-test"); err != nil {
		t.Fatal(err)
	}
	// Establish bundle trust before adding the generated secret used by the
	// response-filter tests. The validation response itself is not persisted in
	// the lock, so each plan still executes and filters the Hook afresh.
	if _, err := captureRunnerStdout(t, func() error {
		return runLock([]string{"-w", workspace, "--root", moduleRoot}, false)
	}); err != nil {
		t.Fatal(err)
	}
	store := &secretStore{
		path: filepath.Join(base, "secrets.yml"),
		values: map[string]string{
			"DEMO_PASSWORD": secret,
		},
		metadata: map[string]secretMetadata{
			"DEMO_PASSWORD": {Owner: "demo", Kind: "generated", Provenance: "test"},
		},
		dirty: true,
	}
	if err := store.Save(); err != nil {
		t.Fatal(err)
	}
	return workspace, moduleRoot
}
