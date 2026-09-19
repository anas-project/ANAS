package computeimage

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func newArtifactArchiveFixture(t *testing.T) (*ArtifactArchive, Reference, Target, []string) {
	t.Helper()
	if !artifactArchiveSupported() {
		t.Skip("artifact archive requires Linux or macOS")
	}
	base := t.TempDir()
	archive, err := OpenArtifactArchive(context.Background(), filepath.Join(base, "archive"), true)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := archive.Close(); err != nil {
			t.Error(err)
		}
	})
	paths := []string{filepath.Join(base, "metadata"), filepath.Join(base, "rootfs")}
	for i, body := range []string{"metadata", "rootfs"} {
		if err := os.WriteFile(paths[i], []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	return archive, Reference{Catalog: "anas", Name: "fixture", Revision: "r1"}, Target{Architecture: "amd64", Interface: "incus_container"}, paths
}

func TestArtifactArchiveRecordRestoresOnlyIdenticalBytes(t *testing.T) {
	archive, ref, target, sources := newArtifactArchiveFixture(t)
	ctx := context.Background()
	release, existing, err := archive.Record(ctx, ref, target, []byte("recipe"), ArtifactSplit, sources)
	if err != nil || existing {
		t.Fatalf("initial record: existing=%v, err=%v", existing, err)
	}
	name, err := artifactReleaseFilename(ref, target)
	if err != nil {
		t.Fatal(err)
	}
	before, err := archive.root.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	inspected, err := archive.Inspect(ctx, ref, target)
	if err != nil || !reflect.DeepEqual(inspected, release) {
		t.Fatalf("inspect: %#v, %v", inspected, err)
	}
	missing := artifactObjectFilename(release.Artifact.Parts[1].SHA256)
	if err := archive.root.Remove(missing); err != nil {
		t.Fatal(err)
	}
	if _, err := archive.Inspect(ctx, ref, target); !errors.Is(err, ErrArtifactUnavailable) {
		t.Fatalf("missing bytes must fail: %v", err)
	}
	restored, existing, err := archive.Record(ctx, ref, target, []byte("recipe"), ArtifactSplit, sources)
	if err != nil || !existing || !reflect.DeepEqual(restored, release) {
		t.Fatalf("restore: %#v, existing=%v, err=%v", restored, existing, err)
	}
	after, err := archive.root.ReadFile(name)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("recovery changed the immutable revision record")
	}
	if err := os.WriteFile(sources[1], []byte("different image bytes"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := archive.Record(ctx, ref, target, []byte("recipe"), ArtifactSplit, sources); !errors.Is(err, ErrArtifactConflict) {
		t.Fatalf("revision byte conflict: %v", err)
	}
	if _, _, err := archive.Record(ctx, ref, target, []byte("different recipe"), ArtifactSplit, sources); !errors.Is(err, ErrArtifactConflict) {
		t.Fatalf("revision recipe conflict: %v", err)
	}
	final, err := archive.Inspect(ctx, ref, target)
	if err != nil || !reflect.DeepEqual(final, release) {
		t.Fatal("a conflicting record changed the published artifact")
	}
}

func TestArtifactArchiveExportRestoresRecordedBytes(t *testing.T) {
	archive, ref, target, sources := newArtifactArchiveFixture(t)
	release, _, err := archive.Record(context.Background(), ref, target, []byte("recipe"), ArtifactSplit, sources)
	if err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(t.TempDir(), "export")
	exported, err := archive.Export(context.Background(), ref, target, destination)
	if err != nil {
		t.Fatal(err)
	}
	if exported.Release.Entry != release.Entry || exported.MetadataPath != filepath.Join(destination, "incus.tar.xz") || exported.RootFSPath != filepath.Join(destination, "rootfs.squashfs") {
		t.Fatalf("export metadata = %#v", exported)
	}
	for _, pair := range [][2]string{{sources[0], exported.MetadataPath}, {sources[1], exported.RootFSPath}} {
		want, _ := os.ReadFile(pair[0])
		got, err := os.ReadFile(pair[1])
		if err != nil || !bytes.Equal(got, want) {
			t.Fatalf("exported bytes changed for %s", pair[1])
		}
	}
	body, err := os.ReadFile(filepath.Join(destination, "artifact.json"))
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeArtifactRelease(body)
	if err != nil || !reflect.DeepEqual(decoded, release) {
		t.Fatalf("export descriptor changed: %#v %v", decoded, err)
	}
	if _, err := archive.Export(context.Background(), ref, target, destination); !errors.Is(err, ErrArtifactConflict) {
		t.Fatalf("export adopted an existing directory: %v", err)
	}
}

func TestArtifactArchiveDoesNotOverwriteCorruption(t *testing.T) {
	archive, ref, target, sources := newArtifactArchiveFixture(t)
	ctx := context.Background()
	release, _, err := archive.Record(ctx, ref, target, []byte("recipe"), ArtifactSplit, sources)
	if err != nil {
		t.Fatal(err)
	}
	object := filepath.Join(archive.path, artifactObjectFilename(release.Artifact.Parts[1].SHA256))
	if err := os.Chmod(object, 0600); err != nil {
		t.Fatal(err)
	}
	corrupt := []byte("BROKEN")
	if err := os.WriteFile(object, corrupt, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(object, 0400); err != nil {
		t.Fatal(err)
	}
	if _, _, err := archive.Record(ctx, ref, target, []byte("recipe"), ArtifactSplit, sources); !errors.Is(err, ErrArtifactUnavailable) {
		t.Fatalf("corrupt object error: %v", err)
	}
	body, err := os.ReadFile(object)
	if err != nil || !bytes.Equal(body, corrupt) {
		t.Fatal("corrupt, pre-existing object was overwritten or deleted")
	}
	if _, err := archive.Catalog(ctx, nil); !errors.Is(err, ErrArtifactUnavailable) {
		t.Fatalf("catalog accepted corrupt bytes: %v", err)
	}
}

func TestArtifactArchiveCatalogProtectsPreviousHistory(t *testing.T) {
	archive, ref, target, sources := newArtifactArchiveFixture(t)
	ctx := context.Background()
	first, _, err := archive.Record(ctx, ref, target, []byte("recipe"), ArtifactSplit, sources)
	if err != nil {
		t.Fatal(err)
	}
	previous := []Entry{first.Entry}
	ref.Revision = "r2"
	second, _, err := archive.Record(ctx, ref, target, []byte("recipe revision two"), ArtifactSplit, sources)
	if err != nil {
		t.Fatal(err)
	}
	entries, err := archive.Catalog(ctx, previous)
	if err != nil || !reflect.DeepEqual(entries, []Entry{first.Entry, second.Entry}) {
		t.Fatalf("catalog: %#v, %v", entries, err)
	}
	missing := first.Entry
	missing.Revision = "lost-history"
	if _, err := archive.Catalog(ctx, []Entry{missing}); !errors.Is(err, ErrArtifactConflict) {
		t.Fatalf("missing prior revision was silently dropped: %v", err)
	}
	changed := first.Entry
	changed.Fingerprint = artifactTestDigest("another artifact")
	if _, err := archive.Catalog(ctx, []Entry{changed}); !errors.Is(err, ErrArtifactConflict) {
		t.Fatalf("changed prior fingerprint was accepted: %v", err)
	}
	unknown := filepath.Join(archive.path, "releases", "unknown.json")
	if err := os.WriteFile(unknown, []byte("{}\n"), 0400); err != nil {
		t.Fatal(err)
	}
	if _, err := archive.Catalog(ctx, previous); !errors.Is(err, ErrArtifactUnavailable) {
		t.Fatalf("unknown metadata was silently ignored: %v", err)
	}
	if _, err := os.Stat(unknown); err != nil {
		t.Fatal("unknown metadata must not be deleted")
	}
}

func TestArtifactArchiveOwnershipLockAndPathChanges(t *testing.T) {
	archive, ref, target, sources := newArtifactArchiveFixture(t)
	ctx := context.Background()
	if another, err := OpenArtifactArchive(ctx, archive.path, false); !errors.Is(err, ErrArtifactBusy) {
		if another != nil {
			_ = another.Close()
		}
		t.Fatalf("concurrent lock error: %v", err)
	}
	if another, err := OpenArtifactArchive(ctx, archive.path, true); !errors.Is(err, ErrArtifactConflict) {
		if another != nil {
			_ = another.Close()
		}
		t.Fatalf("reinitialize error: %v", err)
	}
	if _, _, err := archive.Record(ctx, ref, target, []byte("recipe"), ArtifactSplit, sources); err != nil {
		t.Fatal(err)
	}
	moved := archive.path + "-moved"
	if err := os.Rename(archive.path, moved); err != nil {
		t.Fatal(err)
	}
	if _, err := archive.Inspect(ctx, ref, target); !errors.Is(err, ErrArtifactUnavailable) {
		t.Fatalf("moved archive should be rejected: %v", err)
	}
	if err := os.Rename(moved, archive.path); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(filepath.Dir(archive.path), "archive-link")
	if err := os.Symlink(archive.path, link); err != nil {
		t.Fatal(err)
	}
	if another, err := OpenArtifactArchive(ctx, link, false); !errors.Is(err, ErrArtifactUnavailable) {
		if another != nil {
			_ = another.Close()
		}
		t.Fatalf("symlink root error: %v", err)
	}
}

func TestArtifactArchiveRejectsSymlinkObjectsAndCancelledWrites(t *testing.T) {
	archive, ref, target, sources := newArtifactArchiveFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := archive.Record(ctx, ref, target, []byte("recipe"), ArtifactSplit, sources); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled record error: %v", err)
	}
	ctx = context.Background()
	if _, err := archive.Inspect(ctx, ref, target); !errors.Is(err, ErrArtifactNotFound) {
		t.Fatalf("cancelled operation published a revision: %v", err)
	}
	release, _, err := archive.Record(ctx, ref, target, []byte("recipe"), ArtifactSplit, sources)
	if err != nil {
		t.Fatal(err)
	}
	object := filepath.Join(archive.path, artifactObjectFilename(release.Artifact.Parts[0].SHA256))
	if err := os.Remove(object); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(sources[0], object); err != nil {
		t.Fatal(err)
	}
	if _, err := archive.Inspect(ctx, ref, target); !errors.Is(err, ErrArtifactUnavailable) {
		t.Fatalf("symlink object error: %v", err)
	}
	if _, _, err := archive.Record(ctx, ref, target, []byte("recipe"), ArtifactSplit, sources); !errors.Is(err, ErrArtifactUnavailable) {
		t.Fatalf("symlink object must not be overwritten: %v", err)
	}
}

func TestReadArtifactCatalogRejectsAliasesDuplicatesAndMissingHistory(t *testing.T) {
	if !artifactArchiveSupported() {
		t.Skip("artifact inputs require Linux or macOS")
	}
	release := artifactTestRelease(t)
	body, err := json.Marshal([]Entry{release.Entry})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "catalog.json")
	for name, data := range map[string][]byte{
		"canonical": append(append([]byte(nil), body...), '\n'),
		"duplicate": bytes.Replace(body, []byte(`"catalog":`), []byte(`"catalog":"anas","catalog":`), 1),
		"alias":     bytes.Replace(body, []byte(`"fingerprint":`), []byte(`"Fingerprint":`), 1),
		"null":      []byte("null\n"),
	} {
		t.Run(name, func(t *testing.T) {
			if err := os.WriteFile(path, data, 0600); err != nil {
				t.Fatal(err)
			}
			entries, err := ReadArtifactCatalog(context.Background(), path)
			if name == "canonical" {
				if err != nil || !reflect.DeepEqual(entries, []Entry{release.Entry}) {
					t.Fatalf("canonical catalog: %#v, %v", entries, err)
				}
			} else if !errors.Is(err, ErrArtifactInvalid) {
				t.Fatalf("invalid catalog accepted: %v", err)
			}
		})
	}
	_, err = ReadArtifactCatalog(context.Background(), path+"-missing")
	if !errors.Is(err, ErrArtifactUnavailable) || strings.Contains(err.Error(), path) {
		t.Fatalf("missing catalog error must not expose a path or create empty history: %v", err)
	}
}
