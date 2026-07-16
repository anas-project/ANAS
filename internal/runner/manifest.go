package runner

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/whlsxl/anas/internal/config"
	"gopkg.in/yaml.v3"
)

const currentCaskABI = "anas.cask/v1"

type manifestABI struct {
	Supports []string `yaml:"supports"`
}

type caskManifest struct {
	APIVersion   string                 `yaml:"api_version"`
	Kind         string                 `yaml:"kind"`
	Name         string                 `yaml:"name"`
	Version      string                 `yaml:"version"`
	ABI          manifestABI            `yaml:"abi"`
	Runtime      manifestRuntime        `yaml:"runtime"`
	Dependencies manifestDependencies   `yaml:"dependencies"`
	Upgrade      manifestUpgrade        `yaml:"upgrade"`
	Config       manifestConfig         `yaml:"config"`
	Features     manifestFeatures       `yaml:"features"`
	Logic        manifestLogic          `yaml:"logic"`
	Status       string                 `yaml:"status"`
	Extra        map[string]interface{} `yaml:",inline"`
}

type manifestRuntime struct {
	Type        string `yaml:"type"`
	Compose     string `yaml:"compose"`
	ComposeFile string `yaml:"compose_file"`
}

type manifestDependencies struct {
	Before   []string             `yaml:"before"`
	Requires []manifestDependency `yaml:"requires"`
	After    []string             `yaml:"after"`
}

type manifestDependency struct {
	Name     string `yaml:"name"`
	Version  string `yaml:"version"`
	Optional bool   `yaml:"optional"`
}

func (d *manifestDependency) UnmarshalYAML(value *yaml.Node) error {
	type dep manifestDependency
	if value.Kind == yaml.ScalarNode {
		d.Name = strings.TrimSpace(value.Value)
		return nil
	}
	var out dep
	if err := value.Decode(&out); err != nil {
		return err
	}
	*d = manifestDependency(out)
	return nil
}

type manifestUpgrade struct {
	From string `yaml:"from"`
}

type manifestConfig struct {
	EnvPrefix string                          `yaml:"env_prefix"`
	Required  []string                        `yaml:"required"`
	Defaults  map[string]any                  `yaml:"defaults"`
	Changes   map[string]manifestChangePolicy `yaml:"changes"`
}

type manifestChangePolicy struct {
	Effect      string `yaml:"effect"`
	Apply       string `yaml:"apply"`
	Description string `yaml:"description"`
	Sensitive   bool   `yaml:"sensitive"`
}

type manifestFeatures struct {
	LDAPClient bool     `yaml:"ldap_client"`
	HostLAN    string   `yaml:"host_lan"`
	AfterStart string   `yaml:"after_start"`
	Special    []string `yaml:"special_files"`
}

type manifestLogic struct {
	Hook HookConfig `yaml:"hook"`
}

func loadRegistry(root string) (map[string]Module, error) {
	casksRoot := filepath.Join(root, "casks", "mods")
	entries, err := os.ReadDir(casksRoot)
	if err != nil {
		return nil, err
	}
	reg := map[string]Module{}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		dir := filepath.Join(casksRoot, entry.Name())
		mod, err := loadModuleManifest(dir, entry.Name())
		if err != nil {
			return nil, err
		}
		reg[mod.Name] = mod
	}
	if _, ok := reg["core"]; !ok {
		return nil, fmt.Errorf("missing core cask")
	}
	return reg, nil
}

func loadModuleManifest(dir, dirname string) (Module, error) {
	path := filepath.Join(dir, "cask.yml")
	b, err := os.ReadFile(path)
	if err != nil {
		return Module{}, err
	}
	var manifest caskManifest
	if err := yaml.Unmarshal(b, &manifest); err != nil {
		return Module{}, fmt.Errorf("%s: %w", path, err)
	}
	if manifest.APIVersion != "anas.dev/v1" {
		return Module{}, fmt.Errorf("%s api_version = %q", dirname, manifest.APIVersion)
	}
	if manifest.Kind != "Cask" {
		return Module{}, fmt.Errorf("%s kind = %q", dirname, manifest.Kind)
	}
	if manifest.Name != dirname {
		return Module{}, fmt.Errorf("%s manifest name = %q", dirname, manifest.Name)
	}
	if manifest.Version == "" {
		return Module{}, fmt.Errorf("cask %q is missing version", dirname)
	}
	if _, err := parseSemver(manifest.Version); err != nil {
		return Module{}, fmt.Errorf("cask %q version %q is invalid: %w", dirname, manifest.Version, err)
	}
	if manifest.Upgrade.From != "" {
		if _, err := parseVersionConstraint(manifest.Upgrade.From); err != nil {
			return Module{}, fmt.Errorf("cask %q upgrade.from %q is invalid: %w", dirname, manifest.Upgrade.From, err)
		}
	}
	for _, dep := range manifest.Dependencies.Requires {
		if strings.TrimSpace(dep.Name) == "" {
			return Module{}, fmt.Errorf("cask %q has dependency without name", dirname)
		}
		if dep.Version != "" {
			if _, err := parseVersionConstraint(dep.Version); err != nil {
				return Module{}, fmt.Errorf("cask %q dependency %q version %q is invalid: %w", dirname, dep.Name, dep.Version, err)
			}
		}
	}
	if !contains(manifest.ABI.Supports, currentCaskABI) {
		return Module{}, fmt.Errorf("cask %q does not support runner ABI %s", dirname, currentCaskABI)
	}
	if manifest.Runtime.Type != "builtin" && manifest.Runtime.Type != "compose" {
		return Module{}, fmt.Errorf("cask %q has unsupported runtime type %q", dirname, manifest.Runtime.Type)
	}
	changes := map[string]ChangePolicy{}
	for key, policy := range manifest.Config.Changes {
		if !validChangeEffect(policy.Effect) {
			return Module{}, fmt.Errorf("cask %q config.changes.%s has invalid effect %q", dirname, key, policy.Effect)
		}
		changes[strings.ToLower(strings.TrimSpace(key))] = ChangePolicy{
			Effect: policy.Effect, Apply: policy.Apply,
			Description: policy.Description, Sensitive: policy.Sensitive,
		}
	}
	composeFile := strings.TrimSpace(manifest.Runtime.ComposeFile)
	if manifest.Runtime.Type == "compose" {
		if composeFile == "" {
			composeFile = "docker-compose.yml"
		}
		if filepath.IsAbs(composeFile) || strings.HasPrefix(filepath.Clean(composeFile), ".."+string(filepath.Separator)) {
			return Module{}, fmt.Errorf("cask %q has invalid compose_file %q", dirname, composeFile)
		}
		if _, err := os.Stat(filepath.Join(dir, composeFile)); err != nil {
			return Module{}, fmt.Errorf("cask %q compose_file %q: %w", dirname, composeFile, err)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "runner.rb")); err == nil {
		return Module{}, fmt.Errorf("cask %q still contains unsupported runner.rb", dirname)
	} else if !os.IsNotExist(err) {
		return Module{}, err
	}

	envPrefix := manifest.Config.EnvPrefix
	if envPrefix == "" {
		envPrefix = defaultEnvPrefix(manifest.Name)
	}
	mod := Module{
		Name:        manifest.Name,
		Version:     manifest.Version,
		UpgradeFrom: manifest.Upgrade.From,
		SourceDir:   dir,
		EnvPrefix:   envPrefix,
		Defaults:    normalizeDefaults(manifest.Name, envPrefix, manifest.Config.Defaults),
		Required:    normalizeRequired(manifest.Name, envPrefix, manifest.Config.Required),
		Changes:     changes,
		Deps:        append([]string{}, manifest.Dependencies.Before...),
		Requires:    normalizeManifestDependencies(manifest.Dependencies.Requires),
		RunAfter:    append([]string{}, manifest.Dependencies.After...),
		UseLDAP:     manifest.Features.LDAPClient,
		UseHostLAN:  manifest.Features.HostLAN,
		Hook:        manifest.Logic.Hook,
		RuntimeType: manifest.Runtime.Type,
		ComposeFile: composeFile,
	}
	return mod, nil
}

func validChangeEffect(effect string) bool {
	switch effect {
	case "hot_reload", "process_restart", "container_restart", "container_recreate", "reconcile", "credential_rotate", "data_migrate", "immutable":
		return true
	default:
		return false
	}
}

func normalizeManifestDependencies(in []manifestDependency) []Dependency {
	out := []Dependency{}
	for _, dep := range in {
		name := strings.TrimSpace(dep.Name)
		if name == "" {
			continue
		}
		out = append(out, Dependency{Name: name, Version: strings.TrimSpace(dep.Version), Optional: dep.Optional})
	}
	return out
}

func normalizeDefaults(module, prefix string, in map[string]any) map[string]string {
	out := map[string]string{}
	for k, v := range in {
		out[paramEnvKey(module, prefix, k)] = config.Scalar(v)
	}
	return out
}

func normalizeRequired(module, prefix string, in []string) []string {
	out := []string{}
	for _, k := range in {
		out = append(out, paramEnvKey(module, prefix, k))
	}
	return out
}

func defaultEnvPrefix(module string) string {
	return strings.ToUpper(strings.ReplaceAll(module, "-", "_"))
}

func paramEnvKey(module, prefix, key string) string {
	key = strings.TrimSpace(key)
	if key == "" {
		return key
	}
	if strings.HasPrefix(key, "global.") {
		return globalParamEnv(strings.TrimPrefix(key, "global."))
	}
	if isEnvKey(key) {
		return key
	}
	if module == "core" {
		return globalParamEnv(key)
	}
	p := strings.ToUpper(strings.ReplaceAll(prefix, "-", "_"))
	return p + "_" + strings.ToUpper(key)
}

func globalParamEnv(key string) string {
	switch strings.ToLower(key) {
	case "domain", "base_domain":
		return "BASE_DOMAIN"
	case "email":
		return "EMAIL"
	case "timezone", "tz":
		return "TZ"
	case "ipv4":
		return "IPv4"
	case "ipv6":
		return "IPv6"
	default:
		return strings.ToUpper(key)
	}
}

func isEnvKey(key string) bool {
	if strings.Contains(key, ".") {
		return false
	}
	for _, r := range key {
		if r >= 'a' && r <= 'z' {
			return false
		}
	}
	return strings.Contains(key, "_") || key == "IPv4" || key == "IPv6" || strings.ToUpper(key) == key
}
