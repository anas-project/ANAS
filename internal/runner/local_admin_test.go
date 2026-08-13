package runner

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/anas-project/ANAS/internal/config"
)

func localAdminTestApp(t *testing.T, base string, template string) *app {
	t.Helper()
	secrets := &secretStore{path: filepath.Join(base, "secrets.generated.yml"), values: map[string]string{}}
	return &app{
		base: base,
		cfg: &config.File{Administration: config.Administration{LocalAccounts: config.LocalAccountDefaults{
			UsernameTemplate: template, PasswordPolicy: "generated_per_module", PasswordLength: 24,
		}}},
		reg: map[string]Module{"ddns_go": {
			Name: "ddns_go", EnvPrefix: "DDNS_GO",
			ManagementSurfaces: []ManagementSurface{{ID: "web", URIFrom: "DDNS_GO_DOMAIN_FULL", Authentication: "local"}},
			LocalAccounts:      []LocalAccount{{ID: "primary", Purpose: "primary", UsernameTemplate: "global", PasswordPolicy: "generated_per_module", ContainerFormat: "bcrypt"}},
		}},
		order: []string{"ddns_go"}, env: map[string]string{}, envOwner: map[string]string{}, secrets: secrets,
	}
}

func TestLocalAdministratorIsGeneratedAndPlaintextStaysOutOfDeploymentEnv(t *testing.T) {
	base := t.TempDir()
	a := localAdminTestApp(t, base, "admin_{module}")
	if err := a.materializeLocalAccounts(); err != nil {
		t.Fatal(err)
	}
	if got := a.env["DDNS_GO_LOCAL_ADMIN_USERNAME"]; got != "admin_ddns_go" {
		t.Fatalf("username = %q", got)
	}
	if got := a.env["DDNS_GO_LOCAL_ADMIN_PASSWORD"]; got != "" {
		t.Fatal("local administrator plaintext leaked into the deployment environment")
	}
	hookEnv := a.localAdminHookEnv("ddns_go", a.env)
	if got := hookEnv["DDNS_GO_LOCAL_ADMIN_PASSWORD"]; len(got) != 24 {
		t.Fatalf("hook password length = %d", len(got))
	}
}

func TestLocalAdministratorUsernameIsLockedAcrossTemplateChanges(t *testing.T) {
	base := t.TempDir()
	first := localAdminTestApp(t, base, "admin_{module}")
	if err := first.materializeLocalAccounts(); err != nil {
		t.Fatal(err)
	}
	if err := first.localAdmins.Save(); err != nil {
		t.Fatal(err)
	}

	second := localAdminTestApp(t, base, "operator_{module}")
	if err := second.materializeLocalAccounts(); err != nil {
		t.Fatal(err)
	}
	if got := second.env["DDNS_GO_LOCAL_ADMIN_USERNAME"]; got != "admin_ddns_go" {
		t.Fatalf("locked username changed to %q", got)
	}
}

func TestLocalAdministratorUsernameMayBeOverriddenPerModule(t *testing.T) {
	a := localAdminTestApp(t, t.TempDir(), "admin_{module}")
	a.cfg.Modules.Values = map[string]config.ModuleConfig{"ddns_go": {Administration: config.ModuleAdministration{
		LocalAccounts: map[string]config.LocalAccountOverride{"primary": {Username: "dns_operator"}},
	}}}
	if err := a.materializeLocalAccounts(); err != nil {
		t.Fatal(err)
	}
	if got := a.env["DDNS_GO_LOCAL_ADMIN_USERNAME"]; got != "dns_operator" {
		t.Fatalf("username = %q", got)
	}
}

func TestLockedLocalAdministratorRejectsChangedOverride(t *testing.T) {
	base := t.TempDir()
	first := localAdminTestApp(t, base, "admin_{module}")
	if err := first.materializeLocalAccounts(); err != nil {
		t.Fatal(err)
	}
	if err := first.localAdmins.Save(); err != nil {
		t.Fatal(err)
	}
	second := localAdminTestApp(t, base, "admin_{module}")
	second.cfg.Modules.Values = map[string]config.ModuleConfig{"ddns_go": {Administration: config.ModuleAdministration{LocalAccounts: map[string]config.LocalAccountOverride{"primary": {Username: "silently_ignored"}}}}}
	if err := second.materializeLocalAccounts(); err == nil {
		t.Fatal("changed locked override was silently accepted")
	}
}

func TestPlaintextBootstrapCredentialUsesRuntimeFileNotDeploymentEnv(t *testing.T) {
	base := t.TempDir()
	a := localAdminTestApp(t, base, "admin_{module}")
	mod := a.reg["ddns_go"]
	mod.LocalAccounts[0].ContainerFormat = "plaintext_on_bootstrap"
	a.reg["ddns_go"] = mod
	if err := a.materializeLocalAccounts(); err != nil {
		t.Fatal(err)
	}
	password := a.secrets.values[localAdminSecretKey("ddns_go", "primary")]
	if password == "" {
		t.Fatal("missing generated password")
	}
	if got := a.env["DDNS_GO_LOCAL_ADMIN_PASSWORD"]; got != "" {
		t.Fatal("plaintext entered deployment env")
	}
	path := a.env["DDNS_GO_LOCAL_ADMIN_PASSWORD_FILE"]
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(b)) != password {
		t.Fatal("runtime projection does not contain managed secret")
	}
}

func TestSelectLocalAdministratorRequiresIDWhenAmbiguous(t *testing.T) {
	records := []localAdminRecord{{Module: "app", ID: "recovery"}, {Module: "app", ID: "guard"}}
	if _, err := selectLocalAdmin(records, "app", ""); err == nil {
		t.Fatal("ambiguous local account selection succeeded")
	}
	got, err := selectLocalAdmin(records, "app", "recovery")
	if err != nil || got.ID != "recovery" {
		t.Fatalf("selection = %+v, %v", got, err)
	}
}

func TestSelectLocalAdministratorDefaultsToPrimaryBeforeUniqueness(t *testing.T) {
	records := []localAdminRecord{{Module: "app", ID: "primary"}, {Module: "app", ID: "recovery"}}
	got, err := selectLocalAdmin(records, "app", "")
	if err != nil || got.ID != "primary" {
		t.Fatalf("selection = %+v, %v", got, err)
	}
}

func TestLocalAdministratorDocumentUsesActiveDeploymentURL(t *testing.T) {
	base := t.TempDir()
	deploymentID := "d1"
	if err := saveActiveState(base, &activeDeploymentState{ActiveDeployment: deploymentID}); err != nil {
		t.Fatal(err)
	}
	envPath := filepath.Join(base, "deployments", deploymentID, "modules", "ddns_go", ".env")
	if err := os.MkdirAll(filepath.Dir(envPath), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(envPath, []byte("DDNS_GO_DOMAIN_FULL=https://ddns.example.test:443\n"), 0600); err != nil {
		t.Fatal(err)
	}
	record := localAdminRecord{Module: "ddns_go", ID: "primary", URIFrom: "DDNS_GO_DOMAIN_FULL"}
	if got := activeLocalAdminURL(base, record); got != "https://ddns.example.test:443" {
		t.Fatalf("active local administrator URL = %q", got)
	}
}

func localAdminRotationFixture(t *testing.T, hookBody string) (string, localAdminRecord) {
	t.Helper()
	base, id := t.TempDir(), "d1"
	moduleDir := filepath.Join(base, "deployments", id, "modules", "app")
	if err := os.MkdirAll(moduleDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(moduleDir, ".env"), []byte("CONTAINER_PREFIX=anas_\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(moduleDir, "hook.sh"), []byte(hookBody), 0700); err != nil {
		t.Fatal(err)
	}
	account := LocalAccount{ID: "primary", Purpose: "primary", PasswordPolicy: "generated_per_module", ContainerFormat: "bcrypt", Rotate: "rotate-test"}
	manifest := &deploymentManifest{APIVersion: deploymentAPIVersion, ID: id, ModuleOrder: []string{"app"}, Modules: map[string]deploymentModule{"app": {Name: "app", ArtifactDeployment: id, RuntimeType: "compose", EnvPrefix: "APP", Hook: HookConfig{Command: []string{"sh", "hook.sh"}}, LocalAccounts: []LocalAccount{account}}}}
	if err := writeYAMLAtomic(filepath.Join(base, "deployments", id, "deployment.yml"), manifest, 0600); err != nil {
		t.Fatal(err)
	}
	if err := saveActiveState(base, &activeDeploymentState{ActiveDeployment: id}); err != nil {
		t.Fatal(err)
	}
	record := localAdminRecord{Module: "app", ID: "primary", Purpose: "primary", Username: "admin_app", SecretKey: localAdminSecretKey("app", "primary")}
	state := &localAdminState{APIVersion: localAdminStateVersion, Path: localAdminStatePath(base), Accounts: map[string]localAdminRecord{"app.primary": record}, dirty: true}
	if err := state.Save(); err != nil {
		t.Fatal(err)
	}
	secrets := &secretStore{path: filepath.Join(base, "secrets.generated.yml"), values: map[string]string{record.SecretKey: "old-password"}, dirty: true}
	if err := secrets.Save(); err != nil {
		t.Fatal(err)
	}
	return base, record
}

func TestRotateTransactionCommitsCandidateOnlyAfterHandlerSuccess(t *testing.T) {
	base, record := localAdminRotationFixture(t, "#!/bin/sh\nread request\nprintf '{}'\n")
	if err := rotateLocalAdministrator(base, record, "candidate-password"); err != nil {
		t.Fatal(err)
	}
	secrets, err := loadSecretStore(base)
	if err != nil {
		t.Fatal(err)
	}
	if got := secrets.values[record.SecretKey]; got != "candidate-password" {
		t.Fatalf("secret = %q", got)
	}
}

func TestRotateTransactionKeepsOldSecretWhenHandlerFails(t *testing.T) {
	base, record := localAdminRotationFixture(t, "#!/bin/sh\nread request\nexit 1\n")
	if err := rotateLocalAdministrator(base, record, "candidate-password"); err == nil {
		t.Fatal("rotation unexpectedly succeeded")
	}
	secrets, err := loadSecretStore(base)
	if err != nil {
		t.Fatal(err)
	}
	if got := secrets.values[record.SecretKey]; got != "old-password" {
		t.Fatalf("secret changed to %q", got)
	}
}
