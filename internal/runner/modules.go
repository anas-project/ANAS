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
	// Parameters is every parameter cask.yml declares, in config spelling.
	// Defaults and Required hold the same names already converted to env keys,
	// which is the form calculation needs and the wrong form for an inventory.
	Parameters []string
	Consumes   []string
	Exports    []string
	Changes    map[string]ChangePolicy
	// Types is what each parameter accepts, by config name.
	Types                map[string]ParamType
	Requires             []Dependency
	RequiresOne          []AlternativeDependency
	Provides             []ProvidedCapability
	RequiresCapabilities []RequiredCapability
	RunAfter             []string
	IdentityInterfaces   []string
	IdentityAppGroup     bool
	UseHostLAN           string
	Hook                 HookConfig
	RuntimeType          string
	ComposeFile          string
}

type AlternativeDependency struct {
	Capability string
	SelectedBy string
	Providers  []string
	Default    string
}

// ProvidedCapability is a capability a cask implements, together with the
// protocol interfaces it serves. Unlike requires_one, consumers never name
// the providing cask: the deployment picks one provider and the runner binds
// consumers to it by capability.
type ProvidedCapability struct {
	Name       string
	Interfaces []string
}

// RequiredCapability is a capability a cask consumes. It carries no provider
// selector by design: the provider is a deployment-level choice
// (config iam.provider) and a cask may only narrow the protocol.
type RequiredCapability struct {
	Name string
	// InterfaceSelectedBy is the cask parameter holding the protocol
	// override, resolved against the cask env prefix (iam_protocol ->
	// NEXTCLOUD_IAM_PROTOCOL).
	InterfaceSelectedBy string
	// AnyOf lists every protocol this cask can speak; the resolved protocol
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

// bareEnvParameter reports the env key for a parameter a cask publishes under a
// bare env name instead of under its own prefix, which is what listing that
// name in `config.exports` declares. Such a parameter is owned by the cask but
// addressed by its bare name, so it is set in the config's top level `env:`
// block rather than under `services.<cask>.env`, where every key acquires the
// prefix.
func (m Module) bareEnvParameter(parameter string) (string, bool) {
	key := globalParamEnv(parameter)
	if contains(m.Exports, key) {
		return key, true
	}
	return "", false
}

// ParamType is a parameter's accepted shape. An empty Kind with no Enum means
// nothing was declared, which is the state every cask parameter was in before
// this existed: any string went in and a wrong one was found, at best, when a
// container failed to start.
type ParamType struct {
	Kind string
	Enum []string
}

// Declared reports whether the cask said anything about this parameter.
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
