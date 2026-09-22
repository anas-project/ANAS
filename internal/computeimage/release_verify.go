package computeimage

import (
	"context"
	"io"
	"reflect"
)

// DescribeArtifactRelease binds measured bytes to caller-supplied, trusted
// recipe provenance. It is an inspection step, not publication or permission
// to replace a catalog key. An existing fingerprint is an expected value and
// must not be overwritten when a bake yields different bytes.
func DescribeArtifactRelease(ctx context.Context, image Entry, format string, readers []io.Reader) (ArtifactRelease, error) {
	if (Reference{Catalog: image.Catalog, Name: image.Name, Revision: image.Revision}).validate() != nil ||
		image.Target.validate() != nil || !fingerprintPattern.MatchString(image.RecipeDigest) ||
		(image.Fingerprint != "" && !fingerprintPattern.MatchString(image.Fingerprint)) {
		return ArtifactRelease{}, ErrArtifactInvalid
	}
	artifact, err := DescribeArtifact(ctx, image.Target, format, readers)
	if err != nil {
		return ArtifactRelease{}, err
	}
	if image.Fingerprint != "" && image.Fingerprint != artifact.Fingerprint {
		return ArtifactRelease{}, ErrArtifactConflict
	}
	image.Fingerprint = artifact.Fingerprint
	release := ArtifactRelease{Entry: image, Artifact: artifact}
	return release, release.Validate()
}

// VerifyArtifactRelease verifies an artifact against an independently trusted
// frozen resolution, not against a fingerprint learned from the descriptor.
// For named images it checks the complete version/target key and recipe digest.
// The frozen catalog digest must be present; authenticating that snapshot is
// the caller's responsibility, not something a local hash check can establish.
// The input is consumed, never extracted, imported, executed or published.
func VerifyArtifactRelease(ctx context.Context, release ArtifactRelease, frozen Resolution, readers []io.Reader) error {
	if err := ValidateArtifactResolution(release, frozen); err != nil {
		return err
	}
	observed, err := DescribeArtifactRelease(ctx, release.Entry, release.Artifact.Format, readers)
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(observed, release) {
		return ErrArtifactConflict
	}
	return nil
}

// ValidateArtifactResolution checks metadata against an independently frozen
// resolution before any files are opened. It neither authenticates a catalog
// nor verifies image bytes; callers must still use VerifyArtifactRelease for
// byte integrity and obtain the frozen resolution from their trusted snapshot.
func ValidateArtifactResolution(release ArtifactRelease, frozen Resolution) error {
	if release.Validate() != nil {
		return ErrArtifactInvalid
	}
	if frozen.Reference.validate() != nil || frozen.Target.validate() != nil ||
		!fingerprintPattern.MatchString(frozen.Fingerprint) || frozen.Target != release.Entry.Target ||
		frozen.Fingerprint != release.Entry.Fingerprint {
		return ErrArtifactConflict
	}
	if frozen.Reference.Fingerprint != "" {
		if frozen.Reference.Fingerprint != frozen.Fingerprint || frozen.CatalogDigest != "" || frozen.RecipeDigest != "" {
			return ErrArtifactConflict
		}
	} else if !fingerprintPattern.MatchString(frozen.CatalogDigest) ||
		frozen.Reference.Catalog != release.Entry.Catalog || frozen.Reference.Name != release.Entry.Name ||
		frozen.Reference.Revision != release.Entry.Revision || frozen.RecipeDigest != release.Entry.RecipeDigest {
		return ErrArtifactConflict
	}
	return nil
}
