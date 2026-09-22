package runner

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"syscall"

	"github.com/anas-project/ANAS/internal/computeimage"
)

type computeImageSupplyArtifact struct {
	Release      computeimage.ArtifactRelease
	MetadataPath string
	RootFSPath   string
}

type computeImageSupplyKey struct {
	Fingerprint string
	Target      computeimage.Target
}

type computeImageSupplyReference struct {
	Reference computeimage.Reference
	Target    computeimage.Target
}

type computeImageSupplyMount struct {
	hostDescriptor string
	hostRoot       string
}

type computeImageSupplyFile struct {
	Version string                    `json:"version"`
	Images  []computeImageSupplyEntry `json:"images"`
}

type computeImageSupplyEntry struct {
	computeimage.SuppliedImage
	MetadataPath string `json:"metadata_path"`
	RootFSPath   string `json:"rootfs_path"`
}

const (
	computeImageSupplyContainerDescriptor = "/run/anas/compute-image-supply.json"
	computeImageSupplyContainerRoot       = "/run/anas/compute-image-supply"
)

// computeImageSupplyJSON pairs already-frozen deployment resolutions with
// independently restored release artifacts. It never resolves aliases,
// downloads URLs, rebuilds a revision or learns trust from an artifact
// descriptor. The artifact list may be a subset: missing local bytes do not
// prevent an apply when the daemon already has the pinned image.
func computeImageSupplyJSON(snapshot *computeimage.Snapshot, artifacts []computeImageSupplyArtifact) ([]byte, error) {
	if snapshot == nil || len(snapshot.Images) == 0 {
		return nil, fmt.Errorf("compute image supply requires frozen images")
	}
	byReference := map[computeImageSupplyReference]computeImageSupplyArtifact{}
	byFingerprint := map[computeImageSupplyKey]computeImageSupplyArtifact{}
	for _, artifact := range artifacts {
		if artifact.Release.Validate() != nil || artifact.Release.Artifact.Format != computeimage.ArtifactSplit {
			return nil, fmt.Errorf("compute image supply artifact is invalid")
		}
		if artifact.MetadataPath == "" || artifact.RootFSPath == "" ||
			!filepath.IsAbs(artifact.MetadataPath) || !filepath.IsAbs(artifact.RootFSPath) ||
			filepath.Clean(artifact.MetadataPath) != artifact.MetadataPath || filepath.Clean(artifact.RootFSPath) != artifact.RootFSPath {
			return nil, fmt.Errorf("compute image supply artifact paths must be absolute")
		}
		entry := artifact.Release.Entry
		ref := computeImageSupplyReference{Reference: computeimage.Reference{Catalog: entry.Catalog, Name: entry.Name, Revision: entry.Revision}, Target: entry.Target}
		if _, exists := byReference[ref]; exists {
			return nil, fmt.Errorf("compute image supply artifact duplicates a catalog reference")
		}
		byReference[ref] = artifact
		key := computeImageSupplyKey{Fingerprint: entry.Fingerprint, Target: entry.Target}
		if previous, exists := byFingerprint[key]; exists {
			if !reflect.DeepEqual(previous.Release.Artifact, artifact.Release.Artifact) {
				return nil, fmt.Errorf("compute image supply artifacts disagree about identical image bytes")
			}
		} else {
			byFingerprint[key] = artifact
		}
	}
	doc := computeImageSupplyFile{Version: computeimage.ImageSupplyVersion}
	seen := map[computeImageSupplyKey]bool{}
	for _, image := range snapshot.Images {
		key := computeImageSupplyKey{Fingerprint: image.Fingerprint, Target: image.Target}
		artifact, ok := byFingerprint[key]
		if image.Reference.Fingerprint == "" {
			artifact, ok = byReference[computeImageSupplyReference{Reference: image.Reference, Target: image.Target}]
		}
		if !ok {
			continue
		}
		if computeimage.ValidateArtifactResolution(artifact.Release, image) != nil {
			return nil, fmt.Errorf("compute image supply artifact does not match the frozen catalog reference")
		}
		// Validate every reference before deduplicating physical bytes. Multiple
		// runtimes or named revisions may legitimately resolve to one image.
		if seen[key] {
			continue
		}
		seen[key] = true
		doc.Images = append(doc.Images, computeImageSupplyEntry{
			SuppliedImage: computeimage.SuppliedImage{Resolution: image, Release: artifact.Release},
			MetadataPath:  artifact.MetadataPath, RootFSPath: artifact.RootFSPath,
		})
	}
	if len(doc.Images) == 0 {
		return nil, nil
	}
	shared := computeimage.ImageSupplyDocument{Version: doc.Version}
	for _, image := range doc.Images {
		shared.Images = append(shared.Images, image.SuppliedImage)
	}
	if shared.Validate() != nil {
		return nil, fmt.Errorf("compute image supply metadata is invalid")
	}
	body, err := json.Marshal(doc)
	if err != nil || len(body)+1 > computeimage.MaxImageSupplyBytes {
		return nil, fmt.Errorf("compute image supply metadata exceeds its bounded descriptor")
	}
	return append(body, '\n'), nil
}

func (a *app) prepareComputeImageSupply(providerDir string, request ResourceRequest) (*computeImageSupplyMount, func(), error) {
	ctx := a.subprocessContext()
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	if request.Contract != "compute" || request.ComputeImages == nil || len(request.ComputeImages.Images) == 0 {
		return nil, func() {}, nil
	}
	artifacts, err := collectComputeImageSupplyArtifacts(ctx, providerDir, request.ComputeImages)
	if err != nil {
		return nil, nil, err
	}
	if len(artifacts) == 0 {
		return nil, func() {}, nil
	}
	tmpRoot := filepath.Join(a.base, "tmp")
	if err := os.MkdirAll(tmpRoot, 0700); err != nil {
		return nil, nil, err
	}
	stage, err := os.MkdirTemp(tmpRoot, "compute-image-supply-")
	if err != nil {
		return nil, nil, err
	}
	cleanup := func() {
		_ = filepath.WalkDir(stage, func(path string, entry os.DirEntry, err error) error {
			if err == nil && entry.IsDir() {
				_ = os.Chmod(path, 0700)
			}
			return nil
		})
		_ = os.RemoveAll(stage)
	}
	keep := false
	defer func() {
		if !keep {
			cleanup()
		}
	}()
	hostRoot := filepath.Join(stage, "artifacts")
	if err := os.Mkdir(hostRoot, 0700); err != nil {
		return nil, nil, err
	}
	staged := make([]computeImageSupplyArtifact, 0, len(artifacts))
	copied := map[string]computeimage.Artifact{}
	for _, artifact := range artifacts {
		if err := ctx.Err(); err != nil {
			return nil, nil, err
		}
		dir := filepath.Join(hostRoot, artifact.Release.Entry.Fingerprint)
		metadata := filepath.Join(dir, "incus.tar.xz")
		rootfsName := "rootfs.squashfs"
		if artifact.Release.Entry.Interface == "incus_vm" {
			rootfsName = "disk.qcow2"
		}
		rootfs := filepath.Join(dir, rootfsName)
		if previous, exists := copied[artifact.Release.Entry.Fingerprint]; exists {
			if !reflect.DeepEqual(previous, artifact.Release.Artifact) {
				return nil, nil, fmt.Errorf("compute image supply artifacts disagree about identical image bytes")
			}
		} else {
			if err := os.Mkdir(dir, 0700); err != nil {
				return nil, nil, err
			}
			if err := copySupplyFileVerified(ctx, artifact.MetadataPath, metadata, artifact.Release.Artifact.Parts[0]); err != nil {
				return nil, nil, err
			}
			if err := copySupplyFileVerified(ctx, artifact.RootFSPath, rootfs, artifact.Release.Artifact.Parts[1]); err != nil {
				return nil, nil, err
			}
			if err := os.Chmod(dir, 0500); err != nil {
				return nil, nil, err
			}
			copied[artifact.Release.Entry.Fingerprint] = artifact.Release.Artifact
		}
		staged = append(staged, computeImageSupplyArtifact{
			Release:      artifact.Release,
			MetadataPath: filepath.Join(computeImageSupplyContainerRoot, artifact.Release.Entry.Fingerprint, "incus.tar.xz"),
			RootFSPath:   filepath.Join(computeImageSupplyContainerRoot, artifact.Release.Entry.Fingerprint, rootfsName),
		})
	}
	body, err := computeImageSupplyJSON(request.ComputeImages, staged)
	if err != nil {
		return nil, nil, err
	}
	if body == nil {
		return nil, cleanup, nil
	}
	descriptor := filepath.Join(stage, "compute-image-supply.json")
	if err := os.WriteFile(descriptor, body, 0400); err != nil {
		return nil, nil, err
	}
	if err := os.Chmod(hostRoot, 0500); err != nil {
		return nil, nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	keep = true
	return &computeImageSupplyMount{hostDescriptor: descriptor, hostRoot: hostRoot}, cleanup, nil
}

func collectComputeImageSupplyArtifacts(ctx context.Context, providerDir string, snapshot *computeimage.Snapshot) ([]computeImageSupplyArtifact, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if snapshot == nil || len(snapshot.Images) == 0 {
		return nil, fmt.Errorf("compute image supply requires frozen images")
	}
	refs := make([]computeimage.Reference, len(snapshot.Images))
	for i, image := range snapshot.Images {
		refs[i] = image.Reference
	}
	if err := snapshot.Validate(refs, snapshot.Images[0].Target.Interface); err != nil {
		return nil, fmt.Errorf("compute image supply requires a valid frozen snapshot")
	}
	var artifacts []computeImageSupplyArtifact
	seen := map[computeimage.Resolution]bool{}
	for _, image := range snapshot.Images {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if image.Reference.Fingerprint != "" || seen[image] {
			continue
		}
		seen[image] = true
		dir := filepath.Join(providerDir, "images", "artifacts", image.Reference.Catalog, image.Reference.Name, image.Reference.Revision, image.Target.Architecture, image.Target.Interface)
		info, err := os.Lstat(dir)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("compute image artifact directory is invalid")
		}
		if err := validateNoSymlinkAncestors(providerDir, dir); err != nil {
			return nil, err
		}
		release, err := readSupplyRelease(ctx, filepath.Join(dir, "artifact.json"))
		if err != nil {
			return nil, err
		}
		if release.Artifact.Format != computeimage.ArtifactSplit || computeimage.ValidateArtifactResolution(release, image) != nil {
			return nil, fmt.Errorf("compute image artifact does not match frozen resolution")
		}
		metadata := filepath.Join(dir, "incus.tar.xz")
		rootfsName := "rootfs.squashfs"
		if image.Target.Interface == "incus_vm" {
			rootfsName = "disk.qcow2"
		}
		rootfs := filepath.Join(dir, rootfsName)
		if err := validateSupplySource(ctx, metadata, release.Artifact.Parts[0]); err != nil {
			return nil, err
		}
		if err := validateSupplySource(ctx, rootfs, release.Artifact.Parts[1]); err != nil {
			return nil, err
		}
		artifacts = append(artifacts, computeImageSupplyArtifact{Release: release, MetadataPath: metadata, RootFSPath: rootfs})
	}
	return artifacts, nil
}

func readSupplyRelease(ctx context.Context, path string) (computeimage.ArtifactRelease, error) {
	if err := ctx.Err(); err != nil {
		return computeimage.ArtifactRelease{}, err
	}
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() <= 0 || info.Size() > computeimage.MaxArtifactRecordBytes {
		return computeimage.ArtifactRelease{}, fmt.Errorf("compute image artifact descriptor is invalid")
	}
	file, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return computeimage.ArtifactRelease{}, fmt.Errorf("compute image artifact descriptor is unavailable")
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !sameLocalFile(info, opened) {
		return computeimage.ArtifactRelease{}, fmt.Errorf("compute image artifact descriptor is unavailable")
	}
	body, err := io.ReadAll(io.LimitReader(computeSupplyReader{ctx: ctx, reader: file}, computeimage.MaxArtifactRecordBytes+1))
	if ctx.Err() != nil {
		return computeimage.ArtifactRelease{}, ctx.Err()
	}
	current, pathErr := os.Lstat(path)
	if err != nil || pathErr != nil || len(body) > computeimage.MaxArtifactRecordBytes || int64(len(body)) != info.Size() || !sameLocalFile(info, current) {
		return computeimage.ArtifactRelease{}, fmt.Errorf("compute image artifact descriptor is unavailable")
	}
	return computeimage.DecodeArtifactRelease(body)
}

func validateSupplySource(ctx context.Context, path string, part computeimage.ArtifactPart) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() != part.Size || info.Mode().Perm()&0222 != 0 {
		return fmt.Errorf("compute image artifact bytes are invalid")
	}
	file, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return fmt.Errorf("compute image artifact bytes are unavailable")
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !sameLocalFile(info, opened) {
		return fmt.Errorf("compute image artifact bytes are invalid")
	}
	digest, err := digestBoundedFile(ctx, file, part.Size)
	if ctx.Err() != nil {
		return ctx.Err()
	}
	current, pathErr := os.Lstat(path)
	if err != nil || pathErr != nil || digest != part.SHA256 || !sameLocalFile(info, current) {
		return fmt.Errorf("compute image artifact bytes are invalid")
	}
	return nil
}

func copySupplyFile(ctx context.Context, source, destination string, size int64) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	in, err := os.OpenFile(source, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return fmt.Errorf("compute image artifact bytes are unavailable")
	}
	defer in.Close()
	before, err := in.Stat()
	if err != nil || !before.Mode().IsRegular() || before.Size() != size {
		return fmt.Errorf("compute image artifact bytes are invalid")
	}
	out, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0400)
	if err != nil {
		return fmt.Errorf("compute image artifact staging failed")
	}
	keep := false
	defer func() {
		_ = out.Close()
		if !keep {
			_ = os.Remove(destination)
		}
	}()
	n, err := io.CopyBuffer(out, io.LimitReader(computeSupplyReader{ctx: ctx, reader: in}, size+1), make([]byte, 128<<10))
	if ctx.Err() != nil {
		return ctx.Err()
	}
	after, statErr := in.Stat()
	current, pathErr := os.Lstat(source)
	if err != nil || statErr != nil || pathErr != nil || n != size || !sameLocalFile(before, after) || !sameLocalFile(before, current) || out.Sync() != nil {
		return fmt.Errorf("compute image artifact staging failed")
	}
	if err := out.Close(); err != nil {
		return fmt.Errorf("compute image artifact staging failed")
	}
	keep = true
	return nil
}

func copySupplyFileVerified(ctx context.Context, source, destination string, part computeimage.ArtifactPart) error {
	if err := copySupplyFile(ctx, source, destination, part.Size); err != nil {
		return err
	}
	keep := false
	defer func() {
		if !keep {
			_ = os.Remove(destination)
		}
	}()
	file, err := os.OpenFile(destination, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return fmt.Errorf("compute image artifact staging failed")
	}
	defer file.Close()
	digest, err := digestBoundedFile(ctx, file, part.Size)
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err != nil || digest != part.SHA256 {
		return fmt.Errorf("compute image artifact staging failed")
	}
	keep = true
	return nil
}

func digestBoundedFile(ctx context.Context, file *os.File, size int64) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return "", err
	}
	hash := sha256.New()
	n, err := io.CopyBuffer(hash, io.LimitReader(computeSupplyReader{ctx: ctx, reader: file}, size+1), make([]byte, 128<<10))
	if ctx.Err() != nil {
		return "", ctx.Err()
	}
	if err != nil || n != size {
		return "", fmt.Errorf("compute image artifact bytes are invalid")
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

// All sources are nonblocking-opened regular local files. The wrapper also
// prevents io.Copy from bypassing cancellation through an os.File fast path.
type computeSupplyReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r computeSupplyReader) Read(body []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(body)
}

func sameLocalFile(a, b os.FileInfo) bool {
	return a != nil && b != nil && os.SameFile(a, b) && a.Size() == b.Size() && a.Mode() == b.Mode() && a.ModTime().Equal(b.ModTime())
}

func validateNoSymlinkAncestors(root, path string) error {
	root = filepath.Clean(root)
	path = filepath.Clean(path)
	rel, err := filepath.Rel(root, path)
	if err != nil || rel == "." || rel == ".." || filepath.IsAbs(rel) || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return fmt.Errorf("compute image artifact path escapes provider module")
	}
	if info, err := os.Lstat(root); err != nil || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("compute image artifact path contains a symbolic link")
	}
	current := root
	for _, part := range strings.Split(rel, string(os.PathSeparator)) {
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if err != nil || info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("compute image artifact path contains a symbolic link")
		}
	}
	return nil
}

type computeImagePruneDryRun struct {
	Retain []string `json:"retain"`
	Delete []string `json:"delete"`
}

func computeImageRetainedFingerprints(current, previous *deploymentManifest, running []string) []string {
	retain := map[string]bool{}
	for _, manifest := range []*deploymentManifest{current, previous} {
		if manifest == nil {
			continue
		}
		for _, resource := range manifest.Resources {
			if resource.Contract != "compute" || resource.ComputeImages == nil {
				continue
			}
			for _, image := range resource.ComputeImages.Images {
				retain[image.Fingerprint] = true
			}
		}
	}
	for _, fingerprint := range running {
		if fingerprint != "" {
			retain[fingerprint] = true
		}
	}
	out := make([]string, 0, len(retain))
	for fingerprint := range retain {
		out = append(out, fingerprint)
	}
	slices.Sort(out)
	return out
}

func computeImagePruneDryRunPlan(imported, retained []string) (computeImagePruneDryRun, error) {
	plan, err := computeimage.PlanPrune(imported, retained, nil, nil)
	if err != nil {
		return computeImagePruneDryRun{}, err
	}
	return computeImagePruneDryRun{Retain: plan.Retain, Delete: plan.Delete}, nil
}
