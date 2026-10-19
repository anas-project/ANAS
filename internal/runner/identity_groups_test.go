package runner

// TEST_CASES: GROUPCAT-T-001

import (
	"github.com/anas-project/ANAS/internal/config"
	"github.com/anas-project/ANAS/internal/groupcatalog"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestIdentityCatalogDerivedNamesAndReadOnlyScope(t *testing.T) {
	d := groupcatalog.Declaration{ID: "role", Name: "Admins", ParentDNFrom: "DIRECTORY_GROUPS", Managed: true, Category: "roles", Risk: "normal", DisplayName: groupcatalog.Text{"en": "Admins"}, Description: groupcatalog.Text{"en": "Platform administration"}}
	cfg := &config.File{}
	cfg.Identity.Directory.Provider = "directory"
	a := &app{cfg: cfg, order: []string{"directory", "cloud"}, reg: map[string]Module{"directory": {IdentityGroups: []groupcatalog.Declaration{d}, IdentityGroupsFrom: "DIRECTORY_GROUP_NAMES"}, "cloud": {Name: "cloud", Title: "Cloud"}}, env: map[string]string{"DIRECTORY_GROUPS": "OU=Role,OU=Groups,DC=example", "SAMBA_DC_APP_FILTER": "true", "SAMBA_DC_BASE_APP_DN": "OU=Apps,OU=Groups,DC=example", envIdentityAppClients: "cloud"}, envOwner: map[string]string{"DIRECTORY_GROUPS": "directory"}}
	if err := a.prepareIdentityGroupCatalog(); err != nil {
		t.Fatal(err)
	}
	if a.env["DIRECTORY_GROUP_NAMES"] != "APP_cloud,Admins" {
		t.Fatal("CSV is not a derived projection")
	}
	if len(a.identityGroupCatalog.Groups) != 2 {
		t.Fatal("application access not derived")
	}
	a.artifactRoot = "/workspace/deployments/final/modules"
	env := map[string]string{}
	a.identityGroupRenderEnvironment(Module{}, "/staging/modules", env)
	if len(env) != 0 {
		t.Fatal("undeclared consumer received a mount")
	}
	a.identityGroupRenderEnvironment(Module{IdentityGroupCatalog: true}, "/staging/modules", env)
	if env["ANAS_IDENTITY_GROUPS_DIR"] != "/workspace/deployments/final/shared/identity-groups" {
		t.Fatal(env)
	}
	a.envOwner["DIRECTORY_GROUPS"] = "other"
	if a.prepareIdentityGroupCatalog() == nil {
		t.Fatal("foreign provider parent accepted")
	}
}
func TestIdentityCatalogMountCannotBeWritableOrPublic(t *testing.T) {
	for _, volume := range []string{"${ANAS_IDENTITY_GROUPS_DIR}:/run/anas/identity-groups:ro", "${ANAS_IDENTITY_GROUPS_DIR}:/run/anas/identity-groups:rw", "${ANAS_IDENTITY_GROUPS_DIR}:/var/www/groups:ro"} {
		path := filepath.Join(t.TempDir(), "compose.yml")
		os.WriteFile(path, []byte("services:\n  app:\n    volumes: [\""+volume+"\"]\n"), 0600)
		err := validateIdentityGroupMount(path)
		if (err == nil) != strings.HasSuffix(volume, ":/run/anas/identity-groups:ro") {
			t.Fatal(volume, err)
		}
	}
}

func TestIdentityCatalogUsesImplicitSambaDirectoryProvider(t *testing.T) {
	d := groupcatalog.Declaration{ID: "role", Name: "Admins", ParentDNFrom: "SAMBA_DC_GROUPS_DN", Managed: true, Category: "roles", Risk: "normal", DisplayName: groupcatalog.Text{"en": "Admins"}, Description: groupcatalog.Text{"en": "Platform administration"}}
	a := &app{cfg: &config.File{}, order: []string{"samba_dc"}, reg: map[string]Module{"samba_dc": {IdentityGroups: []groupcatalog.Declaration{d}, IdentityGroupsFrom: "SAMBA_DC_REGISTERED_GROUPS"}}, env: map[string]string{"SAMBA_DC_GROUPS_DN": "OU=Role,OU=Groups,DC=example"}, envOwner: map[string]string{"SAMBA_DC_GROUPS_DN": "samba_dc"}}
	if err := a.prepareIdentityGroupCatalog(); err != nil {
		t.Fatal("default directory provider rejected", err)
	}
	if a.env["SAMBA_DC_REGISTERED_GROUPS"] != "Admins" {
		t.Fatal("default provider projection unavailable")
	}
}
