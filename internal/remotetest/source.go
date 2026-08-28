package remotetest

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

const SourceBundleAPI = "anas.remote-test-source/v1"

var (
	commitDigestPattern = regexp.MustCompile(`^[0-9a-f]{40,64}$`)
	sha256DigestPattern = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
)

type SourceMode string

const (
	SourceCommitted SourceMode = "committed"
	SourceWorktree  SourceMode = "worktree"
)

type SourceIdentity struct {
	APIVersion        string     `json:"api_version"`
	Mode              SourceMode `json:"mode"`
	BaseCommit        string     `json:"base_commit"`
	BaseArchiveSHA256 string     `json:"base_archive_sha256"`
	PatchSHA256       string     `json:"patch_sha256"`
	UntrackedSHA256   string     `json:"untracked_sha256"`
	SourceSHA256      string     `json:"source_sha256"`
	BundleSHA256      string     `json:"bundle_sha256"`
}

type sourceManifest struct {
	APIVersion        string     `json:"api_version"`
	Mode              SourceMode `json:"mode"`
	BaseCommit        string     `json:"base_commit"`
	BaseArchiveSHA256 string     `json:"base_archive_sha256"`
	PatchSHA256       string     `json:"patch_sha256"`
	UntrackedSHA256   string     `json:"untracked_sha256"`
	SourceSHA256      string     `json:"source_sha256"`
}

func BuildSourceBundle(ctx context.Context, repoRoot, output string, mode SourceMode, baseRef string) (SourceIdentity, error) {
	root, err := filepath.Abs(repoRoot)
	if err != nil {
		return SourceIdentity{}, err
	}
	if mode != SourceCommitted && mode != SourceWorktree {
		return SourceIdentity{}, fmt.Errorf("source mode must be %q or %q", SourceCommitted, SourceWorktree)
	}
	if baseRef == "" || strings.HasPrefix(baseRef, "-") || strings.ContainsAny(baseRef, "\r\n") {
		return SourceIdentity{}, errors.New("source base must be a non-empty Git revision")
	}
	resolved, err := gitCommand(ctx, root, "rev-parse", "--verify", "--end-of-options", baseRef+"^{commit}")
	if err != nil {
		return SourceIdentity{}, err
	}
	baseCommit := strings.TrimSpace(string(resolved))
	baseArchive, err := gitCommand(ctx, root, "archive", "--format=tar", baseCommit)
	if err != nil {
		return SourceIdentity{}, err
	}
	var patch, untracked []byte
	if mode == SourceWorktree {
		patch, err = gitCommand(ctx, root, "diff", "--binary", "--full-index", baseCommit, "--", ".")
		if err != nil {
			return SourceIdentity{}, err
		}
		untracked, err = archiveUntracked(ctx, root)
		if err != nil {
			return SourceIdentity{}, err
		}
	}
	manifest := sourceManifest{
		APIVersion: SourceBundleAPI, Mode: mode, BaseCommit: baseCommit,
		BaseArchiveSHA256: digestBytes(baseArchive), PatchSHA256: digestBytes(patch), UntrackedSHA256: digestBytes(untracked),
	}
	manifest.SourceSHA256 = digestStrings(string(mode), baseCommit, manifest.BaseArchiveSHA256, manifest.PatchSHA256, manifest.UntrackedSHA256)
	manifestJSON, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return SourceIdentity{}, err
	}
	manifestJSON = append(manifestJSON, '\n')
	bundle, err := buildBundleArchive(map[string][]byte{
		"manifest.json":  manifestJSON,
		"base.tar":       baseArchive,
		"worktree.patch": patch,
		"untracked.tar":  untracked,
	})
	if err != nil {
		return SourceIdentity{}, err
	}
	if err := os.MkdirAll(filepath.Dir(output), 0o700); err != nil {
		return SourceIdentity{}, err
	}
	file, err := os.OpenFile(output, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return SourceIdentity{}, err
	}
	_, writeErr := file.Write(bundle)
	closeErr := file.Close()
	if writeErr != nil {
		return SourceIdentity{}, writeErr
	}
	if closeErr != nil {
		return SourceIdentity{}, closeErr
	}
	if err := os.Chmod(output, 0o600); err != nil {
		return SourceIdentity{}, err
	}
	return SourceIdentity{
		APIVersion: manifest.APIVersion, Mode: manifest.Mode, BaseCommit: manifest.BaseCommit,
		BaseArchiveSHA256: manifest.BaseArchiveSHA256, PatchSHA256: manifest.PatchSHA256,
		UntrackedSHA256: manifest.UntrackedSHA256, SourceSHA256: manifest.SourceSHA256,
		BundleSHA256: digestBytes(bundle),
	}, nil
}

func MaterializeSourceBundle(ctx context.Context, bundlePath, expectedBundleSHA256, destination string) (SourceIdentity, error) {
	bundle, err := os.ReadFile(bundlePath)
	if err != nil {
		return SourceIdentity{}, err
	}
	bundleDigest := digestBytes(bundle)
	if expectedBundleSHA256 == "" || bundleDigest != expectedBundleSHA256 {
		return SourceIdentity{}, fmt.Errorf("source bundle digest mismatch: got %s, want %s", bundleDigest, expectedBundleSHA256)
	}
	parts, err := readBundleArchive(bundle)
	if err != nil {
		return SourceIdentity{}, err
	}
	manifestData, ok := parts["manifest.json"]
	if !ok {
		return SourceIdentity{}, errors.New("source bundle has no manifest.json")
	}
	var manifest sourceManifest
	decoder := json.NewDecoder(bytes.NewReader(manifestData))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&manifest); err != nil {
		return SourceIdentity{}, err
	}
	var extra any
	if err := decoder.Decode(&extra); err == nil {
		return SourceIdentity{}, errors.New("source bundle manifest contains multiple JSON values")
	} else if !errors.Is(err, io.EOF) {
		return SourceIdentity{}, err
	}
	if manifest.APIVersion != SourceBundleAPI || (manifest.Mode != SourceCommitted && manifest.Mode != SourceWorktree) {
		return SourceIdentity{}, errors.New("source bundle manifest has an unsupported api_version or mode")
	}
	if !commitDigestPattern.MatchString(manifest.BaseCommit) {
		return SourceIdentity{}, errors.New("source bundle manifest has an invalid base commit")
	}
	for _, digest := range []string{manifest.BaseArchiveSHA256, manifest.PatchSHA256, manifest.UntrackedSHA256, manifest.SourceSHA256} {
		if !sha256DigestPattern.MatchString(digest) {
			return SourceIdentity{}, errors.New("source bundle manifest has an invalid SHA-256 digest")
		}
	}
	baseArchive := parts["base.tar"]
	patch := parts["worktree.patch"]
	untracked := parts["untracked.tar"]
	if manifest.Mode == SourceCommitted && (len(patch) != 0 || len(untracked) != 0) {
		return SourceIdentity{}, errors.New("committed source bundle must not contain worktree components")
	}
	if digestBytes(baseArchive) != manifest.BaseArchiveSHA256 || digestBytes(patch) != manifest.PatchSHA256 || digestBytes(untracked) != manifest.UntrackedSHA256 {
		return SourceIdentity{}, errors.New("source bundle component digest mismatch")
	}
	wantSourceDigest := digestStrings(string(manifest.Mode), manifest.BaseCommit, manifest.BaseArchiveSHA256, manifest.PatchSHA256, manifest.UntrackedSHA256)
	if manifest.SourceSHA256 != wantSourceDigest {
		return SourceIdentity{}, errors.New("source identity digest mismatch")
	}
	if info, err := os.Lstat(destination); err == nil && info.Mode()&os.ModeSymlink != 0 {
		return SourceIdentity{}, errors.New("source destination must not be a symlink")
	} else if err != nil && !os.IsNotExist(err) {
		return SourceIdentity{}, err
	}
	if entries, err := os.ReadDir(destination); err == nil && len(entries) > 0 {
		return SourceIdentity{}, errors.New("source destination must be empty")
	} else if err != nil && !os.IsNotExist(err) {
		return SourceIdentity{}, err
	}
	if err := os.MkdirAll(destination, 0o700); err != nil {
		return SourceIdentity{}, err
	}
	if err := extractTar(baseArchive, destination); err != nil {
		return SourceIdentity{}, fmt.Errorf("extract committed source: %w", err)
	}
	if len(patch) > 0 {
		command := exec.CommandContext(ctx, "git", "apply", "--binary", "--whitespace=nowarn", "-")
		command.Dir = destination
		command.Stdin = bytes.NewReader(patch)
		if output, err := command.CombinedOutput(); err != nil {
			return SourceIdentity{}, fmt.Errorf("apply worktree patch: %w: %s", err, strings.TrimSpace(string(output)))
		}
	}
	if err := rejectSourceSymlinks(destination); err != nil {
		return SourceIdentity{}, err
	}
	if len(untracked) > 0 {
		if err := extractTar(untracked, destination); err != nil {
			return SourceIdentity{}, fmt.Errorf("extract untracked source: %w", err)
		}
	}
	return SourceIdentity{
		APIVersion: manifest.APIVersion, Mode: manifest.Mode, BaseCommit: manifest.BaseCommit,
		BaseArchiveSHA256: manifest.BaseArchiveSHA256, PatchSHA256: manifest.PatchSHA256,
		UntrackedSHA256: manifest.UntrackedSHA256, SourceSHA256: manifest.SourceSHA256,
		BundleSHA256: bundleDigest,
	}, nil
}

func archiveUntracked(ctx context.Context, root string) ([]byte, error) {
	output, err := gitCommand(ctx, root, "ls-files", "--others", "--exclude-standard", "-z", "--", ".")
	if err != nil {
		return nil, err
	}
	var names []string
	for _, raw := range bytes.Split(bytes.TrimSuffix(output, []byte{0}), []byte{0}) {
		if len(raw) > 0 {
			names = append(names, string(raw))
		}
	}
	sort.Strings(names)
	var archive bytes.Buffer
	writer := tar.NewWriter(&archive)
	for _, name := range names {
		if filepath.IsAbs(name) || filepath.Clean(name) != filepath.FromSlash(name) || strings.HasPrefix(name, "..") {
			return nil, fmt.Errorf("untracked path %q is unsafe", name)
		}
		full := filepath.Join(root, filepath.FromSlash(name))
		info, err := os.Lstat(full)
		if err != nil {
			return nil, err
		}
		if !info.Mode().IsRegular() {
			return nil, fmt.Errorf("untracked path %q must be a regular file", name)
		}
		header, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return nil, err
		}
		header.Name = filepath.ToSlash(name)
		header.ModTime = time.Unix(0, 0)
		header.AccessTime = time.Time{}
		header.ChangeTime = time.Time{}
		if err := writer.WriteHeader(header); err != nil {
			return nil, err
		}
		data, err := os.ReadFile(full)
		if err != nil {
			return nil, err
		}
		if _, err := writer.Write(data); err != nil {
			return nil, err
		}
	}
	if err := writer.Close(); err != nil {
		return nil, err
	}
	if len(names) == 0 {
		return nil, nil
	}
	return archive.Bytes(), nil
}

func buildBundleArchive(entries map[string][]byte) ([]byte, error) {
	var compressed bytes.Buffer
	gzipWriter, err := gzip.NewWriterLevel(&compressed, gzip.BestCompression)
	if err != nil {
		return nil, err
	}
	gzipWriter.Header.ModTime = time.Unix(0, 0)
	gzipWriter.Header.OS = 255
	tarWriter := tar.NewWriter(gzipWriter)
	for _, name := range []string{"manifest.json", "base.tar", "worktree.patch", "untracked.tar"} {
		data := entries[name]
		header := &tar.Header{Name: name, Mode: 0o600, Size: int64(len(data)), ModTime: time.Unix(0, 0), Typeflag: tar.TypeReg}
		if err := tarWriter.WriteHeader(header); err != nil {
			return nil, err
		}
		if _, err := tarWriter.Write(data); err != nil {
			return nil, err
		}
	}
	if err := tarWriter.Close(); err != nil {
		return nil, err
	}
	if err := gzipWriter.Close(); err != nil {
		return nil, err
	}
	return compressed.Bytes(), nil
}

func readBundleArchive(bundle []byte) (map[string][]byte, error) {
	gzipReader, err := gzip.NewReader(bytes.NewReader(bundle))
	if err != nil {
		return nil, err
	}
	defer gzipReader.Close()
	reader := tar.NewReader(gzipReader)
	allowed := map[string]bool{"manifest.json": true, "base.tar": true, "worktree.patch": true, "untracked.tar": true}
	parts := make(map[string][]byte)
	for {
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, err
		}
		_, duplicate := parts[header.Name]
		if !allowed[header.Name] || header.Typeflag != tar.TypeReg || duplicate {
			return nil, fmt.Errorf("unexpected source bundle entry %q", header.Name)
		}
		if header.Size < 0 || header.Size > 1<<30 {
			return nil, fmt.Errorf("source bundle entry %q is too large", header.Name)
		}
		data, err := io.ReadAll(io.LimitReader(reader, header.Size+1))
		if err != nil || int64(len(data)) != header.Size {
			return nil, fmt.Errorf("read source bundle entry %q", header.Name)
		}
		parts[header.Name] = data
	}
	if len(parts) != len(allowed) {
		return nil, errors.New("source bundle is incomplete")
	}
	return parts, nil
}

func extractTar(data []byte, destination string) error {
	reader := tar.NewReader(bytes.NewReader(data))
	for {
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		clean := filepath.Clean(filepath.FromSlash(header.Name))
		if header.Name == "" || filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
			return fmt.Errorf("archive entry %q escapes destination", header.Name)
		}
		full := filepath.Join(destination, clean)
		rel, err := filepath.Rel(destination, full)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return fmt.Errorf("archive entry %q escapes destination", header.Name)
		}
		switch header.Typeflag {
		case tar.TypeXHeader, tar.TypeXGlobalHeader:
			continue
		case tar.TypeDir:
			if err := ensureNoSymlinkParents(destination, full); err != nil {
				return err
			}
			if err := os.MkdirAll(full, os.FileMode(header.Mode)&0o755); err != nil {
				return err
			}
		case tar.TypeReg, tar.TypeRegA:
			if err := ensureNoSymlinkParents(destination, full); err != nil {
				return err
			}
			if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
				return err
			}
			file, err := os.OpenFile(full, os.O_CREATE|os.O_EXCL|os.O_WRONLY, os.FileMode(header.Mode)&0o755)
			if err != nil {
				return err
			}
			_, copyErr := io.CopyN(file, reader, header.Size)
			closeErr := file.Close()
			if copyErr != nil {
				return copyErr
			}
			if closeErr != nil {
				return closeErr
			}
		default:
			return fmt.Errorf("archive entry %q uses unsupported type %d", header.Name, header.Typeflag)
		}
	}
}

func rejectSourceSymlinks(root string) error {
	return filepath.WalkDir(root, func(name string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("source bundle may not materialize symlink %s", name)
		}
		return nil
	})
}

func ensureNoSymlinkParents(root, name string) error {
	rel, err := filepath.Rel(root, filepath.Dir(name))
	if err != nil {
		return err
	}
	current := root
	if rel == "." {
		return nil
	}
	for _, component := range strings.Split(rel, string(filepath.Separator)) {
		current = filepath.Join(current, component)
		info, err := os.Lstat(current)
		if os.IsNotExist(err) {
			return nil
		}
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("source path parent %s is a symlink", current)
		}
	}
	return nil
}

func gitCommand(ctx context.Context, root string, args ...string) ([]byte, error) {
	command := exec.CommandContext(ctx, "git", append([]string{"-C", root}, args...)...)
	output, err := command.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(output)))
	}
	return output, nil
}

func digestBytes(data []byte) string {
	digest := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(digest[:])
}

func digestStrings(values ...string) string {
	hash := sha256.New()
	for _, value := range values {
		fmt.Fprintf(hash, "%d\x00%s\n", len(value), value)
	}
	return "sha256:" + hex.EncodeToString(hash.Sum(nil))
}
