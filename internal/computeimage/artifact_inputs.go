package computeimage

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
)

const MaxArtifactCatalogBytes = 2 << 20

// ReadArtifactRecipe reads one self-contained, reviewed distrobuilder recipe.
// The caller owns its provenance; this does not execute or validate the recipe.
// The recipe digest must change whenever a build input is changed. Recipes with
// external inputs must pin them explicitly inside the reviewed recipe bytes.
func ReadArtifactRecipe(ctx context.Context, path string) ([]byte, error) {
	return readArtifactInput(ctx, path, MaxRecipeBytes)
}

// ReadArtifactCatalog accepts the canonical Entry array emitted by the release
// tool (insignificant whitespace is allowed). A missing/corrupt previous catalog
// is an error, never an instruction to initialize empty history.
func ReadArtifactCatalog(ctx context.Context, path string) ([]Entry, error) {
	body, err := readArtifactInput(ctx, path, MaxArtifactCatalogBytes)
	if err != nil {
		return nil, err
	}
	var entries []Entry
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&entries) != nil || decoder.Decode(&struct{}{}) != io.EOF || entries == nil || len(entries) > maxArtifactReleases {
		return nil, ErrArtifactInvalid
	}
	if _, err := NewCatalog(entries, nil); err != nil {
		return nil, ErrArtifactInvalid
	}
	canonical, err := json.Marshal(entries)
	var compact bytes.Buffer
	if err != nil || json.Compact(&compact, body) != nil || !bytes.Equal(canonical, compact.Bytes()) {
		return nil, ErrArtifactInvalid
	}
	return entries, nil
}

func readArtifactInput(ctx context.Context, path string, limit int64) ([]byte, error) {
	if ctx == nil || path == "" || !artifactArchiveSupported() {
		return nil, ErrArtifactInvalid
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	file, err := os.OpenFile(path, os.O_RDONLY|artifactNoFollowFlags(), 0)
	if err != nil {
		return nil, ErrArtifactUnavailable
	}
	defer file.Close()
	before, err := file.Stat()
	if err != nil || !before.Mode().IsRegular() || before.Size() <= 0 || before.Size() > limit {
		return nil, ErrArtifactInvalid
	}
	body, err := io.ReadAll(io.LimitReader(&artifactContextReader{ctx: ctx, reader: file}, limit+1))
	after, statErr := file.Stat()
	current, pathErr := os.Lstat(path)
	if err != nil || statErr != nil || pathErr != nil || int64(len(body)) != before.Size() || !sameArtifactFile(before, after) || !os.SameFile(before, current) {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, ErrArtifactUnavailable
	}
	return body, nil
}
