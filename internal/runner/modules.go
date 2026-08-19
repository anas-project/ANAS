package runner

import (
	"fmt"
	"sort"
	"strings"

	"github.com/anas-project/ANAS/internal/configschema"
	"github.com/anas-project/ANAS/internal/deployment"
)

type Module struct {
	Name         string
	Version      string
	Revision     int
	AppVersion   string
	Lifecycle    string
	UpgradeFrom  string
	DataBreaking *[]string
	SourceDir    string
	EnvPrefix    string
	Defaults     map[string]string
	// InputRequired is the caller-input contract. Required preserves the legacy
	// pre-Hook invariant after defaults and resolvers have run. MustResolve is the
	// final invariant after the calculate Hook patch. Their runtime unions are
	// computed at the enforcement and inventory boundaries so the manifest fields
	// remain distinguishable here.
	InputRequired []string
	Required      []string
	MustResolve   []string
	// Parameters is every parameter module.yml declares, in config spelling.
	// Defaults and all three requirement lists hold the same names already
	// converted to env keys, which is the form calculation needs and the wrong
	// form for an inventory.
	Parameters []string
	Consumes   []string
	Exports    []string
	Changes    map[string]ChangePolicy
	// Types is what each parameter accepts, by config name.
	Types                  map[string]ParamType
	Requires               []Dependency
	RequiresOne            []AlternativeDependency
	RequiresContracts      []ContractDependency
	ContractProviders      []ContractProvider
	Resources              []ResourceRequirement
	Provides               []ProvidedCapability
	RequiresCapabilities   []RequiredCapability
	RunAfter               []string
	IdentityInterfaces     []string
	IdentityAppGroup       bool
	IdentityProvisioning   *IdentityProvisioning
	IdentityAuthentication *IdentityAuthentication
	ManagementSurfaces     []ManagementSurface
	LocalAccounts          []LocalAccount
	UseHostLAN             string
	PublishesDomain        bool
	Hook                   HookConfig
	RuntimeType            string
	ComposeFile            string
}

// preHookRequirements preserves the original config.required check point while
// also carrying the stronger caller-input contract through calculation.
func (m Module) preHookRequirements() []string {
	return uniqueStrings(append(append([]string{}, m.InputRequired...), m.Required...))
}

// finalRequirements is the public must-resolve meaning: every requirement
// class must still be non-empty once a calculate Hook has applied its patch.
func (m Module) finalRequirements() []string {
	out := m.preHookRequirements()
	return uniqueStrings(append(out, m.MustResolve...))
}

func (a *app) moduleLifecyclePlanSummary() string {
	lines := []string{}
	for _, name := range a.order {
		switch a.reg[name].Lifecycle {
		case "developing":
			lines = append(lines, fmt.Sprintf("module lifecycle: %s=developing (not release quality)\n", name))
		case "deprecated":
			lines = append(lines, fmt.Sprintf("module lifecycle: %s=deprecated (do not use for new deployments)\n", name))
		}
	}
	sort.Strings(lines)
	return strings.Join(lines, "")
}

// ContractDependency describes a versioned protocol a module consumes.  It is
// resolved to a provider module and contributes a normal module dependency
// edge, but its resources have a separate ensure lifecycle.
type ContractDependency struct {
	Name       string
	Version    string
	SelectedBy string
	Interfaces []string
	Default    string
}

// ContractProvider is a provider implementation shipped inside one module.
// Operations are deliberately data: the runner dispatches runtimes such as
// compose_run without knowing anything about PostgreSQL or MariaDB.
type ContractProvider = deployment.ContractProvider
type ProviderOperation = deployment.ProviderOperation

type ResourceRequirement struct {
	ID       string
	Contract string
	Binding  string
	Spec     map[string]any
	SpecFrom map[string]string
}

func (m Module) providedContract(name, iface string) (ContractProvider, bool) {
	for _, provider := range m.ContractProviders {
		if provider.Name == name && provider.Interface == iface {
			return provider, true
		}
	}
	return ContractProvider{}, false
}

type IdentityProvisioning struct {
	Capability  string
	AnyOf       []string
	Objects     []string
	IdentityKey string
	Required    bool
}

type IdentityAuthentication struct {
	Capability string
	SelectedBy string
	AnyOf      []string
	Prefer     []string
}

type ManagementSurface struct {
	ID             string
	URIFrom        string
	Authentication string
}

type LocalAccount = deployment.LocalAccount

// localAccountOperation is ephemeral hook input for a credential transaction.
// Passwords are intentionally absent; current and candidate values travel in
// the module-scoped Secrets map instead of metadata, argv, or deployment env.
type localAccountOperation struct {
	Handler            string `json:"handler"`
	AccountID          string `json:"account_id"`
	Username           string `json:"username"`
	SecretKey          string `json:"secret_key"`
	CandidateSecretKey string `json:"candidate_secret_key"`
}

type AlternativeDependency struct {
	Capability string
	SelectedBy string
	Providers  []string
	Default    string
}

// ProvidedCapability is a capability a module implements, together with the
// protocol interfaces it serves. Unlike requires_one, consumers never name
// the providing module: the deployment picks one provider and the runner binds
// consumers to it by capability.
type ProvidedCapability struct {
	Name       string
	Interfaces []string
}

// RequiredCapability is a capability a module consumes. It carries no provider
// selector by design: the provider is a deployment-level choice
// (config iam.provider) and a module may only narrow the protocol.
type RequiredCapability struct {
	Name string
	// InterfaceSelectedBy is the module parameter holding the protocol
	// override, resolved against the module env prefix (iam_protocol ->
	// NEXTCLOUD_IAM_PROTOCOL).
	InterfaceSelectedBy string
	// AnyOf lists every protocol this module can speak; the resolved protocol
	// must be one of them.
	AnyOf []string
	// Prefer orders AnyOf for "auto" resolution.
	Prefer []string
}

func (m Module) providedCapability(name string) (ProvidedCapability, bool) {
	for _, capability := range m.Provides {
		if capability.Name == name {
			return capability, true
		}
	}
	return ProvidedCapability{}, false
}

type ChangePolicy = deployment.ChangePolicy

// bareEnvParameter reports the env key for a parameter a module publishes under a
// bare env name instead of under its own prefix, which is what listing that
// name in `config.exports` declares. Such a parameter is owned by the module but
// addressed by its bare name, so it is set in the config's top level `env:`
// block rather than under `modules.<module>.config`, where every key acquires the
// prefix.
func (m Module) bareEnvParameter(parameter string) (string, bool) {
	key := globalParamEnv(parameter)
	if contains(m.Exports, key) {
		return key, true
	}
	return "", false
}

// ParamType remains the runner spelling for compatibility while the common
// definition and normalization semantics live in configschema.
type ParamType = configschema.Parameter

type Dependency struct {
	Name     string
	Version  string
	Optional bool
}

func requireKeys(e map[string]string, keys []string) error {
	missing := []string{}
	for _, k := range keys {
		if strings.TrimSpace(e[k]) == "" {
			missing = append(missing, k)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("missing required config: %s", strings.Join(missing, ", "))
	}
	return nil
}
