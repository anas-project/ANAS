package main

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// checkStagedBuildContexts checks a rendered deployment without invoking
// Compose, evaluating its environment, reading secrets, or touching Docker.
// sourceRoot must be the exact source revision used to render the deployment.
// An override merely pointing at a checkout is not evidence of matching bytes.
func checkStagedBuildContexts(sourceRoot, stagingRoot, sharedRoot string) error {
	if !filepath.IsAbs(sharedRoot) || filepath.Clean(sharedRoot) != sharedRoot {
		return fmt.Errorf("staging requires an absolute ANAS_SHARED_BUILD_CONTEXT pointing to matching shared sources")
	}
	var err error
	sourceRoot, err = filepath.Abs(sourceRoot)
	if err != nil {
		return err
	}
	stagingRoot, err = filepath.Abs(stagingRoot)
	if err != nil {
		return err
	}
	body, err := os.ReadFile(filepath.Join(sourceRoot, ".github", "images.json"))
	if err != nil {
		return err
	}
	var images []imageEntry
	if err := json.Unmarshal(body, &images); err != nil {
		return err
	}
	checked := map[string]bool{}
	matched := 0
	for _, image := range images {
		if len(image.Shared) == 0 {
			continue
		}
		moduleRoot := filepath.Join(stagingRoot, "modules", image.Module)
		if _, err := os.Stat(moduleRoot); os.IsNotExist(err) {
			// Deployments contain only selected modules.
			continue
		} else if err != nil {
			return err
		}
		if !filepath.IsLocal(image.Context) || !filepath.IsLocal(image.Dockerfile) {
			return fmt.Errorf("%s: image build paths must be local", image.Module)
		}
		body, err := os.ReadFile(filepath.Join(moduleRoot, "docker-compose.yml"))
		if err != nil {
			return err
		}
		var compose struct {
			Services map[string]struct {
				Build struct {
					Context    string            `yaml:"context"`
					Dockerfile string            `yaml:"dockerfile"`
					Additional map[string]string `yaml:"additional_contexts"`
				} `yaml:"build"`
			} `yaml:"services"`
		}
		if err := yaml.Unmarshal(body, &compose); err != nil {
			return err
		}
		found := false
		for name, service := range compose.Services {
			if service.Build.Context == "" {
				continue
			}
			contextPath := filepath.Clean(filepath.Join(moduleRoot, service.Build.Context))
			if contextPath != filepath.Join(stagingRoot, image.Context) {
				continue
			}
			if service.Build.Additional["shared"] != "${ANAS_SHARED_BUILD_CONTEXT:-../..}" {
				return fmt.Errorf("%s/%s: staged shared context override is missing or changed", image.Module, name)
			}
			dockerfile := service.Build.Dockerfile
			if dockerfile == "" {
				dockerfile = "Dockerfile"
			}
			if !filepath.IsLocal(dockerfile) || filepath.Clean(filepath.Join(contextPath, dockerfile)) != filepath.Join(stagingRoot, image.Dockerfile) {
				return fmt.Errorf("%s/%s: staged Dockerfile does not match the image inventory", image.Module, name)
			}
			if err := sameBuildInputTree(filepath.Join(sourceRoot, image.Context), contextPath); err != nil {
				return fmt.Errorf("%s/%s: staged image sources differ from the selected checkout: %w", image.Module, name, err)
			}
			found = true
		}
		if !found {
			return fmt.Errorf("%s: no matching staged Compose build for %s", image.Module, image.Context)
		}
		matched++
		for _, shared := range image.Shared {
			if checked[shared] {
				continue
			}
			if !filepath.IsLocal(shared) || filepath.Clean(shared) != shared {
				return fmt.Errorf("%s: invalid shared input path", image.Module)
			}
			if err := sameBuildInputTree(filepath.Join(sourceRoot, shared), filepath.Join(sharedRoot, shared)); err != nil {
				return fmt.Errorf("%s: shared input %s differs from the selected checkout: %w", image.Module, shared, err)
			}
			checked[shared] = true
		}
	}
	if matched == 0 {
		return fmt.Errorf("staging contains no selected shared-context images; nothing was validated")
	}
	return nil
}

// The developer check compares bounded regular source trees. Symlinks and
// special files fail rather than causing a read outside the declared context
// or blocking on a FIFO. This is not a hostile-filesystem runtime verifier.
func sameBuildInputTree(expected, actual string) error {
	left, err := buildInputDigests(expected)
	if err != nil {
		return err
	}
	right, err := buildInputDigests(actual)
	if err != nil {
		return err
	}
	if len(left) != len(right) {
		return fmt.Errorf("source file sets differ")
	}
	for path, digest := range left {
		if other, exists := right[path]; !exists || other != digest {
			return fmt.Errorf("source contents differ at %s", path)
		}
	}
	return nil
}

type buildInputSignature struct {
	Digest     [sha256.Size]byte
	Executable fs.FileMode
}

func buildInputDigests(root string) (map[string]buildInputSignature, error) {
	result := map[string]buildInputSignature{}
	var total int64
	entries := 0
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		entries++
		if entries > 10000 {
			return fmt.Errorf("build input tree exceeds 10000 entries")
		}
		if entry.IsDir() {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() || info.Size() > 32<<20 {
			return fmt.Errorf("build input is not a regular source file within 32 MiB")
		}
		total += info.Size()
		if total > 64<<20 {
			return fmt.Errorf("build input tree exceeds 64 MiB")
		}
		file, err := os.Open(path)
		if err != nil {
			return err
		}
		hash := sha256.New()
		n, copyErr := io.Copy(hash, io.LimitReader(file, (32<<20)+1))
		after, statErr := file.Stat()
		closeErr := file.Close()
		if copyErr != nil || statErr != nil || closeErr != nil || n != info.Size() || !os.SameFile(info, after) || after.Size() != info.Size() || after.Mode() != info.Mode() || !after.ModTime().Equal(info.ModTime()) {
			return fmt.Errorf("build input changed or could not be fully read")
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		var digest [sha256.Size]byte
		copy(digest[:], hash.Sum(nil))
		result[relative] = buildInputSignature{Digest: digest, Executable: info.Mode() & 0111}
		return nil
	})
	return result, err
}
