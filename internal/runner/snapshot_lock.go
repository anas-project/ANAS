package runner

import (
	"fmt"
	"strings"

	"github.com/anas-project/ANAS/internal/config"
)

var (
	snapshotFilesystemIsBtrfs = filesystemIsBtrfs
	snapshotSubvolumeShow     = btrfsSubvolumeShow
)

// resolveSnapshotLock turns an optional snapshot preference into a host-specific
// decision. The decision belongs in config.lock.yml: probing on every render
// would let the same desired config silently change behaviour after a mount or
// filesystem change.
func resolveSnapshotLock(workspace string, cfg *config.File) (*moduleLockSnapshot, error) {
	backend := strings.ToLower(strings.TrimSpace(cfg.Rollback.Snapshot.Backend))
	keep, keepSet := cfg.Rollback.Snapshot.KeepAuto.Value()
	if !keepSet {
		keep = snapshotDefaultKeepAuto
	}
	if keep < 0 {
		keep = 0
	}

	switch backend {
	case "":
		btrfs, err := snapshotFilesystemIsBtrfs(workspace)
		if err == nil && btrfs && snapshotSubvolumeShow(dataDir(workspace)) == nil {
			return &moduleLockSnapshot{Backend: "btrfs", KeepAuto: keep}, nil
		}
		return &moduleLockSnapshot{Backend: "none"}, nil
	case "none":
		return &moduleLockSnapshot{Backend: "none"}, nil
	case "btrfs":
		return &moduleLockSnapshot{Backend: "btrfs", KeepAuto: keep}, nil
	default:
		return nil, fmt.Errorf("rollback.snapshot.backend must be btrfs or none, got %q", backend)
	}
}

// validateLockedSnapshot makes an explicit config change require a new lock,
// while an omitted setting continues to use the host decision already frozen
// in the lock.
func validateLockedSnapshot(cfg *config.File, lock *moduleLock) error {
	if lock == nil || lock.Snapshot == nil {
		return fmt.Errorf("config lock has no snapshot policy; run anas lock")
	}
	backend := strings.ToLower(strings.TrimSpace(cfg.Rollback.Snapshot.Backend))
	if backend != "" && backend != lock.Snapshot.Backend {
		return fmt.Errorf("rollback.snapshot.backend is %s but lock records %s; run anas lock", backend, lock.Snapshot.Backend)
	}
	if keep, set := cfg.Rollback.Snapshot.KeepAuto.Value(); set && lock.Snapshot.Backend == "btrfs" {
		if keep < 0 {
			keep = 0
		}
		if keep != lock.Snapshot.KeepAuto {
			return fmt.Errorf("rollback.snapshot.keep_auto is %d but lock records %d; run anas lock", keep, lock.Snapshot.KeepAuto)
		}
	}
	return nil
}
