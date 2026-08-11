package runner

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/anas-project/ANAS/internal/config"
	"gopkg.in/yaml.v3"
)

const currentCaskABI = "anas.cask/v2"

type manifestABI struct {
	Supports []string `yaml:"supports"`
}

type caskManifest struct {
	APIVersion   string               `yaml:"api_version"`
	Kind         string               `yaml:"kind"`
	Name         string               `yaml:"name"`
	Version      string               `yaml:"version"`
	Revision     int                  `yaml:"revision"`
	AppVersion   string               `yaml:"app_version"`
	ABI          manifestABI          `yaml:"abi"`
	Title        string               `yaml:"title"`
	Description  string               `yaml:"description"`
	Category     string               `yaml:"category"`
	Runtime      manifestRuntime      `yaml:"runtime"`
	Capabilities manifestCapabilities `yaml:"capabilities"`
	Dependencies manifestDependencies `yaml:"dependencies"`
	Upgrade      manifestUpgrade      `yaml:"upgrade"`
	Config       manifestConfig       `yaml:"config"`
	Features     manifestFeatures     `yaml:"features"`
	Identity     manifestIdentity     `yaml:"identity"`
	Services     manifestServices     `yaml:"services"`
	Logic        manifestLogic        `yaml:"logic"`
	Status       string               `yaml:"status"`
}

type manifestRuntime struct {
	Type        string `yaml:"type"`
	Compose     string `yaml:"compose"`
	ComposeFile string `yaml:"compose_file"`
}

type manifestDependencies struct {
	Requires             []manifestDependency            `yaml:"requires"`
	RequiresOne          []manifestAlternativeDependency `yaml:"requires_one"`
	RequiresCapabilities []manifestRequiredCapability    `yaml:"requires_capabilities"`
	After                []string                        `yaml:"after"`
}

type manifestCapabilities struct {
	Provides []manifestProvidedCapability `yaml:"provides"`
}

type manifestProvidedCapability struct {
	Name       string   `yaml:"name"`
	Interfaces []string `yaml:"interfaces"`
}

// manifestRequiredCapability deliberately has no provider-selection field.
// Decoding runs with KnownFields(true), so a cask that tries to reintroduce
// one (selected_by, provider_selected_by, providers) fails to load.
type manifestRequiredCapability struct {
	Name                string                       `yaml:"name"`
	InterfaceSelectedBy string                       `yaml:"interface_selected_by"`
	Interfaces          manifestCapabilityInterfaces `yaml:"interfaces"`
}

type manifestCapabilityInterfaces struct {
	AnyOf  []string `yaml:"any_of"`
	Prefer []string `yaml:"prefer"`
}

type manifestAlternativeDependency struct {
	Capability string   `yaml:"capability"`
	SelectedBy string   `yaml:"selected_by"`
	Providers  []string `yaml:"providers"`
	Default    string   `yaml:"default"`
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
	// From constrains which installed versions may upgrade to this one.
	From string `yaml:"from"`
	// DataBreaking lists the versions at which this cask's on-disk data format
	// changed, so that data written at or above one of them cannot be read by
	// anything below it.
	//
	// It is a pointer because "not declared" and "declared to be empty" are
	// different claims and lead to opposite decisions. An absent field means the
	// cask author has said nothing, and the conservative reading of silence is
	// that any version change might have moved the format; an explicit `[]` is a
	// checkable statement that no release ever did, which is what lets a rollback
	// through. Modelling the absent case as an empty slice would make the
	// crossing predicate vacuously false for every cask that has not been
	// annotated and silently flip the default from "block" to "allow".
	//
	// From and DataBreaking answer different questions and neither implies the
	// other: From says whether an upgrade may happen at all, DataBreaking says
	// whether it can be undone afterwards.
	DataBreaking *[]string `yaml:"data_breaking"`
}

type manifestConfig struct {
	EnvPrefix string                          `yaml:"env_prefix"`
	Required  []string                        `yaml:"required"`
	Defaults  map[string]any                  `yaml:"defaults"`
	Changes   map[string]manifestChangePolicy `yaml:"changes"`
	// Consumes lists env keys (exact or trailing-* glob) produced outside this
	// cask's dependency closure that its rendering and hooks may read.
	Consumes []string `yaml:"consumes"`
	// Exports lists env keys (exact or trailing-* glob) outside the cask's own
	// prefix that its calculate hook is allowed to publish.
	Exports []string `yaml:"exports"`
}

type manifestChangePolicy struct {
	Effect      string `yaml:"effect"`
	Apply       string `yaml:"apply"`
	Description string `yaml:"description"`
	Sensitive   bool   `yaml:"sensitive"`
}

type manifestFeatures struct {
	LDAPProvider         bool     `yaml:"ldap_provider"`
	GeneratedSecrets     bool     `yaml:"generated_secrets"`
	Domain               bool     `yaml:"domain"`
	HostLAN              string   `yaml:"host_lan"`
	HostNetworkDiscovery bool     `yaml:"host_network_discovery"`
	KerberosEnv          bool     `yaml:"kerberos_env"`
	AfterStart           string   `yaml:"after_start"`
	Special              []string `yaml:"special_files"`
}

// manifestIdentity declares direct identity protocols used by a cask. IAM
// protocols are resolved from requires_capabilities; direct directory access
// such as LDAPS is declared here. AppGroup is deliberately explicit: a daemon
// may query a directory without representing an interactive application.
type manifestIdentity struct {
	Interfaces []string `yaml:"interfaces"`
	AppGroup   bool     `yaml:"application_group"`
}

type manifestServices struct {
	Optional []manifestOptionalService `yaml:"optional"`
}

type manifestOptionalService struct {
	Name      string `yaml:"name"`
	EnabledBy string `yaml:"enabled_by"`
}

type manifestLogic struct {
	Hook HookConfig `yaml:"hook"`
}

func loadRegistry(root string) (map[string]Module, error) {
	return loadRegistryDir(filepath.Join(root, "casks", "mods"))
}

// loadRegistryDir loads independent cask bundles from a directory whose
// immediate children each contain cask.yml. The runner only needs this while
// resolving or materializing a deployment; artifact lifecycle commands read
// the frozen deployment manifest and do not need a cask source tree.
func loadRegistryDir(casksRoot string) (map[string]Module, error) {
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

// caskRootCandidate accepts either the bundle directory or the directory above
// it. Every caller used to do this for the value it had just parsed out of
// --root or --cask-root, six copies of the same three lines, which is why the
// one input nobody owned — ANAS_CASK_ROOT — was the one that missed out: it is
// read here rather than by a caller. The same value therefore worked as a flag
// and failed as an export, and the error named the two as interchangeable.
func caskRootCandidate(path string) string {
	if path != "" && exists(filepath.Join(path, "casks", "mods")) {
		return filepath.Join(path, "casks", "mods")
	}
	return path
}

func locateCaskRoot(explicit string) (string, error) {
	candidates := []string{
		caskRootCandidate(explicit),
		caskRootCandidate(os.Getenv("ANAS_CASK_ROOT")),
	}
	if cwd, err := os.Getwd(); err == nil {
		candidates = append(candidates, filepath.Join(cwd, "casks", "mods"))
	}
	if exe, err := os.Executable(); err == nil {
		dir := filepath.Dir(exe)
		candidates = append(candidates,
			filepath.Join(dir, "casks", "mods"),
			filepath.Join(filepath.Dir(dir), "casks", "mods"))
	}
	seen := map[string]bool{}
	for _, candidate := range candidates {
		if strings.TrimSpace(candidate) == "" {
			continue
		}
		candidate, _ = filepath.Abs(candidate)
		candidate = filepath.Clean(candidate)
		if seen[candidate] {
			continue
		}
		seen[candidate] = true
		entries, err := os.ReadDir(candidate)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			if entry.IsDir() && exists(filepath.Join(candidate, entry.Name(), "cask.yml")) {
				return candidate, nil
			}
		}
	}
	return "", fmt.Errorf("could not locate cask bundle directory; use --cask-root or ANAS_CASK_ROOT")
}

func loadModuleManifest(dir, dirname string) (Module, error) {
	path := filepath.Join(dir, "cask.yml")
	b, err := os.ReadFile(path)
	if err != nil {
		return Module{}, err
	}
	var manifest caskManifest
	dec := yaml.NewDecoder(bytes.NewReader(b))
	dec.KnownFields(true)
	if err := dec.Decode(&manifest); err != nil {
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
	if manifest.Revision < 1 {
		return Module{}, fmt.Errorf("cask %q revision must be at least 1", dirname)
	}
	if manifest.Upgrade.From != "" {
		if _, err := parseVersionConstraint(manifest.Upgrade.From); err != nil {
			return Module{}, fmt.Errorf("cask %q upgrade.from %q is invalid: %w", dirname, manifest.Upgrade.From, err)
		}
	}
	// Every entry is validated at load rather than at comparison time. A
	// data_breaking entry that does not parse would otherwise be silently
	// unmatchable, which reads on disk as a declared guard and behaves as no
	// guard at all.
	if manifest.Upgrade.DataBreaking != nil {
		for _, raw := range *manifest.Upgrade.DataBreaking {
			if _, err := parseSemver(raw); err != nil {
				return Module{}, fmt.Errorf("cask %q upgrade.data_breaking entry %q is invalid: %w", dirname, raw, err)
			}
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
	for _, dep := range manifest.Dependencies.RequiresOne {
		if strings.TrimSpace(dep.Capability) == "" {
			return Module{}, fmt.Errorf("cask %q has requires_one dependency without capability", dirname)
		}
		if strings.TrimSpace(dep.SelectedBy) == "" {
			return Module{}, fmt.Errorf("cask %q requires_one %q has no selected_by parameter", dirname, dep.Capability)
		}
		if len(dep.Providers) == 0 {
			return Module{}, fmt.Errorf("cask %q requires_one %q has no providers", dirname, dep.Capability)
		}
		if !contains(dep.Providers, dep.Default) {
			return Module{}, fmt.Errorf("cask %q requires_one %q default %q is not a provider", dirname, dep.Capability, dep.Default)
		}
		for _, provider := range dep.Providers {
			if strings.TrimSpace(provider) == "" {
				return Module{}, fmt.Errorf("cask %q requires_one %q contains an empty provider", dirname, dep.Capability)
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
	consumes, err := normalizeEnvPatterns(dirname, "consumes", manifest.Config.Consumes)
	if err != nil {
		return Module{}, err
	}
	exports, err := normalizeEnvPatterns(dirname, "exports", manifest.Config.Exports)
	if err != nil {
		return Module{}, err
	}
	provides, err := normalizeProvidedCapabilities(dirname, manifest.Capabilities.Provides)
	if err != nil {
		return Module{}, err
	}
	requiresCapabilities, err := normalizeRequiredCapabilities(dirname, manifest.Dependencies.RequiresCapabilities)
	if err != nil {
		return Module{}, err
	}
	mod := Module{
		Name:                 manifest.Name,
		Version:              manifest.Version,
		Revision:             manifest.Revision,
		AppVersion:           strings.TrimSpace(manifest.AppVersion),
		UpgradeFrom:          manifest.Upgrade.From,
		DataBreaking:         cloneStringListPointer(manifest.Upgrade.DataBreaking),
		SourceDir:            dir,
		EnvPrefix:            envPrefix,
		Defaults:             normalizeDefaults(manifest.Name, envPrefix, manifest.Config.Defaults),
		Required:             normalizeRequired(manifest.Name, envPrefix, manifest.Config.Required),
		Consumes:             consumes,
		Exports:              exports,
		Changes:              changes,
		Requires:             normalizeManifestDependencies(manifest.Dependencies.Requires),
		RequiresOne:          normalizeAlternativeDependencies(manifest.Dependencies.RequiresOne),
		Provides:             provides,
		RequiresCapabilities: requiresCapabilities,
		RunAfter:             append([]string{}, manifest.Dependencies.After...),
		IdentityInterfaces:   normalizeIdentityInterfaces(manifest.Identity.Interfaces),
		IdentityAppGroup:     manifest.Identity.AppGroup,
		UseHostLAN:           manifest.Features.HostLAN,
		Hook:                 manifest.Logic.Hook,
		RuntimeType:          manifest.Runtime.Type,
		ComposeFile:          composeFile,
	}
	return mod, nil
}

func normalizeIdentityInterfaces(in []string) []string {
	out := []string{}
	for _, iface := range in {
		iface = strings.ToLower(strings.TrimSpace(iface))
		if iface != "" && !contains(out, iface) {
			out = append(out, iface)
		}
	}
	return out
}

func normalizeAlternativeDependencies(in []manifestAlternativeDependency) []AlternativeDependency {
	out := []AlternativeDependency{}
	for _, dep := range in {
		providers := make([]string, 0, len(dep.Providers))
		for _, provider := range dep.Providers {
			providers = append(providers, strings.TrimSpace(provider))
		}
		out = append(out, AlternativeDependency{
			Capability: strings.TrimSpace(dep.Capability),
			SelectedBy: strings.TrimSpace(dep.SelectedBy),
			Providers:  providers,
			Default:    strings.TrimSpace(dep.Default),
		})
	}
	return out
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

// normalizeEnvPatterns validates consumes/exports entries: an exact env key,
// a prefix glob such as APPS_LIST__*, or a suffix glob such as *_DB_NAME used
// by capability providers that scan their consumers' declarations.
func normalizeEnvPatterns(cask, field string, in []string) ([]string, error) {
	out := []string{}
	for _, raw := range in {
		pattern := strings.TrimSpace(raw)
		if pattern == "" {
			return nil, fmt.Errorf("cask %q config.%s contains an empty pattern", cask, field)
		}
		if stars := strings.Count(pattern, "*"); stars > 1 ||
			(stars == 1 && !strings.HasSuffix(pattern, "*") && !strings.HasPrefix(pattern, "*")) {
			return nil, fmt.Errorf("cask %q config.%s pattern %q may only have one leading or trailing *", cask, field, pattern)
		}
		out = append(out, pattern)
	}
	return out, nil
}

func matchEnvPattern(patterns []string, key string) bool {
	for _, pattern := range patterns {
		if prefix, ok := strings.CutSuffix(pattern, "*"); ok {
			if strings.HasPrefix(key, prefix) {
				return true
			}
			continue
		}
		if suffix, ok := strings.CutPrefix(pattern, "*"); ok {
			if strings.HasSuffix(key, suffix) {
				return true
			}
			continue
		}
		if key == pattern {
			return true
		}
	}
	return false
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
