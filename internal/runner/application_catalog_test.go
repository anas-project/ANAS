package runner

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/anas-project/ANAS/internal/appcatalog"
	"github.com/anas-project/ANAS/internal/config"
)

func catalogDeclaration() appcatalog.Declaration {
	return appcatalog.Declaration{
		Metadata: appcatalog.Metadata{Name: "cloud", DisplayName: appcatalog.Text{"en": "Cloud", "zh-CN": "云盘"}, Summary: appcatalog.Text{"en": "Files", "zh-CN": "文件"}, Description: appcatalog.Text{"en": "Shared files", "zh-CN": "共享文件"}, DocumentationURL: appcatalog.Text{"en": "https://docs.example/", "zh-CN": "https://docs.example/zh/"}, Kind: "user", Categories: []string{"files", "collaboration"}},
		URLFrom:  "CLOUD_URL", Access: appcatalog.Access{Mode: "iam", Client: "cloud"}, Visibility: appcatalog.Visibility{Mode: "access"},
	}
}

func TestCatalogResolvesExecutionFactsAndRejectsUnknownSubjects(t *testing.T) {
	mod := Module{Name: "cloud", EnvPrefix: "CLOUD"}
	entry := catalogDeclaration()
	a := &app{cfg: &config.File{}, reg: map[string]Module{"cloud": mod}, iamBindings: map[string]string{"cloud": "oidc"}, env: map[string]string{"CLOUD_URL": "https://cloud.example/?a=b#c", iamClientKey("cloud", "ALLOW_GROUPS"): "APP_cloud,APP_all"}}
	known := map[string]bool{"APP_cloud": true, "APP_all": true, "Admins": true}
	roles := map[string]string{"platform_admin": "Admins"}
	input, err := a.resolveApplicationInput(mod, entry, "", false, known, roles)
	if err != nil || strings.Join(input.Application.Groups, ",") != "APP_all,APP_cloud" {
		t.Fatalf("%+v: %v", input, err)
	}
	a.env[iamClientKey("cloud", "ALLOW_GROUPS")] = "unknown"
	if _, err := a.resolveApplicationInput(mod, entry, "", false, known, roles); err == nil {
		t.Fatal("unknown IAM group accepted")
	}
	a.env[iamClientKey("cloud", "ALLOW_GROUPS")] = ""
	if _, err := a.resolveApplicationInput(mod, entry, "", false, known, roles); err == nil {
		t.Fatal("empty IAM allow list accepted")
	}
	entry.Kind = "admin"
	entry.Access = appcatalog.Access{Mode: "local"}
	entry.Visibility = appcatalog.Visibility{Mode: "subjects", AnyOf: []appcatalog.Subject{{Role: "platform_admin"}}}
	if in, err := a.resolveApplicationInput(mod, entry, "", false, known, roles); err != nil || strings.Join(in.Application.Groups, ",") != "Admins" {
		t.Fatalf("%v %v", in, err)
	}
	entry.Visibility.AnyOf = []appcatalog.Subject{{Group: "APP_all"}}
	if _, err := a.resolveApplicationInput(mod, entry, "", false, known, roles); err == nil {
		t.Fatal("user group accepted for admin")
	}
}

func TestCatalogAdminerBindingAndDisabledService(t *testing.T) {
	entry := catalogDeclaration()
	entry.Name = "db-adminer"
	entry.Kind = "admin"
	entry.URLFrom = "DB_URL"
	entry.EnabledBy = "adminer_enabled"
	entry.Access = appcatalog.Access{Mode: "forward_auth", Role: "platform_admin"}
	entry.Visibility = appcatalog.Visibility{Mode: "access"}
	mod := Module{Name: "db", EnvPrefix: "DB"}
	a := &app{cfg: &config.File{}, reg: map[string]Module{"db": mod}, env: map[string]string{"DB_URL": "https://db.example/", "DB_ADMINER_ENABLED": "true", "ANAS_FORWARD_AUTH_PROVIDER": "gate", "ANAS_FORWARD_AUTH_MIDDLEWARE": "admin-gate", "ANAS_PROXY_PLATFORM_ADMIN_GROUP": "Admins"}, envOwner: map[string]string{"ANAS_FORWARD_AUTH_MIDDLEWARE": "gate", "ANAS_PROXY_PLATFORM_ADMIN_GROUP": "gate"}, resolvedBindings: map[string]map[string]string{"db": {"forward_auth": "gate"}}}
	known := map[string]bool{"Admins": true}
	roles := map[string]string{"platform_admin": "Admins"}
	if _, err := a.resolveApplicationInput(mod, entry, "", false, known, roles); err != nil {
		t.Fatal(err)
	}
	a.env["ANAS_PROXY_PLATFORM_ADMIN_GROUP"] = "APP_all"
	if _, err := a.resolveApplicationInput(mod, entry, "", false, known, roles); err == nil {
		t.Fatal("gate role mismatch accepted")
	}
	a.env["DB_ADMINER_ENABLED"] = "false"
	delete(a.resolvedBindings, "db")
	input, err := a.resolveApplicationInput(mod, entry, "", false, known, roles)
	if err != nil || input.Application.Enabled {
		t.Fatalf("%+v %v", input, err)
	}
}

func TestCatalogOnlyDeclaredConsumersGetFinalPaths(t *testing.T) {
	a := &app{artifactRoot: "/workspace/deployments/final/modules", applicationCatalog: &appcatalog.Catalog{}, applicationCatalogDigest: "sha256:test"}
	env := map[string]string{}
	a.catalogRenderEnvironment(Module{}, "/workspace/staging/modules", env)
	if len(env) != 0 {
		t.Fatal("undeclared module got catalog")
	}
	a.catalogRenderEnvironment(Module{ApplicationCatalog: appcatalog.Consumer{Consumes: true}}, "/workspace/staging/modules", env)
	if env["ANAS_APP_CATALOG_DIR"] != "/workspace/deployments/final/shared/application-catalog" || env["ANAS_APP_CATALOG_FILE"] != "/workspace/staging/shared/application-catalog/catalog.json" {
		t.Fatal(env)
	}
}

func TestCatalogMountRejectsPublicMetadataAndWritableVolumes(t *testing.T) {
	for _, tc := range []struct {
		volume string
		valid  bool
	}{
		{"${ANAS_APP_CATALOG_DIR}:/run/anas/application-catalog:ro", true},
		{"${ANAS_APP_CATALOG_DIR}:/files/catalog:ro", false},
		{"${ANAS_APP_CATALOG_DIR}:/run/anas/application-catalog:rw", false},
		{"${ANAS_APP_CATALOG_DIR}/assets:/files/icons:ro", false}, // no private mount
	} {
		path := filepath.Join(t.TempDir(), "compose.yml")
		if err := os.WriteFile(path, []byte("services:\n  iam:\n    volumes:\n      - '"+tc.volume+"'\n"), 0600); err != nil {
			t.Fatal(err)
		}
		if err := validateCatalogComposeMount(path); (err == nil) != tc.valid {
			t.Fatalf("%s: %v", tc.volume, err)
		}
	}
}

func TestBundledApplicationsHaveEntriesOrReasons(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	reg, err := loadRegistry(root)
	if err != nil {
		t.Fatal(err)
	}
	for name, mod := range reg {
		if len(mod.Applications) == 0 && mod.ApplicationsNotPublished == "" {
			t.Errorf("%s lacks catalog inventory", name)
		}
	}
	if _, exists := reg["authentik"]; exists {
		t.Fatal("removed Authentik module is still registered")
	}
	for _, name := range []string{"postgres", "mariadb"} {
		app := reg[name].Applications[0]
		if app.EnabledBy != "adminer_enabled" || app.Access.Mode != "forward_auth" || app.Access.Role != "platform_admin" {
			t.Fatal(app)
		}
	}
}

func TestRemovedIAMProviderCannotBeSelected(t *testing.T) {
	reg, err := loadRegistry(repoRoot(t))
	if err != nil {
		t.Fatal(err)
	}
	a := newIAMApp(reg, iamConfig([]string{"nextcloud"}, "authentik", "", nil))
	if _, err := a.resolveOrder(a.cfg.Modules.Order); err == nil || !strings.Contains(err.Error(), `iam.provider "authentik"`) {
		t.Fatalf("removed provider selection = %v, want explicit rejection", err)
	}
}

func TestCatalogRejectsRemoteDockerAndOnlyChangesConsumers(t *testing.T) {
	t.Setenv("DOCKER_HOST", "ssh://test-host")
	t.Setenv("DOCKER_CONTEXT", "")
	a := &app{order: []string{"iam"}, reg: map[string]Module{"iam": {ApplicationCatalog: appcatalog.Consumer{Consumes: true}}}}
	if err := a.validateCatalogEndpoint(); err == nil || !strings.Contains(err.Error(), "remote projection") {
		t.Fatal(err)
	}
	first := t.TempDir()
	second := t.TempDir()
	for _, root := range []string{first, second} {
		if err := os.MkdirAll(filepath.Join(root, "iam"), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Join(root, "app"), 0755); err != nil {
			t.Fatal(err)
		}
	}
	write := func(root, digest string) {
		if err := os.WriteFile(filepath.Join(root, "iam", ".env"), []byte("ANAS_APP_CATALOG_DIR="+root+"/shared/application-catalog\nANAS_APP_CATALOG_DIGEST="+digest+"\n"), 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, "app", ".env"), []byte("APP_URL=https://app.example/\n"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	write(first, "sha256:first")
	write(second, "sha256:changed-icon")
	one, _ := normalizedModuleDigest(filepath.Join(first, "iam"), first)
	two, _ := normalizedModuleDigest(filepath.Join(second, "iam"), second)
	otherOne, _ := normalizedModuleDigest(filepath.Join(first, "app"), first)
	otherTwo, _ := normalizedModuleDigest(filepath.Join(second, "app"), second)
	if one == two || otherOne != otherTwo {
		t.Fatal("catalog update escaped consumer scope")
	}
}
