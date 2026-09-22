package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/anas-project/ANAS/internal/computeimage"
)

type imageRecord struct {
	Fingerprint  string `json:"fingerprint"`
	Architecture string `json:"architecture"`
	Type         string `json:"type"`
}

func verifyImages(ctx context.Context, c *client, l lease) error {
	architecture := map[string]string{"amd64": "x86_64", "arm64": "aarch64"}[l.ImageArchitecture]
	if architecture == "" {
		return fmt.Errorf("image architecture must be amd64 or arm64")
	}
	imageType := map[string]string{"container": "container", "vm": "virtual-machine"}[l.Isolation]
	if imageType == "" {
		return fmt.Errorf("unsupported image isolation")
	}
	supply, err := loadImageSupply(l.ImageSupplyFile)
	if err != nil {
		return err
	}
	for _, pin := range l.ImageAllowlist {
		if err := verifyImage(ctx, c, l.Sandbox, pin, architecture, imageType); err == nil {
			continue
		} else if !isNotFound(err) {
			return err
		}
		artifact, ok := supply.find(pin, l.ImageArchitecture, "incus_"+l.Isolation)
		if !ok {
			return fmt.Errorf("compute image %s is unavailable in project %s; provide the identical trusted artifact before apply", pin, l.Sandbox)
		}
		if err := importSuppliedImage(ctx, c, l.Sandbox, artifact); err != nil {
			return err
		}
		if err := verifyImage(ctx, c, l.Sandbox, pin, architecture, imageType); err != nil {
			return fmt.Errorf("compute image %s import did not produce the frozen target metadata", pin)
		}
	}
	return nil
}

func verifyImage(ctx context.Context, c *client, project, pin, architecture, imageType string) error {
	var image imageRecord
	if err := c.do(ctx, "GET", "/1.0/images/"+pin+"?project="+url.QueryEscape(project), nil, &image); err != nil {
		return err
	}
	if image.Fingerprint != pin || image.Architecture != architecture || image.Type != imageType {
		return fmt.Errorf("compute image %s does not match its frozen fingerprint, architecture or isolation", pin)
	}
	return nil
}

const (
	maxImageSupplyBytes = computeimage.MaxImageSupplyBytes
)

var (
	defaultImageSupplyFile = "/run/anas/compute-image-supply.json"
	imageSupplyRoot        = "/run/anas/compute-image-supply"
)

type localImageSupply struct {
	Version string             `json:"version"`
	Images  []localSupplyImage `json:"images"`
}

type localSupplyImage struct {
	computeimage.SuppliedImage
	MetadataPath string `json:"metadata_path,omitempty"`
	RootFSPath   string `json:"rootfs_path,omitempty"`
	ImagePath    string `json:"image_path,omitempty"`
}

func loadImageSupply(path string) (localImageSupply, error) {
	if strings.TrimSpace(path) == "" {
		path = defaultImageSupplyFile
	}
	if filepath.Clean(path) != defaultImageSupplyFile {
		return localImageSupply{}, fmt.Errorf("compute image supply path is fixed by the provider runtime")
	}
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return localImageSupply{Version: computeimage.ImageSupplyVersion}, nil
	}
	if err != nil {
		return localImageSupply{}, fmt.Errorf("compute image supply is unavailable")
	}
	if err := validateSupplyParent(filepath.Dir(path)); err != nil {
		return localImageSupply{}, err
	}
	if err := validateSupplyRoot(imageSupplyRoot); err != nil {
		return localImageSupply{}, err
	}
	if !supplyFileInfoOK(info, maxImageSupplyBytes) {
		return localImageSupply{}, fmt.Errorf("compute image supply must be a bounded regular file")
	}
	file, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return localImageSupply{}, fmt.Errorf("compute image supply is unavailable")
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !sameSupplyFile(info, opened) {
		return localImageSupply{}, fmt.Errorf("compute image supply is unavailable")
	}
	body, err := io.ReadAll(io.LimitReader(file, maxImageSupplyBytes+1))
	current, pathErr := os.Lstat(path)
	if err != nil || pathErr != nil || len(body) > maxImageSupplyBytes || int64(len(body)) != info.Size() || !sameSupplyFile(info, current) {
		return localImageSupply{}, fmt.Errorf("compute image supply is unavailable")
	}
	var doc localImageSupply
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&doc) != nil || decoder.Decode(&struct{}{}) != io.EOF {
		return localImageSupply{}, fmt.Errorf("compute image supply is invalid")
	}
	shared := computeimage.ImageSupplyDocument{Version: doc.Version, Images: make([]computeimage.SuppliedImage, len(doc.Images))}
	for i, image := range doc.Images {
		shared.Images[i] = image.SuppliedImage
		if err := validateSupplyPaths(image); err != nil {
			return localImageSupply{}, err
		}
	}
	if shared.Validate() != nil {
		return localImageSupply{}, fmt.Errorf("compute image supply is invalid")
	}
	canonical, err := encodeLocalImageSupply(doc)
	if err != nil || !bytes.Equal(body, canonical) {
		return localImageSupply{}, fmt.Errorf("compute image supply is invalid")
	}
	return doc, nil
}

func encodeLocalImageSupply(doc localImageSupply) ([]byte, error) {
	shared := computeimage.ImageSupplyDocument{Version: doc.Version, Images: make([]computeimage.SuppliedImage, len(doc.Images))}
	for i, image := range doc.Images {
		shared.Images[i] = image.SuppliedImage
		if err := validateSupplyPaths(image); err != nil {
			return nil, err
		}
	}
	if shared.Validate() != nil {
		return nil, fmt.Errorf("compute image supply is invalid")
	}
	body, err := json.Marshal(doc)
	if err != nil || len(body)+1 > maxImageSupplyBytes {
		return nil, fmt.Errorf("compute image supply is invalid")
	}
	return append(body, '\n'), nil
}

func validateSupplyPaths(image localSupplyImage) error {
	switch image.Release.Artifact.Format {
	case computeimage.ArtifactSplit:
		if image.MetadataPath == "" || image.RootFSPath == "" || image.ImagePath != "" {
			return fmt.Errorf("split image supply requires metadata and rootfs paths")
		}
		if err := validateSupplyPartPath(image.MetadataPath, "incus.tar.xz"); err != nil {
			return err
		}
		rootfsName := "rootfs.squashfs"
		if image.Release.Artifact.Target.Interface == "incus_vm" {
			rootfsName = "disk.qcow2"
		}
		if err := validateSupplyPartPath(image.RootFSPath, rootfsName); err != nil {
			return err
		}
	default:
		return fmt.Errorf("provider image import accepts distrobuilder split artifacts only")
	}
	return nil
}

func validateSupplyPartPath(path, basename string) error {
	clean := filepath.Clean(path)
	if path == "" || !filepath.IsAbs(path) || clean != path || filepath.Base(path) != basename {
		return fmt.Errorf("image artifact paths must be canonical paths under the fixed supply root")
	}
	rel, err := filepath.Rel(imageSupplyRoot, path)
	if err != nil || rel == "." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) || rel == ".." || filepath.IsAbs(rel) {
		return fmt.Errorf("image artifact paths must stay under the fixed supply root")
	}
	return nil
}

func (s localImageSupply) find(fingerprint, architecture, iface string) (localSupplyImage, bool) {
	for _, image := range s.Images {
		if image.Resolution.Fingerprint == fingerprint && image.Resolution.Target.Architecture == architecture && image.Resolution.Target.Interface == iface {
			return image, true
		}
	}
	return localSupplyImage{}, false
}

func importSuppliedImage(ctx context.Context, c *client, project string, image localSupplyImage) error {
	parts, readers, err := openSuppliedReaders(image)
	if err != nil {
		return err
	}
	for _, part := range parts {
		defer part.file.Close()
	}
	if err := computeimage.VerifySuppliedImage(ctx, image.SuppliedImage, readers); err != nil {
		return fmt.Errorf("compute image supply failed integrity checks")
	}
	for _, part := range parts {
		if _, err := part.file.Seek(0, io.SeekStart); err != nil {
			return fmt.Errorf("compute image supply is unavailable")
		}
	}
	imported, err := c.doMultipartOperation(ctx, "/1.0/images?project="+url.QueryEscape(project), project, func(writer *multipart.Writer) error {
		if err := copyImagePart(writer, "metadata", "incus.tar.xz", parts[0]); err != nil {
			return err
		}
		rootfsName := "rootfs.squashfs"
		rootfsField := "rootfs"
		if image.Resolution.Target.Interface == "incus_vm" {
			rootfsName = "disk.qcow2"
			rootfsField = "rootfs.img"
		}
		return copyImagePart(writer, rootfsField, rootfsName, parts[1])
	})
	if err != nil {
		return err
	}
	if imported != image.Resolution.Fingerprint {
		return fmt.Errorf("incus image import returned a fingerprint different from the frozen resolution")
	}
	return nil
}

type suppliedPart struct {
	file *os.File
	path string
	info os.FileInfo
	part computeimage.ArtifactPart
}

func openSuppliedReaders(image localSupplyImage) ([]suppliedPart, []io.Reader, error) {
	paths := []string{image.MetadataPath, image.RootFSPath}
	parts := make([]suppliedPart, 0, len(paths))
	readers := make([]io.Reader, 0, len(paths))
	for i, path := range paths {
		part := image.Release.Artifact.Parts[i]
		file, info, err := openSupplyPart(path, part)
		if err != nil {
			for _, opened := range parts {
				_ = opened.file.Close()
			}
			return nil, nil, fmt.Errorf("compute image supply is unavailable")
		}
		parts = append(parts, suppliedPart{file: file, path: path, info: info, part: part})
		readers = append(readers, file)
	}
	return parts, readers, nil
}

func openSupplyPart(path string, part computeimage.ArtifactPart) (*os.File, os.FileInfo, error) {
	if err := validateSupplyPartPath(path, filepath.Base(path)); err != nil {
		return nil, nil, err
	}
	if err := validateSupplyRoot(imageSupplyRoot); err != nil {
		return nil, nil, err
	}
	if err := validateSupplyAncestors(imageSupplyRoot, path); err != nil {
		return nil, nil, err
	}
	info, err := os.Lstat(path)
	if err != nil || !supplyFileInfoOK(info, part.Size) || info.Size() != part.Size {
		return nil, nil, fmt.Errorf("invalid supply file")
	}
	file, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, nil, err
	}
	opened, err := file.Stat()
	if err != nil || !sameSupplyFile(info, opened) {
		_ = file.Close()
		return nil, nil, fmt.Errorf("invalid supply file")
	}
	return file, info, nil
}

func copyImagePart(writer *multipart.Writer, field, filename string, supplied suppliedPart) error {
	part, err := writer.CreateFormFile(field, filename)
	if err != nil {
		return err
	}
	hash := sha256.New()
	n, err := io.CopyBuffer(io.MultiWriter(part, hash), io.LimitReader(supplied.file, supplied.part.Size+1), make([]byte, 128<<10))
	current, statErr := os.Lstat(supplied.path)
	after, fileErr := supplied.file.Stat()
	if err != nil || statErr != nil || fileErr != nil || n != supplied.part.Size || hex.EncodeToString(hash.Sum(nil)) != supplied.part.SHA256 || !sameSupplyFile(supplied.info, current) || !sameSupplyFile(supplied.info, after) {
		return fmt.Errorf("compute image supply changed while it was being uploaded")
	}
	return nil
}

type imagePrunePlan struct {
	Retain []string `json:"retain"`
	Delete []string `json:"delete"`
}

func planImagePrune(imported, current, previous, running []string) (imagePrunePlan, error) {
	plan, err := computeimage.PlanPrune(imported, current, previous, running)
	if err != nil {
		return imagePrunePlan{}, err
	}
	return imagePrunePlan{Retain: plan.Retain, Delete: plan.Delete}, nil
}

func validateSupplyParent(path string) error {
	info, err := os.Lstat(path)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0022 != 0 {
		return fmt.Errorf("compute image supply parent is not a protected directory")
	}
	return nil
}

func validateSupplyRoot(path string) error {
	info, err := os.Lstat(path)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0022 != 0 {
		return fmt.Errorf("compute image supply root is not a protected directory")
	}
	return nil
}

func validateSupplyAncestors(root, path string) error {
	root = filepath.Clean(root)
	path = filepath.Clean(path)
	rel, err := filepath.Rel(root, path)
	if err != nil || rel == "." || rel == ".." || filepath.IsAbs(rel) || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return fmt.Errorf("image artifact paths must stay under the fixed supply root")
	}
	current := root
	parts := strings.Split(rel, string(os.PathSeparator))
	for _, part := range parts[:len(parts)-1] {
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0022 != 0 {
			return fmt.Errorf("compute image supply path is not protected")
		}
	}
	return nil
}

func supplyFileInfoOK(info os.FileInfo, limit int64) bool {
	if info == nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0222 != 0 || info.Size() <= 0 || info.Size() > limit {
		return false
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && stat.Nlink == 1
}

func sameSupplyFile(a, b os.FileInfo) bool {
	return a != nil && b != nil && os.SameFile(a, b) && a.Size() == b.Size() && a.Mode() == b.Mode() && a.ModTime().Equal(b.ModTime())
}
