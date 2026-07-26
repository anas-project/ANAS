package runner

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	semver "github.com/Masterminds/semver/v3"
	"gopkg.in/yaml.v3"
)

func parseSemver(raw string) (*semver.Version, error) {
	version, err := semver.NewVersion(strings.TrimSpace(raw))
	if err != nil {
		return nil, err
	}
	return version, nil
}

func parseVersionConstraint(raw string) (*semver.Constraints, error) {
	constraint, err := semver.NewConstraint(strings.TrimSpace(raw))
	if err != nil {
		return nil, err
	}
	return constraint, nil
}

type caskLock struct {
	APIVersion string                    `yaml:"api_version"`
	Casks      map[string]caskLockRecord `yaml:"casks"`
	// IAM records the deployment's single identity provider once. The protocol
	// each consumer resolved to is per-app and lives in Bindings under
	// "iam.interface".
	IAM      *caskLockIAM                 `yaml:"iam,omitempty"`
	Bindings map[string]map[string]string `yaml:"bindings,omitempty"`
}

type caskLockIAM struct {
	Provider string `yaml:"provider"`
}

type caskLockRecord struct {
	Version string `yaml:"version"`
	// AppVersion records the upstream application/image version separately
	// from the cask packaging version used for constraints and upgrades.
	AppVersion string `yaml:"app_version,omitempty"`
	Source     string `yaml:"source,omitempty"`
	Digest     string `yaml:"digest"`
}

func loadCaskLock(base string) (*caskLock, error) {
	return loadCaskLockFile(caskLockPath(base))
}

func loadCaskLockFile(path string) (*caskLock, error) {
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return &caskLock{APIVersion: "anas.dev/v1", Casks: map[string]caskLockRecord{}}, nil
	}
	if err != nil {
		return nil, err
	}
	var lock caskLock
	dec := yaml.NewDecoder(bytes.NewReader(b))
	dec.KnownFields(true)
	if err := dec.Decode(&lock); err != nil {
		return nil, err
	}
	if lock.APIVersion != "anas.dev/v1" {
		return nil, fmt.Errorf("cask lock api_version = %q", lock.APIVersion)
	}
	if lock.Casks == nil {
		lock.Casks = map[string]caskLockRecord{}
	}
	if lock.Bindings == nil {
		lock.Bindings = map[string]map[string]string{}
	}
	return &lock, nil
}

func (l *caskLock) Save(base string) error {
	return saveCaskLockFile(caskLockPath(base), l)
}

func saveCaskLockFile(path string, l *caskLock) error {
	if l.APIVersion == "" {
		l.APIVersion = "anas.dev/v1"
	}
	if l.Casks == nil {
		l.Casks = map[string]caskLockRecord{}
	}
	if l.Bindings == nil {
		l.Bindings = map[string]map[string]string{}
	}
	b, err := yaml.Marshal(l)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func caskLockPath(base string) string {
	return filepath.Join(base, "cask.lock.yml")
}

func (a *app) validateVersions(lock *caskLock) error {
	for _, name := range a.order {
		mod := a.reg[name]
		current, ok := lock.Casks[name]
		if ok && current.Version != "" {
			if err := validateUpgrade(mod, current.Version); err != nil {
				return err
			}
		}
		for _, dep := range mod.Requires {
			depMod, exists := a.reg[dep.Name]
			if !exists {
				return fmt.Errorf("%s requires unknown cask %q", name, dep.Name)
			}
			if dep.Optional && !contains(a.order, dep.Name) {
				continue
			}
			if !contains(a.order, dep.Name) {
				return fmt.Errorf("%s requires cask %q", name, dep.Name)
			}
			if dep.Version == "" {
				continue
			}
			if err := validateDependencyVersion(name, dep, depMod.Version); err != nil {
				return err
			}
		}
	}
	return nil
}

func validateUpgrade(mod Module, currentVersion string) error {
	current, err := parseSemver(currentVersion)
	if err != nil {
		return fmt.Errorf("installed cask %q version %q is invalid: %w", mod.Name, currentVersion, err)
	}
	target, err := parseSemver(mod.Version)
	if err != nil {
		return err
	}
	cmp := current.Compare(target)
	if cmp == 0 {
		return nil
	}
	if cmp > 0 {
		return fmt.Errorf("cask %q downgrade from %s to %s is not supported", mod.Name, currentVersion, mod.Version)
	}
	if mod.UpgradeFrom == "" {
		return nil
	}
	constraint, err := parseVersionConstraint(mod.UpgradeFrom)
	if err != nil {
		return err
	}
	if !constraint.Check(current) {
		return fmt.Errorf("cask %q cannot upgrade from %s to %s; supported source versions: %s", mod.Name, currentVersion, mod.Version, mod.UpgradeFrom)
	}
	return nil
}

func validateDependencyVersion(owner string, dep Dependency, actual string) error {
	v, err := parseSemver(actual)
	if err != nil {
		return fmt.Errorf("%s dependency %q version %q is invalid: %w", owner, dep.Name, actual, err)
	}
	constraint, err := parseVersionConstraint(dep.Version)
	if err != nil {
		return err
	}
	if !constraint.Check(v) {
		return fmt.Errorf("%s requires %s %s, got %s", owner, dep.Name, dep.Version, actual)
	}
	return nil
}

func (a *app) updateCaskLock(lock *caskLock, persistBindings bool) error {
	if lock.APIVersion == "" {
		lock.APIVersion = "anas.dev/v1"
	}
	if lock.Casks == nil {
		lock.Casks = map[string]caskLockRecord{}
	}
	if lock.Bindings == nil {
		lock.Bindings = map[string]map[string]string{}
	}
	for _, name := range a.order {
		mod := a.reg[name]
		digest, err := caskBundleDigest(mod.SourceDir)
		if err != nil {
			return fmt.Errorf("digest cask %s: %w", name, err)
		}
		lock.Casks[name] = caskLockRecord{
			Version:    mod.Version,
			AppVersion: mod.AppVersion,
			Source:     "bundle:" + name,
			Digest:     digest,
		}
	}
	if !persistBindings {
		return nil
	}
	if a.iamProvider != "" {
		lock.IAM = &caskLockIAM{Provider: a.iamProvider}
	}
	for module, bindings := range a.resolvedBindings {
		if lock.Bindings[module] == nil {
			lock.Bindings[module] = map[string]string{}
		}
		for capability, provider := range bindings {
			lock.Bindings[module][capability] = provider
		}
	}
	return nil
}

func caskBundleDigest(root string) (string, error) {
	paths := []string{}
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if path == root {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if entry.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		paths = append(paths, filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		return "", err
	}
	sort.Strings(paths)
	h := sha256.New()
	for _, rel := range paths {
		if _, err := io.WriteString(h, rel+"\x00"); err != nil {
			return "", err
		}
		path := filepath.Join(root, filepath.FromSlash(rel))
		info, err := os.Lstat(path)
		if err != nil {
			return "", err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			target, err := os.Readlink(path)
			if err != nil {
				return "", err
			}
			if _, err := io.WriteString(h, "symlink\x00"+target+"\x00"); err != nil {
				return "", err
			}
			continue
		}
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
		if _, err := io.WriteString(h, "\x00"); err != nil {
			return "", err
		}
	}
	return fmt.Sprintf("sha256:%x", h.Sum(nil)), nil
}
