package computeimage

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
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
	output, err := newArtifactExportRoot(destination)
	if err != nil {
		return ArtifactExport{}, err
	}
	defer output.root.Close()
	if err := archive.exportRelease(ctx, release, output.root); err != nil {
		return ArtifactExport{}, err
	}
	if err := output.check(); err != nil {
		return ArtifactExport{}, err
	}
	out := ArtifactExport{Release: release}
	for i, part := range release.Artifact.Parts {
		name := exportPartName(release.Artifact, part.Role)
		if name == "" {
			return ArtifactExport{}, ErrArtifactInvalid
		}
		path := filepath.Join(destination, name)
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
	return out, archive.check()
}

func (archive *ArtifactArchive) exportRelease(ctx context.Context, release ArtifactRelease, output *os.Root) error {
	for _, part := range release.Artifact.Parts {
		name := exportPartName(release.Artifact, part.Role)
		if name == "" {
			return ErrArtifactInvalid
		}
		if err := archive.exportPart(ctx, part, output, name); err != nil {
			return err
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	body, err := EncodeArtifactRelease(release)
	if err != nil {
		return err
	}
	if err := writeExportFile(output, "artifact.json", body); err != nil {
		return err
	}
	return syncExportDirectory(output, ".")
}

func (archive *ArtifactArchive) exportPart(ctx context.Context, part ArtifactPart, output *os.Root, name string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	source, before, err := archive.openObject(part)
	if err != nil {
		return err
	}
	defer source.Close()
	target, err := output.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0400)
	if err != nil {
		return ErrArtifactUnavailable
	}
	keep := false
	defer func() {
		_ = target.Close()
		if !keep {
			_ = output.Remove(name)
		}
	}()
	digest := sha256.New()
	n, err := io.CopyBuffer(io.MultiWriter(target, digest),
		io.LimitReader(&artifactContextReader{ctx: ctx, reader: source}, part.Size+1), make([]byte, 128<<10))
	if err != nil || ctx.Err() != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return ErrArtifactUnavailable
	}
	if n != part.Size || hex.EncodeToString(digest.Sum(nil)) != part.SHA256 || target.Chmod(0400) != nil || target.Sync() != nil {
		return ErrArtifactUnavailable
	}
	after, err := source.Stat()
	current, pathErr := archive.root.Lstat(artifactObjectFilename(part.SHA256))
	if err != nil || pathErr != nil || !sameArtifactFile(before, after) || !sameArtifactFile(before, current) {
		return ErrArtifactUnavailable
	}
	if err := target.Close(); err != nil {
		return ErrArtifactUnavailable
	}
	keep = true
	return nil
}

func writeExportFile(output *os.Root, name string, body []byte) error {
	file, err := output.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0400)
	if err != nil {
		return ErrArtifactUnavailable
	}
	keep := false
	defer func() {
		_ = file.Close()
		if !keep {
			_ = output.Remove(name)
		}
	}()
	if n, err := file.Write(body); err != nil || n != len(body) || file.Sync() != nil {
		return ErrArtifactUnavailable
	}
	if file.Close() != nil {
		return ErrArtifactUnavailable
	}
	keep = true
	return nil
}

func syncExportDirectory(output *os.Root, name string) error {
	directory, err := output.Open(name)
	if err != nil {
		return ErrArtifactUnavailable
	}
	defer directory.Close()
	if info, err := directory.Stat(); err != nil || !artifactPrivateDirectory(info) || directory.Sync() != nil {
		return ErrArtifactUnavailable
	}
	return nil
}

type artifactExportRoot struct {
	root     *os.Root
	path     string
	identity os.FileInfo
}

// All output writes use the opened directory, never a re-resolved absolute
// destination. A replaced path is rejected instead of redirecting later parts.
func newArtifactExportRoot(destination string) (*artifactExportRoot, error) {
	if destination == "" || !filepath.IsAbs(destination) || filepath.Clean(destination) != destination {
		return nil, ErrArtifactInvalid
	}
	if err := os.Mkdir(destination, 0700); err != nil {
		if errors.Is(err, os.ErrExist) {
			return nil, ErrArtifactConflict
		}
		return nil, ErrArtifactUnavailable
	}
	identity, err := os.Lstat(destination)
	if err != nil || !artifactPrivateDirectory(identity) {
		return nil, ErrArtifactUnavailable
	}
	root, err := os.OpenRoot(destination)
	if err != nil {
		return nil, ErrArtifactUnavailable
	}
	output := &artifactExportRoot{root: root, path: destination, identity: identity}
	if err := output.check(); err != nil {
		_ = root.Close()
		return nil, err
	}
	return output, nil
}

func (output *artifactExportRoot) check() error {
	current, err := os.Lstat(output.path)
	pinned, pinErr := output.root.Stat(".")
	if err != nil || pinErr != nil || !artifactPrivateDirectory(current) || !artifactPrivateDirectory(pinned) ||
		!os.SameFile(output.identity, current) || !os.SameFile(output.identity, pinned) {
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
