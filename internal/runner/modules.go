package runner

import (
	"fmt"
	"strings"
)

type Module struct {
	Name                 string
	Version              string
	AppVersion           string
	UpgradeFrom          string
	DataBreaking         *[]string
	SourceDir            string
	EnvPrefix            string
	Defaults             map[string]string
	Required             []string
	Consumes             []string
	Exports              []string
	Changes              map[string]ChangePolicy
	Requires             []Dependency
	RequiresOne          []AlternativeDependency
	Provides             []ProvidedCapability
	RequiresCapabilities []RequiredCapability
	RunAfter             []string
	UseLDAP              bool
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
