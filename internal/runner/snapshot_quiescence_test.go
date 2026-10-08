package runner

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/anas-project/ANAS/internal/application"
)

// The tag points to newID, while the old running container proves oldID.
// Removing the container makes a second inventory resolve the wrong tag.
func movingTagSnapshotWorkspace(t *testing.T) (workspace, stopped, oldID string) {
	t.Helper()
	fakeBtrfs(t)
	workspace, _ = newSnapshotWorkspace(t)
	artifact, _ := recoveryImageFixture(t, workspace, "changed-tag")
	manifest, err := loadDeploymentManifest(artifact)
	if err != nil {
		t.Fatal(err)
	}
	manifest.Resources = nil
	module := manifest.Modules["photos"]
	module.Version = "1.0.0"
	module.Providers = []ContractProvider{{Name: "relational_database", Interface: "postgres"}}
	manifest.Modules["photos"] = module
	if err := writeYAMLAtomic(filepath.Join(artifact, "deployment.yml"), manifest, 0600); err != nil {
		t.Fatal(err)
	}
	stopped = filepath.Join(workspace, "containers-stopped")
	bin := t.TempDir()
	script := `#!/bin/sh
case "$*" in
  *"config --format json"*) printf '%s' '{"services":{"service":{"image":"changed-tag"}}}';;
  *"config --services"*) printf '%s' 'service';;
  *"ps -q"*) if [ ! -f '` + stopped + `' ]; then printf '%s' 'old-container'; fi;;
  *" stop"*|*" down"*) : > '` + stopped + `';;
  *" start"*) /bin/rm -f '` + stopped + `';;
  *) exit 0;;
esac
`
	if err := os.WriteFile(filepath.Join(bin, "docker"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bin, "findmnt"), []byte("#!/bin/sh\nprintf '%s' '{\"filesystems\":[{\"target\":\"/\"}]}'\n"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bin, "btrfs"), []byte("#!/bin/sh\nexit 0\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	oldID = "sha256:" + strings.Repeat("a", 64)
	newID := "sha256:" + strings.Repeat("b", 64)
	previous := snapshotImageDockerCommand
	t.Cleanup(func() { snapshotImageDockerCommand = previous })
	snapshotImageDockerCommand = func(_ context.Context, _ []string, out io.Writer, args ...string) error {
		switch strings.Join(args[:2], " ") {
		case "ps --all":
			if args[len(args)-1] == "{{.ID}}" && !exists(stopped) {
				_, err := fmt.Fprintln(out, "old-container")
				return err
			}
			return nil
		case "container inspect":
			if exists(stopped) {
				return fmt.Errorf("the running container is gone")
			}
			_, err := fmt.Fprintln(out, oldID)
			return err
		case "image inspect":
			id := args[len(args)-1]
			if id == "changed-tag" {
				id = newID
			}
			_, err := fmt.Fprintln(out, id)
			return err
		case "image save":
			writeUntaggedRecoveryArchive(t, args[3])
			return nil
		case "image load":
			return nil
		default:
			return fmt.Errorf("unexpected image command %v", args)
		}
	}
	return workspace, stopped, oldID
}

func TestManualPostgresSnapshotStopsWorkspaceAndCapturesOldContainerImage(t *testing.T) {
	workspace, stopped, oldID := movingTagSnapshotWorkspace(t)
	command := btrfsCommand
	btrfsCommand = func(args ...string) error {
		if len(args) >= 5 && args[0] == "subvolume" && args[1] == "snapshot" && args[2] == "-r" && args[3] == dataDir(workspace) && !exists(stopped) {
			return fmt.Errorf("snapshot taken while workspace is writing")
		}
		return command(args...)
	}
	meta, err := createManualSnapshot(workspace, snapshotOptions{kind: snapshotKindManual, reason: snapshotReasonManual})
	if err != nil {
		t.Fatal(err)
	}
	metadata, err := loadSnapshotImageMetadata(snapshotRoot(workspace, meta.ID))
	if err != nil || metadata.Services[0].ID != oldID {
		t.Fatalf("manual snapshot followed moved tag: %+v, %v", metadata, err)
	}
	if exists(stopped) {
		t.Fatal("manual snapshot did not restore the previously running containers")
	}
	entries, err := os.ReadDir(transactionsDir(stateDir(workspace)))
	if err != nil || len(entries) != 0 {
		t.Fatalf("completed manual snapshot left a stop transaction: %v, %v", entries, err)
	}
}

func TestApplicationPostgresSnapshotUsesSameQuiescenceBarrier(t *testing.T) {
	workspace, stopped, oldID := movingTagSnapshotWorkspace(t)
	command := btrfsCommand
	btrfsCommand = func(args ...string) error {
		if len(args) >= 5 && args[0] == "subvolume" && args[1] == "snapshot" && args[2] == "-r" && args[3] == dataDir(workspace) && !exists(stopped) {
			return fmt.Errorf("API snapshot taken while workspace is writing")
		}
		return command(args...)
	}
	service := NewWorkspaceMaintenanceServiceFactory(nil)(workspace, application.NopEventSink{})
	record, err := service.CreateSnapshot(context.Background(), application.SnapshotCreateRequest{})
	if err != nil {
		t.Fatal(err)
	}
	metadata, err := loadSnapshotImageMetadata(snapshotRoot(workspace, record.ID))
	if err != nil || metadata.Services[0].ID != oldID || exists(stopped) {
		t.Fatalf("API snapshot did not quiesce/capture/resume: metadata=%+v stopped=%v err=%v", metadata, exists(stopped), err)
	}
}

func TestRestoreRecoveryPointKeepsOldContainerImageAfterDown(t *testing.T) {
	for _, mode := range []string{"snapshot", "backup"} {
		t.Run(mode, func(t *testing.T) {
			workspace, stopped, oldID := movingTagSnapshotWorkspace(t)
			original, err := createSnapshot(workspace, snapshotOptions{kind: snapshotKindManual, reason: snapshotReasonManual})
			if err != nil {
				t.Fatal(err)
			}
			root := snapshotRoot(workspace, original.ID)
			if mode == "snapshot" {
				command := btrfsCommand
				btrfsCommand = func(args ...string) error {
					if len(args) >= 4 && args[0] == "subvolume" && args[1] == "snapshot" && args[2] == snapshotDataPath(root) {
						return fmt.Errorf("injected restored-data failure")
					}
					return command(args...)
				}
				_, err = restoreSnapshot(workspace, original, false, false)
			} else {
				dest := t.TempDir()
				backup := &backupManifest{BackupID: "moving-tag-backup", DeploymentID: original.DeploymentID, Mode: backupModeCopy, Complete: true}
				if err := copyTree(root, backupRoot(dest, backup.BackupID), func(from, to string) error { return copyFileMode(from, to, 0600) }); err != nil {
					t.Fatal(err)
				}
				if err := os.MkdirAll(dataDir(workspace)+".restoring-backup", 0700); err != nil {
					t.Fatal(err)
				}
				_, err = restoreBackup(workspace, dest, backup, nil, false)
			}
			if err == nil {
				t.Fatal("injected restore failure was not observed")
			}
			if !exists(stopped) {
				t.Fatal("restore never reached its stop barrier")
			}
			all, err := listSnapshots(workspace)
			if err != nil {
				t.Fatal(err)
			}
			found := false
			for _, meta := range all {
				if meta.Reason != snapshotReasonPreRestore {
					continue
				}
				found = true
				metadata, err := loadSnapshotImageMetadata(snapshotRoot(workspace, meta.ID))
				if err != nil || metadata.Services[0].ID != oldID {
					t.Fatalf("pre-restore snapshot followed the moved tag after down: %+v, %v", metadata, err)
				}
			}
			if !found {
				t.Fatal("restore failure lost the PostgreSQL pre-restore recovery point")
			}
			assertRestoreBlocksStart(t, workspace)
		})
	}
}

func TestPostgresRecoveryCaptureFailurePrecedesStop(t *testing.T) {
	for _, mode := range []string{"manual", "snapshot", "backup"} {
		t.Run(mode, func(t *testing.T) {
			workspace, stopped, _ := movingTagSnapshotWorkspace(t)
			original, err := createSnapshot(workspace, snapshotOptions{kind: snapshotKindManual, reason: snapshotReasonManual})
			if err != nil {
				t.Fatal(err)
			}
			command := snapshotImageDockerCommand
			snapshotImageDockerCommand = func(ctx context.Context, env []string, out io.Writer, args ...string) error {
				if args[0] == "container" && args[1] == "inspect" {
					return fmt.Errorf("injected image capture failure")
				}
				return command(ctx, env, out, args...)
			}
			switch mode {
			case "manual":
				_, err = createManualSnapshot(workspace, snapshotOptions{kind: snapshotKindManual, reason: snapshotReasonManual})
			case "snapshot":
				_, err = restoreSnapshot(workspace, original, false, false)
			case "backup":
				dest := t.TempDir()
				backup := &backupManifest{BackupID: "capture-failure", DeploymentID: original.DeploymentID, Mode: backupModeCopy, Complete: true}
				if err := copyTree(snapshotRoot(workspace, original.ID), backupRoot(dest, backup.BackupID), func(from, to string) error { return copyFileMode(from, to, 0600) }); err != nil {
					t.Fatal(err)
				}
				_, err = restoreBackup(workspace, dest, backup, nil, false)
			}
			if failure, ok := err.(*CLIError); !ok || failure.Code != "image_capture_failed" {
				t.Fatalf("failed image capture did not close the recovery path: %v", err)
			}
			if exists(stopped) {
				t.Fatal("containers stopped before their actual images were secured")
			}
			all, err := listSnapshots(workspace)
			if err != nil || len(all) != 1 {
				t.Fatalf("capture failure published a second recovery point: %+v, %v", all, err)
			}
		})
	}
}

func TestManualPostgresSnapshotRefusesUncertainRunningInventory(t *testing.T) {
	workspace, stopped, _ := movingTagSnapshotWorkspace(t)
	docker := filepath.Join(os.Getenv("PATH"), "docker")
	body, err := os.ReadFile(docker)
	if err != nil {
		t.Fatal(err)
	}
	body = []byte(strings.Replace(string(body), "#!/bin/sh\n", "#!/bin/sh\ncase \"$*\" in *\"ps -q\"*) exit 1;; esac\n", 1))
	if err := os.WriteFile(docker, body, 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := createManualSnapshot(workspace, snapshotOptions{kind: snapshotKindManual, reason: snapshotReasonManual}); err == nil {
		t.Fatal("Compose running-state query failed but the snapshot was published")
	}
	if exists(stopped) {
		t.Fatal("uncertain running inventory crossed the stop barrier")
	}
	all, err := listSnapshots(workspace)
	if err != nil || len(all) != 0 {
		t.Fatalf("uncertain running inventory published a recovery point: %+v, %v", all, err)
	}
}

func TestPostgresRecoveryCoverageFailurePrecedesStop(t *testing.T) {
	for _, mode := range []string{"manual", "snapshot", "backup", "snapshot-backup"} {
		t.Run(mode, func(t *testing.T) {
			workspace, stopped, _ := movingTagSnapshotWorkspace(t)
			original, err := createSnapshot(workspace, snapshotOptions{kind: snapshotKindManual, reason: snapshotReasonManual})
			if err != nil {
				t.Fatal(err)
			}
			// A coupled-data link outside data/ cannot be recovered by the
			// parent snapshot. Every execution barrier must reject it first.
			if err := os.Symlink(t.TempDir(), filepath.Join(dataDir(workspace), "external-database")); err != nil {
				t.Fatal(err)
			}
			switch mode {
			case "manual":
				_, err = createManualSnapshot(workspace, snapshotOptions{kind: snapshotKindManual, reason: snapshotReasonManual})
			case "snapshot":
				_, err = restoreSnapshot(workspace, original, false, false)
			case "backup":
				dest := t.TempDir()
				backup := &backupManifest{BackupID: "coverage-failure", DeploymentID: original.DeploymentID, Mode: backupModeCopy, Complete: true}
				if err := copyTree(snapshotRoot(workspace, original.ID), backupRoot(dest, backup.BackupID), func(from, to string) error { return copyFileMode(from, to, 0600) }); err != nil {
					t.Fatal(err)
				}
				_, err = restoreBackup(workspace, dest, backup, nil, false)
			case "snapshot-backup":
				_, _, _, err = prepareBackupSource(workspace, stateDir(workspace), &backupPlan{StopContainers: true, useSnapshot: true}, backupOptions{})
			}
			if failure, ok := err.(*CLIError); !ok || failure.Code != "postgres_recovery_coverage_incomplete" {
				t.Fatalf("unrecoverable data crossed its coverage barrier: %v", err)
			}
			if exists(stopped) {
				t.Fatal("containers stopped before recovery coverage was qualified")
			}
			all, err := listSnapshots(workspace)
			if err != nil || len(all) != 1 {
				t.Fatalf("incomplete data coverage published a recovery point: %+v, %v", all, err)
			}
		})
	}
}
