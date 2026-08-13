package runner

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/anas-project/ANAS/internal/config"
)

func stubSnapshotProbe(t *testing.T, btrfs bool, fsErr, subvolumeErr error) {
	t.Helper()
	oldFS := snapshotFilesystemIsBtrfs
	oldSubvolume := snapshotSubvolumeShow
	snapshotFilesystemIsBtrfs = func(string) (bool, error) { return btrfs, fsErr }
	snapshotSubvolumeShow = func(string) error { return subvolumeErr }
	t.Cleanup(func() {
		snapshotFilesystemIsBtrfs = oldFS
		snapshotSubvolumeShow = oldSubvolume
	})
}

func TestResolveSnapshotLockAutoSelectsBtrfs(t *testing.T) {
	stubSnapshotProbe(t, true, nil, nil)
	got, err := resolveSnapshotLock(t.TempDir(), &config.File{})
	if err != nil {
		t.Fatal(err)
	}
	if got.Backend != "btrfs" || got.KeepAuto != snapshotDefaultKeepAuto {
		t.Fatalf("snapshot lock = %#v", got)
	}
}

func TestResolveSnapshotLockAutoSelectsNoneWhenUnavailable(t *testing.T) {
	for _, tc := range []struct {
		name         string
		btrfs        bool
		fsErr        error
		subvolumeErr error
	}{
		{name: "other filesystem"},
		{name: "filesystem probe failure", fsErr: errors.New("statfs failed")},
		{name: "data is not a subvolume", btrfs: true, subvolumeErr: errors.New("not a subvolume")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stubSnapshotProbe(t, tc.btrfs, tc.fsErr, tc.subvolumeErr)
			got, err := resolveSnapshotLock(t.TempDir(), &config.File{})
			if err != nil {
				t.Fatal(err)
			}
			if got.Backend != "none" || got.KeepAuto != 0 {
				t.Fatalf("snapshot lock = %#v", got)
			}
		})
	}
}

func TestResolveSnapshotLockPreservesExplicitPolicy(t *testing.T) {
	stubSnapshotProbe(t, false, errors.New("must not matter"), errors.New("must not matter"))
	cfg := &config.File{Rollback: config.Rollback{Snapshot: config.Snapshot{
		Backend: "btrfs", KeepAuto: config.Int("2"),
	}}}
	got, err := resolveSnapshotLock(t.TempDir(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	if got.Backend != "btrfs" || got.KeepAuto != 2 {
		t.Fatalf("snapshot lock = %#v", got)
	}
}

func TestValidateLockedSnapshotUsesFrozenAutoDecision(t *testing.T) {
	lock := &moduleLock{Snapshot: &moduleLockSnapshot{Backend: "btrfs", KeepAuto: 5}}
	if err := validateLockedSnapshot(&config.File{}, lock); err != nil {
		t.Fatal(err)
	}
	cfg := &config.File{Rollback: config.Rollback{Snapshot: config.Snapshot{Backend: "none"}}}
	if err := validateLockedSnapshot(cfg, lock); err == nil {
		t.Fatal("explicit backend change did not make the lock stale")
	}
}

func TestWorkspaceKeepAutoUsesLockedPolicy(t *testing.T) {
	workspace := t.TempDir()
	lock := &moduleLock{Snapshot: &moduleLockSnapshot{Backend: "btrfs", KeepAuto: 3}}
	if err := saveModuleLockFile(filepath.Join(workspace, "config.lock.yml"), lock); err != nil {
		t.Fatal(err)
	}
	if got := workspaceKeepAuto(workspace); got != 3 {
		t.Fatalf("workspaceKeepAuto = %d, want 3", got)
	}
}

func TestResolveSnapshotLockRejectsUnknownBackend(t *testing.T) {
	cfg := &config.File{Rollback: config.Rollback{Snapshot: config.Snapshot{Backend: "zfs"}}}
	if _, err := resolveSnapshotLock(t.TempDir(), cfg); err == nil {
		t.Fatal("unknown snapshot backend was accepted")
	}
}
