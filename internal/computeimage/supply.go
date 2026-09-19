package computeimage

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
)

const ImageSupplyVersion = "anas.compute-image-supply/v1"

// SuppliedImage binds a frozen deployment resolution to one immutable release
// artifact and the local byte streams that carry it. Paths, URLs and aliases
// are deliberately not part of this package; the caller opens local files and
// passes readers so trust comes from the frozen release/catalog path, not from
// a descriptor claiming a fingerprint.
type SuppliedImage struct {
	Resolution Resolution      `json:"resolution"`
	Release    ArtifactRelease `json:"release"`
}

type ImageSupplyDocument struct {
	Version string          `json:"version"`
	Images  []SuppliedImage `json:"images"`
}

func (doc ImageSupplyDocument) Validate() error {
	if doc.Version != ImageSupplyVersion || len(doc.Images) == 0 || len(doc.Images) > 256 {
		return ErrArtifactInvalid
	}
	seen := map[string]bool{}
	for _, image := range doc.Images {
		if image.Release.Validate() != nil || image.Resolution.Target.validate() != nil || !fingerprintPattern.MatchString(image.Resolution.Fingerprint) {
			return ErrArtifactInvalid
		}
		key := image.Resolution.Fingerprint + "\x00" + image.Resolution.Target.Architecture + "\x00" + image.Resolution.Target.Interface
		if seen[key] {
			return ErrArtifactInvalid
		}
		seen[key] = true
	}
	return nil
}

func EncodeImageSupplyDocument(doc ImageSupplyDocument) ([]byte, error) {
	if doc.Validate() != nil {
		return nil, ErrArtifactInvalid
	}
	body, err := json.Marshal(doc)
	if err != nil || len(body)+1 > MaxArtifactRecordBytes*256 {
		return nil, ErrArtifactInvalid
	}
	return append(body, '\n'), nil
}

func DecodeImageSupplyDocument(body []byte) (ImageSupplyDocument, error) {
	if len(body) == 0 || len(body) > MaxArtifactRecordBytes*256 {
		return ImageSupplyDocument{}, ErrArtifactInvalid
	}
	var doc ImageSupplyDocument
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&doc) != nil || decoder.Decode(&struct{}{}) != io.EOF {
		return ImageSupplyDocument{}, ErrArtifactInvalid
	}
	canonical, err := EncodeImageSupplyDocument(doc)
	if err != nil || !bytes.Equal(body, canonical) {
		return ImageSupplyDocument{}, ErrArtifactInvalid
	}
	return doc, nil
}

// VerifySuppliedImage verifies bytes against an independently frozen
// resolution. It never accepts the release descriptor's own fingerprint as
// authority and never imports, extracts or executes the image.
func VerifySuppliedImage(ctx context.Context, image SuppliedImage, readers []io.Reader) error {
	if (ImageSupplyDocument{Version: ImageSupplyVersion, Images: []SuppliedImage{image}}).Validate() != nil {
		return ErrArtifactInvalid
	}
	return VerifyArtifactRelease(ctx, image.Release, image.Resolution, readers)
}
