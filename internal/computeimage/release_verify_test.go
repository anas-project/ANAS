package computeimage

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"
)

func frozenReleaseFixture(t *testing.T) (ArtifactRelease, Resolution, []byte, []byte) {
	t.Helper()
	metadata, rootfs := []byte("metadata fixture"), []byte("root filesystem fixture")
	entry := Entry{Catalog: "anas", Name: "verification-fixture", Revision: "r1", Target: Target{Architecture: "amd64", Interface: "incus_vm"}, RecipeDigest: strings.Repeat("a", 64)}
	release, err := DescribeArtifactRelease(context.Background(), entry, ArtifactSplit, []io.Reader{bytes.NewReader(metadata), bytes.NewReader(rootfs)})
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := NewCatalog([]Entry{release.Entry}, nil)
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := Resolve([]Reference{{Catalog: entry.Catalog, Name: entry.Name, Revision: entry.Revision}}, entry.Target, catalog)
	if err != nil {
		t.Fatal(err)
	}
	return release, resolved[0], metadata, rootfs
}

func TestFrozenArtifactReleaseVerification(t *testing.T) {
	release, frozen, metadata, rootfs := frozenReleaseFixture(t)
	for _, resolution := range []Resolution{
		frozen,
		{Reference: Reference{Fingerprint: frozen.Fingerprint}, Target: frozen.Target, Fingerprint: frozen.Fingerprint},
	} {
		if err := VerifyArtifactRelease(context.Background(), release, resolution, []io.Reader{bytes.NewReader(metadata), bytes.NewReader(rootfs)}); err != nil {
			t.Fatal(err)
		}
	}
}

func TestFrozenArtifactReleaseRejectsDrift(t *testing.T) {
	for _, scenario := range []string{"metadata", "rootfs", "swapped", "trailing", "part-size", "part-hash", "recipe", "revision", "architecture", "interface", "fingerprint", "missing-catalog-digest", "bare-with-catalog"} {
		t.Run(scenario, func(t *testing.T) {
			release, frozen, metadata, rootfs := frozenReleaseFixture(t)
			switch scenario {
			case "metadata":
				metadata[0] ^= 1
			case "rootfs":
				rootfs[0] ^= 1
			case "swapped":
				metadata, rootfs = rootfs, metadata
			case "trailing":
				rootfs = append(rootfs, 'x')
			case "part-size":
				release.Artifact.Parts[0].Size++
			case "part-hash":
				release.Artifact.Parts[1].SHA256 = strings.Repeat("b", 64)
			case "recipe":
				frozen.RecipeDigest = strings.Repeat("b", 64)
			case "revision":
				frozen.Reference.Revision = "r2"
			case "architecture":
				frozen.Target.Architecture = "arm64"
			case "interface":
				frozen.Target.Interface = "incus_container"
			case "fingerprint":
				frozen.Fingerprint = strings.Repeat("b", 64)
			case "missing-catalog-digest":
				frozen.CatalogDigest = ""
			case "bare-with-catalog":
				frozen.Reference = Reference{Fingerprint: frozen.Fingerprint}
			}
			if err := VerifyArtifactRelease(context.Background(), release, frozen, []io.Reader{bytes.NewReader(metadata), bytes.NewReader(rootfs)}); !errors.Is(err, ErrArtifactConflict) {
				t.Fatalf("%s: %v", scenario, err)
			}
		})
	}
}

func TestFrozenArtifactReleaseCannotReplaceFingerprint(t *testing.T) {
	release, _, metadata, rootfs := frozenReleaseFixture(t)
	rootfs[0] ^= 1
	if _, err := DescribeArtifactRelease(context.Background(), release.Entry, ArtifactSplit, []io.Reader{bytes.NewReader(metadata), bytes.NewReader(rootfs)}); !errors.Is(err, ErrArtifactConflict) {
		t.Fatalf("replaced existing fingerprint: %v", err)
	}
}

func TestFrozenArtifactReleaseNoProgressReader(t *testing.T) {
	entry := Entry{Catalog: "anas", Name: "verification-fixture", Revision: "r1", Target: Target{Architecture: "arm64", Interface: "incus_container"}, RecipeDigest: strings.Repeat("a", 64)}
	if _, err := DescribeArtifactRelease(context.Background(), entry, ArtifactUnified, []io.Reader{releaseNoProgressReader{}}); !errors.Is(err, ErrArtifactUnavailable) {
		t.Fatalf("stalled reader: %v", err)
	}
}

type releaseNoProgressReader struct{}

func (releaseNoProgressReader) Read([]byte) (int, error) { return 0, nil }
