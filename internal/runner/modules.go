package runner

import (
	"fmt"
	"strings"
)

type Module struct {
	Name        string
	Version     string
	UpgradeFrom string
	SourceDir   string
	EnvPrefix   string
	Defaults    map[string]string
	Required    []string
	Changes     map[string]ChangePolicy
	Requires    []Dependency
	RequiresOne []AlternativeDependency
	RunAfter    []string
	UseLDAP     bool
	UseHostLAN  string
	Hook        HookConfig
	RuntimeType string
	ComposeFile string
}

type AlternativeDependency struct {
	Capability string
	SelectedBy string
	Providers  []string
	Default    string
}

type ChangePolicy struct {
	Effect      string
	Apply       string
	Description string
	Sensitive   bool
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
