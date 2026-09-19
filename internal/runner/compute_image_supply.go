package runner

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
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
	byFingerprint := map[string]computeImageSupplyArtifact{}
	for _, artifact := range artifacts {
		if artifact.Release.Validate() != nil || artifact.Release.Artifact.Format != computeimage.ArtifactSplit {
			return nil, fmt.Errorf("compute image supply artifact is invalid")
		}
		if artifact.MetadataPath == "" || artifact.RootFSPath == "" ||
			!filepath.IsAbs(artifact.MetadataPath) || !filepath.IsAbs(artifact.RootFSPath) ||
			filepath.Clean(artifact.MetadataPath) != artifact.MetadataPath || filepath.Clean(artifact.RootFSPath) != artifact.RootFSPath {
			return nil, fmt.Errorf("compute image supply artifact paths must be absolute")
		}
		key := artifact.Release.Entry.Fingerprint + "\x00" + artifact.Release.Entry.Architecture + "\x00" + artifact.Release.Entry.Interface
		if _, exists := byFingerprint[key]; exists {
			return nil, fmt.Errorf("compute image supply artifact duplicates a frozen target")
		}
		byFingerprint[key] = artifact
	}
	doc := computeImageSupplyFile{Version: computeimage.ImageSupplyVersion}
	for _, image := range snapshot.Images {
		key := image.Fingerprint + "\x00" + image.Target.Architecture + "\x00" + image.Target.Interface
		artifact, ok := byFingerprint[key]
		if !ok {
			continue
		}
		if image.Reference.Fingerprint == "" &&
			(image.Reference.Catalog != artifact.Release.Entry.Catalog || image.Reference.Name != artifact.Release.Entry.Name ||
				image.Reference.Revision != artifact.Release.Entry.Revision || image.RecipeDigest != artifact.Release.Entry.RecipeDigest) {
			return nil, fmt.Errorf("compute image supply artifact does not match the frozen catalog reference")
		}
		doc.Images = append(doc.Images, computeImageSupplyEntry{
			SuppliedImage: computeimage.SuppliedImage{Resolution: image, Release: artifact.Release},
			MetadataPath:  artifact.MetadataPath, RootFSPath: artifact.RootFSPath,
		})
	}
	if len(doc.Images) == 0 {
		return nil, nil
	}
	body, err := json.Marshal(doc)
	if err != nil {
		return nil, err
	}
	return append(body, '\n'), nil
}

func (a *app) prepareComputeImageSupply(providerDir string, request ResourceRequest) (*computeImageSupplyMount, func(), error) {
	if request.Contract != "compute" || request.ComputeImages == nil || len(request.ComputeImages.Images) == 0 {
		return nil, func() {}, nil
	}
	artifacts, err := collectComputeImageSupplyArtifacts(providerDir, request.ComputeImages)
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
	for _, artifact := range artifacts {
		dir := filepath.Join(hostRoot, artifact.Release.Entry.Fingerprint)
		if err := os.Mkdir(dir, 0700); err != nil {
			return nil, nil, err
		}
		metadata := filepath.Join(dir, "incus.tar.xz")
		rootfsName := "rootfs.squashfs"
		if artifact.Release.Entry.Interface == "incus_vm" {
			rootfsName = "disk.qcow2"
		}
		rootfs := filepath.Join(dir, rootfsName)
		if err := copySupplyFileVerified(artifact.MetadataPath, metadata, artifact.Release.Artifact.Parts[0]); err != nil {
			return nil, nil, err
		}
		if err := copySupplyFileVerified(artifact.RootFSPath, rootfs, artifact.Release.Artifact.Parts[1]); err != nil {
			return nil, nil, err
		}
		if err := os.Chmod(dir, 0500); err != nil {
			return nil, nil, err
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
	keep = true
	return &computeImageSupplyMount{hostDescriptor: descriptor, hostRoot: hostRoot}, cleanup, nil
}

func collectComputeImageSupplyArtifacts(providerDir string, snapshot *computeimage.Snapshot) ([]computeImageSupplyArtifact, error) {
	var artifacts []computeImageSupplyArtifact
	for _, image := range snapshot.Images {
		if image.Reference.Fingerprint != "" {
			continue
		}
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
		release, err := readSupplyRelease(filepath.Join(dir, "artifact.json"))
		if err != nil {
			return nil, err
		}
		if release.Entry.Catalog != image.Reference.Catalog || release.Entry.Name != image.Reference.Name ||
			release.Entry.Revision != image.Reference.Revision || release.Entry.Target != image.Target ||
			release.Entry.Fingerprint != image.Fingerprint || release.Entry.RecipeDigest != image.RecipeDigest {
			return nil, fmt.Errorf("compute image artifact does not match frozen resolution")
		}
		metadata := filepath.Join(dir, "incus.tar.xz")
		rootfsName := "rootfs.squashfs"
		if image.Target.Interface == "incus_vm" {
			rootfsName = "disk.qcow2"
		}
		rootfs := filepath.Join(dir, rootfsName)
		if err := validateSupplySource(metadata, release.Artifact.Parts[0]); err != nil {
			return nil, err
		}
		if err := validateSupplySource(rootfs, release.Artifact.Parts[1]); err != nil {
			return nil, err
		}
		artifacts = append(artifacts, computeImageSupplyArtifact{Release: release, MetadataPath: metadata, RootFSPath: rootfs})
	}
	return artifacts, nil
}

func readSupplyRelease(path string) (computeimage.ArtifactRelease, error) {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() <= 0 || info.Size() > computeimage.MaxArtifactRecordBytes {
		return computeimage.ArtifactRelease{}, fmt.Errorf("compute image artifact descriptor is invalid")
	}
	file, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return computeimage.ArtifactRelease{}, fmt.Errorf("compute image artifact descriptor is unavailable")
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !sameLocalFile(info, opened) {
		return computeimage.ArtifactRelease{}, fmt.Errorf("compute image artifact descriptor is unavailable")
	}
	body, err := io.ReadAll(io.LimitReader(file, computeimage.MaxArtifactRecordBytes+1))
	current, pathErr := os.Lstat(path)
	if err != nil || pathErr != nil || len(body) > computeimage.MaxArtifactRecordBytes || int64(len(body)) != info.Size() || !sameLocalFile(info, current) {
		return computeimage.ArtifactRelease{}, fmt.Errorf("compute image artifact descriptor is unavailable")
	}
	return computeimage.DecodeArtifactRelease(body)
}

func validateSupplySource(path string, part computeimage.ArtifactPart) error {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() != part.Size || info.Mode().Perm()&0222 != 0 {
		return fmt.Errorf("compute image artifact bytes are invalid")
	}
	file, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return fmt.Errorf("compute image artifact bytes are unavailable")
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !sameLocalFile(info, opened) {
		return fmt.Errorf("compute image artifact bytes are invalid")
	}
	digest, err := digestBoundedFile(file, part.Size)
	current, pathErr := os.Lstat(path)
	if err != nil || pathErr != nil || digest != part.SHA256 || !sameLocalFile(info, current) {
		return fmt.Errorf("compute image artifact bytes are invalid")
	}
	return nil
}

func copySupplyFile(source, destination string, size int64) error {
	in, err := os.OpenFile(source, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
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
	n, err := io.CopyBuffer(out, io.LimitReader(in, size+1), make([]byte, 128<<10))
	after, statErr := in.Stat()
	current, pathErr := os.Lstat(source)
	if err != nil || statErr != nil || pathErr != nil || n != size || !sameLocalFile(before, after) || !sameLocalFile(before, current) || out.Sync() != nil {
		return fmt.Errorf("compute image artifact staging failed")
	}
	keep = true
	return nil
}

func copySupplyFileVerified(source, destination string, part computeimage.ArtifactPart) error {
	if err := copySupplyFile(source, destination, part.Size); err != nil {
		return err
	}
	file, err := os.OpenFile(destination, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return fmt.Errorf("compute image artifact staging failed")
	}
	defer file.Close()
	digest, err := digestBoundedFile(file, part.Size)
	if err != nil || digest != part.SHA256 {
		_ = os.Remove(destination)
		return fmt.Errorf("compute image artifact staging failed")
	}
	return nil
}

func digestBoundedFile(file *os.File, size int64) (string, error) {
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return "", err
	}
	hash := sha256.New()
	n, err := io.CopyBuffer(hash, io.LimitReader(file, size+1), make([]byte, 128<<10))
	if err != nil || n != size {
		return "", fmt.Errorf("compute image artifact bytes are invalid")
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
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
