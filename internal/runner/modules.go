package runner

import (
	"fmt"
	"strings"
)

type Module struct {
	Name         string
	Version      string
	Revision     int
	AppVersion   string
	UpgradeFrom  string
	DataBreaking *[]string
	SourceDir    string
	EnvPrefix    string
	Defaults     map[string]string
	Required     []string
	// Parameters is every parameter module.yml declares, in config spelling.
	// Defaults and Required hold the same names already converted to env keys,
	// which is the form calculation needs and the wrong form for an inventory.
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
	Hook                   HookConfig
	RuntimeType            string
	ComposeFile            string
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
type ContractProvider struct {
	Name          string                       `yaml:"name" json:"name"`
	Version       string                       `yaml:"version" json:"version"`
	Interface     string                       `yaml:"interface" json:"interface"`
	Manifest      string                       `yaml:"manifest" json:"manifest"`
	Operations    map[string]ProviderOperation `yaml:"operations" json:"operations"`
	OperationSvcs []string                     `yaml:"operation_services" json:"operation_services"`
}

type ProviderOperation struct {
	Runtime string   `yaml:"runtime" json:"runtime"`
	Service string   `yaml:"service" json:"service"`
	Command []string `yaml:"command" json:"command"`
}

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

type LocalAccount struct {
	ID              string `yaml:"id" json:"id"`
	Purpose         string `yaml:"purpose" json:"purpose"`
	FixedUsername   string `yaml:"fixed_username,omitempty" json:"fixed_username,omitempty"`
	PasswordPolicy  string `yaml:"password_policy" json:"password_policy"`
	ContainerFormat string `yaml:"container_format" json:"container_format"`
	Apply           string `yaml:"apply,omitempty" json:"apply,omitempty"`
	Rotate          string `yaml:"rotate,omitempty" json:"rotate,omitempty"`
}

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

type ChangePolicy struct {
	Effect      string `json:"effect"`
	Apply       string `json:"apply"`
	Description string `json:"description,omitempty"`
	Sensitive   bool   `json:"sensitive"`
}

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

// ParamType is a parameter's accepted shape. An empty Kind with no Enum means
// nothing was declared, which is the state every module parameter was in before
// this existed: any string went in and a wrong one was found, at best, when a
// container failed to start.
type ParamType struct {
	Kind string
	Enum []string
}

// Declared reports whether the module said anything about this parameter.
func (t ParamType) Declared() bool { return t.Kind != "" || len(t.Enum) > 0 }

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
