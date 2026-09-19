package computeimage

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
)

type ArtifactExport struct {
	Release      ArtifactRelease `json:"release"`
	MetadataPath string          `json:"metadata_path,omitempty"`
	RootFSPath   string          `json:"rootfs_path,omitempty"`
	ImagePath    string          `json:"image_path,omitempty"`
}

// Export restores the exact recorded bytes for one immutable revision into a
// new private directory. It is a release preparation step: it does not run a
// builder, connect to Incus, assign aliases or infer trust from the descriptor.
func (archive *ArtifactArchive) Export(ctx context.Context, reference Reference, target Target, destination string) (ArtifactExport, error) {
	unlock, err := archive.acquire(ctx)
	if err != nil {
		return ArtifactExport{}, err
	}
	defer unlock()
	if destination == "" || !filepath.IsAbs(destination) || filepath.Clean(destination) != destination {
		return ArtifactExport{}, ErrArtifactInvalid
	}
	name, err := artifactReleaseFilename(reference, target)
	if err != nil {
		return ArtifactExport{}, err
	}
	release, err := archive.readRelease(name)
	if err != nil {
		return ArtifactExport{}, err
	}
	if err := archive.verifyRelease(ctx, release); err != nil {
		return ArtifactExport{}, err
	}
	if err := os.Mkdir(destination, 0700); err != nil {
		if errors.Is(err, os.ErrExist) {
			return ArtifactExport{}, ErrArtifactConflict
		}
		return ArtifactExport{}, ErrArtifactUnavailable
	}
	info, err := os.Lstat(destination)
	if err != nil || !artifactPrivateDirectory(info) {
		return ArtifactExport{}, ErrArtifactUnavailable
	}
	out := ArtifactExport{Release: release}
	for i, part := range release.Artifact.Parts {
		name := exportPartName(release.Artifact, part.Role)
		if name == "" {
			return ArtifactExport{}, ErrArtifactInvalid
		}
		path := filepath.Join(destination, name)
		if err := archive.exportPart(ctx, part, path); err != nil {
			return ArtifactExport{}, err
		}
		switch i {
		case 0:
			if release.Artifact.Format == ArtifactUnified {
				out.ImagePath = path
			} else {
				out.MetadataPath = path
			}
		case 1:
			out.RootFSPath = path
		}
	}
	body, err := EncodeArtifactRelease(release)
	if err != nil {
		return ArtifactExport{}, err
	}
	if err := writeExportFile(filepath.Join(destination, "artifact.json"), body); err != nil {
		return ArtifactExport{}, err
	}
	if err := syncExportDirectory(destination); err != nil {
		return ArtifactExport{}, err
	}
	return out, archive.check()
}

func (archive *ArtifactArchive) exportPart(ctx context.Context, part ArtifactPart, destination string) error {
	source, before, err := archive.openObject(part)
	if err != nil {
		return err
	}
	defer source.Close()
	target, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0400)
	if err != nil {
		return ErrArtifactUnavailable
	}
	keep := false
	defer func() {
		_ = target.Close()
		if !keep {
			_ = os.Remove(destination)
		}
	}()
	if _, err := io.CopyBuffer(target, &artifactContextReader{ctx: ctx, reader: source}, make([]byte, 128<<10)); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return ErrArtifactUnavailable
	}
	if target.Chmod(0400) != nil || target.Sync() != nil {
		return ErrArtifactUnavailable
	}
	after, err := source.Stat()
	current, pathErr := archive.root.Lstat(artifactObjectFilename(part.SHA256))
	if err != nil || pathErr != nil || !sameArtifactFile(before, after) || !sameArtifactFile(before, current) {
		return ErrArtifactUnavailable
	}
	keep = true
	return nil
}

func writeExportFile(path string, body []byte) error {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0400)
	if err != nil {
		return ErrArtifactUnavailable
	}
	keep := false
	defer func() {
		_ = file.Close()
		if !keep {
			_ = os.Remove(path)
		}
	}()
	if n, err := file.Write(body); err != nil || n != len(body) || file.Sync() != nil {
		return ErrArtifactUnavailable
	}
	keep = true
	return nil
}

func syncExportDirectory(path string) error {
	directory, err := os.Open(path)
	if err != nil {
		return ErrArtifactUnavailable
	}
	defer directory.Close()
	if info, err := directory.Stat(); err != nil || !artifactPrivateDirectory(info) || directory.Sync() != nil {
		return ErrArtifactUnavailable
	}
	return nil
}

func exportPartName(artifact Artifact, role string) string {
	switch {
	case artifact.Format == ArtifactUnified && role == "image":
		return "image.tar.xz"
	case artifact.Format == ArtifactSplit && role == "metadata":
		return "incus.tar.xz"
	case artifact.Format == ArtifactSplit && role == "rootfs" && artifact.Target.Interface == "incus_vm":
		return "disk.qcow2"
	case artifact.Format == ArtifactSplit && role == "rootfs":
		return "rootfs.squashfs"
	default:
		return ""
	}
}
