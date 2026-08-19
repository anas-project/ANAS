package runner

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/anas-project/ANAS/internal/config"
)

func identityAddressTestRegistry() map[string]Module {
	return map[string]Module{
		"demo": {
			Name: "demo", EnvPrefix: "DEMO",
			Parameters: []string{"iam_protocol"},
			Types: map[string]ParamType{
				"iam_protocol": {Kind: "enum", Enum: []string{"auto", "oidc", "saml"}},
			},
			RequiresCapabilities: []RequiredCapability{{
				Name: capabilityIAM, InterfaceSelectedBy: "iam_protocol",
				AnyOf: []string{interfaceOIDC, interfaceSAML}, Prefer: []string{interfaceOIDC},
			}},
		},
	}
}

func TestIdentityLoginProtocolHasOneCanonicalConfigAddress(t *testing.T) {
	reg := identityAddressTestRegistry()
	wantPath := []string{"modules", "demo", "identity", "login_protocol"}
	for _, spelling := range []string{"demo.iam_protocol", "modules.demo.config.iam_protocol", "modules.demo.identity.login_protocol"} {
		target, err := resolveConfigTarget(spelling, reg)
		if err != nil {
			t.Fatalf("resolve %s: %v", spelling, err)
		}
		if !reflect.DeepEqual(target.YAMLPath, wantPath) {
			t.Fatalf("resolve %s path = %v, want %v", spelling, target.YAMLPath, wantPath)
		}
	}

	t.Run("set and runtime", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "config.yml")
		if err := os.WriteFile(path, []byte("modules:\n  demo: {}\nglobal:\n  base_domain: nas.test\n  email: admin@nas.test\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := config.SetString(path, wantPath, "saml"); err != nil {
			t.Fatal(err)
		}
		loaded, err := config.Load(path)
		if err != nil {
			t.Fatal(err)
		}
		if got := configBaseEnv(loaded, reg)["DEMO_IAM_PROTOCOL"]; got != "saml" {
			t.Fatalf("runtime login protocol = %q, want saml", got)
		}
		settings, err := config.Settings(path)
		if err != nil {
			t.Fatal(err)
		}
		if _, legacy := settings["modules.demo.config.iam_protocol"]; legacy {
			t.Fatal("set wrote the legacy config selector address")
		}
		if settings["modules.demo.identity.login_protocol"] != "saml" {
			t.Fatalf("canonical identity setting = %q", settings["modules.demo.identity.login_protocol"])
		}
	})

	t.Run("import migration base env and list", func(t *testing.T) {
		source := filepath.Join(t.TempDir(), "source.yml")
		body := `modules:
  demo:
    config:
      iam_protocol: oidc
global:
  base_domain: nas.test
  email: admin@nas.test
`
		if err := os.WriteFile(source, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		result, err := normalizeImportedConfig(source, reg)
		if err != nil {
			t.Fatal(err)
		}
		normalized := filepath.Join(t.TempDir(), "config.yml")
		if err := os.WriteFile(normalized, result.Normalized, 0o600); err != nil {
			t.Fatal(err)
		}
		loaded, err := config.Load(normalized)
		if err != nil {
			t.Fatal(err)
		}
		if got := configBaseEnv(loaded, reg)["DEMO_IAM_PROTOCOL"]; got != "oidc" {
			t.Fatalf("imported runtime login protocol = %q, want oidc", got)
		}
		settings, err := config.Settings(normalized)
		if err != nil {
			t.Fatal(err)
		}
		if _, legacy := settings["modules.demo.config.iam_protocol"]; legacy {
			t.Fatal("import retained the legacy selector address")
		}
		entries, err := collectConfigParameters(reg, settings)
		if err != nil {
			t.Fatal(err)
		}
		count := 0
		for _, entry := range entries {
			if entry.Module == "demo" && entry.Parameter == "iam_protocol" {
				count++
				if entry.Path != "demo.iam_protocol" || entry.EnvKey != "DEMO_IAM_PROTOCOL" || !entry.Set || entry.Value != "oidc" {
					t.Fatalf("identity list entry = %#v", entry)
				}
			}
		}
		if count != 1 {
			t.Fatalf("identity parameter list count = %d, want 1", count)
		}
	})

	t.Run("dual source collision", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "config.yml")
		body := `modules:
  demo:
    identity:
      login_protocol: oidc
    config:
      iam_protocol: saml
global:
  base_domain: nas.test
  email: admin@nas.test
`
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		for name, err := range map[string]error{
			"loaded collision": validateConfigRuntimeKeyCollisions(path, reg),
			"import collision": func() error { _, err := normalizeImportedConfig(path, reg); return err }(),
		} {
			if err == nil || !strings.Contains(err.Error(), "DEMO_IAM_PROTOCOL") {
				t.Fatalf("%s error = %v, want runtime-key collision", name, err)
			}
		}
	})
}

func TestBootstrapUsernameAliasesParticipateInRuntimeKeyCollisions(t *testing.T) {
	reg := map[string]Module{
		"samba_dc": {
			Name: "samba_dc", EnvPrefix: "SAMBA_DC",
			Parameters: []string{"admin_name"},
			Types:      map[string]ParamType{"admin_name": {Kind: "string"}},
		},
	}
	keys := bootstrapUsernameRuntimeKeys(reg)
	for _, want := range []string{"ANAS_BOOTSTRAP_ADMIN_USERNAME", "SAMBA_DC_ADMIN_NAME"} {
		if !contains(keys, want) {
			t.Fatalf("bootstrap runtime keys %v missing %s", keys, want)
		}
	}
	path := filepath.Join(t.TempDir(), "config.yml")
	body := `administration:
  bootstrap:
    username: operator
modules:
  samba_dc:
    config:
      admin_name: administrator
global:
  base_domain: nas.test
  email: admin@nas.test
`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	err := validateConfigRuntimeKeyCollisions(path, reg)
	if err == nil || !strings.Contains(err.Error(), "SAMBA_DC_ADMIN_NAME") {
		t.Fatalf("bootstrap alias collision = %v", err)
	}
}

func TestRunnerOwnedRawInputsAreRejectedButConfigOverridesRemainAvailable(t *testing.T) {
	reg := map[string]Module{"dummy": {Name: "dummy", EnvPrefix: "DUMMY"}}
	for _, test := range []struct {
		name, section, key string
	}{
		{name: "env topology", section: "env", key: "DOMAINS"},
		{name: "env application list", section: "env", key: "APPS_LIST"},
		{name: "secret identity topology", section: "secrets", key: "ANAS_IAM_PROVIDER"},
	} {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.yml")
			body := "modules:\n  dummy: {}\nglobal:\n  base_domain: nas.test\n  email: admin@nas.test\n" + test.section + ":\n  " + test.key + ": attacker\n"
			if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := validateConfigRuntimeKeyCollisions(path, reg); err == nil || !strings.Contains(err.Error(), "runner-owned") {
				t.Fatalf("shared plan/apply collision boundary error = %v", err)
			}
			err := validateConfigImportSource(path, reg)
			if err == nil || !strings.Contains(err.Error(), "runner-owned") || !strings.Contains(err.Error(), test.key) {
				t.Fatalf("reserved raw input error = %v", err)
			}
		})
	}
	if _, err := resolveConfigTarget("env.ALL_MODS_NAME", reg); err == nil || !strings.Contains(err.Error(), "runner-owned") {
		t.Fatalf("config set reserved target error = %v", err)
	}
	if _, err := resolveConfigTarget("env.APPS_LIST", reg); err == nil || !strings.Contains(err.Error(), "runner-owned") {
		t.Fatalf("config set application list target error = %v", err)
	}
	if _, err := resolveConfigTarget("env.GHCR_REGISTRY", reg); err != nil {
		t.Fatalf("configurable build override was rejected: %v", err)
	}
	for _, key := range []string{"DOCKER_SOCKET_PATH", "ANAS_RUNTIME_ENTRY_IP"} {
		if _, err := resolveConfigTarget("env."+key, reg); err != nil {
			t.Fatalf("documented runtime override %s was rejected: %v", key, err)
		}
	}
}

func TestBareExportRawEnvKeepsModuleOwnershipAndScope(t *testing.T) {
	reg := map[string]Module{
		"publisher": {
			Name: "publisher", EnvPrefix: "PUBLISHER",
			Parameters: []string{"shared_token", "normal"}, Exports: []string{"SHARED_TOKEN"},
			Types: map[string]ParamType{
				"shared_token": {Kind: "string"},
				"normal":       {Kind: "string"},
			},
		},
		"other": {Name: "other", EnvPrefix: "OTHER"},
	}
	cfg := &config.File{
		Modules: config.NewModuleSelection("publisher", "other"),
		Env:     map[string]any{"SHARED_TOKEN": "initial", "PUBLISHER_NORMAL": "owned", "RAW_GLOBAL": "visible"},
		Secrets: map[string]any{"PRIVATE_TOKEN": "secret"},
	}
	env, owners := configBaseEnvWithRegistry(cfg, reg)
	if owners["SHARED_TOKEN"] != "publisher" {
		t.Fatalf("bare export owner = %q, want publisher", owners["SHARED_TOKEN"])
	}
	if owners["PUBLISHER_NORMAL"] != "publisher" {
		t.Fatalf("prefixed declared raw env owner = %q, want publisher", owners["PUBLISHER_NORMAL"])
	}
	if owners["RAW_GLOBAL"] != globalScope {
		t.Fatalf("ordinary raw env owner = %q, want global", owners["RAW_GLOBAL"])
	}
	if owners["PRIVATE_TOKEN"] != config.OwnerUserSecret {
		t.Fatalf("raw secret owner = %q, want user-secret", owners["PRIVATE_TOKEN"])
	}
	a := &app{cfg: cfg, reg: reg, env: env, envOwner: owners, deps: map[string][]string{}}
	if got := a.scopedEnv("publisher")["SHARED_TOKEN"]; got != "initial" {
		t.Fatalf("publisher scoped bare export = %q", got)
	}
	if _, leaked := a.scopedEnv("other")["SHARED_TOKEN"]; leaked {
		t.Fatal("bare export leaked to an unrelated Module as a global value")
	}
	if _, leaked := a.scopedEnv("other")["PUBLISHER_NORMAL"]; leaked {
		t.Fatal("prefixed declared raw env leaked to an unrelated Module as a global value")
	}
	if got := a.scopedEnv("other")["RAW_GLOBAL"]; got != "visible" {
		t.Fatalf("ordinary raw global value = %q", got)
	}
	if err := a.applyCalculatePatch(reg["publisher"], map[string]string{"SHARED_TOKEN": "updated"}); err != nil {
		t.Fatalf("owner Hook could not update its bare export: %v", err)
	}
	if a.env["SHARED_TOKEN"] != "updated" || a.envOwner["SHARED_TOKEN"] != "publisher" {
		t.Fatalf("updated bare export = %q owner=%q", a.env["SHARED_TOKEN"], a.envOwner["SHARED_TOKEN"])
	}
}

func TestUnsetConfigCoreKeyStillRejectsDefaultPrefixHookOverwrite(t *testing.T) {
	reg := map[string]Module{
		"github_download_proxy": {Name: "github_download_proxy", EnvPrefix: "GITHUB_DOWNLOAD_PROXY"},
	}
	cfg := &config.File{Modules: config.NewModuleSelection("github_download_proxy")}
	env, owners := configBaseEnvWithRegistry(cfg, reg)
	if _, present := env["GITHUB_DOWNLOAD_PROXY_PREFIX"]; present {
		t.Fatal("disabled speedup unexpectedly materialized a config-core default")
	}
	if owners["GITHUB_DOWNLOAD_PROXY_PREFIX"] != globalScope {
		t.Fatalf("unset config-core owner = %q, want global", owners["GITHUB_DOWNLOAD_PROXY_PREFIX"])
	}
	a := &app{cfg: cfg, reg: reg, env: env, envOwner: owners}
	err := a.applyCalculatePatch(reg["github_download_proxy"], map[string]string{
		"GITHUB_DOWNLOAD_PROXY_PREFIX": "https://attacker.invalid/",
	})
	if err == nil || !strings.Contains(err.Error(), "owned by another source") {
		t.Fatalf("default-prefix Hook overwrite error = %v", err)
	}
	if _, landed := a.env["GITHUB_DOWNLOAD_PROXY_PREFIX"]; landed {
		t.Fatal("rejected default-prefix Hook value was applied")
	}
}

func TestRuntimeEnvironmentKeySyntaxAtImportPlanAndSetBoundaries(t *testing.T) {
	reg := map[string]Module{"legacy": {Name: "legacy", EnvPrefix: "LEGACY"}}
	valid := `modules:
  legacy:
    config:
      good_name: value
global:
  base_domain: nas.test
  email: admin@nas.test
env:
  lower_key: visible
`
	validPath := filepath.Join(t.TempDir(), "valid.yml")
	if err := os.WriteFile(validPath, []byte(valid), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := validateConfigImportSource(validPath, reg); err != nil {
		t.Fatalf("legacy lower_snake runtime keys were rejected: %v", err)
	}
	loaded, err := config.Load(validPath)
	if err != nil {
		t.Fatal(err)
	}
	if got := configBaseEnv(loaded, reg)["LOWER_KEY"]; got != "visible" {
		t.Fatalf("lowercase raw key canonical value = %q", got)
	}
	if _, err := resolveConfigTarget("legacy.good_name", reg); err != nil {
		t.Fatalf("legacy undeclared lower_snake parameter was rejected: %v", err)
	}
	if target, err := resolveConfigTarget("env.lower_key", reg); err != nil || !reflect.DeepEqual(target.YAMLPath, []string{"env", "LOWER_KEY"}) {
		t.Fatalf("lowercase raw set target = %#v err=%v", target, err)
	}

	invalid := map[string]string{
		"raw newline": `env:
  "BAD\nKEY": value
`,
		"raw equals": `env:
  "BAD=KEY": value
`,
		"raw hyphen": `env:
  bad-key: value
`,
		"raw empty": `env:
  "": value
`,
		"secret space": `secrets:
  "BAD KEY": value
`,
		"legacy module hyphen": `modules:
  legacy:
    config:
      bad-key: value
`,
	}
	baseDocument := `modules:
  legacy: {}
global:
  base_domain: nas.test
  email: admin@nas.test
`
	for name, fragment := range invalid {
		t.Run(name, func(t *testing.T) {
			body := baseDocument + fragment
			if strings.HasPrefix(fragment, "modules:") {
				body = fragment + "global:\n  base_domain: nas.test\n  email: admin@nas.test\n"
			}
			path := filepath.Join(t.TempDir(), "config.yml")
			if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := validateConfigImportSource(path, reg); err == nil || (!strings.Contains(err.Error(), "environment key") && !strings.Contains(err.Error(), "empty key")) {
				t.Fatalf("import invalid-key error = %v", err)
			}
			workspace := t.TempDir()
			planPath := workspaceConfigPath(workspace)
			if err := os.WriteFile(planPath, []byte(body), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.MkdirAll(stateDir(workspace), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := reportConfigPlan(workspace, planPath, stateDir(workspace), reg, true); err == nil || !strings.Contains(err.Error(), "environment key") {
				t.Fatalf("plan invalid-key error = %v", err)
			}
		})
	}

	for _, path := range []string{"env.bad-key", "env.BAD=KEY", "env.BAD\nKEY", "env. "} {
		if _, err := resolveConfigTarget(path, reg); err == nil || !strings.Contains(err.Error(), "environment key") {
			t.Fatalf("set target %q error = %v", path, err)
		}
	}
	if _, err := resolveConfigTarget("legacy.bad-key", reg); err == nil || !strings.Contains(err.Error(), "environment key") {
		t.Fatalf("legacy module set invalid-key error = %v", err)
	}

	t.Run("managed set is atomic", func(t *testing.T) {
		workspace := t.TempDir()
		if err := os.MkdirAll(stateDir(workspace), 0o700); err != nil {
			t.Fatal(err)
		}
		path := workspaceConfigPath(workspace)
		body := baseDocument + "env:\n  \"BAD=KEY\": value\n"
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := writeManagedConfigState(workspace, "test"); err != nil {
			t.Fatal(err)
		}
		before, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		err = setManagedConfigScalar(workspace, path, []string{"modules", "legacy", "config", "good_name"}, "after", true, reg)
		if err == nil || !strings.Contains(err.Error(), "environment key") {
			t.Fatalf("managed set invalid-key error = %v", err)
		}
		after, readErr := os.ReadFile(path)
		if readErr != nil {
			t.Fatal(readErr)
		}
		if string(after) != string(before) {
			t.Fatal("rejected invalid-key set changed managed config")
		}
		if err := validateManagedConfig(workspace, path); err != nil {
			t.Fatalf("rejected invalid-key set changed managed digest: %v", err)
		}
	})
}
