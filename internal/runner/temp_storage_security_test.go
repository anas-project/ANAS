package runner

// TEST_CASES: TEMP-T-004, TEMP-T-006

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTemporaryOwnershipFilesRejectLinksAndReadablePermissions(t *testing.T) {
	for _, kind := range []string{"symlink", "hardlink", "permissions"} {
		t.Run(kind, func(t *testing.T) {
			a, _ := temporaryTestApp(t)
			if err := a.prepareModuleTemporaryStorage(a.reg["demo"]); err != nil {
				t.Fatal(err)
			}
			registry, err := a.loadTemporaryRegistry(false)
			if err != nil {
				t.Fatal(err)
			}
			lease := &registry.Leases[0]
			marker := filepath.Join(lease.Path, ".anas-temp-owner.yml")
			body, err := os.ReadFile(marker)
			if err != nil {
				t.Fatal(err)
			}
			switch kind {
			case "symlink":
				copy := filepath.Join(lease.Path, "copied-marker")
				if err := os.WriteFile(copy, body, 0400); err != nil {
					t.Fatal(err)
				}
				if err := os.Remove(marker); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(copy, marker); err != nil {
					t.Fatal(err)
				}
			case "hardlink":
				if err := os.Link(marker, filepath.Join(lease.Path, "marker-alias")); err != nil {
					t.Fatal(err)
				}
			case "permissions":
				if err := os.Chmod(marker, 0644); err != nil {
					t.Fatal(err)
				}
			}
			if err := validateTemporaryLease(registry, lease); err == nil {
				t.Fatal("unsafe ownership marker authorized a lease")
			}
			status, err := a.gcTemporaryStorage(true)
			if err != nil || status.Directories[0].Reclaimable {
				t.Fatalf("unsafe marker became reclaimable: %+v, %v", status, err)
			}
		})
	}
}

func TestTemporaryRegistryRejectsHardlinkAndSymlink(t *testing.T) {
	a, _ := temporaryTestApp(t)
	if err := a.prepareModuleTemporaryStorage(a.reg["demo"]); err != nil {
		t.Fatal(err)
	}
	path := temporaryStatePath(a.base)
	alias := filepath.Join(filepath.Dir(path), "registry-alias")
	if err := os.Link(path, alias); err != nil {
		t.Fatal(err)
	}
	if _, err := a.loadTemporaryRegistry(false); err == nil {
		t.Fatal("hardlinked registry accepted")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(alias, path); err != nil {
		t.Fatal(err)
	}
	if _, err := a.loadTemporaryRegistry(false); err == nil {
		t.Fatal("symlink registry accepted")
	}
}

func TestTemporaryPostMountMarkerReadRejectsReplacement(t *testing.T) {
	a, _ := temporaryTestApp(t)
	if err := a.prepareModuleTemporaryStorage(a.reg["demo"]); err != nil {
		t.Fatal(err)
	}
	registry, err := a.loadTemporaryRegistry(false)
	if err != nil {
		t.Fatal(err)
	}
	lease := &registry.Leases[0]
	if err := validateTemporaryLease(registry, lease); err != nil {
		t.Fatal(err)
	}
	if marker, err := readTemporaryLeaseOwnershipMarker(lease); err != nil || len(marker) == 0 {
		t.Fatal("valid marker could not be compared after Docker cp", err)
	}
	outside := filepath.Join(a.workspace, "outside-synthetic-content")
	if err := os.WriteFile(outside, []byte("external-canary"), 0400); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(lease.Path, ".anas-temp-owner.yml")
	if err := os.Remove(marker); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, marker); err != nil {
		t.Fatal(err)
	}
	if body, err := readTemporaryLeaseOwnershipMarker(lease); err == nil || len(body) != 0 {
		t.Fatalf("post-validation replacement was followed: %q %v", body, err)
	}
}

func TestTemporaryDockerMarkerStreamRejectsOversizedSubprocessOutput(t *testing.T) {
	a, _ := temporaryTestApp(t)
	payload := filepath.Join(a.workspace, "oversized-tar-stream")
	if err := os.WriteFile(payload, []byte(strings.Repeat("x", (2<<20)+1)), 0600); err != nil {
		t.Fatal(err)
	}
	command := filepath.Join(a.workspace, "docker")
	if err := os.WriteFile(command, []byte("#!/bin/sh\ncat '"+payload+"'\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", a.workspace+string(os.PathListSeparator)+os.Getenv("PATH"))
	if body, err := a.temporaryDockerOutput("cp", "synthetic-container:/marker", "-"); err == nil || len(body) != 0 {
		t.Fatalf("Docker cp buffered oversized application content: length=%d error=%v", len(body), err)
	}
}

func TestTemporaryGCDryRunPreviewsOrphanReconciliationWithoutWriting(t *testing.T) {
	a, _ := temporaryTestApp(t)
	if err := a.prepareModuleTemporaryStorage(a.reg["demo"]); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(temporaryStatePath(a.base))
	if err != nil {
		t.Fatal(err)
	}
	preview, err := a.gcTemporaryStorage(true)
	if err != nil || len(preview.Directories) != 1 || !preview.Directories[0].Reclaimable {
		t.Fatalf("orphan not previewed: %+v, %v", preview, err)
	}
	after, err := os.ReadFile(temporaryStatePath(a.base))
	if err != nil || string(after) != string(before) {
		t.Fatal("dry-run changed registration")
	}
	if err := a.reconcileTemporaryStorage(); err != nil {
		t.Fatal(err)
	}
	reconciled, err := a.gcTemporaryStorage(true)
	if err != nil || preview.Directories[0].Reclaimable != reconciled.Directories[0].Reclaimable {
		t.Fatal("dry-run disagrees with reconciled GC")
	}
}

func TestTemporaryTextCapacityShowsBtrfsInodesNotApplicable(t *testing.T) {
	bytes := uint64(9000)
	directory := TemporaryDirectoryStatus{TemporaryLease: TemporaryLease{Filesystem: TemporaryFilesystem{Type: "btrfs"}}, FreeBytes: &bytes}
	if text := temporaryCapacityText(directory); !strings.Contains(text, "9000 bytes") || !strings.Contains(text, "not applicable") {
		t.Fatal(text)
	}
	inodes := uint64(8)
	directory.Filesystem.Type = "ext4"
	directory.FreeInodes = &inodes
	if text := temporaryCapacityText(directory); !strings.Contains(text, "8 inodes") || strings.Contains(text, "not applicable") {
		t.Fatal(text)
	}
}
