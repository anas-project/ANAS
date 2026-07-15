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
	Deps        []string
	Requires    []Dependency
	RunAfter    []string
	UseLDAP     bool
	UseHostLAN  string
	Hook        HookConfig
	RuntimeType string
	ComposeFile string
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
