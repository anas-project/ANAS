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

func formatModuleRelease(version string, revision int) string {
	if revision < 1 {
		return version
	}
	return fmt.Sprintf("%s-r%d", version, revision)
}

type moduleLock struct {
	APIVersion string                        `yaml:"api_version"`
	Modules    map[string]moduleLockRecord   `yaml:"modules"`
	Contracts  map[string]contractLockRecord `yaml:"contracts,omitempty"`
	// IAM records the deployment's single identity provider once. The protocol
	// each consumer resolved to is per-app and lives in Bindings under
	// "iam.interface".
	IAM      *moduleLockIAM               `yaml:"iam,omitempty"`
	Bindings map[string]map[string]string `yaml:"bindings,omitempty"`
	Snapshot *moduleLockSnapshot          `yaml:"snapshot,omitempty"`
}

type contractLockRecord struct {
	Version string `yaml:"version"`
	Digest  string `yaml:"digest"`
}

type moduleLockSnapshot struct {
	Backend  string `yaml:"backend"`
	KeepAuto int    `yaml:"keep_auto,omitempty"`
}

type moduleLockIAM struct {
	Provider string `yaml:"provider"`
}

type moduleLockRecord struct {
	Version  string `yaml:"version"`
	Revision int    `yaml:"revision"`
	// AppVersion records the upstream application/image version separately
	// when its original spelling differs from normalized Version.
	AppVersion string `yaml:"app_version,omitempty"`
	Lifecycle  string `yaml:"lifecycle"`
	Source     string `yaml:"source,omitempty"`
	Digest     string `yaml:"digest"`
	// Registry installations retain all three identities. Digest remains the
	// Runner's installed-tree guard used for local and remote bundles alike.
	OCIDigest     string `yaml:"oci_digest,omitempty"`
	ContentDigest string `yaml:"content_digest,omitempty"`
	Repository    string `yaml:"repository,omitempty"`
}

func loadModuleLock(base string) (*moduleLock, error) {
	return loadModuleLockFile(moduleLockPath(base))
}

func loadModuleLockFile(path string) (*moduleLock, error) {
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return &moduleLock{APIVersion: "anas.module-lock/v1", Modules: map[string]moduleLockRecord{}}, nil
	}
	if err != nil {
		return nil, err
	}
	var lock moduleLock
	dec := yaml.NewDecoder(bytes.NewReader(b))
	dec.KnownFields(true)
	if err := dec.Decode(&lock); err != nil {
		return nil, err
	}
	if lock.APIVersion != "anas.module-lock/v1" {
		return nil, fmt.Errorf("module lock api_version = %q", lock.APIVersion)
	}
	if lock.Modules == nil {
		lock.Modules = map[string]moduleLockRecord{}
	}
	if lock.Contracts == nil {
		lock.Contracts = map[string]contractLockRecord{}
	}
	if lock.Bindings == nil {
		lock.Bindings = map[string]map[string]string{}
	}
	return &lock, nil
}

func (l *moduleLock) Save(base string) error {
	return saveModuleLockFile(moduleLockPath(base), l)
}

func marshalModuleLock(l *moduleLock) ([]byte, error) {
	if l.APIVersion == "" {
		l.APIVersion = "anas.module-lock/v1"
	}
	if l.Modules == nil {
		l.Modules = map[string]moduleLockRecord{}
	}
	if l.Contracts == nil {
		l.Contracts = map[string]contractLockRecord{}
	}
	if l.Bindings == nil {
		l.Bindings = map[string]map[string]string{}
	}
	return yaml.Marshal(l)
}

func saveModuleLockFile(path string, l *moduleLock) error {
	b, err := marshalModuleLock(l)
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

func moduleLockPath(base string) string {
	return filepath.Join(base, "module.lock.yml")
}

func (a *app) validateVersions(lock *moduleLock) error {
	for _, name := range a.order {
		mod := a.reg[name]
		if a.cfg != nil {
			if selected, ok := a.cfg.Modules.Values[name]; ok && strings.TrimSpace(selected.Version) != "" {
				actual := formatModuleRelease(mod.Version, mod.Revision)
				if actual != strings.TrimSpace(selected.Version) {
					return fmt.Errorf("module %q is pinned to %s, got %s; run `anas module update %s`", name, strings.TrimSpace(selected.Version), actual, name)
				}
			}
		}
		current, ok := lock.Modules[name]
		if ok && current.Version != "" {
			if err := validateUpgrade(mod, current.Version, current.Revision); err != nil {
				return err
			}
		}
		for _, dep := range mod.Requires {
			depMod, exists := a.reg[dep.Name]
			if !exists {
				return fmt.Errorf("%s requires unknown module %q", name, dep.Name)
			}
			if dep.Optional && !contains(a.order, dep.Name) {
				continue
			}
			if !contains(a.order, dep.Name) {
				return fmt.Errorf("%s requires module %q", name, dep.Name)
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

func validateUpgrade(mod Module, currentVersion string, currentRevision int) error {
	current, err := parseSemver(currentVersion)
	if err != nil {
		return fmt.Errorf("installed module %q version %q is invalid: %w", mod.Name, currentVersion, err)
	}
	target, err := parseSemver(mod.Version)
	if err != nil {
		return err
	}
	cmp := current.Compare(target)
	if cmp == 0 {
		if currentRevision > mod.Revision {
			return fmt.Errorf("module %q downgrade from %s-r%d to %s-r%d is not supported", mod.Name, currentVersion, currentRevision, mod.Version, mod.Revision)
		}
		return nil
	}
	if cmp > 0 {
		return fmt.Errorf("module %q downgrade from %s to %s is not supported", mod.Name, currentVersion, mod.Version)
	}
	if mod.UpgradeFrom == "" {
		return nil
	}
	constraint, err := parseVersionConstraint(mod.UpgradeFrom)
	if err != nil {
		return err
	}
	if !constraint.Check(current) {
		return fmt.Errorf("module %q cannot upgrade from %s to %s; supported source versions: %s", mod.Name, currentVersion, mod.Version, mod.UpgradeFrom)
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

func (a *app) updateModuleLock(lock *moduleLock, persistBindings bool) error {
	if lock.APIVersion == "" {
		lock.APIVersion = "anas.module-lock/v1"
	}
	resolvedModules := make(map[string]moduleLockRecord, len(a.order))
	for _, name := range a.order {
		mod := a.reg[name]
		digest, err := moduleBundleDigest(mod.SourceDir)
		if err != nil {
			return fmt.Errorf("digest module %s: %w", name, err)
		}
		resolvedModules[name] = moduleLockRecord{
			Version:    mod.Version,
			Revision:   mod.Revision,
			AppVersion: mod.AppVersion,
			Lifecycle:  mod.Lifecycle,
			Source:     "bundle:" + name,
			Digest:     digest,
		}
	}
	// When the active registry is a workspace cache view, retain its immutable
	// Registry identities. A normal `anas lock` or `apply --update-lock` must not
	// silently turn a remote lock back into a local bundle lock merely because
	// the Runner is reading the unpacked cache tree.
	if a.workspace != "" {
		if view, err := loadWorkspaceModuleView(a.workspace); err == nil {
			for name, record := range resolvedModules {
				installation, ok := view.Installations[name]
				if !ok {
					continue
				}
				modulePath, moduleErr := filepath.EvalSymlinks(a.reg[name].SourceDir)
				installedPath, installErr := filepath.EvalSymlinks(installation.Path)
				if moduleErr != nil || installErr != nil || modulePath != installedPath {
					continue
				}
				record.Source = installation.ImmutableReference
				record.OCIDigest = installation.OCIDigest
				record.ContentDigest = installation.ContentDigest
				record.Repository = installation.Repository
				resolvedModules[name] = record
			}
		}
	}
	lock.Modules = resolvedModules
	usedContracts := map[string]bool{}
	for _, name := range a.order {
		for _, dependency := range a.reg[name].RequiresContracts {
			usedContracts[dependency.Name] = true
		}
		for _, provider := range a.reg[name].ContractProviders {
			usedContracts[provider.Name] = true
		}
	}
	lock.Contracts = map[string]contractLockRecord{}
	for name := range usedContracts {
		contract, ok := a.contracts[name]
		if !ok {
			continue
		}
		lock.Contracts[name] = contractLockRecord{Version: contract.Version, Digest: contract.Digest}
	}
	if !persistBindings {
		return nil
	}
	lock.IAM = nil
	lock.Bindings = map[string]map[string]string{}
	if a.iamProvider != "" {
		lock.IAM = &moduleLockIAM{Provider: a.iamProvider}
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

func moduleBundleDigest(root string) (string, error) {
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
