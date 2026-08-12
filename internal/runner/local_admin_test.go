package runner

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/anas-project/ANAS/internal/config"
)

func localAdminTestApp(t *testing.T, base string, template string) *app {
	t.Helper()
	secrets := &secretStore{path: filepath.Join(base, "secrets.generated.yml"), values: map[string]string{}}
	return &app{
		base: base,
		cfg: &config.File{Administration: config.Administration{LocalAccounts: config.LocalAccountDefaults{
			UsernameTemplate: template, PasswordPolicy: "generated_per_cask", PasswordLength: 24,
		}}},
		reg: map[string]Module{"ddns_go": {
			Name: "ddns_go", EnvPrefix: "DDNS_GO",
			ManagementSurfaces: []ManagementSurface{{ID: "web", URIFrom: "DDNS_GO_DOMAIN_FULL", Authentication: "local"}},
			LocalAccounts:      []LocalAccount{{ID: "primary", Purpose: "primary", UsernameTemplate: "global", PasswordPolicy: "generated_per_cask", ContainerFormat: "bcrypt"}},
		}},
		order: []string{"ddns_go"}, env: map[string]string{}, envOwner: map[string]string{}, secrets: secrets,
	}
}

func TestLocalAdministratorIsGeneratedAndPlaintextStaysOutOfDeploymentEnv(t *testing.T) {
	base := t.TempDir()
	a := localAdminTestApp(t, base, "admin_{cask}")
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
	first := localAdminTestApp(t, base, "admin_{cask}")
	if err := first.materializeLocalAccounts(); err != nil {
		t.Fatal(err)
	}
	if err := first.localAdmins.Save(); err != nil {
		t.Fatal(err)
	}

	second := localAdminTestApp(t, base, "operator_{cask}")
	if err := second.materializeLocalAccounts(); err != nil {
		t.Fatal(err)
	}
	if got := second.env["DDNS_GO_LOCAL_ADMIN_USERNAME"]; got != "admin_ddns_go" {
		t.Fatalf("locked username changed to %q", got)
	}
}

func TestLocalAdministratorUsernameMayBeOverriddenPerCask(t *testing.T) {
	a := localAdminTestApp(t, t.TempDir(), "admin_{cask}")
	a.cfg.Services = map[string]config.Service{"ddns_go": {Administration: config.ServiceAdministration{
		LocalAccounts: map[string]config.LocalAccountOverride{"primary": {Username: "dns_operator"}},
	}}}
	if err := a.materializeLocalAccounts(); err != nil {
		t.Fatal(err)
	}
	if got := a.env["DDNS_GO_LOCAL_ADMIN_USERNAME"]; got != "dns_operator" {
		t.Fatalf("username = %q", got)
	}
}

func TestSelectLocalAdministratorRequiresIDWhenAmbiguous(t *testing.T) {
	records := []localAdminRecord{{Cask: "app", ID: "primary"}, {Cask: "app", ID: "recovery"}}
	if _, err := selectLocalAdmin(records, "app", ""); err == nil {
		t.Fatal("ambiguous local account selection succeeded")
	}
	got, err := selectLocalAdmin(records, "app", "recovery")
	if err != nil || got.ID != "recovery" {
		t.Fatalf("selection = %+v, %v", got, err)
	}
}

func TestLocalAdministratorDocumentUsesActiveDeploymentURL(t *testing.T) {
	base := t.TempDir()
	deploymentID := "d1"
	if err := saveActiveState(base, &activeDeploymentState{ActiveDeployment: deploymentID}); err != nil {
		t.Fatal(err)
	}
	envPath := filepath.Join(base, "deployments", deploymentID, "casks", "ddns_go", ".env")
	if err := os.MkdirAll(filepath.Dir(envPath), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(envPath, []byte("DDNS_GO_DOMAIN_FULL=https://ddns.example.test:443\n"), 0600); err != nil {
		t.Fatal(err)
	}
	record := localAdminRecord{Cask: "ddns_go", ID: "primary", URIFrom: "DDNS_GO_DOMAIN_FULL"}
	if got := activeLocalAdminURL(base, record); got != "https://ddns.example.test:443" {
		t.Fatalf("active local administrator URL = %q", got)
	}
}
