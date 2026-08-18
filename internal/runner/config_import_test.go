package runner

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/anas-project/ANAS/internal/config"
	"github.com/anas-project/ANAS/internal/configschema"
)

func importTestRegistry(t *testing.T) map[string]Module {
	t.Helper()
	reg, err := loadRegistryDir(filepath.Join("..", "..", "modules"))
	if err != nil {
		t.Fatal(err)
	}
	return reg
}

func TestConfigImportPersistsCNSourceRuntimeDefault(t *testing.T) {
	source := filepath.Join(t.TempDir(), "external.yml")
	if err := os.WriteFile(source, []byte(`module_source: cn
modules:
  traefik: {}
global:
  base_domain: nas.test
`), 0600); err != nil {
		t.Fatal(err)
	}
	result, err := normalizeImportedConfig(source, importTestRegistry(t))
	if err != nil {
		t.Fatal(err)
	}
	body := string(result.Normalized)
	if !strings.Contains(body, "chinese_speedup: true") {
		t.Fatalf("normalized config did not persist CN runtime default:\n%s", body)
	}
	if !strings.Contains(body, "module_source: official-cn") {
		t.Fatalf("normalized config did not canonicalize CN source:\n%s", body)
	}

	if err := os.WriteFile(source, []byte(`module_source: official-cn
modules:
  traefik: {}
global:
  chinese_speedup: false
`), 0600); err != nil {
		t.Fatal(err)
	}
	result, err = normalizeImportedConfig(source, importTestRegistry(t))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(result.Normalized), "chinese_speedup: true") {
		t.Fatalf("explicit opt-out was replaced:\n%s", result.Normalized)
	}

	if err := os.WriteFile(source, []byte(`module_source: cn
modules:
  traefik: {}
global:
  chinese_speedup: null
`), 0600); err != nil {
		t.Fatal(err)
	}
	result, err = normalizeImportedConfig(source, importTestRegistry(t))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(result.Normalized), "chinese_speedup: true") {
		t.Fatalf("null CN runtime default was not persisted:\n%s", result.Normalized)
	}
}

func TestConfigImportUsesInstalledCNSourceWhenOmitted(t *testing.T) {
	preference := filepath.Join(t.TempDir(), "source")
	if err := os.WriteFile(preference, []byte("official-cn\n"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ANAS_SOURCE_CONFIG", preference)
	source := filepath.Join(t.TempDir(), "external.yml")
	if err := os.WriteFile(source, []byte("modules:\n  traefik: {}\nglobal:\n  timezone: UTC\n"), 0600); err != nil {
		t.Fatal(err)
	}
	result, err := normalizeImportedConfig(source, importTestRegistry(t))
	if err != nil {
		t.Fatal(err)
	}
	body := string(result.Normalized)
	if !strings.Contains(body, "module_source: official-cn") || !strings.Contains(body, "chinese_speedup: true") {
		t.Fatalf("installer source was not persisted during import:\n%s", body)
	}
}

func TestConfigImportExtractsOnlyLifecycleManagedSecrets(t *testing.T) {
	workspace := t.TempDir()
	if err := os.MkdirAll(stateDir(workspace), 0700); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(t.TempDir(), "external.yml")
	raw := `modules:
  samba_dc:
    config:
      admin_password: Directory-Admin-Initial-1!
  nextcloud:
    administration:
      local_accounts:
        break_glass:
          password: Nextcloud-Recovery-Initial-1!
global:
  base_domain: nas.test
  email: admin@nas.test
  timezone: Asia/Singapore
  virtual_domain: true
secrets:
  cloudflare_dns_api_token: Cloudflare-Imported-Token
`
	if err := os.WriteFile(source, []byte(raw), 0600); err != nil {
		t.Fatal(err)
	}

	result, err := importConfigIntoWorkspace(workspace, source, importTestRegistry(t))
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Secrets) != 2 {
		t.Fatalf("imported secrets = %d, want 2", len(result.Secrets))
	}
	sourceAfter, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	if string(sourceAfter) != raw {
		t.Fatal("external source config was modified")
	}
	normalized, err := os.ReadFile(workspaceConfigPath(workspace))
	if err != nil {
		t.Fatal(err)
	}
	for _, plaintext := range []string{"Directory-Admin-Initial-1!", "Nextcloud-Recovery-Initial-1!", "admin_password:", "password:"} {
		if strings.Contains(string(normalized), plaintext) {
			t.Errorf("normalized config retains sensitive input %q", plaintext)
		}
	}
	if !strings.Contains(string(normalized), "CLOUDFLARE_DNS_API_TOKEN: Cloudflare-Imported-Token") {
		t.Fatal("normalized config must retain ordinary deployment secrets")
	}
	if info, err := os.Stat(workspaceConfigPath(workspace)); err != nil {
		t.Fatal(err)
	} else if info.Mode().Perm() != 0600 {
		t.Fatalf("managed config mode = %v, want 0600", info.Mode().Perm())
	}

	store, err := loadSecretStore(stateDir(workspace))
	if err != nil {
		t.Fatal(err)
	}
	if got := store.values["SAMBA_DC_ADMIN_PASSWORD"]; got != "Directory-Admin-Initial-1!" {
		t.Fatalf("user-owned Samba secret = %q", got)
	}
	if _, ok := store.values["CLOUDFLARE_DNS_API_TOKEN"]; ok {
		t.Fatal("ordinary deployment secret was incorrectly extracted")
	}
	localKey := localAdminSecretKey("nextcloud", "break_glass")
	if got := store.values[localKey]; got != "Nextcloud-Recovery-Initial-1!" {
		t.Fatalf("managed local administrator secret = %q", got)
	}
	if store.metadata["SAMBA_DC_ADMIN_PASSWORD"].Kind != "lifecycle_managed" || store.metadata[localKey].Kind != "local_admin" {
		t.Fatalf("secret metadata does not distinguish lifecycle and local-admin values: %+v", store.metadata)
	}
	for _, path := range []string{store.path} {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if got := info.Mode().Perm(); got != 0600 {
			t.Errorf("%s mode = %o, want 600", path, got)
		}
	}
}

func TestConfigImportRejectsLocalAdministratorUsernameOverride(t *testing.T) {
	workspace := t.TempDir()
	if err := os.MkdirAll(stateDir(workspace), 0700); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(t.TempDir(), "external.yml")
	if err := os.WriteFile(source, []byte(`modules:
  nextcloud:
    administration:
      local_accounts:
        break_glass:
          username: custom_recovery
          password: Initial-Password-1!
global:
  timezone: UTC
`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := importConfigIntoWorkspace(workspace, source, importTestRegistry(t)); err == nil || !strings.Contains(err.Error(), "username is not configurable") {
		t.Fatalf("import error = %v", err)
	}
	if exists(workspaceConfigPath(workspace)) || exists(filepath.Join(stateDir(workspace), "secrets.yml")) {
		t.Fatal("rejected username override modified the workspace")
	}
}

func TestConfigImportRawEnvCannotBypassDeclaredType(t *testing.T) {
	source := filepath.Join(t.TempDir(), "external.yml")
	if err := os.WriteFile(source, []byte(`modules:
  samba_fs: {}
env:
  SHARE_ACCESS_MODE: not-a-mode
`), 0o600); err != nil {
		t.Fatal(err)
	}
	err := validateConfigImportSource(source, importTestRegistry(t))
	if err == nil || !strings.Contains(err.Error(), "all_read_group_write") {
		t.Fatalf("import error = %v, want declared enum rejection", err)
	}
}

func TestConfigImportCanonicalizesTypedValuesGenerically(t *testing.T) {
	source := filepath.Join(t.TempDir(), "external.yml")
	if err := os.WriteFile(source, []byte(`modules:
  authentik:
    config:
      db_type: POSTGRES
      ldap_enabled: " TRUE "
env:
  SHARE_ACCESS_MODE: ALL_RW
global:
  ipv6: " TRUE "
`), 0o600); err != nil {
		t.Fatal(err)
	}
	result, err := normalizeImportedConfig(source, importTestRegistry(t))
	if err != nil {
		t.Fatal(err)
	}
	body := string(result.Normalized)
	for _, want := range []string{"db_type: postgres", `ldap_enabled: "true"`, "SHARE_ACCESS_MODE: all_rw", `ipv6: "true"`} {
		if !strings.Contains(body, want) {
			t.Errorf("normalized import is missing %q:\n%s", want, body)
		}
	}
}

func TestConfigImportCanonicalizesKeysAndMigratesBareExports(t *testing.T) {
	source := filepath.Join(t.TempDir(), "external.yml")
	if err := os.WriteFile(source, []byte(`modules:
  SAMBA_FS:
    config:
      SHARE_DIR_NAME: Media
global:
  BASE_DOMAIN: nas.test
  EMAIL: admin@nas.test
env:
  custom_flag: enabled
`), 0o600); err != nil {
		t.Fatal(err)
	}
	reg := importTestRegistry(t)
	result, err := normalizeImportedConfig(source, reg)
	if err != nil {
		t.Fatal(err)
	}
	if err := validateNormalizedImportedConfig(result.Normalized, reg); err != nil {
		t.Fatal(err)
	}
	body := string(result.Normalized)
	for _, want := range []string{"samba_fs:", "base_domain: nas.test", "email: admin@nas.test", "SHARE_DIR_NAME: Media", "CUSTOM_FLAG: enabled"} {
		if !strings.Contains(body, want) {
			t.Errorf("normalized import is missing %q:\n%s", want, body)
		}
	}
	if strings.Contains(body, "SAMBA_FS_SHARE_DIR_NAME") {
		t.Fatalf("normalized import retained a dead prefixed bare export:\n%s", body)
	}

	managed := filepath.Join(t.TempDir(), "config.yml")
	if err := os.WriteFile(managed, result.Normalized, 0o600); err != nil {
		t.Fatal(err)
	}
	loaded, err := config.Load(managed)
	if err != nil {
		t.Fatal(err)
	}
	if got := len(loaded.Modules.Values["samba_fs"].Config); got != 0 {
		t.Fatalf("structured bare export remained in module config: %+v", loaded.Modules.Values["samba_fs"].Config)
	}
	env := loaded.BaseEnv()
	if env["SHARE_DIR_NAME"] != "Media" || env["SAMBA_FS_SHARE_DIR_NAME"] != "" || env["CUSTOM_FLAG"] != "enabled" {
		t.Fatalf("BaseEnv after import = %+v", env)
	}
	settings, err := config.Settings(managed)
	if err != nil {
		t.Fatal(err)
	}
	entries, err := collectConfigParameters(reg, settings)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.Path == "env.SHARE_DIR_NAME" {
			if !entry.Set || entry.Value != "Media" {
				t.Fatalf("config list entry = %+v", entry)
			}
			return
		}
	}
	t.Fatal("config list is missing imported env.SHARE_DIR_NAME")
}

func TestConfigImportRejectsCaseFoldedKeyCollisions(t *testing.T) {
	tests := map[string]string{
		"environment": `modules:
  traefik: {}
env:
  custom_key: one
  CUSTOM_KEY: two
`,
		"module": `modules:
  TRAEFIK: {}
  traefik: {}
`,
		"parameter": `modules:
  samba_fs:
    config:
      SHARE_DIR_NAME: one
      share_dir_name: two
`,
		"global parameter": `modules:
  traefik: {}
global:
  EMAIL: one@example.com
  email: two@example.com
`,
		"bare export and environment": `modules:
  samba_fs:
    config:
      share_dir_name: one
env:
  SHARE_DIR_NAME: two
`,
	}
	reg := importTestRegistry(t)
	for name, body := range tests {
		t.Run(name, func(t *testing.T) {
			source := filepath.Join(t.TempDir(), "external.yml")
			if err := os.WriteFile(source, []byte(body), 0o600); err != nil {
				t.Fatal(err)
			}
			_, err := normalizeImportedConfig(source, reg)
			if err == nil || !strings.Contains(err.Error(), "after canonicalization") {
				t.Fatalf("collision error = %v", err)
			}
		})
	}
}

func TestConfigImportRedactsNonCredentialSensitiveConstraintErrors(t *testing.T) {
	minimum := 12
	reg := map[string]Module{
		"demo": {
			Name: "demo", EnvPrefix: "DEMO", Parameters: []string{"token"},
			Types: map[string]ParamType{
				"token": {Kind: "string", Constraints: configschema.Constraints{MinLength: &minimum}},
			},
			Changes: map[string]ChangePolicy{
				"token": {Effect: "container_recreate", Sensitive: true},
			},
		},
	}
	write := func(body string) string {
		path := filepath.Join(t.TempDir(), "external.yml")
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	for name, test := range map[string]struct {
		value string
		body  string
	}{
		"structured":  {value: "guess-me", body: "modules:\n  demo:\n    config:\n      token: guess-me\n"},
		"environment": {value: "GUESS-ME", body: "modules:\n  demo: {}\nenv:\n  DEMO_TOKEN: GUESS-ME\n"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := normalizeImportedConfig(write(test.body), reg)
			if err == nil || strings.Contains(err.Error(), test.value) || !strings.Contains(err.Error(), "does not satisfy its declared string type or constraints") {
				t.Fatalf("sensitive normalization error = %v", err)
			}
		})
	}
	result, err := normalizeImportedConfig(write("modules:\n  demo:\n    config:\n      token: long-enough-secret\n"), reg)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Secrets) != 0 || !strings.Contains(string(result.Normalized), "token: long-enough-secret") {
		t.Fatalf("non-credential sensitive value was extracted instead of retained: secrets=%+v\n%s", result.Secrets, result.Normalized)
	}
}

func TestConfigImportRejectsExplicitNullSecretsWithoutEcho(t *testing.T) {
	tests := map[string]string{
		"credential rotate": `modules:
  samba_dc:
    config:
      admin_password: null
`,
		"managed bootstrap": `modules:
  nextcloud:
    config:
      admin_password: ~
`,
		"local account": `modules:
  nextcloud:
    administration:
      local_accounts:
        break_glass:
          password: null
`,
	}
	reg := importTestRegistry(t)
	for name, body := range tests {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "external.yml")
			if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
				t.Fatal(err)
			}
			_, err := normalizeImportedConfig(path, reg)
			if err == nil || !strings.Contains(err.Error(), "must be a non-empty scalar secret") {
				t.Fatalf("null secret error = %v", err)
			}
			if strings.Contains(strings.ToLower(err.Error()), "null") || strings.Contains(err.Error(), "~") {
				t.Fatalf("null spelling leaked in error: %v", err)
			}
		})
	}
}

func TestConfigImportKeepsTypedEmptyAndScalarAliasValuesCompatible(t *testing.T) {
	source := filepath.Join(t.TempDir(), "external.yml")
	if err := os.WriteFile(source, []byte(`global:
  base_domain: nas.test
  email: admin@nas.test
  ipv6: &enabled " TRUE "
modules:
  authentik:
    config:
      db_type: null
      ldap_enabled: *enabled
env:
  SHARE_ACCESS_MODE: ""
`), 0o600); err != nil {
		t.Fatal(err)
	}
	reg := importTestRegistry(t)
	result, err := normalizeImportedConfig(source, reg)
	if err != nil {
		t.Fatalf("normalizing typed null/alias values: %v", err)
	}
	if err := validateNormalizedImportedConfig(result.Normalized, reg); err != nil {
		t.Fatalf("validating typed null/alias values: %v\n%s", err, result.Normalized)
	}
	body := string(result.Normalized)
	if !strings.Contains(body, `ipv6: &enabled "true"`) {
		t.Fatalf("global anchor was not canonicalized:\n%s", body)
	}
	if !strings.Contains(body, "ldap_enabled: *enabled") {
		t.Fatalf("typed scalar alias was not preserved:\n%s", body)
	}
}

func TestConfigImportFailureDoesNotModifyWorkspace(t *testing.T) {
	workspace := t.TempDir()
	base := stateDir(workspace)
	if err := os.MkdirAll(base, 0700); err != nil {
		t.Fatal(err)
	}
	targets := map[string]string{
		workspaceConfigPath(workspace):     "modules:\n  lam: {}\nglobal:\n  timezone: UTC\n",
		filepath.Join(base, "secrets.yml"): "EXISTING_MANAGED: keep-managed\n",
	}
	for path, value := range targets {
		if err := os.WriteFile(path, []byte(value), 0600); err != nil {
			t.Fatal(err)
		}
	}
	source := filepath.Join(t.TempDir(), "invalid.yml")
	if err := os.WriteFile(source, []byte("modules:\n  lam: {}\nglobal:\n  timezone: Invalid/Zone\nsecrets:\n  token: should-not-land\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := importConfigIntoWorkspace(workspace, source, importTestRegistry(t)); err == nil {
		t.Fatal("invalid import unexpectedly succeeded")
	}
	for path, want := range targets {
		got, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != want {
			t.Errorf("failed import modified %s", path)
		}
	}
}

func TestManagedBootstrapPasswordCannotBeReimportedAsRotation(t *testing.T) {
	workspace := t.TempDir()
	if err := os.MkdirAll(stateDir(workspace), 0700); err != nil {
		t.Fatal(err)
	}
	writeSource := func(password string) string {
		path := filepath.Join(t.TempDir(), "config.yml")
		body := "modules:\n  nextcloud:\n    administration:\n      local_accounts:\n        break_glass:\n          password: " + password + "\nglobal:\n  base_domain: nas.test\n  email: admin@nas.test\n  timezone: UTC\n"
		if err := os.WriteFile(path, []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	reg := importTestRegistry(t)
	if _, err := importConfigIntoWorkspace(workspace, writeSource("Initial-Local-Password-1!"), reg); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(filepath.Join(stateDir(workspace), "secrets.yml"))
	if _, err := importConfigIntoWorkspace(workspace, writeSource("Replacement-Must-Rotate-2!"), reg); err == nil || !strings.Contains(err.Error(), "admin local rotate") {
		t.Fatalf("reimport error = %v", err)
	}
	after, _ := os.ReadFile(filepath.Join(stateDir(workspace), "secrets.yml"))
	if string(after) != string(before) {
		t.Fatal("rejected bootstrap replacement changed the managed secret store")
	}
}

func TestImportedLifecycleSecretsHydrateRuntimeWithoutConfigPlaintext(t *testing.T) {
	workspace := t.TempDir()
	base := stateDir(workspace)
	if err := os.MkdirAll(base, 0700); err != nil {
		t.Fatal(err)
	}
	store := &secretStore{
		path:     filepath.Join(base, "secrets.yml"),
		values:   map[string]string{"SAMBA_DC_ADMIN_PASSWORD": "Imported-Only-1!"},
		metadata: map[string]secretMetadata{"SAMBA_DC_ADMIN_PASSWORD": {Owner: "samba_dc", Kind: "lifecycle_managed", Provenance: "config-import:test"}},
		dirty:    true,
	}
	if err := store.Save(); err != nil {
		t.Fatal(err)
	}
	a := &app{base: base, env: map[string]string{}, envOwner: map[string]string{}}
	if err := a.loadImportedSecrets(); err != nil {
		t.Fatal(err)
	}
	if got := a.env["SAMBA_DC_ADMIN_PASSWORD"]; got != "Imported-Only-1!" {
		t.Fatalf("runtime user secret = %q", got)
	}
	if got := a.envOwner["SAMBA_DC_ADMIN_PASSWORD"]; got != "!user-secret" {
		t.Fatalf("runtime user secret owner = %q", got)
	}
}

func TestManagedConfigRejectsTamperingAndExternalPaths(t *testing.T) {
	workspace := t.TempDir()
	if err := os.MkdirAll(stateDir(workspace), 0700); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(t.TempDir(), "config.yml")
	if err := os.WriteFile(source, []byte("modules:\n  lam: {}\nglobal:\n  base_domain: nas.test\n  email: admin@nas.test\n  timezone: UTC\nsecrets:\n  dns_token: retained\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := importConfigIntoWorkspace(workspace, source, importTestRegistry(t)); err != nil {
		t.Fatal(err)
	}
	if err := validateManagedConfig(workspace, workspaceConfigPath(workspace)); err != nil {
		t.Fatalf("valid managed config rejected: %v", err)
	}
	if err := validateManagedConfig(workspace, source); err == nil || !strings.Contains(err.Error(), "import-only") {
		t.Fatalf("external config error = %v", err)
	}
	if err := os.WriteFile(workspaceConfigPath(workspace), []byte("modules: {}\nglobal:\n  timezone: UTC\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := validateManagedConfig(workspace, workspaceConfigPath(workspace)); err == nil || !strings.Contains(err.Error(), "outside ANAS CLI") {
		t.Fatalf("tamper error = %v", err)
	}
}
