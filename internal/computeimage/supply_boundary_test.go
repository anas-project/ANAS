package computeimage

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestImageSupplyRejectsResolutionDriftBeforeReadingBytes(t *testing.T) {
	for _, scenario := range []string{"revision", "recipe", "fingerprint", "target", "catalog", "bare-fingerprint", "bare-provenance"} {
		t.Run(scenario, func(t *testing.T) {
			release, frozen, _, _ := frozenReleaseFixture(t)
			switch scenario {
			case "revision":
				frozen.Reference.Revision = "r2"
			case "recipe":
				frozen.RecipeDigest = strings.Repeat("b", 64)
			case "fingerprint":
				frozen.Fingerprint = strings.Repeat("b", 64)
			case "target":
				frozen.Target.Interface = "incus_container"
			case "catalog":
				frozen.CatalogDigest = ""
			case "bare-fingerprint":
				frozen.Reference = Reference{Fingerprint: strings.Repeat("b", 64)}
				frozen.CatalogDigest, frozen.RecipeDigest = "", ""
			case "bare-provenance":
				frozen.Reference = Reference{Fingerprint: frozen.Fingerprint}
			}
			doc := ImageSupplyDocument{Version: ImageSupplyVersion, Images: []SuppliedImage{{Resolution: frozen, Release: release}}}
			if doc.Validate() == nil {
				t.Fatal("accepted a release that does not match its frozen resolution")
			}
			if _, err := EncodeImageSupplyDocument(doc); err == nil {
				t.Fatal("encoded inconsistent release metadata")
			}
		})
	}
}

func TestArtifactExportPartRehashesBytes(t *testing.T) {
	archive, ref, target, sources := newArtifactArchiveFixture(t)
	release, _, err := archive.Record(context.Background(), ref, target, []byte("recipe"), ArtifactSplit, sources)
	if err != nil {
		t.Fatal(err)
	}
	part := release.Artifact.Parts[1]
	object := filepath.Join(archive.path, artifactObjectFilename(part.SHA256))
	before, err := os.Stat(object)
	if err != nil {
		t.Fatal(err)
	}
	// A same-size replacement after the initial release verification must not
	// be trusted merely because the object name and stat metadata match.
	if err := os.Chmod(object, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(object, []byte(strings.Repeat("x", int(part.Size))), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(object, 0400); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(object, before.ModTime(), before.ModTime()); err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(t.TempDir(), "rootfs.squashfs")
	output, err := os.OpenRoot(filepath.Dir(destination))
	if err != nil {
		t.Fatal(err)
	}
	defer output.Close()
	if err := archive.exportPart(context.Background(), part, output, filepath.Base(destination)); err == nil {
		t.Fatal("export copied corrupted bytes without rechecking their digest")
	}
	if _, err := os.Lstat(destination); !os.IsNotExist(err) {
		t.Fatal("failed export left an output artifact")
	}
}
