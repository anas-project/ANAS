package runner

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/anas-project/ANAS/internal/configschema"
	"github.com/anas-project/ANAS/internal/modulepackage"
)

const BuiltinInventoryAPIVersion = "anas.builtin-inventory/v1"

// ConfigParameterInventoryEntry is the typed, read-only projection behind
// `anas config list --json`. Documentation and future schema consumers use
// this instead of decoding manifests independently, so addressability,
// defaults, required phases, constraints, and editability cannot drift from
// the CLI contract.
type ConfigParameterInventoryEntry struct {
	Path          string                   `json:"path"`
	Module        string                   `json:"module"`
	Parameter     string                   `json:"parameter"`
	Type          string                   `json:"type"`
	AllowedValues []string                 `json:"allowed_values,omitempty"`
	Default       string                   `json:"default"`
	HasDefault    bool                     `json:"has_default"`
	DefaultSource string                   `json:"default_source"`
	EnvKey        string                   `json:"env_key"`
	InputRequired bool                     `json:"input_required"`
	MustResolve   bool                     `json:"must_resolve"`
	Constraints   configschema.Constraints `json:"constraints,omitempty"`
	Sensitive     bool                     `json:"sensitive"`
	Editable      bool                     `json:"editable"`
	EditCommand   string                   `json:"edit_command,omitempty"`
	Effect        string                   `json:"effect"`
	Apply         string                   `json:"apply,omitempty"`
	Description   string                   `json:"description,omitempty"`
}

// BuiltinModuleInventoryEntry is the documentation and publication projection
// of one bundled Module. Runtime-only details stay on Module; this projection
// contains the manifest facts that current catalogs are allowed to publish.
type BuiltinModuleInventoryEntry struct {
	APIVersion     string `json:"api_version"`
	Name           string `json:"name"`
	Version        string `json:"version"`
	Revision       int    `json:"revision"`
	AppVersion     string `json:"app_version,omitempty"`
	Title          string `json:"title"`
	Description    string `json:"description"`
	Category       string `json:"category"`
	Status         string `json:"status"`
	RuntimeType    string `json:"runtime_type"`
	ComposeFile    string `json:"compose_file,omitempty"`
	ParameterCount int    `json:"parameter_count"`
}

// BuiltinInventorySummary contains only values derived from Modules and
// Parameters. It is deliberately not decoded from a checked-in statistics
// file, so every consumer observes the same current totals and distributions.
type BuiltinInventorySummary struct {
	ModuleCount                    int            `json:"module_count"`
	ParameterCount                 int            `json:"parameter_count"`
	GlobalParameterCount           int            `json:"global_parameter_count"`
	ModuleParameterCount           int            `json:"module_parameter_count"`
	StructuredModuleParameterCount int            `json:"structured_module_parameter_count"`
	BareEnvParameterCount          int            `json:"bare_env_parameter_count"`
	InputRequiredCount             int            `json:"input_required_count"`
	MustResolveCount               int            `json:"must_resolve_count"`
	ConstraintCount                int            `json:"constraint_count"`
	UnknownCount                   int            `json:"unknown_count"`
	ByStatus                       map[string]int `json:"by_status"`
	ByOwner                        map[string]int `json:"by_owner"`
	ByType                         map[string]int `json:"by_type"`
	ByDefaultSource                map[string]int `json:"by_default_source"`
	ByEffect                       map[string]int `json:"by_effect"`
	ModuleByEffect                 map[string]int `json:"module_by_effect"`
}

// BuiltinInventory is the single logical read model for the current bundled
// Module catalog, CLI configuration projection, and their derived statistics.
// The underlying declarations remain distributed with their owning Modules.
type BuiltinInventory struct {
	APIVersion string                          `json:"api_version"`
	Modules    []BuiltinModuleInventoryEntry   `json:"modules"`
	Parameters []ConfigParameterInventoryEntry `json:"parameters"`
	Summary    BuiltinInventorySummary         `json:"summary"`
}

// BuiltinInventorySurface is the one intentionally reviewed snapshot. Counts
// are omitted because they are mechanically derived from these complete sets.
type BuiltinInventorySurface struct {
	APIVersion string   `json:"api_version"`
	Modules    []string `json:"modules"`
	Parameters []string `json:"parameters"`
}

// LoadConfigParameterInventory loads every bundled parameter from root. Root
// is the repository or bundle parent containing modules/.
func LoadConfigParameterInventory(root string) ([]ConfigParameterInventoryEntry, error) {
	inventory, err := loadBuiltinInventory(root, false)
	if err != nil {
		return nil, err
	}
	return inventory.Parameters, nil
}

// LoadBuiltinInventory loads and validates the complete repository inventory.
// Unlike LoadConfigParameterInventory, it requires the publication catalog and
// therefore targets repository tooling rather than installed Module bundles.
func LoadBuiltinInventory(root string) (BuiltinInventory, error) {
	return loadBuiltinInventory(root, true)
}

func loadBuiltinInventory(root string, validatePublicationCatalog bool) (BuiltinInventory, error) {
	reg, err := loadRegistry(root)
	if err != nil {
		return BuiltinInventory{}, err
	}
	if validatePublicationCatalog {
		for _, module := range reg {
			manifestPath := filepath.Join(module.SourceDir, "module.yml")
			if err := ValidateModuleConfigMetadataFile(manifestPath); err != nil {
				return BuiltinInventory{}, fmt.Errorf("validate built-in Module configuration metadata: %w", err)
			}
		}
		catalogPath := filepath.Join(root, ".github", "modules.json")
		catalog, err := modulepackage.LoadCatalog(catalogPath)
		if err != nil {
			return BuiltinInventory{}, fmt.Errorf("load Module publication catalog: %w", err)
		}
		if err := modulepackage.ValidateCatalog(root, catalog); err != nil {
			return BuiltinInventory{}, fmt.Errorf("validate Module publication catalog: %w", err)
		}
	}

	parameters, err := configParameterInventory(reg)
	if err != nil {
		return BuiltinInventory{}, err
	}
	modules := make([]BuiltinModuleInventoryEntry, 0, len(reg))
	for _, module := range reg {
		modules = append(modules, BuiltinModuleInventoryEntry{
			APIVersion:     module.APIVersion,
			Name:           module.Name,
			Version:        module.Version,
			Revision:       module.Revision,
			AppVersion:     module.AppVersion,
			Title:          module.Title,
			Description:    module.Description,
			Category:       module.Category,
			Status:         module.Lifecycle,
			RuntimeType:    module.RuntimeType,
			ComposeFile:    module.ComposeFile,
			ParameterCount: len(module.Parameters),
		})
	}
	sort.Slice(modules, func(i, j int) bool { return modules[i].Name < modules[j].Name })
	sort.Slice(parameters, func(i, j int) bool { return parameters[i].Path < parameters[j].Path })

	inventory := BuiltinInventory{
		APIVersion: BuiltinInventoryAPIVersion,
		Modules:    modules,
		Parameters: parameters,
	}
	inventory.Summary = summarizeBuiltinInventory(inventory.Modules, inventory.Parameters)
	return inventory, nil
}

// Surface returns the complete, stable identity projection used by the single
// reviewed golden file.
func (inventory BuiltinInventory) Surface() BuiltinInventorySurface {
	surface := BuiltinInventorySurface{APIVersion: inventory.APIVersion}
	for _, module := range inventory.Modules {
		surface.Modules = append(surface.Modules, module.Name)
	}
	for _, parameter := range inventory.Parameters {
		surface.Parameters = append(surface.Parameters, parameter.Path)
	}
	return surface
}

func summarizeBuiltinInventory(modules []BuiltinModuleInventoryEntry, parameters []ConfigParameterInventoryEntry) BuiltinInventorySummary {
	summary := BuiltinInventorySummary{
		ModuleCount:     len(modules),
		ParameterCount:  len(parameters),
		ByStatus:        map[string]int{},
		ByOwner:         map[string]int{},
		ByType:          map[string]int{},
		ByDefaultSource: map[string]int{},
		ByEffect:        map[string]int{},
		ModuleByEffect:  map[string]int{},
	}
	for _, module := range modules {
		summary.ByStatus[module.Status]++
	}
	for _, parameter := range parameters {
		summary.ByOwner[parameter.Module]++
		summary.ByType[parameter.Type]++
		summary.ByDefaultSource[parameter.DefaultSource]++
		summary.ByEffect[parameter.Effect]++
		if parameter.Module == "global" {
			summary.GlobalParameterCount++
		} else {
			summary.ModuleParameterCount++
			summary.ModuleByEffect[parameter.Effect]++
			if strings.HasPrefix(parameter.Path, "env.") {
				summary.BareEnvParameterCount++
			} else {
				summary.StructuredModuleParameterCount++
			}
		}
		if parameter.InputRequired {
			summary.InputRequiredCount++
		}
		if parameter.MustResolve {
			summary.MustResolveCount++
		}
		if hasInventoryConstraints(parameter.Constraints) {
			summary.ConstraintCount++
		}
		if parameter.Type == "unknown" {
			summary.UnknownCount++
		}
	}
	return summary
}

func hasInventoryConstraints(constraints configschema.Constraints) bool {
	return constraints.Minimum != nil || constraints.Maximum != nil ||
		constraints.MinLength != nil || constraints.MaxLength != nil ||
		constraints.Pattern != "" || constraints.Format != ""
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
