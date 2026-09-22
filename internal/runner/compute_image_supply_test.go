package runner

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/anas-project/ANAS/internal/compose"
	"github.com/anas-project/ANAS/internal/computeimage"
)

func TestComputeImageSupplyJSONUsesFrozenResolution(t *testing.T) {
	target := computeimage.Target{Architecture: "amd64", Interface: "incus_container"}
	fingerprint := strings.Repeat("a", 64)
	recipe := strings.Repeat("b", 64)
	snapshot := &computeimage.Snapshot{Images: []computeimage.Resolution{{
		Reference: computeimage.Reference{Catalog: "anas", Name: "runner", Revision: "r1"},
		Target:    target, Fingerprint: fingerprint, CatalogDigest: strings.Repeat("c", 64), RecipeDigest: recipe,
	}}}
	release := computeimage.ArtifactRelease{
		Entry: computeimage.Entry{Catalog: "anas", Name: "runner", Revision: "r1", Target: target, Fingerprint: fingerprint, RecipeDigest: recipe},
		Artifact: computeimage.Artifact{Version: computeimage.ArtifactVersion, Target: target, Format: computeimage.ArtifactSplit, Fingerprint: fingerprint,
			Parts: []computeimage.ArtifactPart{{Role: "metadata", SHA256: strings.Repeat("d", 64), Size: 1}, {Role: "rootfs", SHA256: strings.Repeat("e", 64), Size: 1}}},
	}
	body, err := computeImageSupplyJSON(snapshot, []computeImageSupplyArtifact{{
		Release: release, MetadataPath: filepath.Join(t.TempDir(), "incus.tar.xz"), RootFSPath: filepath.Join(t.TempDir(), "rootfs.squashfs"),
	}})
	if err != nil {
		t.Fatal(err)
	}
	var doc computeImageSupplyFile
	if err := json.Unmarshal(body, &doc); err != nil {
		t.Fatal(err)
	}
	if doc.Version != computeimage.ImageSupplyVersion || len(doc.Images) != 1 || doc.Images[0].Resolution.Fingerprint != fingerprint {
		t.Fatalf("supply = %#v", doc)
	}
	release.Entry.RecipeDigest = strings.Repeat("f", 64)
	if _, err := computeImageSupplyJSON(snapshot, []computeImageSupplyArtifact{{Release: release, MetadataPath: "/tmp/meta", RootFSPath: "/tmp/rootfs"}}); err == nil {
		t.Fatal("accepted release metadata that conflicts with frozen catalog recipe")
	}
	if body, err := computeImageSupplyJSON(snapshot, nil); err != nil || body != nil {
		t.Fatalf("missing local artifact should produce no supply document, got %q err=%v", string(body), err)
	}
}

func TestComputeImagePruneDryRunRetainsCurrentPreviousAndRunning(t *testing.T) {
	a, b, c, d := strings.Repeat("a", 64), strings.Repeat("b", 64), strings.Repeat("c", 64), strings.Repeat("d", 64)
	current := manifestWithComputeImage(a)
	previous := manifestWithComputeImage(b)
	retained := computeImageRetainedFingerprints(current, previous, []string{c})
	if strings.Join(retained, ",") != strings.Join([]string{a, b, c}, ",") {
		t.Fatalf("retained = %v", retained)
	}
	plan, err := computeImagePruneDryRunPlan([]string{a, b, c, d, d}, retained)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Delete) != 1 || plan.Delete[0] != d {
		t.Fatalf("delete = %v", plan.Delete)
	}
	if _, err := computeImagePruneDryRunPlan([]string{"not-a-fingerprint"}, retained); err == nil {
		t.Fatal("invalid imported inventory was ignored")
	}
}

func manifestWithComputeImage(fingerprint string) *deploymentManifest {
	return &deploymentManifest{Resources: []deploymentResource{{Contract: "compute", ComputeImages: &computeimage.Snapshot{Images: []computeimage.Resolution{{Fingerprint: fingerprint}}}}}}
}

func TestComputeSupplyComposeRunMountsReadonlyBeforeService(t *testing.T) {
	a := computeApp(t, map[string]string{"forgejo": "anas-test"})
	a.base = filepath.Join(t.TempDir(), ".anas")
	modules := t.TempDir()
	providerDir := filepath.Join(modules, "incus")
	if err := os.MkdirAll(providerDir, 0700); err != nil {
		t.Fatal(err)
	}
	mod, err := loadModuleManifest(filepath.Join("..", "..", "modules", "incus"), "incus")
	if err != nil {
		t.Fatal(err)
	}
	a.reg["incus"] = mod
	if err := a.materializeResourceSecrets(); err != nil {
		t.Fatal(err)
	}
	target := computeimage.Target{Architecture: "amd64", Interface: "incus_vm"}
	metadata, rootfs := []byte("metadata"), []byte("rootfs")
	entry := computeimage.Entry{Catalog: "anas", Name: "runner", Revision: "r1", Target: target, Fingerprint: sha256Hex(append(metadata, rootfs...)), RecipeDigest: strings.Repeat("c", 64)}
	snapshot, err := computeimage.Freeze([]computeimage.Reference{{Catalog: "anas", Name: "runner", Revision: "r1"}}, target, []computeimage.Entry{entry}, nil)
	if err != nil {
		t.Fatal(err)
	}
	req := &a.resourceRequests[0]
	req.Spec["image_allowlist"] = []any{map[string]any{"catalog": "anas", "name": "runner", "revision": "r1"}}
	req.ComputeImages = snapshot
	writeProviderArtifactFixture(t, providerDir, entry, metadata, rootfs)
	if err := os.WriteFile(filepath.Join(providerDir, ".env"), []byte("INCUS_ENDPOINT="+a.env["INCUS_ENDPOINT"]+"\nINCUS_SERVER_CERT_B64="+a.env["INCUS_SERVER_CERT_B64"]+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(t.TempDir(), "compose-probe")
	body := `#!/bin/sh
set -eu
[ "$ANAS_RESOURCE_IMAGE_SUPPLY_FILE" = /run/anas/compute-image-supply.json ]
seen_service=0
volumes=0
while [ "$#" -gt 0 ]; do
    if [ "$1" = "anas_incus_provision" ]; then
        seen_service=1
    fi
    if [ "$1" = "--volume" ]; then
        [ "$seen_service" = 0 ]
        volumes=$((volumes + 1))
        shift
        case "$1" in
            *:/run/anas/compute-image-supply.json:ro|*:/run/anas/compute-image-supply:ro) ;;
            *) exit 72 ;;
        esac
    fi
    shift
done
[ "$volumes" -eq 2 ]
`
	if err := os.WriteFile(script, []byte(body), 0700); err != nil {
		t.Fatal(err)
	}
	a.compose = compose.CLI{Bin: []string{script}}
	previous := inspectComposeProjectOwners
	t.Cleanup(func() { inspectComposeProjectOwners = previous })
	inspectComposeProjectOwners = func(string) ([]string, error) { return nil, nil }
	if err := a.ensureResourcesFor("forgejo", modules); err != nil {
		t.Fatal(err)
	}
}

func TestComputeSupplyMissingLocalArtifactDoesNotRequireDockerMount(t *testing.T) {
	a := computeApp(t, map[string]string{"forgejo": "anas-test"})
	a.base = filepath.Join(t.TempDir(), ".anas")
	modules := t.TempDir()
	providerDir := filepath.Join(modules, "incus")
	if err := os.MkdirAll(providerDir, 0700); err != nil {
		t.Fatal(err)
	}
	mod, err := loadModuleManifest(filepath.Join("..", "..", "modules", "incus"), "incus")
	if err != nil {
		t.Fatal(err)
	}
	a.reg["incus"] = mod
	if err := a.materializeResourceSecrets(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(providerDir, ".env"), []byte("INCUS_ENDPOINT="+a.env["INCUS_ENDPOINT"]+"\nINCUS_SERVER_CERT_B64="+a.env["INCUS_SERVER_CERT_B64"]+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(t.TempDir(), "compose-probe")
	body := `#!/bin/sh
set -eu
[ "$ANAS_RESOURCE_IMAGE_SUPPLY_FILE" = /run/anas/compute-image-supply.json ]
for arg in "$@"; do
    [ "$arg" != "--volume" ] || exit 73
done
`
	if err := os.WriteFile(script, []byte(body), 0700); err != nil {
		t.Fatal(err)
	}
	a.compose = compose.CLI{Bin: []string{script}}
	previous := inspectComposeProjectOwners
	t.Cleanup(func() { inspectComposeProjectOwners = previous })
	inspectComposeProjectOwners = func(string) ([]string, error) { return nil, nil }
	if err := a.ensureResourcesFor("forgejo", modules); err != nil {
		t.Fatal(err)
	}
}

func TestCollectComputeSupplyUsesFrozenHistoricalResolution(t *testing.T) {
	providerDir := t.TempDir()
	target := computeimage.Target{Architecture: "amd64", Interface: "incus_container"}
	oldMeta, oldRoot := []byte("old metadata"), []byte("old rootfs")
	newMeta, newRoot := []byte("new metadata"), []byte("new rootfs")
	oldEntry := computeimage.Entry{Catalog: "anas", Name: "runner", Revision: "old", Target: target, Fingerprint: sha256Hex(append(oldMeta, oldRoot...)), RecipeDigest: strings.Repeat("a", 64)}
	newEntry := computeimage.Entry{Catalog: "anas", Name: "runner", Revision: "new", Target: target, Fingerprint: sha256Hex(append(newMeta, newRoot...)), RecipeDigest: strings.Repeat("b", 64)}
	writeProviderArtifactFixture(t, providerDir, oldEntry, oldMeta, oldRoot)
	writeProviderArtifactFixture(t, providerDir, newEntry, newMeta, newRoot)
	snapshot, err := computeimage.Freeze([]computeimage.Reference{{Catalog: "anas", Name: "runner", Revision: "old"}}, target, []computeimage.Entry{oldEntry, newEntry}, nil)
	if err != nil {
		t.Fatal(err)
	}
	artifacts, err := collectComputeImageSupplyArtifacts(context.Background(), providerDir, snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if len(artifacts) != 1 || artifacts[0].Release.Entry.Revision != "old" || artifacts[0].Release.Entry.Fingerprint != oldEntry.Fingerprint {
		t.Fatalf("artifact selection = %+v", artifacts)
	}
}

func TestCollectComputeSupplyRejectsChangedArtifactBytes(t *testing.T) {
	providerDir := t.TempDir()
	target := computeimage.Target{Architecture: "amd64", Interface: "incus_container"}
	metadata, rootfs := []byte("metadata"), []byte("rootfs")
	entry := computeimage.Entry{Catalog: "anas", Name: "runner", Revision: "r1", Target: target, Fingerprint: sha256Hex(append(metadata, rootfs...)), RecipeDigest: strings.Repeat("a", 64)}
	writeProviderArtifactFixture(t, providerDir, entry, metadata, rootfs)
	if err := os.Chmod(providerArtifactPath(providerDir, entry, "rootfs.squashfs"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(providerArtifactPath(providerDir, entry, "rootfs.squashfs"), []byte("tampered"), 0400); err != nil {
		t.Fatal(err)
	}
	snapshot, err := computeimage.Freeze([]computeimage.Reference{{Catalog: "anas", Name: "runner", Revision: "r1"}}, target, []computeimage.Entry{entry}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := collectComputeImageSupplyArtifacts(context.Background(), providerDir, snapshot); err == nil {
		t.Fatal("accepted artifact bytes that no longer match the release descriptor")
	}
}

func TestCollectComputeSupplyRejectsSymlinkAncestor(t *testing.T) {
	realProvider := t.TempDir()
	linkProvider := filepath.Join(t.TempDir(), "incus")
	if err := os.Symlink(realProvider, linkProvider); err != nil {
		t.Fatal(err)
	}
	target := computeimage.Target{Architecture: "amd64", Interface: "incus_container"}
	metadata, rootfs := []byte("metadata"), []byte("rootfs")
	entry := computeimage.Entry{Catalog: "anas", Name: "runner", Revision: "r1", Target: target, Fingerprint: sha256Hex(append(metadata, rootfs...)), RecipeDigest: strings.Repeat("a", 64)}
	writeProviderArtifactFixture(t, realProvider, entry, metadata, rootfs)
	snapshot, err := computeimage.Freeze([]computeimage.Reference{{Catalog: "anas", Name: "runner", Revision: "r1"}}, target, []computeimage.Entry{entry}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := collectComputeImageSupplyArtifacts(context.Background(), linkProvider, snapshot); err == nil {
		t.Fatal("accepted artifact path through a symlinked provider directory")
	}
}

func writeProviderArtifactFixture(t *testing.T, providerDir string, entry computeimage.Entry, metadata, rootfs []byte) {
	t.Helper()
	dir := filepath.Join(providerDir, "images", "artifacts", entry.Catalog, entry.Name, entry.Revision, entry.Architecture, entry.Interface)
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	metadataPath := filepath.Join(dir, "incus.tar.xz")
	rootfsName := "rootfs.squashfs"
	if entry.Interface == "incus_vm" {
		rootfsName = "disk.qcow2"
	}
	rootfsPath := filepath.Join(dir, rootfsName)
	if err := os.WriteFile(metadataPath, metadata, 0400); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(rootfsPath, rootfs, 0400); err != nil {
		t.Fatal(err)
	}
	release := computeimage.ArtifactRelease{
		Entry: entry,
		Artifact: computeimage.Artifact{Version: computeimage.ArtifactVersion, Target: entry.Target, Format: computeimage.ArtifactSplit, Fingerprint: entry.Fingerprint,
			Parts: []computeimage.ArtifactPart{{Role: "metadata", SHA256: sha256Hex(metadata), Size: int64(len(metadata))}, {Role: "rootfs", SHA256: sha256Hex(rootfs), Size: int64(len(rootfs))}}},
	}
	body, err := computeimage.EncodeArtifactRelease(release)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "artifact.json"), body, 0400); err != nil {
		t.Fatal(err)
	}
}

func providerArtifactPath(providerDir string, entry computeimage.Entry, name string) string {
	return filepath.Join(providerDir, "images", "artifacts", entry.Catalog, entry.Name, entry.Revision, entry.Architecture, entry.Interface, name)
}

func sha256Hex(body []byte) string {
	sum := sha256.Sum256(body)
	return fmt.Sprintf("%x", sum[:])
}
