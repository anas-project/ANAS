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

const currentModuleABI = "anas.module-hook/v1"

type manifestABI struct {
	Supports []string `yaml:"supports"`
}

type moduleManifest struct {
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
	Contracts    manifestContracts    `yaml:"contracts"`
	Dependencies manifestDependencies `yaml:"dependencies"`
	Resources    manifestResources    `yaml:"resources"`
	Upgrade      manifestUpgrade      `yaml:"upgrade"`
	Config       manifestConfig       `yaml:"config"`
	Features     manifestFeatures     `yaml:"features"`
	Identity     manifestIdentity     `yaml:"identity"`
	Management   manifestManagement   `yaml:"management"`
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
	Contracts            []manifestContractDependency    `yaml:"contracts"`
	After                []string                        `yaml:"after"`
}

type manifestContracts struct {
	Provides []manifestContractProvider `yaml:"provides"`
}

type manifestContractProvider struct {
	Name           string `yaml:"name"`
	Version        string `yaml:"version"`
	Interface      string `yaml:"interface"`
	Implementation string `yaml:"implementation"`
}

type manifestContractDependency struct {
	Name       string   `yaml:"name"`
	Version    string   `yaml:"version"`
	SelectedBy string   `yaml:"selected_by"`
	Interfaces []string `yaml:"interfaces"`
	Default    string   `yaml:"default"`
}

type manifestResources struct {
	Requires []manifestResourceRequirement `yaml:"requires"`
}

type manifestResourceRequirement struct {
	ID       string            `yaml:"id"`
	Contract string            `yaml:"contract"`
	Binding  string            `yaml:"binding"`
	Spec     map[string]any    `yaml:"spec"`
	SpecFrom map[string]string `yaml:"spec_from"`
}

type manifestCapabilities struct {
	Provides []manifestProvidedCapability `yaml:"provides"`
}

type manifestProvidedCapability struct {
	Name       string   `yaml:"name"`
	Interfaces []string `yaml:"interfaces"`
}

// manifestRequiredCapability deliberately has no provider-selection field.
// Decoding runs with KnownFields(true), so a module that tries to reintroduce
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
	// DataBreaking lists the versions at which this module's on-disk data format
	// changed, so that data written at or above one of them cannot be read by
	// anything below it.
	//
	// It is a pointer because "not declared" and "declared to be empty" are
	// different claims and lead to opposite decisions. An absent field means the
	// module author has said nothing, and the conservative reading of silence is
	// that any version change might have moved the format; an explicit `[]` is a
	// checkable statement that no release ever did, which is what lets a rollback
	// through. Modelling the absent case as an empty slice would make the
	// crossing predicate vacuously false for every module that has not been
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
	// module's dependency closure that its rendering and hooks may read.
	Consumes []string `yaml:"consumes"`
	// Exports lists env keys (exact or trailing-* glob) outside the module's own
	// prefix that its calculate hook is allowed to publish.
	Exports []string `yaml:"exports"`
	// Types declares what a parameter accepts, so a wrong value is refused when
	// it is set rather than discovered at render time -- or not discovered at
	// all, which is what happens to a value nothing validates.
	Types map[string]manifestParamType `yaml:"types"`
}

// manifestParamType is written either as a bare kind (`log_level: int`) or as a
// mapping carrying an enumeration (`share_access_mode: {enum: [...]}`). The
// short form is what most parameters need and the long form is what an
// enumeration requires, so both are accepted rather than forcing every
// declaration into the verbose shape.
type manifestParamType struct {
	Kind string   `yaml:"kind"`
	Enum []string `yaml:"enum"`
}

func (t *manifestParamType) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind == yaml.ScalarNode {
		return node.Decode(&t.Kind)
	}
	type raw manifestParamType
	var out raw
	if err := node.Decode(&out); err != nil {
		return err
	}
	*t = manifestParamType(out)
	return nil
}

type manifestChangePolicy struct {
	Effect      string `yaml:"effect"`
	Apply       string `yaml:"apply"`
	Description string `yaml:"description"`
	Sensitive   bool   `yaml:"sensitive"`
	Executor    string `yaml:"executor"`
	Verify      string `yaml:"verify"`
}

// manifestFeatures is decoded with KnownFields(true), so every key a module may
// write has to appear here. Only HostLAN reaches the runner; the rest are
// declarations modules make about themselves that nothing yet consults.
type manifestFeatures struct {
	LDAPProvider     bool     `yaml:"ldap_provider"`
	GeneratedSecrets bool     `yaml:"generated_secrets"`
	Domain           bool     `yaml:"domain"`
	HostLAN          string   `yaml:"host_lan"`
	KerberosEnv      bool     `yaml:"kerberos_env"`
	AfterStart       string   `yaml:"after_start"`
	Special          []string `yaml:"special_files"`
}

// manifestIdentity declares direct identity protocols used by a module. IAM
// protocols are resolved from requires_capabilities; direct directory access
// such as LDAPS is declared here. AppGroup is deliberately explicit: a daemon
// may query a directory without representing an interactive application.
type manifestIdentity struct {
	Interfaces     []string                        `yaml:"interfaces"`
	AppGroup       bool                            `yaml:"application_group"`
	Provisioning   *manifestIdentityProvisioning   `yaml:"provisioning"`
	Authentication *manifestIdentityAuthentication `yaml:"authentication"`
}

type manifestIdentityProvisioning struct {
	Capability  string                       `yaml:"capability"`
	Interfaces  manifestCapabilityInterfaces `yaml:"interfaces"`
	Objects     []string                     `yaml:"objects"`
	IdentityKey string                       `yaml:"identity_key"`
	Required    bool                         `yaml:"required"`
}

type manifestIdentityAuthentication struct {
	Capability string                       `yaml:"capability"`
	SelectedBy string                       `yaml:"selected_by"`
	Interfaces manifestCapabilityInterfaces `yaml:"interfaces"`
}

type manifestManagement struct {
	Surfaces      []manifestManagementSurface `yaml:"surfaces"`
	LocalAccounts []manifestLocalAccount      `yaml:"local_accounts"`
}

type manifestManagementSurface struct {
	ID             string                        `yaml:"id"`
	URIFrom        string                        `yaml:"uri_from"`
	Authentication manifestSurfaceAuthentication `yaml:"authentication"`
}

type manifestSurfaceAuthentication struct {
	Primary string `yaml:"primary"`
}

type manifestLocalAccount struct {
	ID            string                  `yaml:"id"`
	Purpose       string                  `yaml:"purpose"`
	FixedUsername string                  `yaml:"fixed_username"`
	Credential    manifestLocalCredential `yaml:"credential"`
	Lifecycle     manifestLocalLifecycle  `yaml:"lifecycle"`
}

type manifestLocalCredential struct {
	Policy          string `yaml:"policy"`
	ContainerFormat string `yaml:"container_format"`
}

type manifestLocalLifecycle struct {
	Apply  string `yaml:"apply"`
	Rotate string `yaml:"rotate"`
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
	return loadRegistryDir(filepath.Join(root, "modules"))
}

// loadRegistryDir loads independent module bundles from a directory whose
// immediate children each contain module.yml. The runner only needs this while
// resolving or materializing a deployment; artifact lifecycle commands read
// the frozen deployment manifest and do not need a module source tree.
func loadRegistryDir(modulesRoot string) (map[string]Module, error) {
	entries, err := os.ReadDir(modulesRoot)
	if err != nil {
		return nil, err
	}
	reg := map[string]Module{}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		dir := filepath.Join(modulesRoot, entry.Name())
		mod, err := loadModuleManifest(dir, entry.Name())
		if err != nil {
			return nil, err
		}
		reg[mod.Name] = mod
	}
	return reg, nil
}

// moduleRootCandidate accepts either the bundle directory or the directory above
// it. Every caller used to do this for the value it had just parsed out of
// --root or --module-root, six copies of the same three lines, which is why the
// one input nobody owned — ANAS_MODULE_ROOT — was the one that missed out: it is
// read here rather than by a caller. The same value therefore worked as a flag
// and failed as an export, and the error named the two as interchangeable.
func moduleRootCandidate(path string) string {
	if path != "" && exists(filepath.Join(path, "modules")) {
		return filepath.Join(path, "modules")
	}
	return path
}

func locateModuleRoot(explicit string) (string, error) {
	candidates := []string{
		moduleRootCandidate(explicit),
		moduleRootCandidate(os.Getenv("ANAS_MODULE_ROOT")),
	}
	if cwd, err := os.Getwd(); err == nil {
		candidates = append(candidates, filepath.Join(cwd, "modules"))
	}
	if exe, err := os.Executable(); err == nil {
		dir := filepath.Dir(exe)
		candidates = append(candidates,
			filepath.Join(dir, "modules"),
			filepath.Join(filepath.Dir(dir), "modules"))
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
			if entry.IsDir() && exists(filepath.Join(candidate, entry.Name(), "module.yml")) {
				return candidate, nil
			}
		}
	}
	return "", fmt.Errorf("could not locate module bundle directory; use --module-root or ANAS_MODULE_ROOT")
}

func loadModuleManifest(dir, dirname string) (Module, error) {
	path := filepath.Join(dir, "module.yml")
	b, err := os.ReadFile(path)
	if err != nil {
		return Module{}, err
	}
	var manifest moduleManifest
	dec := yaml.NewDecoder(bytes.NewReader(b))
	dec.KnownFields(true)
	if err := dec.Decode(&manifest); err != nil {
		return Module{}, fmt.Errorf("%s: %w", path, err)
	}
	if manifest.APIVersion != "anas.module/v1" {
		return Module{}, fmt.Errorf("%s api_version = %q", dirname, manifest.APIVersion)
	}
	if manifest.Kind != "Module" {
		return Module{}, fmt.Errorf("%s kind = %q", dirname, manifest.Kind)
	}
	if manifest.Name != dirname {
		return Module{}, fmt.Errorf("%s manifest name = %q", dirname, manifest.Name)
	}
	if manifest.Version == "" {
		return Module{}, fmt.Errorf("module %q is missing version", dirname)
	}
	if _, err := parseSemver(manifest.Version); err != nil {
		return Module{}, fmt.Errorf("module %q version %q is invalid: %w", dirname, manifest.Version, err)
	}
	if manifest.Revision < 1 {
		return Module{}, fmt.Errorf("module %q revision must be at least 1", dirname)
	}
	manifest.Status = strings.ToLower(strings.TrimSpace(manifest.Status))
	if !contains([]string{"release", "developing", "deprecated"}, manifest.Status) {
		return Module{}, fmt.Errorf("module %q status %q is invalid; expected release, developing, or deprecated", dirname, manifest.Status)
	}
	if manifest.Upgrade.From != "" {
		if _, err := parseVersionConstraint(manifest.Upgrade.From); err != nil {
			return Module{}, fmt.Errorf("module %q upgrade.from %q is invalid: %w", dirname, manifest.Upgrade.From, err)
		}
	}
	// Every entry is validated at load rather than at comparison time. A
	// data_breaking entry that does not parse would otherwise be silently
	// unmatchable, which reads on disk as a declared guard and behaves as no
	// guard at all.
	if manifest.Upgrade.DataBreaking != nil {
		for _, raw := range *manifest.Upgrade.DataBreaking {
			if _, err := parseSemver(raw); err != nil {
				return Module{}, fmt.Errorf("module %q upgrade.data_breaking entry %q is invalid: %w", dirname, raw, err)
			}
		}
	}
	for _, dep := range manifest.Dependencies.Requires {
		if strings.TrimSpace(dep.Name) == "" {
			return Module{}, fmt.Errorf("module %q has dependency without name", dirname)
		}
		if dep.Version != "" {
			if _, err := parseVersionConstraint(dep.Version); err != nil {
				return Module{}, fmt.Errorf("module %q dependency %q version %q is invalid: %w", dirname, dep.Name, dep.Version, err)
			}
		}
	}
	for _, dep := range manifest.Dependencies.RequiresOne {
		if strings.TrimSpace(dep.Capability) == "" {
			return Module{}, fmt.Errorf("module %q has requires_one dependency without capability", dirname)
		}
		if strings.TrimSpace(dep.SelectedBy) == "" {
			return Module{}, fmt.Errorf("module %q requires_one %q has no selected_by parameter", dirname, dep.Capability)
		}
		if len(dep.Providers) == 0 {
			return Module{}, fmt.Errorf("module %q requires_one %q has no providers", dirname, dep.Capability)
		}
		if !contains(dep.Providers, dep.Default) {
			return Module{}, fmt.Errorf("module %q requires_one %q default %q is not a provider", dirname, dep.Capability, dep.Default)
		}
		for _, provider := range dep.Providers {
			if strings.TrimSpace(provider) == "" {
				return Module{}, fmt.Errorf("module %q requires_one %q contains an empty provider", dirname, dep.Capability)
			}
		}
	}
	if !contains(manifest.ABI.Supports, currentModuleABI) {
		return Module{}, fmt.Errorf("module %q does not support runner ABI %s", dirname, currentModuleABI)
	}
	if manifest.Runtime.Type != "builtin" && manifest.Runtime.Type != "compose" {
		return Module{}, fmt.Errorf("module %q has unsupported runtime type %q", dirname, manifest.Runtime.Type)
	}
	changes := map[string]ChangePolicy{}
	for key, policy := range manifest.Config.Changes {
		if !validChangeEffect(policy.Effect) {
			return Module{}, fmt.Errorf("module %q config.changes.%s has invalid effect %q", dirname, key, policy.Effect)
		}
		changes[strings.ToLower(strings.TrimSpace(key))] = ChangePolicy{
			Effect: policy.Effect, Apply: policy.Apply,
			Description: policy.Description, Sensitive: policy.Sensitive,
			Executor: policy.Executor, Verify: policy.Verify,
		}
	}
	composeFile := strings.TrimSpace(manifest.Runtime.ComposeFile)
	if manifest.Runtime.Type == "compose" {
		if composeFile == "" {
			composeFile = "docker-compose.yml"
		}
		if filepath.IsAbs(composeFile) || strings.HasPrefix(filepath.Clean(composeFile), ".."+string(filepath.Separator)) {
			return Module{}, fmt.Errorf("module %q has invalid compose_file %q", dirname, composeFile)
		}
		if _, err := os.Stat(filepath.Join(dir, composeFile)); err != nil {
			return Module{}, fmt.Errorf("module %q compose_file %q: %w", dirname, composeFile, err)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "runner.rb")); err == nil {
		return Module{}, fmt.Errorf("module %q still contains unsupported runner.rb", dirname)
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
	types, err := normalizeParamTypes(dirname, manifest.Config.Types)
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
	requiresContracts, err := normalizeContractDependencies(dirname, manifest.Dependencies.Contracts)
	if err != nil {
		return Module{}, err
	}
	contractProviders, err := loadContractProviders(dir, dirname, manifest.Contracts.Provides)
	if err != nil {
		return Module{}, err
	}
	resources, err := normalizeResourceRequirements(dirname, manifest.Resources.Requires, requiresContracts)
	if err != nil {
		return Module{}, err
	}
	managementSurfaces, localAccounts, err := normalizeManagement(dirname, manifest.Management)
	if err != nil {
		return Module{}, err
	}
	for _, account := range localAccounts {
		if (account.Apply != "" || account.Rotate != "") && len(manifest.Logic.Hook.Command) == 0 {
			return Module{}, fmt.Errorf("module %q local account %q declares lifecycle handlers without a module hook", dirname, account.ID)
		}
	}
	provisioning, authentication, identityInterfaces, err := normalizeIdentity(dirname, manifest.Identity)
	if err != nil {
		return Module{}, err
	}
	mod := Module{
		Name:                   manifest.Name,
		Version:                manifest.Version,
		Revision:               manifest.Revision,
		AppVersion:             strings.TrimSpace(manifest.AppVersion),
		Lifecycle:              manifest.Status,
		UpgradeFrom:            manifest.Upgrade.From,
		DataBreaking:           cloneStringListPointer(manifest.Upgrade.DataBreaking),
		SourceDir:              dir,
		EnvPrefix:              envPrefix,
		Defaults:               normalizeDefaults(manifest.Name, envPrefix, exports, manifest.Config.Defaults),
		Required:               normalizeRequired(manifest.Name, envPrefix, exports, manifest.Config.Required),
		Parameters:             declaredParameters(manifest.Config),
		Types:                  types,
		Consumes:               consumes,
		Exports:                exports,
		Changes:                changes,
		Requires:               normalizeManifestDependencies(manifest.Dependencies.Requires),
		RequiresOne:            normalizeAlternativeDependencies(manifest.Dependencies.RequiresOne),
		RequiresContracts:      requiresContracts,
		ContractProviders:      contractProviders,
		Resources:              resources,
		Provides:               provides,
		RequiresCapabilities:   requiresCapabilities,
		RunAfter:               append([]string{}, manifest.Dependencies.After...),
		IdentityInterfaces:     identityInterfaces,
		IdentityAppGroup:       manifest.Identity.AppGroup,
		IdentityProvisioning:   provisioning,
		IdentityAuthentication: authentication,
		ManagementSurfaces:     managementSurfaces,
		LocalAccounts:          localAccounts,
		UseHostLAN:             manifest.Features.HostLAN,
		Hook:                   manifest.Logic.Hook,
		RuntimeType:            manifest.Runtime.Type,
		ComposeFile:            composeFile,
	}
	return mod, nil
}

// normalizeParamTypes validates the declarations themselves. A type nobody can
// satisfy -- an unknown kind, or an enumeration with no members -- would reject
// every value including the module's own default, so it is refused when the module
// is loaded rather than when somebody tries to set the parameter.
func normalizeParamTypes(module string, in map[string]manifestParamType) (map[string]ParamType, error) {
	out := map[string]ParamType{}
	for name, declared := range in {
		kind := strings.ToLower(strings.TrimSpace(declared.Kind))
		enum := []string{}
		for _, value := range declared.Enum {
			if value = strings.TrimSpace(value); value != "" {
				enum = append(enum, value)
			}
		}
		if len(enum) > 0 && kind == "" {
			kind = "enum"
		}
		switch kind {
		case "string", "bool", "int":
		case "enum":
			if len(enum) == 0 {
				return nil, fmt.Errorf("module %q config.types.%s is an enum with no values", module, name)
			}
		default:
			return nil, fmt.Errorf("module %q config.types.%s has unknown type %q; use string, bool, int or enum", module, name, declared.Kind)
		}
		out[strings.ToLower(strings.TrimSpace(name))] = ParamType{Kind: kind, Enum: enum}
	}
	return out, nil
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

func normalizeIdentity(module string, in manifestIdentity) (*IdentityProvisioning, *IdentityAuthentication, []string, error) {
	interfaces := normalizeIdentityInterfaces(in.Interfaces)
	var provisioning *IdentityProvisioning
	if in.Provisioning != nil {
		capability := strings.ToLower(strings.TrimSpace(in.Provisioning.Capability))
		if capability == "" {
			return nil, nil, nil, fmt.Errorf("module %q identity.provisioning has no capability", module)
		}
		anyOf := normalizeIdentityInterfaces(in.Provisioning.Interfaces.AnyOf)
		if len(anyOf) == 0 {
			return nil, nil, nil, fmt.Errorf("module %q identity.provisioning has no interface", module)
		}
		objects := normalizeIdentityInterfaces(in.Provisioning.Objects)
		for _, object := range objects {
			if object != "users" && object != "groups" {
				return nil, nil, nil, fmt.Errorf("module %q identity.provisioning has unsupported object %q", module, object)
			}
		}
		for _, iface := range anyOf {
			if !contains(interfaces, iface) {
				interfaces = append(interfaces, iface)
			}
		}
		provisioning = &IdentityProvisioning{
			Capability: capability, AnyOf: anyOf, Objects: objects,
			IdentityKey: strings.TrimSpace(in.Provisioning.IdentityKey), Required: in.Provisioning.Required,
		}
	}
	var authentication *IdentityAuthentication
	if in.Authentication != nil {
		capability := strings.ToLower(strings.TrimSpace(in.Authentication.Capability))
		selectedBy := strings.ToLower(strings.TrimSpace(in.Authentication.SelectedBy))
		anyOf := normalizeIdentityInterfaces(in.Authentication.Interfaces.AnyOf)
		prefer := normalizeIdentityInterfaces(in.Authentication.Interfaces.Prefer)
		if capability == "" || selectedBy == "" || len(anyOf) == 0 {
			return nil, nil, nil, fmt.Errorf("module %q identity.authentication requires capability, selected_by and interfaces.any_of", module)
		}
		for _, preferred := range prefer {
			if !contains(anyOf, preferred) {
				return nil, nil, nil, fmt.Errorf("module %q identity.authentication prefers unsupported interface %q", module, preferred)
			}
		}
		authentication = &IdentityAuthentication{Capability: capability, SelectedBy: selectedBy, AnyOf: anyOf, Prefer: prefer}
	}
	return provisioning, authentication, interfaces, nil
}

func normalizeManagement(module string, in manifestManagement) ([]ManagementSurface, []LocalAccount, error) {
	surfaces := make([]ManagementSurface, 0, len(in.Surfaces))
	seenSurface := map[string]bool{}
	for _, raw := range in.Surfaces {
		id := strings.ToLower(strings.TrimSpace(raw.ID))
		if id == "" || seenSurface[id] {
			return nil, nil, fmt.Errorf("module %q management.surfaces has an empty or duplicate id %q", module, raw.ID)
		}
		seenSurface[id] = true
		auth := strings.ToLower(strings.TrimSpace(raw.Authentication.Primary))
		switch auth {
		case "local", "iam", "forward_auth":
		default:
			return nil, nil, fmt.Errorf("module %q management surface %q has unsupported authentication %q", module, id, auth)
		}
		uriFrom := strings.TrimSpace(raw.URIFrom)
		if uriFrom == "" || !isEnvKey(uriFrom) {
			return nil, nil, fmt.Errorf("module %q management surface %q has invalid uri_from %q", module, id, uriFrom)
		}
		surfaces = append(surfaces, ManagementSurface{ID: id, URIFrom: uriFrom, Authentication: auth})
	}

	accounts := make([]LocalAccount, 0, len(in.LocalAccounts))
	seenAccount := map[string]bool{}
	for _, raw := range in.LocalAccounts {
		id := strings.ToLower(strings.TrimSpace(raw.ID))
		if id == "" || seenAccount[id] {
			return nil, nil, fmt.Errorf("module %q management.local_accounts has an empty or duplicate id %q", module, raw.ID)
		}
		seenAccount[id] = true
		purpose := strings.ToLower(strings.TrimSpace(raw.Purpose))
		switch purpose {
		case "primary", "break_glass", "embedded_guard":
		default:
			return nil, nil, fmt.Errorf("module %q local account %q has unsupported purpose %q", module, id, purpose)
		}
		fixed := strings.TrimSpace(raw.FixedUsername)
		policy := strings.ToLower(strings.TrimSpace(raw.Credential.Policy))
		if policy == "" {
			policy = "generated_per_module"
		}
		if policy != "generated_per_module" {
			return nil, nil, fmt.Errorf("module %q local account %q has unsupported credential policy %q", module, id, policy)
		}
		format := strings.ToLower(strings.TrimSpace(raw.Credential.ContainerFormat))
		switch format {
		case "bcrypt", "plaintext_on_bootstrap":
		default:
			return nil, nil, fmt.Errorf("module %q local account %q has unsupported container_format %q", module, id, format)
		}
		accounts = append(accounts, LocalAccount{
			ID: id, Purpose: purpose, FixedUsername: fixed,
			PasswordPolicy: policy, ContainerFormat: format,
			Apply: strings.TrimSpace(raw.Lifecycle.Apply), Rotate: strings.TrimSpace(raw.Lifecycle.Rotate),
		})
	}
	return surfaces, accounts, nil
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
	case "hot_reload", "process_restart", "container_restart", "container_recreate", "image_rebuild", "reconcile", "credential_rotate", "data_migrate", "immutable":
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

func normalizeDefaults(module, prefix string, exports []string, in map[string]any) map[string]string {
	out := map[string]string{}
	for k, v := range in {
		out[moduleParamEnvKey(module, prefix, exports, k)] = config.Scalar(v)
	}
	return out
}

func normalizeRequired(module, prefix string, exports []string, in []string) []string {
	out := []string{}
	for _, k := range in {
		out = append(out, moduleParamEnvKey(module, prefix, exports, k))
	}
	return out
}

func defaultEnvPrefix(module string) string {
	return strings.ToUpper(strings.ReplaceAll(module, "-", "_"))
}

// normalizeEnvPatterns validates consumes/exports entries: an exact env key,
// a prefix glob such as APPS_LIST__*, or a suffix glob such as *_DB_NAME used
// by capability providers that scan their consumers' declarations.
func normalizeEnvPatterns(module, field string, in []string) ([]string, error) {
	out := []string{}
	for _, raw := range in {
		pattern := strings.TrimSpace(raw)
		if pattern == "" {
			return nil, fmt.Errorf("module %q config.%s contains an empty pattern", module, field)
		}
		if stars := strings.Count(pattern, "*"); stars > 1 ||
			(stars == 1 && !strings.HasSuffix(pattern, "*") && !strings.HasPrefix(pattern, "*")) {
			return nil, fmt.Errorf("module %q config.%s pattern %q may only have one leading or trailing *", module, field, pattern)
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

// moduleParamEnvKey maps a module's manifest parameter name to the env key it
// produces. A module's parameters normally acquire its prefix; one whose bare env
// name the module lists in `config.exports` keeps that bare name instead. That is
// how a parameter genuinely owned by one module can still be addressed by the
// name people use for it -- SHARE_ACCESS_MODE, not SAMBA_FS_SHARE_ACCESS_MODE
// -- without moving it back into the deployment-wide namespace, and without
// manifests having to spell parameters in two different cases.
func moduleParamEnvKey(module, prefix string, exports []string, key string) string {
	if bare := globalParamEnv(key); contains(exports, bare) {
		return bare
	}
	return paramEnvKey(module, prefix, key)
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
	if module == globalScope {
		return globalParamEnv(key)
	}
	p := strings.ToUpper(strings.ReplaceAll(prefix, "-", "_"))
	return p + "_" + strings.ToUpper(key)
}

// globalParamEnv is the runner's view of the one mapping, which lives in
// internal/config beside the struct it describes. It used to be a second copy
// of the same switch, and the copies had already drifted apart.
func globalParamEnv(key string) string {
	return config.EnvKey(key)
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
	return strings.Contains(key, "_") || strings.ToUpper(key) == key
}
