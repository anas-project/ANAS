package runner

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/anas-project/ANAS/internal/compose"
)

func TestNormalizedModuleDigestIgnoresBareDeploymentID(t *testing.T) {
	base := t.TempDir()
	digests := make([]string, 0, 2)
	for _, id := range []string{"20260819T010203Z-aaaaaaaa", "20260819T040506Z-bbbbbbbb"} {
		root := filepath.Join(base, id, "modules", "samba_dc")
		if err := os.MkdirAll(root, 0700); err != nil {
			t.Fatal(err)
		}
		body := "ANAS_DEPLOYMENT_ID=" + id + "\nSAMBA_DC_DOMAIN=example.test\n"
		if err := os.WriteFile(filepath.Join(root, ".env"), []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
		digest, err := normalizedModuleDigest(root, filepath.Join(base, "deployments", id))
		if err != nil {
			t.Fatal(err)
		}
		digests = append(digests, digest)
	}
	if digests[0] != digests[1] {
		t.Fatalf("deployment identity changed normalized module digest: %v", digests)
	}
	changedID := "20260819T070809Z-cccccccc"
	changedRoot := filepath.Join(base, changedID, "modules", "samba_dc")
	if err := os.MkdirAll(changedRoot, 0700); err != nil {
		t.Fatal(err)
	}
	changedBody := "ANAS_DEPLOYMENT_ID=" + changedID + "\nSAMBA_DC_DOMAIN=other.example.test\n"
	if err := os.WriteFile(filepath.Join(changedRoot, ".env"), []byte(changedBody), 0600); err != nil {
		t.Fatal(err)
	}
	changedDigest, err := normalizedModuleDigest(changedRoot, filepath.Join(base, "deployments", changedID))
	if err != nil {
		t.Fatal(err)
	}
	if changedDigest == digests[0] {
		t.Fatal("normalization hid a real rendered configuration change")
	}
}

func TestDeploymentChangeBlockers(t *testing.T) {
	current := &deploymentManifest{Settings: map[string]deploymentSetting{
		"db.password": {Fingerprint: "old", Effect: "credential_rotate", Apply: "restart"},
		"app.theme":   {Fingerprint: "old", Effect: "runtime", Apply: "restart"},
	}}
	target := &deploymentManifest{Settings: map[string]deploymentSetting{
		"db.password": {Fingerprint: "new", Effect: "credential_rotate", Apply: "restart"},
		"app.theme":   {Fingerprint: "new", Effect: "runtime", Apply: "restart"},
	}}
	want := []string{"db.password (credential_rotate; restart)"}
	if got := deploymentChangeBlockers(current, target); !reflect.DeepEqual(got, want) {
		t.Fatalf("blockers = %v, want %v", got, want)
	}
}

// An undeclared module keeps the pre-contract behaviour: any version difference
// is unknown, and unknown is blocked.
func TestRollbackGuardsUndeclaredModuleVersionChanges(t *testing.T) {
	current := &deploymentManifest{Modules: map[string]deploymentModule{
		"db": {Name: "db", Version: "2.0.0", AppVersion: "16"},
	}}
	target := &deploymentManifest{Modules: map[string]deploymentModule{
		"db": {Name: "db", Version: "1.0.0", AppVersion: "15"},
	}}
	guard := deploymentRollbackVersionGuard(current, target)
	want := "module db 2.0.0/16 -> 1.0.0/15 (data compatibility unknown; the module does not declare upgrade.data_breaking)"
	if len(guard.Blocked) != 1 || guard.Blocked[0] != want {
		t.Fatalf("rollback blockers = %v", guard.Blocked)
	}
	if err := guard.breakingError(); err != nil {
		t.Fatalf("an undeclared version change must be overridable, got a hard error: %v", err)
	}
}

func TestLoadDeploymentAppNeedsNoModuleSourceBundle(t *testing.T) {
	base := t.TempDir()
	id := "deployment-a"
	root := filepath.Join(base, "deployments", id)
	if err := os.MkdirAll(filepath.Join(root, "modules", "core"), 0700); err != nil {
		t.Fatal(err)
	}
	manifest := &deploymentManifest{
		APIVersion: deploymentAPIVersion, ID: id, ModuleOrder: []string{"core"},
		Modules: map[string]deploymentModule{"core": {Name: "core", RuntimeType: "builtin"}},
	}
	if err := writeYAMLAtomic(filepath.Join(root, "deployment.yml"), manifest, 0600); err != nil {
		t.Fatal(err)
	}
	app, modulesRoot, got, err := loadDeploymentApp(base, id, compose.CLI{})
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != id || app.order[0] != "core" || modulesRoot != filepath.Join(root, "modules") {
		t.Fatalf("deployment was not reconstructed from its frozen manifest")
	}
}

func TestDeploymentIDRejectsPathTraversal(t *testing.T) {
	for _, id := range []string{"../outside", "a/b", `a\\b`, "..", ""} {
		if err := validateDeploymentID(id); err == nil {
			t.Fatalf("deployment id %q was accepted", id)
		}
	}
}

func TestChangedOrAddedModulesSkipsIdenticalRenderedModules(t *testing.T) {
	current := &deploymentManifest{
		ModuleOrder: []string{"postgres", "authentik"},
		Modules: map[string]deploymentModule{
			"postgres":  {Name: "postgres", RenderDigest: "sha256:postgres"},
			"authentik": {Name: "authentik", RenderDigest: "sha256:auth-old"},
		},
	}
	target := &deploymentManifest{
		ModuleOrder: []string{"postgres", "authentik", "nextcloud"},
		Modules: map[string]deploymentModule{
			"postgres":  {Name: "postgres", RenderDigest: "sha256:postgres"},
			"authentik": {Name: "authentik", RenderDigest: "sha256:auth-new"},
			"nextcloud": {Name: "nextcloud", RenderDigest: "sha256:nextcloud"},
		},
	}
	want := []string{"authentik", "nextcloud"}
	if got := changedOrAddedModules(current, target); !reflect.DeepEqual(got, want) {
		t.Fatalf("activation selection = %v, want %v", got, want)
	}
}

func TestChangedOrRemovedModulesSelectsOldArtifactsInDependencyOrder(t *testing.T) {
	current := &deploymentManifest{
		ModuleOrder: []string{"postgres", "authentik", "legacy"},
		Modules: map[string]deploymentModule{
			"postgres":  {Name: "postgres", RenderDigest: "sha256:postgres"},
			"authentik": {Name: "authentik", RenderDigest: "sha256:auth-old"},
			"legacy":    {Name: "legacy", RenderDigest: "sha256:legacy"},
		},
	}
	target := &deploymentManifest{
		ModuleOrder: []string{"postgres", "authentik", "nextcloud"},
		Modules: map[string]deploymentModule{
			"postgres":  {Name: "postgres", RenderDigest: "sha256:postgres"},
			"authentik": {Name: "authentik", RenderDigest: "sha256:auth-new"},
			"nextcloud": {Name: "nextcloud", RenderDigest: "sha256:nextcloud"},
		},
	}
	want := []string{"authentik", "legacy"}
	if got := changedOrRemovedModules(current, target); !reflect.DeepEqual(got, want) {
		t.Fatalf("deactivation selection = %v, want %v", got, want)
	}
}

func TestActivationStartModulesSkipsRunningUnchangedPrerequisites(t *testing.T) {
	current := &deploymentManifest{
		ModuleOrder: []string{"lego", "traefik", "postgres"},
		Modules: map[string]deploymentModule{
			"lego":     {Name: "lego", RenderDigest: "sha256:lego"},
			"traefik":  {Name: "traefik", RenderDigest: "sha256:traefik"},
			"postgres": {Name: "postgres", RenderDigest: "sha256:postgres"},
		},
	}
	target := &deploymentManifest{
		ModuleOrder: []string{"lego", "traefik", "postgres", "nextcloud"},
		Modules: map[string]deploymentModule{
			"lego":      {Name: "lego", RenderDigest: "sha256:lego"},
			"traefik":   {Name: "traefik", RenderDigest: "sha256:traefik"},
			"postgres":  {Name: "postgres", RenderDigest: "sha256:postgres"},
			"nextcloud": {Name: "nextcloud", RenderDigest: "sha256:nextcloud"},
		},
	}
	want := []string{"nextcloud"}
	got := activationStartModules(current, target, "running")
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("activation start selection = %v, want %v", got, want)
	}
}

func TestActivationStartModulesRestoresWholeStoppedDeployment(t *testing.T) {
	current := &deploymentManifest{
		ModuleOrder: []string{"postgres", "mariadb"},
		Modules: map[string]deploymentModule{
			"postgres": {Name: "postgres", RenderDigest: "sha256:postgres"},
			"mariadb":  {Name: "mariadb", RenderDigest: "sha256:mariadb"},
		},
	}
	target := &deploymentManifest{
		ModuleOrder: []string{"postgres", "mariadb", "nextcloud"},
		Modules: map[string]deploymentModule{
			"postgres":  {Name: "postgres", RenderDigest: "sha256:postgres"},
			"mariadb":   {Name: "mariadb", RenderDigest: "sha256:mariadb"},
			"nextcloud": {Name: "nextcloud", RenderDigest: "sha256:nextcloud"},
		},
	}
	got := activationStartModules(current, target, "stopped")
	if !reflect.DeepEqual(got, target.ModuleOrder) {
		t.Fatalf("stopped deployment activation = %v, want %v", got, target.ModuleOrder)
	}
}
