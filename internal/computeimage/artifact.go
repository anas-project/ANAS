package computeimage

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
)

const (
	ArtifactVersion              = "anas.incus-image/v1"
	ArtifactSplit                = "split"
	ArtifactUnified              = "unified"
	MaxArtifactRecordBytes       = 16 << 10
	MaxRecipeBytes               = 4 << 20
	MaxMetadataBytes             = 16 << 20
	MaxImageBytes          int64 = 64 << 30
)

var (
	ErrArtifactInvalid     = errors.New("invalid Incus image artifact description")
	ErrArtifactUnavailable = errors.New("Incus image artifact is unavailable or failed integrity checks")
	ErrArtifactConflict    = errors.New("published image revision or artifact cannot be changed")
	ErrArtifactNotFound    = errors.New("image revision has not been recorded")
	ErrArtifactBusy        = errors.New("image artifact archive is already in use")
)

// ArtifactPart names bytes, not a URL, source path, alias, or an executable.
// Object filenames are derived internally from SHA256. Parts are ordered:
// metadata then rootfs for split images, or exactly one image for unified ones.
type ArtifactPart struct {
	Role   string `json:"role"`
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
}

type Artifact struct {
	Version     string         `json:"version"`
	Target      Target         `json:"target"`
	Format      string         `json:"format"`
	Fingerprint string         `json:"fingerprint"`
	Parts       []ArtifactPart `json:"parts"`
}

// ArtifactRelease is an immutable release-side record. It does not prove that
// distrobuilder ran, that guest metadata is truthful, or that Incus accepted the
// image. The trusted build pipeline supplies provenance; Provider verification
// still checks fingerprint, architecture and instance type after import.
type ArtifactRelease struct {
	Entry    Entry    `json:"entry"`
	Artifact Artifact `json:"artifact"`
}

func (artifact Artifact) Validate() error {
	if artifact.Version != ArtifactVersion || artifact.Target.validate() != nil || !fingerprintPattern.MatchString(artifact.Fingerprint) {
		return ErrArtifactInvalid
	}
	roles := artifactRoles(artifact.Format)
	if len(roles) == 0 || len(artifact.Parts) != len(roles) {
		return ErrArtifactInvalid
	}
	for i, part := range artifact.Parts {
		if part.Role != roles[i] || !fingerprintPattern.MatchString(part.SHA256) || part.Size <= 0 || part.Size > artifactPartLimit(part.Role) {
			return ErrArtifactInvalid
		}
	}
	if artifact.Format == ArtifactUnified && artifact.Parts[0].SHA256 != artifact.Fingerprint {
		return ErrArtifactInvalid
	}
	return nil
}

func (release ArtifactRelease) Validate() error {
	if _, err := NewCatalog([]Entry{release.Entry}, nil); err != nil {
		return ErrArtifactInvalid
	}
	if release.Artifact.Validate() != nil || release.Entry.Target != release.Artifact.Target || release.Entry.Fingerprint != release.Artifact.Fingerprint {
		return ErrArtifactInvalid
	}
	return nil
}

// EncodeArtifactRelease produces the only accepted on-disk representation.
// Requiring canonical bytes on read rejects duplicate/unknown/case-aliased
// fields, explicit nulls, trailing values and alternate numeric spellings.
func EncodeArtifactRelease(release ArtifactRelease) ([]byte, error) {
	if release.Validate() != nil {
		return nil, ErrArtifactInvalid
	}
	body, err := json.Marshal(release)
	if err != nil || len(body)+1 > MaxArtifactRecordBytes {
		return nil, ErrArtifactInvalid
	}
	return append(body, '\n'), nil
}

func DecodeArtifactRelease(body []byte) (ArtifactRelease, error) {
	if len(body) == 0 || len(body) > MaxArtifactRecordBytes {
		return ArtifactRelease{}, ErrArtifactInvalid
	}
	var release ArtifactRelease
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&release) != nil || decoder.Decode(&struct{}{}) != io.EOF {
		return ArtifactRelease{}, ErrArtifactInvalid
	}
	canonical, err := EncodeArtifactRelease(release)
	if err != nil || !bytes.Equal(body, canonical) {
		return ArtifactRelease{}, ErrArtifactInvalid
	}
	return release, nil
}

// DescribeArtifact hashes the exact bytes emitted by the trusted builder. It
// never extracts an archive or imports an image. For split images the Incus
// fingerprint hashes the concatenation metadata || rootfs, in that order.
// Readers must be local, bounded-latency sources and honor cancellation where
// applicable; checking ctx cannot interrupt an arbitrary blocked io.Reader.
func DescribeArtifact(ctx context.Context, target Target, format string, readers []io.Reader) (Artifact, error) {
	roles := artifactRoles(format)
	if ctx == nil || target.validate() != nil || len(roles) == 0 || len(readers) != len(roles) {
		return Artifact{}, ErrArtifactInvalid
	}
	artifact := Artifact{Version: ArtifactVersion, Target: target, Format: format}
	combined := sha256.New()
	for i, reader := range readers {
		if reader == nil {
			return Artifact{}, ErrArtifactInvalid
		}
		partHash := sha256.New()
		limit := artifactPartLimit(roles[i])
		n, err := io.CopyBuffer(io.MultiWriter(combined, partHash),
			io.LimitReader(&artifactContextReader{ctx: ctx, reader: reader}, limit+1), make([]byte, 128<<10))
		if err != nil {
			if ctx.Err() != nil {
				return Artifact{}, ctx.Err()
			}
			return Artifact{}, ErrArtifactUnavailable
		}
		if n <= 0 || n > limit {
			return Artifact{}, ErrArtifactInvalid
		}
		artifact.Parts = append(artifact.Parts, ArtifactPart{Role: roles[i], SHA256: hex.EncodeToString(partHash.Sum(nil)), Size: n})
	}
	if err := ctx.Err(); err != nil {
		return Artifact{}, err
	}
	artifact.Fingerprint = hex.EncodeToString(combined.Sum(nil))
	return artifact, artifact.Validate()
}

type artifactContextReader struct {
	ctx        context.Context
	reader     io.Reader
	emptyReads int
}

func (reader *artifactContextReader) Read(body []byte) (int, error) {
	if err := reader.ctx.Err(); err != nil {
		return 0, err
	}
	n, err := reader.reader.Read(body)
	if n < 0 || n > len(body) {
		return 0, ErrArtifactUnavailable
	}
	if n == 0 && err == nil && len(body) > 0 {
		reader.emptyReads++
		if reader.emptyReads >= 100 {
			return 0, io.ErrNoProgress
		}
	} else {
		reader.emptyReads = 0
	}
	return n, err
}

func artifactRoles(format string) []string {
	switch format {
	case ArtifactSplit:
		return []string{"metadata", "rootfs"}
	case ArtifactUnified:
		return []string{"image"}
	default:
		return nil
	}
}

func artifactPartLimit(role string) int64 {
	if role == "metadata" {
		return MaxMetadataBytes
	}
	return MaxImageBytes
}
