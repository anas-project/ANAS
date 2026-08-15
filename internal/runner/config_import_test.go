package runner

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
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
	if !strings.Contains(string(normalized), "cloudflare_dns_api_token: Cloudflare-Imported-Token") {
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
		body := "modules:\n  nextcloud:\n    administration:\n      local_accounts:\n        break_glass:\n          password: " + password + "\nglobal:\n  timezone: UTC\n"
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
	if err := os.WriteFile(source, []byte("modules:\n  lam: {}\nglobal:\n  timezone: UTC\nsecrets:\n  dns_token: retained\n"), 0600); err != nil {
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
