package computeimage

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func TestArtifactBundleExportsEveryFrozenTargetAndHistory(t *testing.T) {
	archive, ref, target, sources := newArtifactArchiveFixture(t)
	ctx := context.Background()
	old, _, err := archive.Record(ctx, ref, target, []byte("old recipe"), ArtifactSplit, sources)
	if err != nil {
		t.Fatal(err)
	}
	ref.Revision = "r2"
	for _, arch := range []string{"amd64", "arm64"} {
		for _, iface := range []string{"incus_container", "incus_vm"} {
			if err := os.WriteFile(sources[0], []byte("metadata "+arch+" "+iface), 0600); err != nil {
				t.Fatal(err)
			}
			if _, _, err := archive.Record(ctx, ref, Target{Architecture: arch, Interface: iface}, []byte("new recipe"), ArtifactSplit, sources); err != nil {
				t.Fatal(err)
			}
		}
	}
	destination := filepath.Join(t.TempDir(), "images")
	bundle, err := archive.ExportBundle(ctx, []Entry{old.Entry}, destination)
	if err != nil {
		t.Fatal(err)
	}
	entries, err := ReadArtifactCatalog(ctx, filepath.Join(destination, "catalog.json"))
	if err != nil || len(entries) != 5 || bundle.ImageCount != 5 {
		t.Fatalf("bundle lost a target or history: %+v entries=%d err=%v", bundle, len(entries), err)
	}
	catalog, err := NewCatalog(entries, nil)
	if err != nil || catalog.digest != bundle.CatalogDigest {
		t.Fatalf("bundle summary does not describe its catalog: %v", err)
	}
	for _, entry := range entries {
		ref := Reference{Catalog: entry.Catalog, Name: entry.Name, Revision: entry.Revision}
		dir := filepath.Join(destination, "artifacts", entry.Catalog, entry.Name, entry.Revision, entry.Architecture, entry.Interface)
		body, err := os.ReadFile(filepath.Join(dir, "artifact.json"))
		if err != nil {
			t.Fatal(err)
		}
		release, err := DecodeArtifactRelease(body)
		if err != nil || release.Entry != entry {
			t.Fatalf("release descriptor differs from catalog: %v", err)
		}
		resolved, err := Resolve([]Reference{ref}, entry.Target, catalog)
		if err != nil {
			t.Fatal(err)
		}
		var readers []io.Reader
		for _, part := range release.Artifact.Parts {
			path := filepath.Join(dir, exportPartName(release.Artifact, part.Role))
			body, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			info, err := os.Stat(path)
			if err != nil || info.Mode().Perm() != 0400 {
				t.Fatalf("artifact is not read-only: %v", err)
			}
			readers = append(readers, bytes.NewReader(body))
		}
		if err := VerifyArtifactRelease(ctx, release, resolved[0], readers); err != nil {
			t.Fatalf("export changed frozen bytes: %v", err)
		}
	}
}

func TestArtifactBundleRefusesAmbiguousOrIncompleteOutput(t *testing.T) {
	for _, scenario := range []string{"empty", "unified", "history", "existing", "symlink", "canceled", "corrupt"} {
		t.Run(scenario, func(t *testing.T) {
			archive, ref, target, sources := newArtifactArchiveFixture(t)
			ctx := context.Background()
			destination := filepath.Join(t.TempDir(), "images")
			var previous []Entry
			if scenario != "empty" {
				format, paths := ArtifactSplit, sources
				if scenario == "unified" {
					format, paths = ArtifactUnified, sources[:1]
				}
				release, _, err := archive.Record(ctx, ref, target, []byte("recipe"), format, paths)
				if err != nil {
					t.Fatal(err)
				}
				if scenario == "history" {
					old := release.Entry
					old.Revision = "missing-history"
					previous = []Entry{old}
				}
				if scenario == "corrupt" {
					object := filepath.Join(archive.path, artifactObjectFilename(release.Artifact.Parts[0].SHA256))
					if err := os.Chmod(object, 0600); err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(object, []byte("BROKEN!!"), 0600); err != nil {
						t.Fatal(err)
					}
					if err := os.Chmod(object, 0400); err != nil {
						t.Fatal(err)
					}
				}
			}
			if scenario == "existing" {
				if err := os.Mkdir(destination, 0700); err != nil {
					t.Fatal(err)
				}
			}
			if scenario == "symlink" {
				if err := os.Symlink(t.TempDir(), destination); err != nil {
					t.Fatal(err)
				}
			}
			if scenario == "canceled" {
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			if _, err := archive.ExportBundle(ctx, previous, destination); err == nil {
				t.Fatal("exported an invalid bundle")
			}
			if _, err := os.Lstat(filepath.Join(destination, "catalog.json")); !os.IsNotExist(err) {
				t.Fatal("failed bundle published a catalog")
			}
			if scenario != "existing" && scenario != "symlink" {
				if _, err := os.Lstat(destination); !os.IsNotExist(err) {
					t.Fatal("preflight failure created a destination")
				}
			}
		})
	}
}

func TestArtifactExportPinsDestinationDirectory(t *testing.T) {
	archive, ref, target, sources := newArtifactArchiveFixture(t)
	release, _, err := archive.Record(context.Background(), ref, target, []byte("recipe"), ArtifactSplit, sources)
	if err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(t.TempDir(), "output")
	output, err := newArtifactExportRoot(destination)
	if err != nil {
		t.Fatal(err)
	}
	defer output.root.Close()
	if err := os.Rename(destination, destination+"-original"); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(destination, 0700); err != nil {
		t.Fatal(err)
	}
	if err := archive.exportPart(context.Background(), release.Artifact.Parts[0], output.root, "metadata"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(filepath.Join(destination, "metadata")); !os.IsNotExist(err) {
		t.Fatal("opened export destination followed a replacement directory")
	}
	if _, err := os.Stat(filepath.Join(destination+"-original", "metadata")); err != nil {
		t.Fatal("export did not stay in its originally opened directory")
	}
	if err := output.check(); !errors.Is(err, ErrArtifactUnavailable) {
		t.Fatalf("replaced destination could be reported successful: %v", err)
	}
}
