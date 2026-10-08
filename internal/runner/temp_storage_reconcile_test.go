package runner

// TEST_CASES: TEMP-T-005, TEMP-T-008

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func temporaryDeletedBeforePersistenceFixture(t *testing.T) (*app, *temporaryDockerSnapshot, TemporaryLease) {
	t.Helper()
	a, observation := temporaryTestApp(t)
	if err := a.prepareModuleTemporaryStorage(a.reg["demo"]); err != nil {
		t.Fatal(err)
	}
	registry, err := a.loadTemporaryRegistry(false)
	if err != nil {
		t.Fatal(err)
	}
	registry.Leases[0].State = "released"
	lease := registry.Leases[0]
	registry.Transition = &TemporaryTransition{Phase: "committed", FromRoot: lease.Root, TargetRoot: filepath.Join(a.workspace, "new-temp")}
	if err := a.saveTemporaryRegistry(registry); err != nil {
		t.Fatal(err)
	}
	// Reproduce the actual crash boundary: the leaf has been unlinked, but
	// the durable record still says released. Do not rewrite that record.
	if err := os.Remove(filepath.Join(lease.Path, ".anas-temp-owner.yml")); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(lease.Path); err != nil {
		t.Fatal(err)
	}
	// Linux tests exercise the real openat2 driver. macOS can still verify the
	// persistent-state/daemon failure logic with a confined descriptor probe.
	previous := temporaryDirectoryAbsenceProbe
	temporaryDirectoryAbsenceProbe = func(lease *TemporaryLease) (bool, error) {
		parent, err := openTemporaryDirectory(filepath.Dir(lease.Path))
		if err != nil {
			return false, err
		}
		defer parent.Close()
		_, err = parent.root.Lstat(filepath.Base(lease.Path))
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return false, err
		}
		return errors.Is(err, os.ErrNotExist), parent.check()
	}
	t.Cleanup(func() { temporaryDirectoryAbsenceProbe = previous })
	return a, observation, lease
}

func TestTemporaryGCReconcilesDeletionInterruptedBeforeRegistryPersistence(t *testing.T) {
	for _, phase := range []string{"committed", "cleanup_deferred"} {
		t.Run(phase, func(t *testing.T) {
			a, _, lease := temporaryDeletedBeforePersistenceFixture(t)
			if err := a.recordTemporaryTransitionPhase(phase); err != nil {
				t.Fatal(err)
			}
			before, err := os.ReadFile(temporaryStatePath(a.base))
			if err != nil {
				t.Fatal(err)
			}
			if _, err := a.gcTemporaryStorage(true); err != nil {
				t.Fatal(err)
			}
			after, err := os.ReadFile(temporaryStatePath(a.base))
			if err != nil || string(before) != string(after) {
				t.Fatal("dry-run repaired persisted state")
			}
			status, err := a.gcTemporaryStorage(false)
			if err != nil || len(status.Directories) != 0 {
				t.Fatalf("completed deletion remained blocked: %+v %v", status, err)
			}
			registry, err := a.loadTemporaryRegistry(false)
			if err != nil || registry.Leases[0].State != "deleted" || registry.Transition.Phase != "complete" {
				t.Fatalf("crashed deletion was not durably reconciled: %+v %v", registry, err)
			}
			if _, err := os.Lstat(lease.Path); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("reconciliation recreated or modified the absent leaf")
			}
		})
	}
}

func TestTemporaryAbsentLeaseReconciliationRetainsUnknownRootsAndUsers(t *testing.T) {
	for _, fault := range []string{"query-failure", "daemon-change", "filesystem-change", "root-replaced", "missing-parent", "substituted-symlink", "pending-transition"} {
		t.Run(fault, func(t *testing.T) {
			a, observation, lease := temporaryDeletedBeforePersistenceFixture(t)
			registry, err := a.loadTemporaryRegistry(false)
			if err != nil {
				t.Fatal(err)
			}
			switch fault {
			case "query-failure":
				temporaryDockerObservation = func(*app) (temporaryDockerSnapshot, error) {
					return temporaryDockerSnapshot{}, errors.New("synthetic unavailable Docker")
				}
			case "daemon-change":
				observation.DaemonID = "different-daemon"
			case "filesystem-change":
				probe := temporaryFilesystemProbe
				temporaryFilesystemProbe = func(path string) (TemporaryFilesystem, uint64, uint64, error) {
					filesystem, bytes, inodes, err := probe(path)
					if path == lease.Root {
						filesystem.ID = "different-filesystem"
					}
					return filesystem, bytes, inodes, err
				}
			case "root-replaced":
				registry.Roots[0].DirectoryID = "different-directory"
			case "missing-parent":
				if err := os.Rename(filepath.Dir(lease.Path), filepath.Dir(lease.Path)+"-moved"); err != nil {
					t.Fatal(err)
				}
			case "substituted-symlink":
				if err := os.Symlink(a.workspace, lease.Path); err != nil {
					t.Fatal(err)
				}
			case "pending-transition":
				registry.Transition.Phase = "starting"
			}
			if err := a.saveTemporaryRegistry(registry); err != nil {
				t.Fatal(err)
			}
			_, _ = a.gcTemporaryStorage(false)
			after, err := a.loadTemporaryRegistry(false)
			if err != nil || after.Leases[0].State != "released" {
				t.Fatalf("unknown state was treated as completed deletion: %+v %v", after, err)
			}
		})
	}
}
