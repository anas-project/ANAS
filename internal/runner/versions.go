package runner

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
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
	APIVersion string                       `yaml:"api_version"`
	Casks      map[string]caskLockRecord    `yaml:"casks"`
	Bindings   map[string]map[string]string `yaml:"bindings,omitempty"`
}

type caskLockRecord struct {
	Version string `yaml:"version"`
	// AppVersion records the upstream application/image version separately
	// from the cask packaging version used for constraints and upgrades.
	AppVersion string `yaml:"app_version,omitempty"`
	Source     string `yaml:"source,omitempty"`
}

func loadCaskLock(base string) (*caskLock, error) {
	path := caskLockPath(base)
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
	path := caskLockPath(base)
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	return os.WriteFile(path, b, 0644)
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

func (a *app) updateCaskLock(lock *caskLock, persistBindings bool) {
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
		lock.Casks[name] = caskLockRecord{
			Version:    mod.Version,
			AppVersion: mod.AppVersion,
			Source:     filepath.ToSlash(filepath.Join("casks", "mods", name)),
		}
	}
	if !persistBindings {
		return
	}
	for module, bindings := range a.resolvedBindings {
		if lock.Bindings[module] == nil {
			lock.Bindings[module] = map[string]string{}
		}
		for capability, provider := range bindings {
			lock.Bindings[module][capability] = provider
		}
	}
}
