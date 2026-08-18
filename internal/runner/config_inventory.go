package runner

import (
	"fmt"

	"github.com/anas-project/ANAS/internal/configschema"
)

// ConfigParameterInventoryEntry is the typed, read-only projection behind
// `anas config list --json`. Documentation and future schema consumers use
// this instead of decoding manifests independently, so addressability,
// defaults, required phases, constraints, and editability cannot drift from
// the CLI contract.
type ConfigParameterInventoryEntry struct {
	Path          string
	Module        string
	Parameter     string
	Type          string
	AllowedValues []string
	Default       string
	HasDefault    bool
	DefaultSource string
	EnvKey        string
	InputRequired bool
	MustResolve   bool
	Constraints   configschema.Constraints
	Sensitive     bool
	Editable      bool
	EditCommand   string
	Effect        string
	Apply         string
	Description   string
}

// LoadConfigParameterInventory loads every bundled parameter from root. Root
// is the repository or bundle parent containing modules/.
func LoadConfigParameterInventory(root string) ([]ConfigParameterInventoryEntry, error) {
	reg, err := loadRegistry(root)
	if err != nil {
		return nil, err
	}
	return configParameterInventory(reg)
}

func configParameterInventory(reg map[string]Module) ([]ConfigParameterInventoryEntry, error) {
	entries, err := collectConfigParameters(reg, nil)
	if err != nil {
		return nil, err
	}
	out := make([]ConfigParameterInventoryEntry, 0, len(entries))
	for _, entry := range entries {
		target := configTarget{Display: entry.Path, Module: entry.Module, Parameter: entry.Parameter}
		spec := targetParamType(target, reg)
		kind, allowed := paramTypeDocument(spec)
		editable, editCommand := configEditability(entry.Policy)
		defaultValue := entry.Default
		if entry.Policy.Sensitive {
			defaultValue = ""
		}
		out = append(out, ConfigParameterInventoryEntry{
			Path:          entry.Path,
			Module:        entry.Module,
			Parameter:     entry.Parameter,
			Type:          kind,
			AllowedValues: allowed,
			Default:       defaultValue,
			HasDefault:    entry.HasDefault,
			DefaultSource: entry.DefaultSource,
			EnvKey:        entry.EnvKey,
			InputRequired: parameterInputRequired(entry.Module, entry.Parameter, reg),
			MustResolve:   parameterMustResolve(entry.Module, entry.Parameter, reg),
			Constraints:   spec.Constraints,
			Sensitive:     entry.Policy.Sensitive,
			Editable:      editable,
			EditCommand:   editCommand,
			Effect:        entry.Policy.Effect,
			Apply:         entry.Policy.Apply,
			Description:   entry.Policy.Description,
		})
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("configuration parameter inventory is empty")
	}
	return out, nil
}
