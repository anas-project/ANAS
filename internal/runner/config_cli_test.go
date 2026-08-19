package runner

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/anas-project/ANAS/internal/config"
	"github.com/anas-project/ANAS/internal/configschema"
)

func TestResolveGlobalAndServiceConfigTargets(t *testing.T) {
	reg, err := loadRegistry(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	// A parameter a module publishes under a bare env name is reachable through
	// the module that owns it, and keeps that module's change policy rather than
	// falling back to the default one.
	guest, err := resolveConfigTarget("samba_fs.share_guest_read_only", reg)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(guest.YAMLPath, "."); got != "env.SHARE_GUEST_READ_ONLY" {
		t.Fatalf("guest YAML path = %q", got)
	}
	policy := policyForTarget(guest, reg)
	if policy.Effect != "reconcile" {
		t.Fatalf("guest effect = %q", policy.Effect)
	}
	password, err := resolveConfigTarget("samba_dc.ldap_bind_password", reg)
	if err != nil {
		t.Fatal(err)
	}
	if policyForTarget(password, reg).Effect != "credential_rotate" {
		t.Fatal("LDAP bind password must require credential rotation")
	}
}

func TestStructuredBareExportSetUsesCanonicalEnvPath(t *testing.T) {
	reg, err := loadRegistry(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	target, err := resolveConfigTarget("modules.SAMBA_FS.config.SHARE_ACCESS_MODE", reg)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(target.YAMLPath, "."); got != "env.SHARE_ACCESS_MODE" {
		t.Fatalf("structured bare-export YAML path = %q, want env.SHARE_ACCESS_MODE", got)
	}
	if target.Display != "env.SHARE_ACCESS_MODE" || target.Module != "samba_fs" || target.Parameter != "share_access_mode" {
		t.Fatalf("structured bare-export target = %+v", target)
	}

	workspace := t.TempDir()
	if err := os.MkdirAll(stateDir(workspace), 0o700); err != nil {
		t.Fatal(err)
	}
	configPath := workspaceConfigPath(workspace)
	if err := os.WriteFile(configPath, []byte(`modules:
  samba_fs: {}
global:
  base_domain: nas.test
  email: admin@nas.test
`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := writeManagedConfigState(workspace, "test"); err != nil {
		t.Fatal(err)
	}
	if err := setManagedConfigScalar(workspace, configPath, target.YAMLPath, "all_rw", true, reg); err != nil {
		t.Fatal(err)
	}
	loaded, err := config.Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if got := loaded.BaseEnv()["SHARE_ACCESS_MODE"]; got != "all_rw" {
		t.Fatalf("BaseEnv SHARE_ACCESS_MODE = %q, want all_rw", got)
	}
	if got := loaded.BaseEnv()["SAMBA_FS_SHARE_ACCESS_MODE"]; got != "" {
		t.Fatalf("dead prefixed export was rendered as %q", got)
	}
	settings, err := config.Settings(configPath)
	if err != nil {
		t.Fatal(err)
	}
	entries, err := collectConfigParameters(reg, settings)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.Path == "env.SHARE_ACCESS_MODE" {
			if !entry.Set || entry.Value != "all_rw" {
				t.Fatalf("config list entry = %+v", entry)
			}
			return
		}
	}
	t.Fatal("config list is missing env.SHARE_ACCESS_MODE")
}

func TestBuildMirrorEnvRequiresImageRebuild(t *testing.T) {
	target, err := resolveConfigTarget("env.APT_MIRROR_URL", map[string]Module{})
	if err != nil {
		t.Fatal(err)
	}
	policy := policyForTarget(target, map[string]Module{})
	if policy.Effect != "image_rebuild" || policy.Apply != "apply-with-build" {
		t.Fatalf("APT mirror policy = %+v", policy)
	}
}

func TestOrdinaryStartRejectsImmutableChange(t *testing.T) {
	reg, err := loadRegistry(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yml")
	writeTestConfig := func(domain string) {
		t.Helper()
		content := "modules:\n  traefik: {}\nglobal:\n  base_domain: " + domain + "\n  email: admin@example.com\n"
		if err := os.WriteFile(cfgPath, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	writeTestConfig("nas.example.com")
	if err := saveAppliedConfig(dir, cfgPath); err != nil {
		t.Fatal(err)
	}
	writeTestConfig("new.example.com")
	err = validateOrdinaryStartChanges(dir, cfgPath, reg)
	if err == nil || !strings.Contains(err.Error(), "immutable") {
		t.Fatalf("expected immutable change error, got %v", err)
	}
	settings, err := config.Settings(cfgPath)
	if err != nil || settings["global.base_domain"] != "new.example.com" {
		t.Fatalf("desired config was not retained: %v, %v", settings, err)
	}
}

func TestOrdinaryStartCannotHideRiskyChangeBehindYAMLAlias(t *testing.T) {
	reg, err := loadRegistry(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yml")
	write := func(database string) {
		t.Helper()
		body := `env:
  DB_MODE: &database ` + database + `
modules:
  nextcloud:
    config:
      db_type: *database
global:
  base_domain: nas.example.com
  email: admin@example.com
`
		if err := os.WriteFile(cfgPath, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("postgres")
	if err := saveAppliedConfig(dir, cfgPath); err != nil {
		t.Fatal(err)
	}
	write("mariadb")
	err = validateOrdinaryStartChanges(dir, cfgPath, reg)
	if err == nil || !strings.Contains(err.Error(), "modules.nextcloud.config.db_type (data_migrate") {
		t.Fatalf("aliased data-migration change was not blocked: %v", err)
	}
}

func TestConfigEffectExecutorAndEditability(t *testing.T) {
	for _, test := range []struct {
		effect, executor string
		editable         bool
	}{
		{"hot_reload", "deployment_apply_fallback", true},
		{"reconcile", "deployment_apply_fallback", true},
		{"container_recreate", "deployment_apply_fallback", true},
		{"image_rebuild", "deployment_build_apply", true},
		{"credential_rotate", "credential_lifecycle_command", false},
		{"data_migrate", "migration_command", false},
		{"immutable", "replacement_workflow", false},
	} {
		policy := ChangePolicy{Effect: test.effect, Apply: "declared-operation"}
		if got := effectExecutor(test.effect); got != test.executor {
			t.Errorf("%s executor = %q, want %q", test.effect, got, test.executor)
		}
		editable, _ := configEditability(policy)
		if editable != test.editable {
			t.Errorf("%s editable = %t, want %t", test.effect, editable, test.editable)
		}
	}
}

func TestParamTypeDocumentDistinguishesUnknownFromExplicitString(t *testing.T) {
	tests := []struct {
		name    string
		spec    ParamType
		kind    string
		allowed string
	}{
		{name: "undeclared", spec: ParamType{}, kind: "unknown"},
		{name: "explicit string", spec: ParamType{Kind: "string"}, kind: "string"},
		{name: "enum", spec: ParamType{Kind: "enum", Enum: []string{"one", "two"}}, kind: "enum", allowed: "one,two"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			kind, allowed := paramTypeDocument(test.spec)
			if kind != test.kind || strings.Join(allowed, ",") != test.allowed {
				t.Fatalf("document = %q, %v, want %q, %q", kind, allowed, test.kind, test.allowed)
			}
		})
	}
}

func TestTargetParamTypeReadsGlobalSchema(t *testing.T) {
	spec := targetParamType(configTarget{Module: globalModuleName, Parameter: "IPv6"}, nil)
	if !spec.Declared() || spec.Kind != "bool" {
		t.Fatalf("global.ipv6 type = %+v, want declared bool", spec)
	}
}

func TestManagedTypedStringKeepsCLITextInsteadOfYAMLMeaning(t *testing.T) {
	reg := importTestRegistry(t)
	workspace := t.TempDir()
	if err := os.MkdirAll(stateDir(workspace), 0o700); err != nil {
		t.Fatal(err)
	}
	configPath := workspaceConfigPath(workspace)
	if err := os.WriteFile(configPath, []byte(`modules:
  traefik: {}
  samba_dc: {}
  nextcloud: {}
global:
  base_domain: nas.example.com
  email: admin@example.com
`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := writeManagedConfigState(workspace, "test"); err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{"null", "1.0", "[x]", "  keep both sides  "} {
		if err := setManagedConfigScalar(workspace, configPath,
			[]string{"modules", "traefik", "config", "domain_prefix"}, value, true, reg); err != nil {
			t.Fatalf("store %q: %v", value, err)
		}
		settings, err := config.Settings(configPath)
		if err != nil {
			t.Fatal(err)
		}
		if got := settings["modules.traefik.config.domain_prefix"]; got != value {
			t.Fatalf("stored value = %q, want exact CLI text %q", got, value)
		}
	}
	for _, test := range []struct {
		name, value, envKey, want string
		path                      []string
	}{
		{name: "bool", path: []string{"global", "ipv6"}, value: "true", envKey: "IPV6", want: "true"},
		{name: "int", path: []string{"modules", "samba_dc", "config", "user_min_pass_length"}, value: "+08", envKey: "SAMBA_DC_USER_MIN_PASS_LENGTH", want: "+08"},
		{name: "enum", path: []string{"modules", "nextcloud", "config", "db_type"}, value: "mariadb", envKey: "NEXTCLOUD_DB_TYPE", want: "mariadb"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if err := setManagedConfigScalar(workspace, configPath, test.path, test.value, true, reg); err != nil {
				t.Fatal(err)
			}
			cfg, err := config.Load(configPath)
			if err != nil {
				t.Fatal(err)
			}
			if got := cfg.BaseEnv()[test.envKey]; got != test.want {
				t.Fatalf("reloaded %s = %q, want %q", test.envKey, got, test.want)
			}
		})
	}
}

func TestManagedConfigSetRejectsEquivalentRuntimeAddresses(t *testing.T) {
	reg := importTestRegistry(t)
	for _, test := range []struct {
		name, existing, target string
	}{
		{
			name:     "raw env then structured",
			existing: "modules:\n  authentik: {}\nglobal:\n  base_domain: nas.test\n  email: admin@nas.test\nenv:\n  AUTHENTIK_LDAP_ENABLED: true\n",
			target:   "authentik.ldap_enabled",
		},
		{
			name:     "structured then raw env",
			existing: "modules:\n  authentik:\n    config:\n      ldap_enabled: true\nglobal:\n  base_domain: nas.test\n  email: admin@nas.test\n",
			target:   "env.AUTHENTIK_LDAP_ENABLED",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			workspace := t.TempDir()
			if err := os.MkdirAll(stateDir(workspace), 0o700); err != nil {
				t.Fatal(err)
			}
			configPath := workspaceConfigPath(workspace)
			if err := os.WriteFile(configPath, []byte(test.existing), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := writeManagedConfigState(workspace, "test"); err != nil {
				t.Fatal(err)
			}
			before, err := os.ReadFile(configPath)
			if err != nil {
				t.Fatal(err)
			}
			target, err := resolveConfigTarget(test.target, reg)
			if err != nil {
				t.Fatal(err)
			}
			err = setManagedConfigScalar(workspace, configPath, target.YAMLPath, "false", true, reg)
			if err == nil || !strings.Contains(err.Error(), "both resolve to runtime key AUTHENTIK_LDAP_ENABLED") {
				t.Fatalf("collision error = %v", err)
			}
			after, readErr := os.ReadFile(configPath)
			if readErr != nil {
				t.Fatal(readErr)
			}
			if string(after) != string(before) {
				t.Fatalf("rejected config set changed managed config:\n%s", after)
			}
			if err := validateManagedConfig(workspace, configPath); err != nil {
				t.Fatalf("rejected config set changed managed digest: %v", err)
			}
		})
	}
}

func TestManagedConfigSetRejectsUnrelatedSchemaDriftAtomically(t *testing.T) {
	minimum := 1
	for _, test := range []struct {
		name       string
		parameters []string
		types      map[string]ParamType
		staleValue string
		want       string
	}{
		{
			name:       "removed declaration",
			parameters: []string{"target"},
			types:      map[string]ParamType{"target": {Kind: "string"}},
			staleValue: "legacy",
			want:       `has no parameter "stale"`,
		},
		{
			name:       "tightened type constraint",
			parameters: []string{"target", "stale"},
			types: map[string]ParamType{
				"target": {Kind: "string"},
				"stale":  {Kind: "int", Constraints: configschema.Constraints{Minimum: &minimum}},
			},
			staleValue: "0",
			want:       "at least 1",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			reg := map[string]Module{
				"demo": {
					Name: "demo", EnvPrefix: "DEMO",
					Parameters: test.parameters, Types: test.types,
				},
			}
			workspace := t.TempDir()
			if err := os.MkdirAll(stateDir(workspace), 0o700); err != nil {
				t.Fatal(err)
			}
			configPath := workspaceConfigPath(workspace)
			body := "modules:\n  demo:\n    config:\n      target: before\n      stale: " + test.staleValue + "\nglobal:\n  base_domain: nas.test\n  email: admin@nas.test\n"
			if err := os.WriteFile(configPath, []byte(body), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := writeManagedConfigState(workspace, "test"); err != nil {
				t.Fatal(err)
			}
			statePath := managedConfigStatePath(stateDir(workspace))
			beforeConfig, err := os.ReadFile(configPath)
			if err != nil {
				t.Fatal(err)
			}
			beforeState, err := os.ReadFile(statePath)
			if err != nil {
				t.Fatal(err)
			}

			err = setManagedConfigScalar(workspace, configPath,
				[]string{"modules", "demo", "config", "target"}, "after", true, reg)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("schema drift error = %v, want %q", err, test.want)
			}
			afterConfig, readErr := os.ReadFile(configPath)
			if readErr != nil {
				t.Fatal(readErr)
			}
			afterState, readErr := os.ReadFile(statePath)
			if readErr != nil {
				t.Fatal(readErr)
			}
			if string(afterConfig) != string(beforeConfig) || string(afterState) != string(beforeState) {
				t.Fatal("rejected config set changed the managed config or its digest state")
			}
			if err := validateManagedConfig(workspace, configPath); err != nil {
				t.Fatalf("rejected config set invalidated managed state: %v", err)
			}
		})
	}
}

func TestManagedConfigSetSchemaCheckDoesNotRequireResolutionInputsOrLock(t *testing.T) {
	reg := map[string]Module{
		"demo": {
			Name: "demo", EnvPrefix: "DEMO",
			Parameters:    []string{"target", "token"},
			InputRequired: []string{"DEMO_TOKEN"},
			Required:      []string{"DEMO_TOKEN"},
			Types: map[string]ParamType{
				"target": {Kind: "string"},
				"token":  {Kind: "string"},
			},
		},
	}
	workspace := t.TempDir()
	if err := os.MkdirAll(stateDir(workspace), 0o700); err != nil {
		t.Fatal(err)
	}
	configPath := workspaceConfigPath(workspace)
	// Both the Module token and global email are input_required. Config set
	// still has to stage an unrelated valid value for --defer; resolution and
	// its lock belong to the later plan/apply boundary.
	if err := os.WriteFile(configPath, []byte("modules:\n  demo:\n    config:\n      target: before\nglobal:\n  base_domain: nas.test\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := writeManagedConfigState(workspace, "test"); err != nil {
		t.Fatal(err)
	}
	if err := setManagedConfigScalar(workspace, configPath,
		[]string{"modules", "demo", "config", "target"}, "after", true, reg); err != nil {
		t.Fatalf("deferred schema-only set was rejected: %v", err)
	}
	settings, err := config.Settings(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if got := settings["modules.demo.config.target"]; got != "after" {
		t.Fatalf("stored target = %q, want after", got)
	}
	if _, err := os.Stat(projectLockPath(configPath)); !os.IsNotExist(err) {
		t.Fatalf("schema-only config set created or required a resolution lock: %v", err)
	}
}

func TestManagedConfigSetRedactsOrdinarySecretValueAliasesOnSchemaDrift(t *testing.T) {
	reg := map[string]Module{
		"demo": {
			Name: "demo", EnvPrefix: "DEMO",
			Parameters: []string{"target", "source", "stale"},
			Types: map[string]ParamType{
				"target": {Kind: "string"},
				"source": {Kind: "string"},
				"stale":  {Kind: "int"},
			},
		},
	}
	workspace := t.TempDir()
	if err := os.MkdirAll(stateDir(workspace), 0o700); err != nil {
		t.Fatal(err)
	}
	configPath := workspaceConfigPath(workspace)
	const secret = "ultra-private-schema-value"
	body := "modules:\n  demo:\n    config:\n      target: before\n      stale: " + secret + "\nglobal:\n  base_domain: nas.test\n  email: admin@nas.test\nsecrets:\n  DEMO_SOURCE: " + secret + "\n"
	if err := os.WriteFile(configPath, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := writeManagedConfigState(workspace, "test"); err != nil {
		t.Fatal(err)
	}
	beforeConfig, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	beforeState, err := os.ReadFile(managedConfigStatePath(stateDir(workspace)))
	if err != nil {
		t.Fatal(err)
	}

	err = setManagedConfigScalar(workspace, configPath,
		[]string{"modules", "demo", "config", "target"}, "after", true, reg)
	if err == nil || !strings.Contains(err.Error(), "DEMO_STALE") {
		t.Fatalf("secret-alias schema error = %v", err)
	}
	if strings.Contains(err.Error(), secret) {
		t.Fatalf("secret-alias schema error leaked source value: %v", err)
	}
	afterConfig, readErr := os.ReadFile(configPath)
	if readErr != nil {
		t.Fatal(readErr)
	}
	afterState, readErr := os.ReadFile(managedConfigStatePath(stateDir(workspace)))
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(afterConfig) != string(beforeConfig) || string(afterState) != string(beforeState) {
		t.Fatal("rejected secret-alias set changed config or managed digest")
	}
}
