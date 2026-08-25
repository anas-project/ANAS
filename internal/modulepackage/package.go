package modulepackage

import (
	"archive/tar"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

const PackageAPIVersion = "anas.module-package/v1"

var (
	moduleNamePattern = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)
	repositoryPattern = regexp.MustCompile(`^anas-module-[a-z0-9-]+$`)
)

type CatalogEntry struct {
	Module         string   `json:"module"`
	Repository     string   `json:"repository"`
	Platforms      []string `json:"platforms"`
	SharedContexts []string `json:"shared_contexts"`
}

type moduleManifest struct {
	APIVersion string `yaml:"api_version"`
	Name       string `yaml:"name"`
	Version    string `yaml:"version"`
	Revision   int    `yaml:"revision"`
	AppVersion string `yaml:"app_version"`
	ABI        struct {
		Supports []string `yaml:"supports"`
	} `yaml:"abi"`
	Management struct {
		CommandExecutor struct {
			Command []string `yaml:"command"`
		} `yaml:"command_executor"`
	} `yaml:"management"`
}

type composeDocument struct {
	Services map[string]struct {
		Image string `yaml:"image"`
	} `yaml:"services"`
}

type SourceMetadata struct {
	Repository string `yaml:"repository" json:"repository"`
	Commit     string `yaml:"commit" json:"commit"`
}

type CompatibilityMetadata struct {
	ModuleAPI   string   `yaml:"module_api" json:"module_api"`
	HookABIs    []string `yaml:"hook_abis,omitempty" json:"hook_abis,omitempty"`
	CommandABIs []string `yaml:"command_abis,omitempty" json:"command_abis,omitempty"`
}

type PackageMetadata struct {
	APIVersion    string                `yaml:"api_version" json:"api_version"`
	Name          string                `yaml:"name" json:"name"`
	Version       string                `yaml:"version" json:"version"`
	Revision      int                   `yaml:"revision" json:"revision"`
	Release       string                `yaml:"release" json:"release"`
	AppVersion    string                `yaml:"app_version,omitempty" json:"app_version,omitempty"`
	Platforms     []string              `yaml:"platforms" json:"platforms"`
	Repository    string                `yaml:"repository" json:"repository"`
	Source        SourceMetadata        `yaml:"source" json:"source"`
	Compatibility CompatibilityMetadata `yaml:"compatibility" json:"compatibility"`
	ContextDigest string                `yaml:"context_digest" json:"context_digest"`
	ContentDigest string                `yaml:"content_digest" json:"content_digest"`
	Images        []string              `yaml:"images,omitempty" json:"images,omitempty"`
}

type BuildOptions struct {
	RepoRoot      string
	CatalogPath   string
	Module        string
	Platform      string
	OutputPath    string
	SkipHookBuild bool
}

type BuildResult struct {
	Metadata PackageMetadata
	Path     string
}

func LoadCatalog(path string) ([]CatalogEntry, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var entries []CatalogEntry
	if err := json.Unmarshal(b, &entries); err != nil {
		return nil, err
	}
	if len(entries) == 0 {
		return nil, errors.New("module package catalog is empty")
	}
	return entries, nil
}

func ValidateCatalog(repoRoot string, entries []CatalogEntry) error {
	seen := map[string]bool{}
	seenRepositories := map[string]bool{}
	for _, entry := range entries {
		if !moduleNamePattern.MatchString(entry.Module) || !repositoryPattern.MatchString(entry.Repository) {
			return fmt.Errorf("invalid Module catalog identity %q / %q", entry.Module, entry.Repository)
		}
		if seen[entry.Module] {
			return fmt.Errorf("module %q appears more than once in package catalog", entry.Module)
		}
		if seenRepositories[entry.Repository] {
			return fmt.Errorf("repository %q appears more than once in package catalog", entry.Repository)
		}
		seen[entry.Module] = true
		seenRepositories[entry.Repository] = true
		manifest := filepath.Join(repoRoot, "modules", entry.Module, "module.yml")
		if _, err := os.Stat(manifest); err != nil {
			return fmt.Errorf("catalog module %q: %w", entry.Module, err)
		}
		if len(entry.Platforms) == 0 {
			return fmt.Errorf("module %q has no package platforms", entry.Module)
		}
		seenPlatforms := map[string]bool{}
		for _, platform := range entry.Platforms {
			if _, _, err := splitPlatform(platform); err != nil {
				return fmt.Errorf("module %q: %w", entry.Module, err)
			}
			if seenPlatforms[platform] {
				return fmt.Errorf("module %q repeats package platform %q", entry.Module, platform)
			}
			seenPlatforms[platform] = true
		}
		seenContexts := map[string]bool{}
		for _, context := range entry.SharedContexts {
			clean := filepath.Clean(context)
			if context == "" || filepath.IsAbs(context) || clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
				return fmt.Errorf("module %q has unsafe shared context %q", entry.Module, context)
			}
			if seenContexts[clean] {
				return fmt.Errorf("module %q repeats shared context %q", entry.Module, context)
			}
			seenContexts[clean] = true
			if _, err := os.Stat(filepath.Join(repoRoot, context)); err != nil {
				return fmt.Errorf("module %q shared context %q: %w", entry.Module, context, err)
			}
		}
	}

	moduleEntries, err := os.ReadDir(filepath.Join(repoRoot, "modules"))
	if err != nil {
		return err
	}
	for _, entry := range moduleEntries {
		if entry.IsDir() {
			if _, err := os.Stat(filepath.Join(repoRoot, "modules", entry.Name(), "module.yml")); err == nil && !seen[entry.Name()] {
				return fmt.Errorf("module %q is missing from package catalog", entry.Name())
			}
		}
	}
	return nil
}

func Build(opts BuildOptions) (BuildResult, error) {
	if strings.TrimSpace(opts.Platform) == "" {
		opts.Platform = DefaultPlatform()
	}
	root, err := filepath.Abs(opts.RepoRoot)
	if err != nil {
		return BuildResult{}, err
	}
	entries, err := LoadCatalog(filepath.Join(root, opts.CatalogPath))
	if err != nil {
		return BuildResult{}, err
	}
	if err := ValidateCatalog(root, entries); err != nil {
		return BuildResult{}, err
	}
	entry, ok := catalogModule(entries, opts.Module)
	if !ok {
		return BuildResult{}, fmt.Errorf("unknown module %q", opts.Module)
	}
	platforms := append([]string(nil), entry.Platforms...)
	if opts.Platform != "all" {
		if !contains(entry.Platforms, opts.Platform) {
			return BuildResult{}, fmt.Errorf("module %q is not published for %s", opts.Module, opts.Platform)
		}
		platforms = []string{opts.Platform}
	}

	manifestPath := filepath.Join(root, "modules", entry.Module, "module.yml")
	manifestBody, err := os.ReadFile(manifestPath)
	if err != nil {
		return BuildResult{}, err
	}
	var manifest moduleManifest
	if err := yaml.Unmarshal(manifestBody, &manifest); err != nil {
		return BuildResult{}, err
	}
	if manifest.Name != entry.Module || manifest.Version == "" || manifest.Revision < 1 {
		return BuildResult{}, fmt.Errorf("module %q has inconsistent release metadata", entry.Module)
	}

	tracked, err := runtimeTrackedFiles(root, entry.Module)
	if err != nil {
		return BuildResult{}, err
	}
	contextFiles, err := expandContextFiles(root, append(
		append([]string(nil), entry.SharedContexts...),
		"cmd/package-module", "internal/modulepackage",
	))
	if err != nil {
		return BuildResult{}, err
	}
	contextDigest, err := digestBuildContext(root, append(append([]string(nil), tracked...), contextFiles...), entry)
	if err != nil {
		return BuildResult{}, err
	}

	stage, err := os.MkdirTemp("", "anas-module-package-*")
	if err != nil {
		return BuildResult{}, err
	}
	defer os.RemoveAll(stage)
	moduleRoot := filepath.Join(root, "modules", entry.Module)
	for _, rel := range tracked {
		within, err := filepath.Rel(moduleRoot, filepath.Join(root, rel))
		if err != nil {
			return BuildResult{}, err
		}
		if err := copyPackageFile(filepath.Join(root, rel), filepath.Join(stage, within)); err != nil {
			return BuildResult{}, err
		}
	}
	// Contract schemas are part of the Runner ABI consumed by Module manifests.
	// A remotely installed Module must not depend on a source checkout merely to
	// validate or execute its provider/resource contracts, so every bundle carries
	// the small runtime-only contract tree. A workspace view de-duplicates it.
	if err := copyRuntimeTree(filepath.Join(root, "contracts"), filepath.Join(stage, "contracts"), "contracts"); err != nil {
		return BuildResult{}, err
	}

	hookDir := filepath.Join(moduleRoot, "hook")
	if info, statErr := os.Stat(hookDir); statErr == nil && info.IsDir() && !opts.SkipHookBuild {
		for _, platform := range platforms {
			osName, arch, splitErr := splitPlatform(platform)
			if splitErr != nil {
				return BuildResult{}, splitErr
			}
			output := filepath.Join(stage, "hook", "bin", osName+"-"+arch, "anas-hook")
			if err := os.MkdirAll(filepath.Dir(output), 0755); err != nil {
				return BuildResult{}, err
			}
			cmd := exec.Command("go", "build", "-trimpath", "-ldflags=-s -w", "-o", output, "./modules/"+entry.Module+"/hook")
			cmd.Dir = root
			cmd.Env = append(os.Environ(), "CGO_ENABLED=0", "GOOS="+osName, "GOARCH="+arch)
			if body, buildErr := cmd.CombinedOutput(); buildErr != nil {
				return BuildResult{}, fmt.Errorf("build %s hook for %s: %w\n%s", entry.Module, platform, buildErr, body)
			}
		}
	}
	command := manifest.Management.CommandExecutor.Command
	if contains(manifest.ABI.Supports, "anas.module-command/v1") && len(command) == 3 && command[0] == "go" && command[1] == "run" &&
		filepath.ToSlash(filepath.Clean(command[2])) == "command" && !opts.SkipHookBuild {
		if err := buildCommandExecutors(root, stage, entry.Module, platforms); err != nil {
			return BuildResult{}, err
		}
	}

	contentDigest, err := digestDirectory(stage)
	if err != nil {
		return BuildResult{}, err
	}
	commit := gitOutput(root, "rev-parse", "HEAD")
	images, err := composeImages(filepath.Join(moduleRoot, "docker-compose.yml"))
	if err != nil {
		return BuildResult{}, err
	}
	metadata := PackageMetadata{
		APIVersion: PackageAPIVersion,
		Name:       manifest.Name,
		Version:    manifest.Version,
		Revision:   manifest.Revision,
		Release:    fmt.Sprintf("%s-r%d", manifest.Version, manifest.Revision),
		AppVersion: manifest.AppVersion,
		Platforms:  platforms,
		Repository: entry.Repository,
		Source: SourceMetadata{
			Repository: "https://github.com/anas-project/ANAS",
			Commit:     commit,
		},
		Compatibility: CompatibilityMetadata{
			ModuleAPI:   manifest.APIVersion,
			HookABIs:    filterABIs(manifest.ABI.Supports, "anas.module-hook/"),
			CommandABIs: filterABIs(manifest.ABI.Supports, "anas.module-command/"),
		},
		ContextDigest: "sha256:" + contextDigest,
		ContentDigest: "sha256:" + contentDigest,
		Images:        images,
	}
	metadataBody, err := yaml.Marshal(&metadata)
	if err != nil {
		return BuildResult{}, err
	}
	if err := os.WriteFile(filepath.Join(stage, "package.yml"), metadataBody, 0644); err != nil {
		return BuildResult{}, err
	}

	output, err := filepath.Abs(opts.OutputPath)
	if err != nil {
		return BuildResult{}, err
	}
	if err := os.MkdirAll(filepath.Dir(output), 0755); err != nil {
		return BuildResult{}, err
	}
	if err := writeTarGz(stage, output); err != nil {
		return BuildResult{}, err
	}
	return BuildResult{Metadata: metadata, Path: output}, nil
}

func catalogModule(entries []CatalogEntry, name string) (CatalogEntry, bool) {
	for _, entry := range entries {
		if entry.Module == name {
			return entry, true
		}
	}
	return CatalogEntry{}, false
}

func splitPlatform(platform string) (string, string, error) {
	parts := strings.Split(platform, "/")
	if len(parts) != 2 || parts[0] != "linux" || (parts[1] != "amd64" && parts[1] != "arm64") {
		return "", "", fmt.Errorf("unsupported Module package platform %q", platform)
	}
	return parts[0], parts[1], nil
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func filterABIs(values []string, prefix string) []string {
	out := []string{}
	for _, value := range values {
		if strings.HasPrefix(value, prefix) {
			out = append(out, value)
		}
	}
	return out
}

func buildCommandExecutors(root, stage, module string, platforms []string) error {
	for _, platform := range platforms {
		osName, arch, err := splitPlatform(platform)
		if err != nil {
			return err
		}
		output := filepath.Join(stage, "command", "bin", osName+"-"+arch, "anas-module-command")
		if err := os.MkdirAll(filepath.Dir(output), 0755); err != nil {
			return err
		}
		cmd := exec.Command("go", "build", "-trimpath", "-ldflags=-s -w", "-o", output, "./modules/"+module+"/command")
		cmd.Dir = root
		cmd.Env = append(os.Environ(), "CGO_ENABLED=0", "GOOS="+osName, "GOARCH="+arch)
		if body, buildErr := cmd.CombinedOutput(); buildErr != nil {
			return fmt.Errorf("build %s command executor for %s: %w\n%s", module, platform, buildErr, body)
		}
	}
	return nil
}

func runtimeTrackedFiles(root, module string) ([]string, error) {
	body, err := exec.Command("git", "-C", root, "ls-files", "-z", "--", "modules/"+module).Output()
	if err != nil {
		return nil, err
	}
	files := []string{}
	for _, rel := range strings.Split(string(body), "\x00") {
		if rel == "" || excludedRuntimeFile(rel) {
			continue
		}
		files = append(files, filepath.Clean(rel))
	}
	sort.Strings(files)
	return files, nil
}

func excludedRuntimeFile(rel string) bool {
	slash := filepath.ToSlash(rel)
	base := filepath.Base(rel)
	if (strings.HasPrefix(base, "README") && strings.HasSuffix(base, ".md")) || base == "localization.yml" || base == ".DS_Store" {
		return true
	}
	// Module documentation is published independently from the runtime bundle.
	// Keep this aligned with scripts/ci/module-revisions.sh so documentation-only
	// changes neither alter package bytes nor manufacture a release revision.
	if moduleRelative := strings.TrimPrefix(slash, "modules/"); moduleRelative != slash {
		parts := strings.Split(moduleRelative, "/")
		if len(parts) >= 3 && parts[1] == "docs" {
			return true
		}
	} else if marker := "/modules/"; strings.Contains(slash, marker) {
		moduleRelative := strings.SplitN(slash, marker, 2)[1]
		parts := strings.Split(moduleRelative, "/")
		if len(parts) >= 3 && parts[1] == "docs" {
			return true
		}
	}
	contractRelative := strings.TrimPrefix(slash, "contracts/")
	if contractRelative == slash {
		if marker := "/contracts/"; strings.Contains(slash, marker) {
			contractRelative = strings.SplitN(slash, marker, 2)[1]
		}
	}
	if contractRelative != slash {
		parts := strings.Split(contractRelative, "/")
		if len(parts) >= 2 && (parts[1] == "docs" || parts[1] == "documentation.yml") {
			return true
		}
	}
	if strings.HasSuffix(base, "_test.go") || (strings.HasPrefix(base, "test_") && strings.HasSuffix(base, ".py")) {
		return true
	}
	if strings.Contains(slash, "/__pycache__/") {
		return true
	}
	// A historical local build accidentally became tracked. The Dockerfile builds
	// the reconciler from source and must never package this host binary.
	return slash == "modules/ddns_go/ddns-go/reconcile/reconcile"
}

func expandContextFiles(root string, contexts []string) ([]string, error) {
	files := []string{}
	for _, context := range contexts {
		path := filepath.Join(root, context)
		info, err := os.Stat(path)
		if err != nil {
			return nil, err
		}
		if !info.IsDir() {
			if !excludedRuntimeFile(context) {
				files = append(files, filepath.Clean(context))
			}
			continue
		}
		err = filepath.WalkDir(path, func(current string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			rel, err := filepath.Rel(root, current)
			if err != nil {
				return err
			}
			if entry.IsDir() {
				if excludedRuntimeDirectory(rel) {
					return fs.SkipDir
				}
				return nil
			}
			if excludedRuntimeFile(rel) {
				return nil
			}
			files = append(files, rel)
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	sort.Strings(files)
	return files, nil
}

func digestFiles(root string, paths []string) (string, error) {
	sort.Strings(paths)
	h := sha256.New()
	previous := ""
	for _, rel := range paths {
		if rel == previous {
			continue
		}
		previous = rel
		path := filepath.Join(root, rel)
		info, err := os.Stat(path)
		if err != nil {
			return "", err
		}
		fmt.Fprintf(h, "%s\x00%o\x00", filepath.ToSlash(rel), info.Mode().Perm())
		file, err := os.Open(path)
		if err != nil {
			return "", err
		}
		_, copyErr := io.Copy(h, file)
		closeErr := file.Close()
		if copyErr != nil {
			return "", copyErr
		}
		if closeErr != nil {
			return "", closeErr
		}
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func digestBuildContext(root string, paths []string, entry CatalogEntry) (string, error) {
	filesDigest, err := digestFiles(root, paths)
	if err != nil {
		return "", err
	}
	catalogBody, err := json.Marshal(entry)
	if err != nil {
		return "", err
	}
	h := sha256.New()
	fmt.Fprintf(h, "files\x00%s\x00catalog\x00", filesDigest)
	_, _ = h.Write(catalogBody)
	return hex.EncodeToString(h.Sum(nil)), nil
}

func copyPackageFile(source, target string) error {
	info, err := os.Stat(source)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
		return err
	}
	in, err := os.Open(source)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, info.Mode().Perm())
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}

func copyRuntimeTree(source, target, catalogRoot string) error {
	return filepath.WalkDir(source, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == source {
			return nil
		}
		rel, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		catalogPath := filepath.Join(catalogRoot, rel)
		if entry.IsDir() && excludedRuntimeDirectory(catalogPath) {
			return fs.SkipDir
		}
		if entry.IsDir() {
			return os.MkdirAll(filepath.Join(target, rel), 0755)
		}
		if excludedRuntimeFile(catalogPath) {
			return nil
		}
		return copyPackageFile(path, filepath.Join(target, rel))
	})
}

func excludedRuntimeDirectory(path string) bool {
	slash := filepath.ToSlash(path)
	if filepath.Base(path) == "__pycache__" {
		return true
	}
	for _, prefix := range []string{"modules/", "contracts/"} {
		if strings.HasPrefix(slash, prefix) {
			parts := strings.Split(strings.TrimPrefix(slash, prefix), "/")
			return len(parts) >= 2 && parts[1] == "docs"
		}
	}
	return false
}

func digestDirectory(root string) (string, error) {
	return digestDirectoryExcept(root, "")
}

func digestDirectoryExcept(root, excluded string) (string, error) {
	paths := []string{}
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if filepath.ToSlash(rel) == excluded {
			return nil
		}
		paths = append(paths, rel)
		return nil
	})
	if err != nil {
		return "", err
	}
	return digestFiles(root, paths)
}

// ReadMetadata loads the self-description of an unpacked Module bundle.
func ReadMetadata(root string) (PackageMetadata, error) {
	body, err := os.ReadFile(filepath.Join(root, "package.yml"))
	if err != nil {
		return PackageMetadata{}, err
	}
	var metadata PackageMetadata
	dec := yaml.NewDecoder(strings.NewReader(string(body)))
	dec.KnownFields(true)
	if err := dec.Decode(&metadata); err != nil {
		return PackageMetadata{}, err
	}
	if metadata.APIVersion != PackageAPIVersion || !moduleNamePattern.MatchString(metadata.Name) ||
		metadata.Version == "" || metadata.Revision < 1 || metadata.Release != fmt.Sprintf("%s-r%d", metadata.Version, metadata.Revision) ||
		!repositoryPattern.MatchString(metadata.Repository) {
		return PackageMetadata{}, fmt.Errorf("invalid Module package identity in package.yml")
	}
	if !strings.HasPrefix(metadata.ContentDigest, "sha256:") || len(metadata.ContentDigest) != len("sha256:")+64 {
		return PackageMetadata{}, fmt.Errorf("invalid Module content digest %q", metadata.ContentDigest)
	}
	return metadata, nil
}

// VerifyUnpacked recalculates the canonical payload identity. package.yml is
// excluded because it contains that digest itself.
func VerifyUnpacked(root string) (PackageMetadata, error) {
	metadata, err := ReadMetadata(root)
	if err != nil {
		return PackageMetadata{}, err
	}
	digest, err := digestDirectoryExcept(root, "package.yml")
	if err != nil {
		return PackageMetadata{}, err
	}
	actual := "sha256:" + digest
	if actual != metadata.ContentDigest {
		return PackageMetadata{}, fmt.Errorf("Module content digest mismatch: package=%s actual=%s", metadata.ContentDigest, actual)
	}
	return metadata, nil
}

// PayloadDigest exposes the packager's canonical digest for installer tests and
// other producers of compatible bundles.
func PayloadDigest(root string) (string, error) {
	digest, err := digestDirectoryExcept(root, "package.yml")
	if err != nil {
		return "", err
	}
	return "sha256:" + digest, nil
}

func composeImages(path string) ([]string, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var compose composeDocument
	if err := yaml.Unmarshal(body, &compose); err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	images := []string{}
	for _, service := range compose.Services {
		image := strings.TrimSpace(service.Image)
		if image != "" && !seen[image] {
			seen[image] = true
			images = append(images, image)
		}
	}
	sort.Strings(images)
	return images, nil
}

func gitOutput(root string, args ...string) string {
	body, err := exec.Command("git", append([]string{"-C", root}, args...)...).Output()
	if err != nil {
		return "unknown"
	}
	return strings.TrimSpace(string(body))
}

func writeTarGz(root, output string) error {
	file, err := os.Create(output)
	if err != nil {
		return err
	}
	gz := gzip.NewWriter(file)
	gz.Header.ModTime = time.Unix(0, 0).UTC()
	gz.Header.OS = 255
	tw := tar.NewWriter(gz)

	paths := []string{}
	err = filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path != root {
			paths = append(paths, path)
		}
		return nil
	})
	if err == nil {
		sort.Slice(paths, func(i, j int) bool {
			a, _ := filepath.Rel(root, paths[i])
			b, _ := filepath.Rel(root, paths[j])
			return filepath.ToSlash(a) < filepath.ToSlash(b)
		})
		for _, path := range paths {
			info, statErr := os.Lstat(path)
			if statErr != nil {
				err = statErr
				break
			}
			rel, _ := filepath.Rel(root, path)
			header, headerErr := tar.FileInfoHeader(info, "")
			if headerErr != nil {
				err = headerErr
				break
			}
			header.Name = filepath.ToSlash(rel)
			header.ModTime = time.Unix(0, 0).UTC()
			header.AccessTime = time.Time{}
			header.ChangeTime = time.Time{}
			header.Uid, header.Gid = 0, 0
			header.Uname, header.Gname = "", ""
			if info.IsDir() && !strings.HasSuffix(header.Name, "/") {
				header.Name += "/"
			}
			if writeErr := tw.WriteHeader(header); writeErr != nil {
				err = writeErr
				break
			}
			if info.Mode().IsRegular() {
				in, openErr := os.Open(path)
				if openErr != nil {
					err = openErr
					break
				}
				_, copyErr := io.Copy(tw, in)
				closeErr := in.Close()
				if copyErr != nil {
					err = copyErr
					break
				}
				if closeErr != nil {
					err = closeErr
					break
				}
			}
		}
	}
	if closeErr := tw.Close(); err == nil {
		err = closeErr
	}
	if closeErr := gz.Close(); err == nil {
		err = closeErr
	}
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		_ = os.Remove(output)
	}
	return err
}

func DefaultPlatform() string { return "all" }
