package runner

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/anas-project/ANAS/internal/compose"
)

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

// An undeclared cask keeps the pre-contract behaviour: any version difference
// is unknown, and unknown is blocked.
func TestRollbackGuardsUndeclaredCaskVersionChanges(t *testing.T) {
	current := &deploymentManifest{Casks: map[string]deploymentCask{
		"db": {Name: "db", Version: "2.0.0", AppVersion: "16"},
	}}
	target := &deploymentManifest{Casks: map[string]deploymentCask{
		"db": {Name: "db", Version: "1.0.0", AppVersion: "15"},
	}}
	guard := deploymentRollbackVersionGuard(current, target)
	want := "cask db 2.0.0/16 -> 1.0.0/15 (data compatibility unknown; the cask does not declare upgrade.data_breaking)"
	if len(guard.Blocked) != 1 || guard.Blocked[0] != want {
		t.Fatalf("rollback blockers = %v", guard.Blocked)
	}
	if err := guard.breakingError(); err != nil {
		t.Fatalf("an undeclared version change must be overridable, got a hard error: %v", err)
	}
}

func TestLoadDeploymentAppNeedsNoCaskSourceBundle(t *testing.T) {
	base := t.TempDir()
	id := "deployment-a"
	root := filepath.Join(base, "deployments", id)
	if err := os.MkdirAll(filepath.Join(root, "casks", "core"), 0700); err != nil {
		t.Fatal(err)
	}
	manifest := &deploymentManifest{
		APIVersion: deploymentAPIVersion, ID: id, ModuleOrder: []string{"core"},
		Casks: map[string]deploymentCask{"core": {Name: "core", RuntimeType: "builtin"}},
	}
	if err := writeYAMLAtomic(filepath.Join(root, "deployment.yml"), manifest, 0600); err != nil {
		t.Fatal(err)
	}
	app, casksRoot, got, err := loadDeploymentApp(base, id, compose.CLI{})
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != id || app.order[0] != "core" || casksRoot != filepath.Join(root, "casks") {
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
