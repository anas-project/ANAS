package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/anas-project/ANAS/internal/computeimage"
)

func TestEnsureRefusesWrongImageBeforeRegisteringTrust(t *testing.T) {
	for _, field := range []string{"fingerprint", "architecture", "type"} {
		t.Run(field, func(t *testing.T) {
			d := newFakeDaemon(t)
			d.imageFilter = func(image *imageRecord) {
				switch field {
				case "fingerprint":
					image.Fingerprint = strings.Repeat("d", 64)
				case "architecture":
					image.Architecture = "aarch64"
				case "type":
					image.Type = "container"
				}
			}
			if _, err := ensure(context.Background(), d.clientFor(t), testLease(t, "vm")); err == nil {
				t.Fatal("accepted incompatible image")
			}
			if len(d.certificates) > 0 {
				t.Fatal("registered trust after image verification failed")
			}
		})
	}
}

func TestEnsureImportsMissingImageFromVerifiedSupply(t *testing.T) {
	d := newFakeDaemon(t)
	l := testLease(t, "container")
	metadata := []byte("metadata fixture")
	rootfs := []byte("rootfs fixture")
	sum := sha256String(append(metadata, rootfs...))
	l.ImageAllowlist = []string{sum}
	d.missingImages[sum] = true
	l.ImageSupplyFile = writeSupplyFixture(t, l, metadata, rootfs, strings.Repeat("c", 64))
	if _, err := ensure(context.Background(), d.clientFor(t), l); err != nil {
		t.Fatalf("ensure: %v", err)
	}
	if d.asyncWaits != 1 {
		t.Fatalf("image import did not wait for operation completion: waits=%d", d.asyncWaits)
	}
	if _, ok := d.importedImages[sum]; !ok {
		t.Fatal("missing image was not imported from supplied bytes")
	}
}

func TestEnsureRejectsSupplyThatDoesNotMatchFrozenFingerprint(t *testing.T) {
	d := newFakeDaemon(t)
	l := testLease(t, "container")
	metadata := []byte("metadata fixture")
	rootfs := []byte("rootfs fixture")
	l.ImageAllowlist = []string{strings.Repeat("b", 64)}
	d.missingImages[l.ImageAllowlist[0]] = true
	l.ImageSupplyFile = writeSupplyFixture(t, l, metadata, rootfs, strings.Repeat("c", 64))
	if _, err := ensure(context.Background(), d.clientFor(t), l); err == nil {
		t.Fatal("accepted supply whose bytes did not match the frozen fingerprint")
	}
	if len(d.importedImages) != 0 || len(d.certificates) != 0 {
		t.Fatal("invalid supply changed daemon state")
	}
}

func TestEnsureRejectsImportOperationFingerprintMismatchBeforeTrust(t *testing.T) {
	d := newFakeDaemon(t)
	l := testLease(t, "container")
	metadata := []byte("metadata fixture")
	rootfs := []byte("rootfs fixture")
	sum := sha256String(append(metadata, rootfs...))
	l.ImageAllowlist = []string{sum}
	d.missingImages[sum] = true
	d.importFingerprintOverride = strings.Repeat("f", 64)
	l.ImageSupplyFile = writeSupplyFixture(t, l, metadata, rootfs, strings.Repeat("c", 64))
	if _, err := ensure(context.Background(), d.clientFor(t), l); err == nil {
		t.Fatal("accepted import operation with the wrong returned fingerprint")
	}
	if len(d.certificates) != 0 {
		t.Fatal("registered trust after import operation mismatch")
	}
}

func TestLoadImageSupplyRejectsHostileJSON(t *testing.T) {
	for name, mutate := range map[string]func(string) []byte{
		"duplicate": func(valid string) []byte {
			return []byte(strings.Replace(valid, `"version":`, `"version":"`+computeimage.ImageSupplyVersion+`","version":`, 1))
		},
		"case alias": func(valid string) []byte {
			return []byte(strings.Replace(valid, `"version":`, `"Version":`, 1))
		},
		"trailing": func(valid string) []byte {
			return []byte(strings.TrimRight(valid, "\n") + "\n{}\n")
		},
		"null": func(valid string) []byte {
			return []byte(strings.Replace(valid, `"images":[`, `"images":[null,`, 1))
		},
	} {
		t.Run(name, func(t *testing.T) {
			l := testLease(t, "container")
			path := writeSupplyFixture(t, l, []byte("metadata"), []byte("rootfs"), strings.Repeat("c", 64))
			valid, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.Chmod(path, 0600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, mutate(string(valid)), 0400); err != nil {
				t.Fatal(err)
			}
			if err := os.Chmod(path, 0400); err != nil {
				t.Fatal(err)
			}
			if _, err := loadImageSupply(path); err == nil {
				t.Fatal("hostile JSON was accepted")
			}
		})
	}
}

func TestOpenSuppliedReadersRejectsUnsafeFiles(t *testing.T) {
	for _, scenario := range []string{"symlink", "fifo", "short"} {
		t.Run(scenario, func(t *testing.T) {
			l := testLease(t, "container")
			path := writeSupplyFixture(t, l, []byte("metadata"), []byte("rootfs"), strings.Repeat("c", 64))
			supply, err := loadImageSupply(path)
			if err != nil {
				t.Fatal(err)
			}
			target := supply.Images[0].RootFSPath
			if err := os.Remove(target); err != nil {
				t.Fatal(err)
			}
			switch scenario {
			case "symlink":
				if err := os.Symlink("/etc/passwd", target); err != nil {
					t.Fatal(err)
				}
			case "fifo":
				if err := syscall.Mkfifo(target, 0400); err != nil {
					t.Fatal(err)
				}
			case "short":
				if err := os.WriteFile(target, []byte("x"), 0400); err != nil {
					t.Fatal(err)
				}
			}
			if _, _, err := openSuppliedReaders(supply.Images[0]); err == nil {
				t.Fatal("unsafe supplied file was accepted")
			}
		})
	}
}

func TestOpenSuppliedReadersRejectsUnsafeSupplyDirectories(t *testing.T) {
	for _, scenario := range []string{"writable root", "symlink ancestor"} {
		t.Run(scenario, func(t *testing.T) {
			l := testLease(t, "container")
			path := writeSupplyFixture(t, l, []byte("metadata"), []byte("rootfs"), strings.Repeat("c", 64))
			supply, err := loadImageSupply(path)
			if err != nil {
				t.Fatal(err)
			}
			switch scenario {
			case "writable root":
				if err := os.Chmod(imageSupplyRoot, 0770); err != nil {
					t.Fatal(err)
				}
			case "symlink ancestor":
				realDir := filepath.Join(imageSupplyRoot, "real")
				if err := os.Rename(filepath.Join(imageSupplyRoot, "image"), realDir); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(realDir, filepath.Join(imageSupplyRoot, "image")); err != nil {
					t.Fatal(err)
				}
			}
			if _, _, err := openSuppliedReaders(supply.Images[0]); err == nil {
				t.Fatal("unsafe supply directory was accepted")
			}
		})
	}
}

func TestCopyImagePartDetectsPathReplacementAfterOpen(t *testing.T) {
	l := testLease(t, "container")
	path := writeSupplyFixture(t, l, []byte("metadata"), []byte("rootfs"), strings.Repeat("c", 64))
	supply, err := loadImageSupply(path)
	if err != nil {
		t.Fatal(err)
	}
	parts, _, err := openSuppliedReaders(supply.Images[0])
	if err != nil {
		t.Fatal(err)
	}
	defer parts[0].file.Close()
	if err := os.Remove(parts[0].path); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(parts[0].path, []byte("metadata"), 0400); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	writer := multipart.NewWriter(&out)
	err = copyImagePart(writer, "metadata", "incus.tar.xz", parts[0])
	_ = writer.Close()
	if err == nil {
		t.Fatal("path replacement after verification was accepted")
	}
}

func TestImagePrunePlanKeepsCurrentPreviousAndRunning(t *testing.T) {
	a, b, c, d := strings.Repeat("a", 64), strings.Repeat("b", 64), strings.Repeat("c", 64), strings.Repeat("d", 64)
	plan, err := planImagePrune([]string{a, b, c, d, d}, []string{a}, []string{b}, []string{c})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(plan.Retain, ",") != strings.Join([]string{a, b, c}, ",") {
		t.Fatalf("retain = %v", plan.Retain)
	}
	if len(plan.Delete) != 1 || plan.Delete[0] != d {
		t.Fatalf("delete = %v", plan.Delete)
	}
	if _, err := planImagePrune([]string{"invalid"}, []string{a}, nil, nil); err == nil {
		t.Fatal("invalid imported inventory was ignored")
	}
}

func writeSupplyFixture(t *testing.T, l lease, metadata, rootfs []byte, recipeDigest string) string {
	t.Helper()
	root := t.TempDir()
	oldFile, oldRoot := defaultImageSupplyFile, imageSupplyRoot
	defaultImageSupplyFile = filepath.Join(root, "compute-image-supply.json")
	imageSupplyRoot = filepath.Join(root, "artifacts")
	t.Cleanup(func() {
		defaultImageSupplyFile, imageSupplyRoot = oldFile, oldRoot
	})
	if err := os.MkdirAll(filepath.Join(imageSupplyRoot, "image"), 0700); err != nil {
		t.Fatal(err)
	}
	metadataPath := filepath.Join(imageSupplyRoot, "image", "incus.tar.xz")
	rootfsPath := filepath.Join(imageSupplyRoot, "image", "rootfs.squashfs")
	if err := os.WriteFile(metadataPath, metadata, 0400); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(rootfsPath, rootfs, 0400); err != nil {
		t.Fatal(err)
	}
	fingerprint := sha256String(append(metadata, rootfs...))
	target := computeimage.Target{Architecture: l.ImageArchitecture, Interface: "incus_" + l.Isolation}
	release := computeimage.ArtifactRelease{
		Entry: computeimage.Entry{Catalog: "anas", Name: "runner", Revision: "r1", Target: target, Fingerprint: fingerprint, RecipeDigest: recipeDigest},
		Artifact: computeimage.Artifact{Version: computeimage.ArtifactVersion, Target: target, Format: computeimage.ArtifactSplit, Fingerprint: fingerprint,
			Parts: []computeimage.ArtifactPart{{Role: "metadata", SHA256: sha256String(metadata), Size: int64(len(metadata))}, {Role: "rootfs", SHA256: sha256String(rootfs), Size: int64(len(rootfs))}}},
	}
	doc := localImageSupply{Version: computeimage.ImageSupplyVersion, Images: []localSupplyImage{{
		SuppliedImage: computeimage.SuppliedImage{Resolution: computeimage.Resolution{
			Reference: computeimage.Reference{Catalog: "anas", Name: "runner", Revision: "r1"}, Target: target, Fingerprint: l.ImageAllowlist[0],
			CatalogDigest: strings.Repeat("d", 64), RecipeDigest: recipeDigest,
		}, Release: release},
		MetadataPath: metadataPath, RootFSPath: rootfsPath,
	}}}
	body, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	path := defaultImageSupplyFile
	if err := os.WriteFile(path, append(body, '\n'), 0400); err != nil {
		t.Fatal(err)
	}
	return path
}

func sha256String(body []byte) string {
	sum := sha256.Sum256(body)
	return fmt.Sprintf("%x", sum[:])
}
