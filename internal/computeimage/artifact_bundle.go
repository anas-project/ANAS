package computeimage

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

// ArtifactBundle reports metadata only. The action itself writes image bytes
// to the explicitly selected local destination; there is no download handle,
// URL, inline payload, daemon import or implied signature in this result.
type ArtifactBundle struct {
	ImageCount    int    `json:"image_count"`
	CatalogDigest string `json:"catalog_digest"`
}

// ExportBundle produces the Provider's images/ layout in a NEW private local
// directory. It retains every committed revision and verifies prior history.
// The catalog is written last, after all split artifacts have been restored.
// A failed attempt leaves its private directory for explicit inspection; it
// never adopts an existing destination or repairs by rebuilding an image.
func (archive *ArtifactArchive) ExportBundle(ctx context.Context, previous []Entry, destination string) (ArtifactBundle, error) {
	unlock, err := archive.acquire(ctx)
	if err != nil {
		return ArtifactBundle{}, err
	}
	defer unlock()
	releases, err := archive.catalogReleases(ctx, previous)
	if err != nil {
		return ArtifactBundle{}, err
	}
	if len(releases) == 0 {
		return ArtifactBundle{}, ErrArtifactNotFound
	}
	// Reject an unsupported format before creating any output directories.
	entries := make([]Entry, 0, len(releases))
	for _, release := range releases {
		if release.Artifact.Format != ArtifactSplit {
			return ArtifactBundle{}, ErrArtifactInvalid
		}
		entries = append(entries, release.Entry)
	}
	body, err := json.Marshal(entries)
	if err != nil || len(body)+1 > MaxArtifactCatalogBytes {
		return ArtifactBundle{}, ErrArtifactInvalid
	}
	catalog, err := NewCatalog(entries, nil)
	if err != nil {
		return ArtifactBundle{}, err
	}
	if err := ctx.Err(); err != nil {
		return ArtifactBundle{}, err
	}
	output, err := newArtifactExportRoot(destination)
	if err != nil {
		return ArtifactBundle{}, err
	}
	defer output.root.Close()
	for _, release := range releases {
		if err := ctx.Err(); err != nil {
			return ArtifactBundle{}, err
		}
		if output.check() != nil || archive.check() != nil {
			return ArtifactBundle{}, ErrArtifactUnavailable
		}
		entry := release.Entry
		relative := filepath.Join("artifacts", entry.Catalog, entry.Name, entry.Revision, entry.Architecture, entry.Interface)
		leaf, err := makeArtifactBundleDirectory(output.root, relative)
		if err != nil {
			return ArtifactBundle{}, err
		}
		exportErr := archive.exportRelease(ctx, release, leaf)
		closeErr := leaf.Close()
		if exportErr != nil {
			return ArtifactBundle{}, exportErr
		}
		if closeErr != nil {
			return ArtifactBundle{}, ErrArtifactUnavailable
		}
	}
	if err := ctx.Err(); err != nil {
		return ArtifactBundle{}, err
	}
	if output.check() != nil || archive.check() != nil {
		return ArtifactBundle{}, ErrArtifactUnavailable
	}
	if err := writeExportFile(output.root, "catalog.json", append(body, '\n')); err != nil {
		return ArtifactBundle{}, err
	}
	if syncExportDirectory(output.root, ".") != nil || output.check() != nil || archive.check() != nil {
		return ArtifactBundle{}, ErrArtifactUnavailable
	}
	return ArtifactBundle{ImageCount: len(entries), CatalogDigest: catalog.digest}, nil
}

func makeArtifactBundleDirectory(output *os.Root, relative string) (*os.Root, error) {
	parent := "."
	for _, component := range strings.Split(relative, string(os.PathSeparator)) {
		path := filepath.Join(parent, component)
		if err := output.Mkdir(path, 0700); err != nil && !os.IsExist(err) {
			return nil, ErrArtifactUnavailable
		}
		info, err := output.Lstat(path)
		if err != nil || !artifactPrivateDirectory(info) || syncExportDirectory(output, parent) != nil {
			return nil, ErrArtifactUnavailable
		}
		parent = path
	}
	leaf, err := output.OpenRoot(relative)
	if err != nil {
		return nil, ErrArtifactUnavailable
	}
	return leaf, nil
}
