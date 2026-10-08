package runner

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func assertRestoreBlocksStart(t *testing.T, workspace string) {
	t.Helper()
	base := stateDir(workspace)
	active, err := loadActiveState(base)
	if err != nil {
		t.Fatal(err)
	}
	pending, err := postgresMaintenancePending(base, active.ActiveDeployment)
	if err != nil || !strings.HasPrefix(pending, "restore:") {
		t.Fatalf("failed restore lost its durable guard: pending=%q err=%v", pending, err)
	}
	err = startDeployment(&app{base: base}, "", nil, false)
	if failure, ok := err.(*CLIError); !ok || failure.Code != "data_restore_incomplete" {
		t.Fatalf("start after a failed restore: %v", err)
	}
}

func TestSnapshotRestoreFailureKeepsDataGuardAfterMetadataReplacement(t *testing.T) {
	fakeBtrfs(t)
	workspace, deploymentID := newSnapshotWorkspace(t)
	meta, err := createSnapshot(workspace, snapshotOptions{kind: snapshotKindManual, reason: snapshotReasonManual})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dataDir(workspace), "marker"), []byte("new incompatible data"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(workspaceConfigPath(workspace), []byte("modules: {changed: {}}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	realFakeCommand := btrfsCommand
	btrfsCommand = func(args ...string) error {
		if len(args) >= 4 && args[0] == "subvolume" && args[1] == "snapshot" && args[2] == snapshotDataPath(snapshotRoot(workspace, meta.ID)) {
			return fmt.Errorf("injected data restore failure")
		}
		return realFakeCommand(args...)
	}
	if _, err := restoreSnapshot(workspace, meta, false, false); err == nil || !strings.Contains(err.Error(), "injected data restore failure") {
		t.Fatalf("restore did not reach the injected data failure: %v", err)
	}
	config, err := os.ReadFile(workspaceConfigPath(workspace))
	if err != nil || string(config) != "modules:\n  core: {}\n" {
		t.Fatalf("failure happened before metadata replacement: %q, %v", config, err)
	}
	marker, err := os.ReadFile(filepath.Join(dataDir(workspace), "marker"))
	if err != nil || string(marker) != "new incompatible data" {
		t.Fatalf("failed data replacement did not retain current data: %q, %v", marker, err)
	}
	assertRestoreBlocksStart(t, workspace)
	state, err := loadDeploymentState(stateDir(workspace), deploymentID)
	if err != nil || state.FailureDetail[dataRestoreGuardKey] != "snapshot:"+meta.ID {
		t.Fatalf("restore guard was replaced by captured clean state: %+v, %v", state, err)
	}
	btrfsCommand = realFakeCommand
	if _, err := restoreSnapshot(workspace, meta, false, false); err != nil {
		t.Fatal(err)
	}
	pending, err := postgresMaintenancePending(stateDir(workspace), deploymentID)
	if err != nil || pending != "" {
		t.Fatalf("successful retry left a data restore guard: %q, %v", pending, err)
	}
}

func TestBackupRestoreFailureKeepsDataGuardAfterMetadataReplacement(t *testing.T) {
	fakeBtrfs(t)
	workspace, deploymentID := newSnapshotWorkspace(t)
	meta, err := createSnapshot(workspace, snapshotOptions{kind: snapshotKindManual, reason: snapshotReasonManual})
	if err != nil {
		t.Fatal(err)
	}
	dest := t.TempDir()
	manifest := &backupManifest{BackupID: "guard-backup", DeploymentID: deploymentID, Mode: backupModeCopy, Complete: true}
	root := backupRoot(dest, manifest.BackupID)
	if err := copyTree(snapshotRoot(workspace, meta.ID), root, func(from, to string) error { return copyFileMode(from, to, 0600) }); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(workspaceConfigPath(workspace), []byte("modules: {changed: {}}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	aside := dataDir(workspace) + ".restoring-backup"
	if err := os.MkdirAll(aside, 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := restoreBackup(workspace, dest, manifest, nil, false); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("backup restore did not reach injected data failure: %v", err)
	}
	config, err := os.ReadFile(workspaceConfigPath(workspace))
	if err != nil || string(config) != "modules:\n  core: {}\n" {
		t.Fatalf("failure happened before metadata replacement: %q, %v", config, err)
	}
	assertRestoreBlocksStart(t, workspace)
	if err := os.RemoveAll(aside); err != nil {
		t.Fatal(err)
	}
	// The builtin copier keeps this test independent of host rsync features.
	t.Setenv("PATH", "")
	if _, err := restoreBackup(workspace, dest, manifest, nil, false); err != nil {
		t.Fatal(err)
	}
	pending, err := postgresMaintenancePending(stateDir(workspace), deploymentID)
	if err != nil || pending != "" {
		t.Fatalf("successful backup retry left a guard: %q, %v", pending, err)
	}
}

func TestDataRestoreGuardSurvivesAnActivePointerChange(t *testing.T) {
	workspace, activeID := newSnapshotWorkspace(t)
	base := stateDir(workspace)
	if err := beginDataRestoreGuard(base, "restored-artifact", "snapshot:recovery-point"); err != nil {
		t.Fatal(err)
	}
	// Model a crash after state metadata and active selection have been copied
	// to a different artifact but before data has been restored.
	if err := saveDeploymentState(base, deploymentState{ID: "restored-artifact", Status: "active"}); err != nil {
		t.Fatal(err)
	}
	if err := saveActiveState(base, &activeDeploymentState{ActiveDeployment: "restored-artifact"}); err != nil {
		t.Fatal(err)
	}
	assertRestoreBlocksStart(t, workspace)
	state, err := loadDeploymentState(base, activeID)
	if err != nil || state.FailureDetail[dataRestoreGuardKey] == nil {
		t.Fatalf("active selection lost the original failure record: %+v, %v", state, err)
	}
}

func TestBackupRestoreStopFailureLeavesTreesUntouched(t *testing.T) {
	fakeBtrfs(t)
	workspace, deploymentID := newSnapshotWorkspace(t)
	meta, err := createSnapshot(workspace, snapshotOptions{kind: snapshotKindManual, reason: snapshotReasonManual})
	if err != nil {
		t.Fatal(err)
	}
	dest := t.TempDir()
	manifest := &backupManifest{BackupID: "stop-backup", DeploymentID: deploymentID, Mode: backupModeCopy, Complete: true}
	if err := copyTree(snapshotRoot(workspace, meta.ID), backupRoot(dest, manifest.BackupID), func(from, to string) error { return copyFileMode(from, to, 0600) }); err != nil {
		t.Fatal(err)
	}
	artifact := deploymentArtifactDir(stateDir(workspace), deploymentID)
	activeManifest, err := loadDeploymentManifest(artifact)
	if err != nil {
		t.Fatal(err)
	}
	module := activeManifest.Modules["core"]
	module.RuntimeType = "compose"
	activeManifest.Modules["core"] = module
	if err := writeYAMLAtomic(filepath.Join(artifact, "deployment.yml"), activeManifest, 0600); err != nil {
		t.Fatal(err)
	}
	config := []byte("modules: {changed: {}}\n")
	if err := os.WriteFile(workspaceConfigPath(workspace), config, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dataDir(workspace), "marker"), []byte("current data"), 0600); err != nil {
		t.Fatal(err)
	}
	// A missing Docker executable makes the full-workspace stop barrier fail.
	t.Setenv("PATH", "")
	_, err = restoreBackup(workspace, dest, manifest, nil, false)
	if failure, ok := err.(*CLIError); !ok || failure.Code != "stop_failed" {
		t.Fatalf("restore crossed a failed stop barrier: %v", err)
	}
	got, err := os.ReadFile(workspaceConfigPath(workspace))
	if err != nil || string(got) != string(config) {
		t.Fatalf("failed stop modified workspace configuration: %q, %v", got, err)
	}
	got, err = os.ReadFile(filepath.Join(dataDir(workspace), "marker"))
	if err != nil || string(got) != "current data" {
		t.Fatalf("failed stop replaced data: %q, %v", got, err)
	}
	assertRestoreBlocksStart(t, workspace)
}
