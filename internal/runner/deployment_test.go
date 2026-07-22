package runner

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/whlsxl/anas/internal/compose"
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

func TestRollbackGuardsCaskVersionChanges(t *testing.T) {
	current := &deploymentManifest{Casks: map[string]deploymentCask{
		"db": {Name: "db", Version: "2.0.0", AppVersion: "16"},
	}}
	target := &deploymentManifest{Casks: map[string]deploymentCask{
		"db": {Name: "db", Version: "1.0.0", AppVersion: "15"},
	}}
	got := deploymentRollbackVersionBlockers(current, target)
	if len(got) != 1 || got[0] != "cask db 2.0.0/16 -> 1.0.0/15 (data compatibility unknown)" {
		t.Fatalf("rollback blockers = %v", got)
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

func TestBtrfsSnapshotRestoreKeepsRecoverySubvolume(t *testing.T) {
	base := t.TempDir()
	source := filepath.Join(base, "data")
	if err := os.MkdirAll(source, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "marker"), []byte("before"), 0600); err != nil {
		t.Fatal(err)
	}
	originalCommand := btrfsCommand
	btrfsCommand = func(args ...string) error {
		if len(args) >= 3 && args[0] == "subvolume" && args[1] == "show" {
			_, err := os.Stat(args[2])
			return err
		}
		if len(args) >= 4 && args[0] == "subvolume" && args[1] == "snapshot" {
			sourceIndex := 2
			if args[2] == "-r" {
				sourceIndex = 3
			}
			src, dst := args[sourceIndex], args[sourceIndex+1]
			if err := os.MkdirAll(dst, 0700); err != nil {
				return err
			}
			contents, err := os.ReadFile(filepath.Join(src, "marker"))
			if err != nil {
				return err
			}
			return os.WriteFile(filepath.Join(dst, "marker"), contents, 0600)
		}
		return nil
	}
	t.Cleanup(func() { btrfsCommand = originalCommand })

	current := &deploymentManifest{ID: "old"}
	target := &deploymentManifest{ID: "new", Snapshot: deploymentSnapshotPolicy{
		Backend: "btrfs", Source: source, Root: filepath.Join(base, "snapshots"),
	}}
	snapshotID, err := createDeploymentSnapshot(base, current, target)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "marker"), []byte("after"), 0600); err != nil {
		t.Fatal(err)
	}
	deploymentRoot := filepath.Join(base, "deployments", "new")
	if err := os.MkdirAll(deploymentRoot, 0700); err != nil {
		t.Fatal(err)
	}
	target.APIVersion = deploymentAPIVersion
	if err := writeYAMLAtomic(filepath.Join(deploymentRoot, "deployment.yml"), target, 0600); err != nil {
		t.Fatal(err)
	}
	if err := saveDeploymentState(base, deploymentState{ID: "new", SnapshotID: snapshotID}); err != nil {
		t.Fatal(err)
	}
	if err := restoreDeploymentSnapshot(base, "new", "old"); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(source, "marker"))
	if err != nil || string(got) != "before" {
		t.Fatalf("restored marker = %q, %v", got, err)
	}
	recovery := source + ".rollback-recovery-" + snapshotID
	got, err = os.ReadFile(filepath.Join(recovery, "marker"))
	if err != nil || string(got) != "after" {
		t.Fatalf("recovery marker = %q, %v", got, err)
	}
}
