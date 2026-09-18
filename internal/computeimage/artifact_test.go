package computeimage

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"
)

func artifactTestDigest(body string) string {
	digest := sha256.Sum256([]byte(body))
	return hex.EncodeToString(digest[:])
}

func artifactTestRelease(t *testing.T) ArtifactRelease {
	t.Helper()
	target := Target{Architecture: "amd64", Interface: "incus_container"}
	artifact, err := DescribeArtifact(context.Background(), target, ArtifactSplit, []io.Reader{strings.NewReader("metadata"), strings.NewReader("rootfs")})
	if err != nil {
		t.Fatal(err)
	}
	return ArtifactRelease{
		Entry:    Entry{Catalog: "anas", Name: "fixture", Revision: "r1", Target: target, Fingerprint: artifact.Fingerprint, RecipeDigest: artifactTestDigest("recipe")},
		Artifact: artifact,
	}
}

func TestDescribeArtifactUsesIncusFingerprintOrder(t *testing.T) {
	for _, iface := range []string{"incus_container", "incus_vm"} {
		t.Run(iface, func(t *testing.T) {
			artifact, err := DescribeArtifact(context.Background(), Target{Architecture: "arm64", Interface: iface}, ArtifactSplit,
				[]io.Reader{strings.NewReader("metadata"), strings.NewReader("rootfs")})
			if err != nil {
				t.Fatal(err)
			}
			if artifact.Fingerprint != artifactTestDigest("metadatarootfs") || artifact.Fingerprint == artifactTestDigest("rootfsmetadata") {
				t.Fatal("split fingerprint did not hash metadata followed by rootfs")
			}
			want := []ArtifactPart{{Role: "metadata", SHA256: artifactTestDigest("metadata"), Size: 8}, {Role: "rootfs", SHA256: artifactTestDigest("rootfs"), Size: 6}}
			if !reflect.DeepEqual(artifact.Parts, want) {
				t.Fatalf("parts = %#v", artifact.Parts)
			}
		})
	}
	artifact, err := DescribeArtifact(context.Background(), Target{Architecture: "amd64", Interface: "incus_vm"}, ArtifactUnified, []io.Reader{strings.NewReader("unified")})
	if err != nil || artifact.Fingerprint != artifactTestDigest("unified") || artifact.Parts[0].SHA256 != artifact.Fingerprint {
		t.Fatalf("unified artifact = %#v, %v", artifact, err)
	}
}

func TestDescribeArtifactRejectsShapeBoundsAndCancellation(t *testing.T) {
	target := Target{Architecture: "amd64", Interface: "incus_container"}
	for name, readers := range map[string][]io.Reader{
		"missing part": {strings.NewReader("metadata")},
		"nil part":     {strings.NewReader("metadata"), nil},
		"empty part":   {strings.NewReader(""), strings.NewReader("rootfs")},
		"extra part":   {strings.NewReader("a"), strings.NewReader("b"), strings.NewReader("c")},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := DescribeArtifact(context.Background(), target, ArtifactSplit, readers); !errors.Is(err, ErrArtifactInvalid) {
				t.Fatalf("error = %v", err)
			}
		})
	}
	if _, err := DescribeArtifact(context.Background(), target, ArtifactSplit, []io.Reader{
		io.LimitReader(artifactZeroReader{}, MaxMetadataBytes+1), strings.NewReader("rootfs"),
	}); !errors.Is(err, ErrArtifactInvalid) {
		t.Fatalf("oversized metadata error = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := DescribeArtifact(ctx, target, ArtifactUnified, []io.Reader{strings.NewReader("bytes")}); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel error = %v", err)
	}
	if _, err := DescribeArtifact(context.Background(), Target{Architecture: "amd64", Interface: "unknown"}, ArtifactUnified, []io.Reader{strings.NewReader("bytes")}); !errors.Is(err, ErrArtifactInvalid) {
		t.Fatalf("target error = %v", err)
	}
}

type artifactZeroReader struct{}

func (artifactZeroReader) Read(body []byte) (int, error) {
	clear(body)
	return len(body), nil
}

func TestArtifactReleaseCanonicalCodec(t *testing.T) {
	release := artifactTestRelease(t)
	body, err := EncodeArtifactRelease(release)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeArtifactRelease(body)
	if err != nil || !reflect.DeepEqual(decoded, release) {
		t.Fatalf("round trip = %#v, %v", decoded, err)
	}
	cases := map[string][]byte{
		"duplicate":   bytes.Replace(body, []byte(`"version":`), []byte(`"version":"ignored","version":`), 1),
		"case alias":  bytes.Replace(body, []byte(`"fingerprint":`), []byte(`"Fingerprint":`), 1),
		"unknown":     bytes.Replace(body, []byte(`{"entry":`), []byte(`{"extra":true,"entry":`), 1),
		"null":        bytes.Replace(body, []byte(`"architecture":"amd64"`), []byte(`"architecture":null`), 1),
		"trailing":    append(append([]byte(nil), body...), []byte("{}\n")...),
		"extra LF":    append(append([]byte(nil), body...), '\n'),
		"missing LF":  body[:len(body)-1],
		"wrong order": bytes.Replace(body, []byte(`"role":"metadata"`), []byte(`"role":"rootfs"`), 1),
	}
	for name, data := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := DecodeArtifactRelease(data); !errors.Is(err, ErrArtifactInvalid) {
				t.Fatalf("accepted noncanonical/invalid description: %v", err)
			}
		})
	}
	release.Entry.Fingerprint = artifactTestDigest("another image")
	if _, err := EncodeArtifactRelease(release); !errors.Is(err, ErrArtifactInvalid) {
		t.Fatalf("entry/artifact disagreement error = %v", err)
	}
}
