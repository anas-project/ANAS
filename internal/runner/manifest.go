package runner

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"

	"github.com/anas-project/ANAS/internal/config"
	"github.com/anas-project/ANAS/internal/configschema"
	"gopkg.in/yaml.v3"
)

const currentModuleABI = "anas.module-hook/v1"

var (
	configParameterNamePattern = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)
	credentialIDPattern        = regexp.MustCompile(`^[a-z][a-z0-9_]*(\.[a-z][a-z0-9_]*)+$`)
	envPrefixPattern           = regexp.MustCompile(`^[A-Z][A-Z0-9_]*$`)
	envKeyPattern              = regexp.MustCompile(`^[A-Z_][A-Z0-9_]*$`)
)

var knownModuleHookPhases = map[string]bool{
	"validate":               true,
	"calculate":              true,
	"render_env":             true,
	"runtime_restore":        true,
	"services":               true,
	"after_start":            true,
	"local_account_apply":    true,
	"local_account_rotate":   true,
	"local_account_rollback": true,
	"credential_probe":       true,
	"credential_reconcile":   true,
	"credential_verify":      true,
}

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
	Credentials  manifestCredentials  `yaml:"credentials"`
	Upgrade      manifestUpgrade      `yaml:"upgrade"`
	Config       manifestConfig       `yaml:"config"`
	Features     manifestFeatures     `yaml:"features"`
	Identity     manifestIdentity     `yaml:"identity"`
	Management   manifestManagement   `yaml:"management"`
	Services     manifestServices     `yaml:"services"`
	Logic        manifestLogic        `yaml:"logic"`
	Status       string               `yaml:"status"`
}

type manifestCredentials struct {
	Provides []manifestCredentialProvider `yaml:"provides"`
	Consumes []manifestCredentialConsumer `yaml:"consumes"`
}

type manifestCredentialProvider struct {
	ID           string                        `yaml:"id"`
	SecretKey    string                        `yaml:"secret_key"`
	Type         string                        `yaml:"type"`
	RotationMode string                        `yaml:"rotation_mode"`
	Generation   deploymentCredentialGenerator `yaml:"generation"`
	Lifecycle    deploymentCredentialLifecycle `yaml:"lifecycle"`
	Controls     []string                      `yaml:"controls"`
}

type manifestCredentialConsumer struct {
	Credential string `yaml:"credential"`
	Projection string `yaml:"projection"`
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
	EnvPrefix     string   `yaml:"env_prefix"`
	InputRequired []string `yaml:"input_required"`
	// Required is the legacy pre-Hook invariant. Keep its decoding and runtime
	// stage stable for existing third-party manifests; input_required is the new
	// caller-input contract and must_resolve is the post-Hook invariant.
	Required    []string                        `yaml:"required"`
	MustResolve []string                        `yaml:"must_resolve"`
	Defaults    map[string]any                  `yaml:"defaults"`
	Changes     map[string]manifestChangePolicy `yaml:"changes"`
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
	Kind          string                     `yaml:"kind"`
	Enum          []string                   `yaml:"enum"`
	Constraints   configschema.Constraints   `yaml:"constraints"`
	DefaultSource configschema.DefaultSource `yaml:"default_source"`
}

func (t *manifestParamType) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind == yaml.ScalarNode {
		return node.Decode(&t.Kind)
	}
	if node.Kind != yaml.MappingNode {
		return fmt.Errorf("parameter type must be a kind scalar or mapping")
	}
	allowed := map[string]bool{
		"kind": true, "enum": true, "constraints": true, "default_source": true,
	}
	for i := 0; i+1 < len(node.Content); i += 2 {
		name := node.Content[i].Value
		if !allowed[name] {
			return fmt.Errorf("parameter type contains unknown field %q", name)
		}
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
		dir := filepath.Join(modulesRoot, entry.Name())
		info, statErr := os.Stat(dir)
		if statErr != nil || !info.IsDir() {
			continue
		}
		// Cache views use symlinks to immutable content-addressed trees. Resolve
		// them here so later WalkDir/copy operations freeze the real Module files
		// into a deployment rather than copying the link itself.
		if resolved, err := filepath.EvalSymlinks(dir); err == nil {
			dir = resolved
		}
		mod, err := loadModuleManifest(dir, entry.Name())
		if err != nil {
			return nil, err
		}
		reg[mod.Name] = mod
	}
	if err := validateRegistryParameterRuntimeKeys(reg); err != nil {
		return nil, err
	}
	if err := validateRegistryCredentials(reg); err != nil {
		return nil, err
	}
	return reg, nil
}

func validateRegistryCredentials(reg map[string]Module) error {
	ownerByID := map[string]string{}
	ownerByKey := map[string]string{}
	controlTargets := map[string]deploymentCredential{}
	names := make([]string, 0, len(reg))
	for name := range reg {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		mod := reg[name]
		for _, provider := range mod.CredentialProviders {
			if previous, duplicate := ownerByID[provider.ID]; duplicate {
				return fmt.Errorf("credential %s is provided by both %s and %s", provider.ID, previous, name)
			}
			if previous, duplicate := ownerByKey[provider.SecretKey]; duplicate {
				return fmt.Errorf("credential providers %s and %s both own Secret key %s", previous, provider.ID, provider.SecretKey)
			}
			ownerByID[provider.ID] = name
			ownerByKey[provider.SecretKey] = provider.ID
			controlTargets[provider.ID] = deploymentCredential{ID: provider.ID, Controls: append([]string{}, provider.Controls...)}
		}
	}
	for _, name := range names {
		mod := reg[name]
		projectionOwner := map[string]string{}
		for _, consumer := range mod.CredentialConsumers {
			if _, ok := ownerByID[consumer.Credential]; !ok {
				return fmt.Errorf("module %q consumes credential %s but no installed Module provides it", name, consumer.Credential)
			}
			if previous, duplicate := projectionOwner[consumer.Projection]; duplicate && previous != consumer.Credential {
				return fmt.Errorf("module %q projects credentials %s and %s onto %s", name, previous, consumer.Credential, consumer.Projection)
			}
			projectionOwner[consumer.Projection] = consumer.Credential
		}
		for _, provider := range mod.CredentialProviders {
			for _, controlled := range provider.Controls {
				if _, ok := ownerByID[controlled]; !ok {
					return fmt.Errorf("credential %s controls unknown credential %s", provider.ID, controlled)
				}
				if controlled == provider.ID {
					return fmt.Errorf("credential %s cannot control itself", provider.ID)
				}
			}
		}
	}
	if _, err := credentialControlOrder(controlTargets); err != nil {
		return fmt.Errorf("credential control graph: %w", err)
	}
	return nil
}

// validateRegistryParameterRuntimeKeys makes ownership unambiguous before any
// config path is resolved. policyOwnerForEnv intentionally searches the whole
// installed registry (not only today's enabled graph), so two Module bundles
// cannot safely advertise parameters that produce the same exact environment
// key even when only one is currently selected.
func validateRegistryParameterRuntimeKeys(reg map[string]Module) error {
	type parameterOwner struct{ module, parameter string }
	seen := map[string]parameterOwner{}
	register := func(key string, owner parameterOwner) error {
		if previous, duplicate := seen[key]; duplicate && previous != owner {
			return fmt.Errorf("config parameter ownership collision: %s.%s and %s.%s both resolve to runtime key %s",
				previous.module, previous.parameter, owner.module, owner.parameter, key)
		}
		seen[key] = owner
		return nil
	}
	for _, parameter := range globalConfig.Parameters {
		if err := register(parameterEnvKey(globalModuleName, parameter, reg), parameterOwner{globalModuleName, parameter}); err != nil {
			return err
		}
	}
	reservedRuntimeKeys := registryReservedRuntimeKeys(reg)
	reservedNamespaceKeys := registryReservedNamespaceKeys()
	for _, key := range reservedRuntimeKeys {
		// A global parameter may also be host-derived. Its declared global owner
		// is already the stronger reservation, so keep that owner rather than
		// manufacturing a collision between two core subsystems.
		if _, globallyOwned := seen[key]; globallyOwned {
			continue
		}
		seen[key] = parameterOwner{module: runnerScope, parameter: "reserved"}
	}
	names := make([]string, 0, len(reg))
	for name := range reg {
		names = append(names, name)
	}
	sort.Strings(names)
	type prefixOwner struct{ module, prefix string }
	prefixes := []prefixOwner{}
	for _, name := range names {
		mod := reg[name]
		for _, pattern := range mod.Exports {
			for _, key := range registryProtectedExportKeys(reg) {
				if matchEnvPattern([]string{pattern}, key) {
					return fmt.Errorf("module %q config.exports pattern %q overlaps runner-owned runtime key %s", name, pattern, key)
				}
			}
		}
		for _, prefix := range uniqueStrings([]string{defaultEnvPrefix(mod.EnvPrefix), defaultEnvPrefix(name)}) {
			if prefix == "ANAS" || strings.HasPrefix(prefix, "ANAS_") {
				return fmt.Errorf("module %q environment prefix %s overlaps the runner-reserved ANAS namespace", name, prefix)
			}
			for _, parameter := range globalConfig.Parameters {
				key := parameterEnvKey(globalModuleName, parameter, reg)
				if strings.HasPrefix(key, prefix+"_") {
					return fmt.Errorf("module %q environment prefix %s overlaps global parameter runtime key %s", name, prefix, key)
				}
			}
			for _, key := range reservedNamespaceKeys {
				if strings.HasPrefix(key, prefix+"_") {
					return fmt.Errorf("module %q environment prefix %s overlaps runner-owned runtime key %s", name, prefix, key)
				}
			}
			// Config-core defaults include historical module-shaped keys such as
			// NEXTCLOUD_APPSTORE_URL and LLNG_DOCKER_HUB_REGISTRY. A Module's
			// default name prefix remains compatible with those read-only global
			// overrides; an explicit custom prefix may not claim their namespace.
			// configBaseEnvWithRegistry reserves every exact key even while unset,
			// so the compatible default prefix still cannot overwrite one by Hook.
			if prefix != defaultEnvPrefix(name) {
				for _, key := range configCoreReservedRuntimeKeys() {
					if strings.HasPrefix(key, prefix+"_") {
						return fmt.Errorf("module %q environment prefix %s overlaps config-owned runtime key %s", name, prefix, key)
					}
				}
			}
			for _, previous := range prefixes {
				if previous.module == name {
					continue
				}
				if strings.HasPrefix(prefix+"_", previous.prefix+"_") || strings.HasPrefix(previous.prefix+"_", prefix+"_") {
					return fmt.Errorf("module environment prefix collision: %s (%s) overlaps %s (%s)",
						previous.module, previous.prefix, name, prefix)
				}
			}
			prefixes = append(prefixes, prefixOwner{name, prefix})
		}
		parameters := append([]string{}, mod.Parameters...)
		sort.Strings(parameters)
		for _, parameter := range parameters {
			key := moduleParamEnvKey(name, mod.EnvPrefix, mod.Exports, parameter)
			if err := register(key, parameterOwner{name, parameter}); err != nil {
				return err
			}
		}
	}
	return nil
}

// registryReservedRuntimeKeys is the exact-key half of runtime ownership
// admission. Prefix checks stop an ordinary Module namespace from overlapping
// core state, but a bare export bypasses that namespace entirely. Registering
// every core-published exact key before Module parameters are admitted makes
// exports such as DATA_PATH or ANAS_IAM_PROVIDER collide with their real owner
// instead of silently replacing it at runtime.
func registryReservedRuntimeKeys(reg map[string]Module) []string {
	set := map[string]bool{}
	add := func(keys ...string) {
		for _, key := range keys {
			key = strings.TrimSpace(key)
			if key != "" {
				set[key] = true
			}
		}
	}

	add(registryReservedNamespaceKeys()...)
	// APPS_LIST is the root of a transitional cooperative Module protocol.
	// Modules may append their own name through an explicit APPS_LIST* export,
	// but callers may not seed the aggregate through raw env/secrets input.
	add("APPS_LIST")
	add(configCoreReservedRuntimeKeys()...)

	identityInterfaces := map[string]bool{}
	for _, definition := range capabilityDefinitions {
		for _, iface := range definition.Interfaces {
			identityInterfaces[iface] = true
		}
	}
	for name, mod := range reg {
		add(iamBindingKey(name, "INTERFACE"))
		add(envIdentityClientPfx + defaultEnvPrefix(name) + "__INTERFACES")
		for _, iface := range mod.IdentityInterfaces {
			identityInterfaces[iface] = true
		}
		for _, account := range mod.LocalAccounts {
			usernameKey, passwordKey := localAdminEnvKeys(mod, account.ID)
			add(usernameKey, passwordKey, localAdminPasswordFileEnvKey(mod, account.ID), localAdminSecretKey(name, account.ID))
		}
	}
	for iface := range identityInterfaces {
		add(fmt.Sprintf(envIdentityClientsTmpl, defaultEnvPrefix(iface)))
	}

	out := make([]string, 0, len(set))
	for key := range set {
		out = append(out, key)
	}
	sort.Strings(out)
	return out
}

// registryReservedNamespaceKeys is deliberately narrower than the exact-key
// registry above. These values are owned independently of any Module and thus
// also reserve a namespace prefix. Per-Module IAM/local-admin keys must remain
// exact-only: an authentik-owned AUTHENTIK_LOCAL_ADMIN__... value, for example,
// must not make the AUTHENTIK prefix itself illegal.
func registryReservedNamespaceKeys() []string {
	keys := []string{
		"ALL_MODS_NAME", "USE_HOST_LAN_REQUIRED_MODS_NAME", "USE_HOST_LAN_OPTIONAL_MODS_NAME", "DOMAINS",
		"DATA_PATH", "USER_DATA_PATH", "DOCKER_SOCKET_PATH", "ANAS_RUNTIME_ENTRY_IP",
		"MODULE_NAME", "ANAS_MODULE_RUNTIME_STATE_PATH", "ANAS_DEPLOYMENT_ID",
		"ANAS_RESOURCE_DATABASE", "ANAS_RESOURCE_USERNAME", "ANAS_RESOURCE_PASSWORD",
		localAdminCandidateSecretKey, credentialDesiredSecretKey,
		envIAMProvider, envIAMInterfaces, envIdentityClients, envIdentityAppClients,
	}
	keys = append(keys, hostEnvKeys...)
	return uniqueStrings(keys)
}

func registryProtectedExportKeys(reg map[string]Module) []string {
	keys := append([]string{}, registryReservedNamespaceKeys()...)
	keys = append(keys, configCoreReservedRuntimeKeys()...)
	for _, parameter := range globalConfig.Parameters {
		keys = append(keys, parameterEnvKey(globalModuleName, parameter, reg))
	}
	sort.Strings(keys)
	return uniqueStrings(keys)
}

// configCoreReservedRuntimeKeys asks config's own flattening table for every
// globally owned key it can derive. Seeding both speedup switches and the
// bootstrap username exercises the conditional defaults without duplicating
// their key lists here; module-owned compatibility aliases are intentionally
// excluded through the owner marker.
func configCoreReservedRuntimeKeys() []string {
	cfg := &config.File{
		Administration: config.Administration{Bootstrap: config.BootstrapAdministrator{Username: "reserved"}},
		Env: map[string]any{
			"CHINESE_SPEEDUP":       true,
			"CHINESE_BUILD_SPEEDUP": true,
		},
	}
	_, owners := cfg.BaseEnvWithOwners()
	keys := make([]string, 0, len(owners))
	for key, owner := range owners {
		if owner == "" {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	return keys
}

// configDefaultOverrideRuntimeKeys are global defaults for which raw env is an
// intentional override surface. They remain reserved from Module ownership,
// but are not rejected as caller configuration the way topology/workspace keys
// are.
func configDefaultOverrideRuntimeKeys() []string {
	cfg := &config.File{Env: map[string]any{
		"CHINESE_SPEEDUP":       true,
		"CHINESE_BUILD_SPEEDUP": true,
	}}
	env, owners := cfg.BaseEnvWithOwners()
	keys := []string{}
	for key, owner := range owners {
		if owner == "" && key != "CHINESE_SPEEDUP" && key != "CHINESE_BUILD_SPEEDUP" {
			if strings.TrimSpace(env[key]) != "" {
				keys = append(keys, key)
			}
		}
	}
	sort.Strings(keys)
	return keys
}

func isRunnerOwnedRuntimeKey(key string, reg map[string]Module) bool {
	key = config.EnvKey(key)
	owned := map[string]bool{}
	for _, reserved := range registryReservedRuntimeKeys(reg) {
		owned[reserved] = true
	}
	for _, parameter := range globalConfig.Parameters {
		delete(owned, parameterEnvKey(globalModuleName, parameter, reg))
	}
	for _, override := range configDefaultOverrideRuntimeKeys() {
		delete(owned, override)
	}
	// Workspace discovery fills these only when the caller did not provide an
	// override. They stay reserved from Module ownership, but are legitimate
	// top-level env/secrets inputs and therefore are not caller-forbidden.
	delete(owned, "DOCKER_SOCKET_PATH")
	delete(owned, "ANAS_RUNTIME_ENTRY_IP")
	return owned[key]
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
			entryPath := filepath.Join(candidate, entry.Name())
			info, statErr := os.Stat(entryPath)
			if statErr == nil && info.IsDir() && exists(filepath.Join(entryPath, "module.yml")) {
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
	hook, err := normalizeHookConfig(dirname, manifest.Logic.Hook)
	if err != nil {
		return Module{}, err
	}
	if manifest.Runtime.Type != "builtin" && manifest.Runtime.Type != "compose" {
		return Module{}, fmt.Errorf("module %q has unsupported runtime type %q", dirname, manifest.Runtime.Type)
	}
	changes, err := normalizeChangePolicies(dirname, manifest.Config.Changes)
	if err != nil {
		return Module{}, err
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

	envPrefix := strings.TrimSpace(manifest.Config.EnvPrefix)
	if envPrefix == "" {
		envPrefix = defaultEnvPrefix(manifest.Name)
	} else {
		envPrefix = defaultEnvPrefix(envPrefix)
	}
	if !envPrefixPattern.MatchString(envPrefix) {
		return Module{}, fmt.Errorf("module %q config.env_prefix %q is not an environment-safe prefix", dirname, manifest.Config.EnvPrefix)
	}
	consumes, err := normalizeEnvPatterns(dirname, "consumes", manifest.Config.Consumes)
	if err != nil {
		return Module{}, err
	}
	exports, err := normalizeEnvPatterns(dirname, "exports", manifest.Config.Exports)
	if err != nil {
		return Module{}, err
	}
	credentialProviders, credentialConsumers, err := normalizeCredentialDeclarations(dirname, manifest.Credentials, consumes, hook)
	if err != nil {
		return Module{}, err
	}
	types, err := normalizeParamTypes(dirname, manifest.Config.Types)
	if err != nil {
		return Module{}, err
	}
	// input_required and default_source are new schema fields, so rejecting
	// contradictory declarations cannot break a legacy manifest. Keep the old
	// required+default behavior compatible, but never load a Module whose public
	// caller-input contract is impossible to satisfy consistently.
	if err := validateInputDefaultSemantics(dirname, manifest.Config, types); err != nil {
		return Module{}, err
	}
	defaults, err := normalizeDefaultsWithTypes(dirname, dirname, envPrefix, exports, manifest.Config.Defaults, types)
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
		if (account.Apply != "" || account.Rotate != "") && len(hook.Command) == 0 {
			return Module{}, fmt.Errorf("module %q local account %q declares lifecycle handlers without a module hook", dirname, account.ID)
		}
		if account.Apply != "" && !hookSupportsPhase(hook, "local_account_apply") {
			return Module{}, fmt.Errorf("module %q local account %q declares an apply handler but hook.phases omits local_account_apply", dirname, account.ID)
		}
		if account.Rotate != "" {
			for _, phase := range []string{"local_account_rotate", "local_account_rollback"} {
				if !hookSupportsPhase(hook, phase) {
					return Module{}, fmt.Errorf("module %q local account %q declares a rotate handler but hook.phases omits %s", dirname, account.ID, phase)
				}
			}
		}
	}
	provisioning, authentication, identityInterfaces, err := normalizeIdentity(dirname, manifest.Identity)
	if err != nil {
		return Module{}, err
	}
	requirements, err := normalizeConfigRequirements(manifest.Name, manifest.Name, envPrefix, exports, manifest.Config)
	if err != nil {
		return Module{}, err
	}
	parameters := declaredParameters(manifest.Name, manifest.Config)
	if err := validateDeclaredParameterRuntimeKeys(dirname, manifest.Name, envPrefix, exports, parameters); err != nil {
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
		Defaults:               defaults,
		InputRequired:          requirements.InputRequired,
		Required:               requirements.Required,
		MustResolve:            requirements.MustResolve,
		Parameters:             parameters,
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
		CredentialProviders:    credentialProviders,
		CredentialConsumers:    credentialConsumers,
		UseHostLAN:             manifest.Features.HostLAN,
		PublishesDomain:        manifest.Features.Domain,
		Hook:                   hook,
		RuntimeType:            manifest.Runtime.Type,
		ComposeFile:            composeFile,
	}
	return mod, nil
}

func normalizeHookConfig(module string, hook HookConfig) (HookConfig, error) {
	if len(hook.Phases) > 0 && len(hook.Command) == 0 {
		return HookConfig{}, fmt.Errorf("module %q declares hook phases without a hook command", module)
	}
	if hook.Phases == nil {
		return hook, nil
	}
	if len(hook.Phases) == 0 {
		return HookConfig{}, fmt.Errorf("module %q declares an empty hook phase list; omit phases only for legacy v1 behavior", module)
	}
	phases := make([]string, 0, len(hook.Phases))
	seen := map[string]bool{}
	for _, raw := range hook.Phases {
		phase := strings.ToLower(strings.TrimSpace(raw))
		if !knownModuleHookPhases[phase] {
			known := make([]string, 0, len(knownModuleHookPhases))
			for name := range knownModuleHookPhases {
				known = append(known, name)
			}
			sort.Strings(known)
			return HookConfig{}, fmt.Errorf("module %q declares unknown hook phase %q; use one of %s", module, raw, strings.Join(known, ", "))
		}
		if seen[phase] {
			return HookConfig{}, fmt.Errorf("module %q declares hook phase %q more than once", module, phase)
		}
		seen[phase] = true
		phases = append(phases, phase)
	}
	hook.Phases = phases
	return hook, nil
}

func normalizeCredentialDeclarations(module string, declared manifestCredentials, consumes []string, hook HookConfig) ([]CredentialProvider, []CredentialConsumer, error) {
	providers := make([]CredentialProvider, 0, len(declared.Provides))
	providerIDs := map[string]bool{}
	providerKeys := map[string]bool{}
	for _, raw := range declared.Provides {
		id := strings.TrimSpace(raw.ID)
		if !credentialIDPattern.MatchString(id) || !strings.HasPrefix(id, module+".") {
			return nil, nil, fmt.Errorf("module %q credential provider id %q must be a lower-case dotted id in the %s namespace", module, raw.ID, module)
		}
		if providerIDs[id] {
			return nil, nil, fmt.Errorf("module %q provides credential %s more than once", module, id)
		}
		providerIDs[id] = true

		secretKey := strings.TrimSpace(raw.SecretKey)
		if !envKeyPattern.MatchString(secretKey) {
			return nil, nil, fmt.Errorf("module %q credential %s secret_key %q is not an environment key", module, id, raw.SecretKey)
		}
		if providerKeys[secretKey] {
			return nil, nil, fmt.Errorf("module %q provides more than one credential through Secret key %s", module, secretKey)
		}
		providerKeys[secretKey] = true

		kind := strings.ToLower(strings.TrimSpace(raw.Type))
		if !contains([]string{"password", "shared_secret", "token", "key", "certificate"}, kind) {
			return nil, nil, fmt.Errorf("module %q credential %s has unsupported type %q", module, id, raw.Type)
		}
		mode := strings.ToLower(strings.TrimSpace(raw.RotationMode))
		if !contains([]string{"reconcile", "overlap", "migrate", "external"}, mode) {
			return nil, nil, fmt.Errorf("module %q credential %s has unsupported rotation_mode %q", module, id, raw.RotationMode)
		}

		generator := raw.Generation
		generator.Kind = strings.ToLower(strings.TrimSpace(generator.Kind))
		lifecycle := raw.Lifecycle
		lifecycle.Probe = strings.TrimSpace(lifecycle.Probe)
		lifecycle.Reconcile = strings.TrimSpace(lifecycle.Reconcile)
		lifecycle.Verify = strings.TrimSpace(lifecycle.Verify)
		if mode == "reconcile" {
			if !contains([]string{"password", "hex"}, generator.Kind) || generator.Length < 16 {
				return nil, nil, fmt.Errorf("module %q credential %s reconcile generation must use password or hex with length at least 16", module, id)
			}
			if lifecycle.Probe == "" || lifecycle.Reconcile == "" || lifecycle.Verify == "" {
				return nil, nil, fmt.Errorf("module %q credential %s reconcile lifecycle requires probe, reconcile, and verify handlers", module, id)
			}
			for _, phase := range []string{"credential_probe", "credential_reconcile", "credential_verify"} {
				if !contains(hook.Phases, phase) {
					return nil, nil, fmt.Errorf("module %q credential %s requires an explicit %s hook phase", module, id, phase)
				}
			}
		} else if generator.Kind != "" || generator.Length != 0 {
			return nil, nil, fmt.Errorf("module %q credential %s rotation mode %s must not declare an ANAS generation policy", module, id, mode)
		}
		controls := make([]string, 0, len(raw.Controls))
		for _, controlledRaw := range raw.Controls {
			controlled := strings.TrimSpace(controlledRaw)
			if !credentialIDPattern.MatchString(controlled) {
				return nil, nil, fmt.Errorf("module %q credential %s controls invalid credential id %q", module, id, controlledRaw)
			}
			if contains(controls, controlled) {
				return nil, nil, fmt.Errorf("module %q credential %s declares control edge %s more than once", module, id, controlled)
			}
			controls = append(controls, controlled)
		}
		providers = append(providers, CredentialProvider{
			ID: id, SecretKey: secretKey, Kind: kind, RotationMode: mode,
			Generator: generator, Lifecycle: lifecycle, Controls: controls,
		})
	}

	consumers := make([]CredentialConsumer, 0, len(declared.Consumes))
	consumerIDs := map[string]bool{}
	for _, raw := range declared.Consumes {
		id := strings.TrimSpace(raw.Credential)
		if !credentialIDPattern.MatchString(id) {
			return nil, nil, fmt.Errorf("module %q consumes invalid credential id %q", module, raw.Credential)
		}
		if consumerIDs[id] {
			return nil, nil, fmt.Errorf("module %q consumes credential %s more than once", module, id)
		}
		consumerIDs[id] = true
		projection := strings.TrimSpace(raw.Projection)
		if !envKeyPattern.MatchString(projection) {
			return nil, nil, fmt.Errorf("module %q credential %s projection %q is not an environment key", module, id, raw.Projection)
		}
		if !matchEnvPattern(consumes, projection) {
			return nil, nil, fmt.Errorf("module %q credential %s projection %s must also be declared in config.consumes", module, id, projection)
		}
		consumers = append(consumers, CredentialConsumer{Credential: id, Projection: projection})
	}
	sort.Slice(providers, func(i, j int) bool { return providers[i].ID < providers[j].ID })
	sort.Slice(consumers, func(i, j int) bool { return consumers[i].Credential < consumers[j].Credential })
	return providers, consumers, nil
}

// normalizeParamTypes validates the declarations themselves. A type nobody can
// satisfy -- an unknown kind, or an enumeration with no members -- would reject
// every value including the module's own default, so it is refused when the module
// is loaded rather than when somebody tries to set the parameter.
func normalizeParamTypes(module string, in map[string]manifestParamType) (map[string]ParamType, error) {
	out := map[string]ParamType{}
	for name, declared := range in {
		parameterName := strings.ToLower(strings.TrimSpace(name))
		if parameterName == "" {
			return nil, fmt.Errorf("module %q config.types contains an empty parameter name", module)
		}
		if !configParameterNamePattern.MatchString(parameterName) {
			return nil, fmt.Errorf("module %q config.types parameter name %q is not lower-snake-case", module, name)
		}
		if _, exists := out[parameterName]; exists {
			return nil, fmt.Errorf("module %q config.types declares %s more than once after normalization", module, parameterName)
		}
		parameter, err := configschema.NormalizeDefinition(configschema.Parameter{
			Kind: declared.Kind, Enum: declared.Enum, Constraints: declared.Constraints,
			DefaultSource: declared.DefaultSource,
		})
		if err != nil {
			return nil, fmt.Errorf("module %q config.types.%s %w", module, name, err)
		}
		// Absence from config.types is the legacy compatibility spelling for an
		// undeclared parameter. Once an entry is present, an empty/null mapping is
		// almost certainly a typo and must not silently collapse back to absence.
		if !parameter.Declared() {
			return nil, fmt.Errorf("module %q config.types.%s explicitly declares an empty type", module, name)
		}
		out[parameterName] = parameter
	}
	return out, nil
}

// validateParamTypeDefaults checks only parameters that declare a type. Missing
// declarations remain valid here for compatibility with older Module bundles;
// repository publication checks enforce complete metadata for current sources.
func validateParamTypeDefaults(owner string, defaults map[string]any, types map[string]ParamType) error {
	_, err := normalizeParamTypeDefaults(owner, defaults, types)
	return err
}

func normalizeParamTypeDefaults(owner string, defaults map[string]any, types map[string]ParamType) (map[string]string, error) {
	out := make(map[string]string, len(defaults))
	for name, value := range defaults {
		parameter := strings.ToLower(strings.TrimSpace(name))
		if parameter == "" {
			return nil, fmt.Errorf("module %q config.defaults contains an empty parameter name", owner)
		}
		if !configParameterNamePattern.MatchString(parameter) {
			return nil, fmt.Errorf("module %q config.defaults parameter name %q is not lower-snake-case", owner, name)
		}
		if _, exists := out[parameter]; exists {
			return nil, fmt.Errorf("module %q config.defaults declares %s more than once after normalization", owner, parameter)
		}
		spec := types[parameter]
		scalar := config.Scalar(value)
		if !spec.Declared() {
			out[parameter] = scalar
			continue
		}
		// Empty is a valid runtime spelling for "unset", but a literal default
		// must actually provide a value. Otherwise metadata would advertise a
		// static default that still fails bool/int/enum resolution. An explicitly
		// empty string remains meaningful and legal.
		if spec.Kind != "string" && strings.TrimSpace(scalar) == "" {
			return nil, fmt.Errorf("module %q config.defaults.%s must provide a non-empty %s value", owner, name, spec.Kind)
		}
		if value != nil {
			switch reflect.TypeOf(value).Kind() {
			case reflect.String, reflect.Bool,
				reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
				reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64,
				reflect.Float32, reflect.Float64:
			default:
				return nil, fmt.Errorf("module %q config.defaults.%s must be a scalar %s", owner, name, spec.Kind)
			}
		}
		normalized, err := normalizeValueAgainstParamType(scalar, spec)
		if err != nil {
			return nil, fmt.Errorf("module %q config.defaults.%s %w", owner, name, err)
		}
		out[parameter] = normalized
	}
	return out, nil
}

func normalizeChangePolicies(owner string, in map[string]manifestChangePolicy) (map[string]ChangePolicy, error) {
	out := make(map[string]ChangePolicy, len(in))
	for name, policy := range in {
		parameter := strings.ToLower(strings.TrimSpace(name))
		if parameter == "" {
			return nil, fmt.Errorf("module %q config.changes contains an empty parameter name", owner)
		}
		if !configParameterNamePattern.MatchString(parameter) {
			return nil, fmt.Errorf("module %q config.changes parameter name %q is not lower-snake-case", owner, name)
		}
		if _, exists := out[parameter]; exists {
			return nil, fmt.Errorf("module %q config.changes declares %s more than once after normalization", owner, parameter)
		}
		if !validChangeEffect(policy.Effect) {
			return nil, fmt.Errorf("module %q config.changes.%s has invalid effect %q", owner, name, policy.Effect)
		}
		if policy.Effect == "credential_rotate" && !policy.Sensitive {
			return nil, fmt.Errorf("module %q config.changes.%s uses credential_rotate but is not marked sensitive", owner, name)
		}
		out[parameter] = ChangePolicy{
			Effect: policy.Effect, Apply: policy.Apply,
			Description: policy.Description, Sensitive: policy.Sensitive,
			Executor: policy.Executor, Verify: policy.Verify,
		}
	}
	return out, nil
}

func normalizeDefaultsWithTypes(owner, module, prefix string, exports []string, in map[string]any, types map[string]ParamType) (map[string]string, error) {
	values, err := normalizeParamTypeDefaults(owner, in, types)
	if err != nil {
		return nil, err
	}
	out := make(map[string]string, len(values))
	for parameter, value := range values {
		out[moduleParamEnvKey(module, prefix, exports, parameter)] = value
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

type normalizedConfigRequirements struct {
	InputRequired []string
	Required      []string
	MustResolve   []string
}

func normalizeConfigRequirements(owner, module, prefix string, exports []string, cfg manifestConfig) (normalizedConfigRequirements, error) {
	inputRequired, err := normalizeRequirementParameters(owner, module, prefix, exports, "input_required", cfg.InputRequired)
	if err != nil {
		return normalizedConfigRequirements{}, err
	}
	required, err := normalizeRequirementParameters(owner, module, prefix, exports, "required", cfg.Required)
	if err != nil {
		return normalizedConfigRequirements{}, err
	}
	mustResolve, err := normalizeRequirementParameters(owner, module, prefix, exports, "must_resolve", cfg.MustResolve)
	if err != nil {
		return normalizedConfigRequirements{}, err
	}
	return normalizedConfigRequirements{
		InputRequired: inputRequired,
		Required:      required,
		MustResolve:   mustResolve,
	}, nil
}

// normalizeRequirementParameters is shared by all requirement stages. Empty
// items and duplicates after trimming/case/env normalization are almost always
// manifest typos, so accepting them would make strict schema parsing illusory.
func normalizeRequirementParameters(owner, module, prefix string, exports []string, field string, in []string) ([]string, error) {
	out := make([]string, 0, len(in))
	seen := map[string]bool{}
	for _, raw := range in {
		trimmed := strings.TrimSpace(raw)
		if trimmed == "" {
			return nil, fmt.Errorf("module %q config.%s contains an empty parameter", owner, field)
		}
		if !isEnvKey(trimmed) && !configParameterNamePattern.MatchString(strings.ToLower(trimmed)) {
			return nil, fmt.Errorf("module %q config.%s parameter name %q is not lower-snake-case or an environment key", owner, field, raw)
		}
		key := moduleParamEnvKey(module, prefix, exports, raw)
		if seen[key] {
			return nil, fmt.Errorf("module %q config.%s declares %s more than once after normalization", owner, field, key)
		}
		seen[key] = true
		out = append(out, key)
	}
	return out, nil
}

func validateDeclaredParameterRuntimeKeys(owner, module, prefix string, exports []string, parameters []string) error {
	seen := map[string]string{}
	for _, parameter := range parameters {
		key := moduleParamEnvKey(module, prefix, exports, parameter)
		if module == globalScope {
			key = parameterEnvKey(globalModuleName, parameter, nil)
		}
		if previous, duplicate := seen[key]; duplicate && previous != parameter {
			return fmt.Errorf("module %q config parameters %s and %s both resolve to runtime key %s", owner, previous, parameter, key)
		}
		seen[key] = parameter
	}
	return nil
}

func defaultEnvPrefix(module string) string {
	return strings.ToUpper(strings.ReplaceAll(module, "-", "_"))
}

// normalizeEnvPatterns validates consumes/exports entries: an exact env key,
// a prefix glob such as APPS_LIST__*, or a suffix glob such as *_DB_NAME used
// by capability providers that scan their consumers' declarations. A glob has
// exactly one leading or trailing *, and its non-wildcard stem is env-safe.
func normalizeEnvPatterns(module, field string, in []string) ([]string, error) {
	out := []string{}
	for _, raw := range in {
		pattern := strings.TrimSpace(raw)
		if pattern == "" {
			return nil, fmt.Errorf("module %q config.%s contains an empty pattern", module, field)
		}
		stem := pattern
		if strings.HasPrefix(pattern, "*") {
			stem = strings.TrimPrefix(pattern, "*")
		} else if strings.HasSuffix(pattern, "*") {
			stem = strings.TrimSuffix(pattern, "*")
		}
		if strings.Contains(stem, "*") || !envKeyPattern.MatchString(stem) {
			return nil, fmt.Errorf("module %q config.%s pattern %q must be an environment key or one environment-safe stem with a leading or trailing *", module, field, pattern)
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
	return envKeyPattern.MatchString(key)
}
