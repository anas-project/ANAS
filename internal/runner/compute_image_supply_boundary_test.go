package runner

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/anas-project/ANAS/internal/computeimage"
)

func supplyBoundaryFixture(t *testing.T, copies int) (*app, string, ResourceRequest) {
	t.Helper()
	providerDir := t.TempDir()
	metadata, rootfs := []byte("metadata"), []byte("root filesystem")
	target := computeimage.Target{Architecture: "amd64", Interface: "incus_container"}
	entry := computeimage.Entry{Catalog: "anas", Name: "runner", Revision: "r1", Target: target,
		Fingerprint: sha256Hex(append(metadata, rootfs...)), RecipeDigest: strings.Repeat("a", 64)}
	refs := make([]computeimage.Reference, copies)
	for i := range refs {
		refs[i] = computeimage.Reference{Catalog: entry.Catalog, Name: entry.Name, Revision: entry.Revision}
	}
	snapshot, err := computeimage.Freeze(refs, target, []computeimage.Entry{entry}, nil)
	if err != nil {
		t.Fatal(err)
	}
	writeProviderArtifactFixture(t, providerDir, entry, metadata, rootfs)
	return &app{base: t.TempDir()}, providerDir, ResourceRequest{Contract: "compute", ComputeImages: snapshot}
}

func TestComputeSupplyExposesOnlyVerifiedPublicFilesToUnprivilegedProvider(t *testing.T) {
	a, provider, request := supplyBoundaryFixture(t, 1)
	if err := os.Chmod(provider, 0700); err != nil {
		t.Fatal(err)
	}
	mount, cleanup, err := a.prepareComputeImageSupply(provider, request)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	if mount == nil {
		t.Fatal("missing supply")
	}
	// The real Provider runs as UID/GID 65532, unlike Core. A root-owned
	// 0400 file or 0500 directory is unreadable through its read-only mount.
	// These two mounts contain only verified, non-secret image artifacts.
	for _, path := range []string{mount.hostDescriptor, mount.hostRoot} {
		err := filepath.WalkDir(path, func(path string, entry os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			info, err := entry.Info()
			if err != nil {
				return err
			}
			want := os.FileMode(0444)
			if info.IsDir() {
				want = 0555
			}
			if info.Mode().Perm() != want {
				t.Errorf("public mount %s has permissions %04o, need %04o for non-root Provider", filepath.Base(path), info.Mode().Perm(), want)
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	for _, path := range []string{filepath.Dir(mount.hostRoot), provider} {
		info, err := os.Stat(path)
		if err != nil || info.Mode().Perm() != 0700 {
			t.Fatal("public artifact projection relaxed its private staging/source parent", err)
		}
	}
	original := providerArtifactPath(provider, request.ComputeImages.Catalog[0], "rootfs.squashfs")
	info, err := os.Stat(original)
	if err != nil || info.Mode().Perm() != 0400 {
		t.Fatal("projection changed original artifact permissions", err)
	}
}

func TestComputeSupplyRepeatedFrozenImageStagesOnce(t *testing.T) {
	a, providerDir, request := supplyBoundaryFixture(t, 2)
	request.ComputeImages.Bindings = map[string]string{
		"node":   request.ComputeImages.Images[0].Fingerprint,
		"python": request.ComputeImages.Images[1].Fingerprint,
	}
	mount, cleanup, err := a.prepareComputeImageSupply(providerDir, request)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	if mount == nil {
		t.Fatal("repeated references lost their artifact supply")
	}
	body, err := os.ReadFile(mount.hostDescriptor)
	if err != nil {
		t.Fatal(err)
	}
	var doc computeImageSupplyFile
	if err := json.Unmarshal(body, &doc); err != nil || len(doc.Images) != 1 {
		t.Fatalf("physical supply must be deduplicated: %s, %v", body, err)
	}
	if len(request.ComputeImages.Images) != 2 || len(request.ComputeImages.Bindings) != 2 {
		t.Fatal("staging changed the frozen deployment bindings")
	}
	cleanup()
	if _, err := os.Lstat(mount.hostRoot); !os.IsNotExist(err) {
		t.Fatal("staging cleanup left image bytes")
	}
}

func TestComputeSupplyUnifiedArtifactFailsWithoutPanic(t *testing.T) {
	a, providerDir, request := supplyBoundaryFixture(t, 1)
	entry := request.ComputeImages.Catalog[0]
	release, err := computeimage.DescribeArtifactRelease(context.Background(),
		computeimage.Entry{Catalog: entry.Catalog, Name: entry.Name, Revision: entry.Revision, Target: entry.Target, RecipeDigest: entry.RecipeDigest},
		computeimage.ArtifactUnified, []io.Reader{bytes.NewReader([]byte("metadata"))})
	if err != nil {
		t.Fatal(err)
	}
	body, err := computeimage.EncodeArtifactRelease(release)
	if err != nil {
		t.Fatal(err)
	}
	path := providerArtifactPath(providerDir, entry, "artifact.json")
	if err := os.Chmod(path, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, body, 0600); err != nil {
		t.Fatal(err)
	}
	request.ComputeImages, err = computeimage.Freeze([]computeimage.Reference{request.ComputeImages.Images[0].Reference}, entry.Target, []computeimage.Entry{release.Entry}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if recovered := recover(); recovered != nil {
			t.Errorf("unsupported artifact panicked instead of failing closed: %v", recovered)
		}
	}()
	if _, _, err := a.prepareComputeImageSupply(providerDir, request); err == nil {
		t.Fatal("accepted a unified artifact on the split-only provider path")
	}
}

func TestComputeSupplyCanceledBeforeStaging(t *testing.T) {
	a, providerDir, request := supplyBoundaryFixture(t, 1)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	a.commandContext = ctx
	mount, cleanup, err := a.prepareComputeImageSupply(providerDir, request)
	if cleanup != nil {
		defer cleanup()
	}
	if !errors.Is(err, context.Canceled) || mount != nil {
		t.Fatalf("canceled apply created supply: mount=%v err=%v", mount, err)
	}
	if _, err := os.Lstat(filepath.Join(a.base, "tmp")); !os.IsNotExist(err) {
		t.Fatal("canceled apply created a staging directory")
	}
}

func TestComputeSupplyNamedAliasesValidateBeforeDeduplication(t *testing.T) {
	for _, drift := range []bool{false, true} {
		t.Run(map[bool]string{false: "same-bytes", true: "drifted-alias"}[drift], func(t *testing.T) {
			a, providerDir, request := supplyBoundaryFixture(t, 1)
			entry := request.ComputeImages.Catalog[0]
			alias := entry
			alias.Revision = "r2"
			alias.RecipeDigest = strings.Repeat("b", 64)
			refs := []computeimage.Reference{request.ComputeImages.Images[0].Reference, {Catalog: alias.Catalog, Name: alias.Name, Revision: alias.Revision}}
			var err error
			request.ComputeImages, err = computeimage.Freeze(refs, entry.Target, []computeimage.Entry{entry, alias}, nil)
			if err != nil {
				t.Fatal(err)
			}
			if drift {
				alias.RecipeDigest = strings.Repeat("c", 64)
			}
			writeProviderArtifactFixture(t, providerDir, alias, []byte("metadata"), []byte("root filesystem"))
			mount, cleanup, err := a.prepareComputeImageSupply(providerDir, request)
			if cleanup != nil {
				defer cleanup()
			}
			if drift {
				if err == nil || mount != nil {
					t.Fatal("deduplication hid a named revision mismatch")
				}
				return
			}
			if err != nil || mount == nil {
				t.Fatalf("equivalent named revisions could not share bytes: %v", err)
			}
			var doc computeImageSupplyFile
			body, err := os.ReadFile(mount.hostDescriptor)
			if err != nil || json.Unmarshal(body, &doc) != nil || len(doc.Images) != 1 {
				t.Fatalf("duplicate physical supply: %v", err)
			}
		})
	}
}

type cancelAfterSupplyOutput struct {
	context.Context
	cancel   context.CancelFunc
	path     string
	observed int64
}

func (c *cancelAfterSupplyOutput) Err() error {
	if info, err := os.Stat(c.path); err == nil && info.Size() > 0 {
		c.observed = info.Size()
		c.cancel()
	}
	return c.Context.Err()
}

func TestComputeSupplyCancellationDuringCopyRemovesPartialFile(t *testing.T) {
	data := bytes.Repeat([]byte("image fixture bytes\n"), 65536)
	source, destination := filepath.Join(t.TempDir(), "source"), filepath.Join(t.TempDir(), "destination")
	if err := os.WriteFile(source, data, 0400); err != nil {
		t.Fatal(err)
	}
	base, cancel := context.WithCancel(context.Background())
	defer cancel()
	ctx := &cancelAfterSupplyOutput{Context: base, cancel: cancel, path: destination}
	part := computeimage.ArtifactPart{Role: "rootfs", SHA256: sha256Hex(data), Size: int64(len(data))}
	if err := copySupplyFileVerified(ctx, source, destination, part); !errors.Is(err, context.Canceled) {
		t.Fatalf("stream ignored cancellation: %v", err)
	}
	if ctx.observed <= 0 || ctx.observed >= int64(len(data)) {
		t.Fatalf("cancellation did not interrupt a partial copy: %d", ctx.observed)
	}
	if _, err := os.Lstat(destination); !os.IsNotExist(err) {
		t.Fatal("cancellation retained partial image bytes")
	}
}

func TestComputeSupplyConsumesExportedBundleWithoutRebuilding(t *testing.T) {
	ctx := context.Background()
	archive, err := computeimage.OpenArtifactArchive(ctx, filepath.Join(t.TempDir(), "archive"), true)
	if err != nil {
		t.Fatal(err)
	}
	defer archive.Close()
	sourceDir, providerDir := t.TempDir(), t.TempDir()
	metadata, rootfs := filepath.Join(sourceDir, "metadata"), filepath.Join(sourceDir, "rootfs")
	for path, body := range map[string]string{metadata: "metadata", rootfs: "root filesystem"} {
		if err := os.WriteFile(path, []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	ref := computeimage.Reference{Catalog: "anas", Name: "runner", Revision: "historical"}
	target := computeimage.Target{Architecture: "amd64", Interface: "incus_container"}
	release, _, err := archive.Record(ctx, ref, target, []byte("reviewed recipe"), computeimage.ArtifactSplit, []string{metadata, rootfs})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := archive.ExportBundle(ctx, []computeimage.Entry{release.Entry}, filepath.Join(providerDir, "images")); err != nil {
		t.Fatal(err)
	}
	entries, err := computeimage.ReadArtifactCatalog(ctx, filepath.Join(providerDir, "images", "catalog.json"))
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := computeimage.Freeze([]computeimage.Reference{ref}, target, entries, nil)
	if err != nil {
		t.Fatal(err)
	}
	a := &app{base: t.TempDir()}
	mount, cleanup, err := a.prepareComputeImageSupply(providerDir, ResourceRequest{Contract: "compute", ComputeImages: snapshot})
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	if mount == nil {
		t.Fatal("published layout cannot supply its frozen image")
	}
	bytes, err := os.ReadFile(filepath.Join(mount.hostRoot, release.Entry.Fingerprint, "rootfs.squashfs"))
	if err != nil || string(bytes) != "root filesystem" {
		t.Fatalf("frozen artifact changed between archive and Provider staging: %v", err)
	}
}
