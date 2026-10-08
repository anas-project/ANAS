//go:build linux

package runner

// TEST_CASES: TEMP-T-002, TEMP-T-005

import (
	"os"
	"path/filepath"
	"testing"
)

func TestTemporaryDescriptorGCDoesNotFollowContentSymlinks(t *testing.T) {
	a, _ := temporaryTestApp(t)
	if err := a.prepareModuleTemporaryStorage(a.reg["demo"]); err != nil {
		t.Fatal(err)
	}
	registry, _ := a.loadTemporaryRegistry(false)
	lease := registry.Leases[0]
	protected := filepath.Join(a.workspace, "protected")
	if err := os.Mkdir(protected, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(protected, "sentinel"), []byte("preserve"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(protected, filepath.Join(lease.Path, "escape")); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(lease.Path, "nested"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(lease.Path, "nested", "discard"), []byte("temporary"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := removeTemporaryLeaseDirectory(&lease); err != nil {
		t.Fatal(err)
	}
	if body, err := os.ReadFile(filepath.Join(protected, "sentinel")); err != nil || string(body) != "preserve" {
		t.Fatalf("GC followed escape: %s %v", body, err)
	}
	if _, err := os.Stat(lease.Path); !os.IsNotExist(err) {
		t.Fatalf("lease tree remained: %v", err)
	}
}

func TestTemporaryDescriptorGCRejectsReplacedInstance(t *testing.T) {
	a, _ := temporaryTestApp(t)
	if err := a.prepareModuleTemporaryStorage(a.reg["demo"]); err != nil {
		t.Fatal(err)
	}
	registry, _ := a.loadTemporaryRegistry(false)
	lease := registry.Leases[0]
	if err := os.Rename(lease.Path, lease.Path+"-original"); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(lease.Path, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(lease.Path, "sentinel"), []byte("preserve"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := removeTemporaryLeaseDirectory(&lease); err == nil {
		t.Fatal("GC accepted substituted directory")
	}
	if _, err := os.Stat(filepath.Join(lease.Path, "sentinel")); err != nil {
		t.Fatal("substituted directory was changed")
	}
}

func TestTemporaryDescriptorAbsenceRequiresExistingSafeParents(t *testing.T) {
	a, _, lease := temporaryDeletedBeforePersistenceFixture(t)
	// The shared fixture seam supports macOS fault tests; explicitly exercise
	// Linux's real descriptor driver for this safety boundary.
	temporaryDirectoryAbsenceProbe = confirmTemporaryDirectoryAbsent
	registry, err := a.loadTemporaryRegistry(false)
	if err != nil {
		t.Fatal(err)
	}
	if absent, err := confirmReleasedTemporaryLeaseAbsent(registry, &lease); err != nil || !absent {
		t.Fatalf("actual unlinked leaf was not confirmed: %v %v", absent, err)
	}
	parent := filepath.Dir(lease.Path)
	if err := os.Rename(parent, parent+"-original"); err != nil {
		t.Fatal(err)
	}
	if absent, err := confirmReleasedTemporaryLeaseAbsent(registry, &lease); err == nil || absent {
		t.Fatalf("missing ancestor was treated as completed leaf deletion: %v %v", absent, err)
	}
	if err := os.Symlink(parent+"-original", parent); err != nil {
		t.Fatal(err)
	}
	if absent, err := confirmReleasedTemporaryLeaseAbsent(registry, &lease); err == nil || absent {
		t.Fatalf("substituted parent symlink was followed: %v %v", absent, err)
	}
}
