package runner

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"text/tabwriter"

	"github.com/anas-project/ANAS/internal/config"
)

// The inventory behind `anas config list`.
//
// Until this existed the only way to discover a parameter was to already know
// its name: `config explain` answers about a path you supply, and `config set`
// accepts one. The parameters themselves had moved out of sight -- the
// deployment-wide ones into a file compiled into the binary, the rest into
// per-module manifests -- so the honest answer to "what can I set?" was "read the
// example config". `config secret list` had long since settled the principle
// for generated secrets; this applies it to configuration.

// configListEntry is one settable parameter, described the way an operator
// needs it: the path that addresses it, what it would do, and what it is now.
type configListEntry struct {
	Path          string
	Module        string
	Parameter     string
	EnvKey        string
	Default       string
	HasDefault    bool
	DefaultSource string
	Value         string
	Set           bool
	// ValueSensitive is true when the effective value came from a storage
	// boundary that is secret regardless of manifest metadata. It prevents a
	// malformed third-party declaration from exposing a lifecycle credential.
	ValueSensitive bool
	Policy         ChangePolicy
}

type configParameterValueView struct {
	Values    map[string]string
	Present   map[string]bool
	Sensitive map[string]bool
}

// collectConfigParameters builds the inventory from the two declaration sites
// that survive: the embedded global schema and each module's manifest.
//
// Parameters are gathered by env key rather than by config name because the env
// key is what every declaration ultimately produces and what makes two spellings
// of one parameter the same entry. The addressable path is then recovered
// through resolveConfigTarget, so the path printed here is the path `set`
// accepts -- the two cannot drift, because they are the same function.
func collectConfigParameters(reg map[string]Module, settings map[string]string, valueViews ...configParameterValueView) ([]configListEntry, error) {
	type source struct {
		module    string
		parameter string
	}
	sources := map[string]source{}
	add := func(module, parameter string) {
		parameter = strings.ToLower(strings.TrimSpace(parameter))
		if parameter == "" {
			return
		}
		key := parameter
		if module != globalModuleName {
			key = module + "." + parameter
		}
		if _, ok := sources[key]; !ok {
			sources[key] = source{module: module, parameter: parameter}
		}
	}
	// config.Global's typed fields are settable whether or not globals.yml
	// gives them a default or a change policy; virtual_domain has neither.
	for _, parameter := range config.GlobalParameters() {
		add(globalModuleName, parameter)
	}
	for _, parameter := range globalConfig.Parameters {
		add(globalModuleName, parameter)
	}
	names := make([]string, 0, len(reg))
	for name := range reg {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		for _, parameter := range reg[name].Parameters {
			add(name, parameter)
		}
	}

	entries := make([]configListEntry, 0, len(sources))
	globalDefaults := globalConfig.defaultValues()
	for _, src := range sources {
		path := src.parameter
		if src.module != globalModuleName {
			path = src.module + "." + src.parameter
		} else {
			path = globalModuleName + "." + src.parameter
		}
		target, err := resolveConfigTarget(path, reg)
		if err != nil {
			// A parameter that cannot be addressed is a manifest bug, and
			// listing it without a path would be advertising a dead end.
			return nil, fmt.Errorf("module %s parameter %s is not addressable: %w", src.module, src.parameter, err)
		}
		entry := configListEntry{
			Path:      target.Display,
			Module:    target.Module,
			Parameter: target.Parameter,
			EnvKey:    parameterEnvKey(src.module, src.parameter, reg),
			Policy:    policyForTarget(target, reg),
		}
		entry.Default, entry.HasDefault, entry.DefaultSource = parameterDefaultMetadata(
			src.module, src.parameter, reg, globalDefaults,
		)
		if len(valueViews) > 0 {
			view := valueViews[0]
			if view.Present[entry.EnvKey] {
				entry.Value, entry.Set = view.Values[entry.EnvKey], true
				entry.ValueSensitive = view.Sensitive[entry.EnvKey]
			} else if value, ok := settings[strings.Join(target.YAMLPath, ".")]; ok && value == "" {
				// BaseEnv intentionally omits empty global fields. Settings is used
				// only to preserve that explicit-empty presence bit; non-empty
				// effective values always come from the canonical env-key view.
				entry.Value, entry.Set = "", true
			}
		} else if value, ok := settings[strings.Join(target.YAMLPath, ".")]; ok {
			// Compatibility for inventory-focused unit callers that do not have
			// a loaded config. Runtime reporting always supplies a value view.
			entry.Value, entry.Set = value, true
		}
		entries = append(entries, entry)
	}
	sort.Slice(entries, func(i, j int) bool {
		// Global parameters first: they are the ones a new deployment must
		// decide, and a module parameter only matters once its module is enabled.
		iGlobal := entries[i].Module == globalModuleName
		jGlobal := entries[j].Module == globalModuleName
		if iGlobal != jGlobal {
			return iGlobal
		}
		return entries[i].Path < entries[j].Path
	})
	return entries, nil
}

// parameterEnvKey is the environment key a parameter ends up as, which is the
// name it appears under in a rendered .env and the name a compose file reads.
func parameterEnvKey(module, parameter string, reg map[string]Module) string {
	if module == globalModuleName {
		// A typed field of config.Global carries its own binding, which is not
		// recoverable from the name: virtual_domain reaches containers as
		// ANAS_VIRTUAL_DOMAIN, and printing VIRTUAL_DOMAIN would name a key
		// nothing sets and nothing reads.
		if key := config.GlobalEnvKey(parameter); key != "" {
			return key
		}
		return paramEnvKey(globalScope, "", parameter)
	}
	mod := reg[module]
	return moduleParamEnvKey(module, mod.EnvPrefix, mod.Exports, parameter)
}

// reportConfigList prints the inventory. Values of parameters their owner marks
// sensitive are reported as set or unset and never printed: an operator running
// an inventory command is asking what exists, and `config secret get` already
// exists for the case where they mean to read a credential.
// A scope of "" lists everything; otherwise it is `global` or a module name. The
// argument exists because the error for a misspelled parameter points the
// reader at this command, and sending them to a 130-line listing to find one
// module's parameters would be answering a different question than they asked.
func reportConfigList(cfgPath string, reg map[string]Module, scope string, jsonMode bool, bases ...string) error {
	settings := map[string]string{}
	valueView := configParameterValueView{
		Values: map[string]string{}, Present: map[string]bool{}, Sensitive: map[string]bool{},
	}
	if cfgPath != "" && exists(cfgPath) {
		loaded, err := config.Load(cfgPath)
		if err != nil {
			return preconditionErrorf("config_invalid", "%s", err.Error())
		}
		for key, value := range configBaseEnv(loaded, reg) {
			key = config.EnvKey(key)
			valueView.Values[key] = value
			valueView.Present[key] = true
		}
		for key := range loaded.Secrets {
			valueView.Sensitive[config.EnvKey(key)] = true
		}
		markSensitiveValueAliases(valueView.Values, valueView.Sensitive)
		settings, err = config.Settings(cfgPath)
		if err != nil {
			return preconditionErrorf("config_invalid", "%s", err.Error())
		}
		if len(bases) > 0 && bases[0] != "" {
			store, err := loadSecretStore(bases[0])
			if err != nil {
				return preconditionErrorf("secrets_unreadable", "%s", err.Error())
			}
			// Every Secret Store kind is a plaintext provenance source even though
			// only lifecycle-managed records participate in caller-input presence.
			// A generated/local-admin value copied into an ordinary config alias
			// must therefore remain hidden from both list projections.
			storeValues := map[string]bool{}
			for _, value := range store.values {
				if value != "" {
					storeValues[value] = true
				}
			}
			for key, value := range valueView.Values {
				if value != "" && storeValues[value] {
					valueView.Sensitive[key] = true
				}
			}
			lifecycleValues := store.lifecycleManagedValues()
			for key, value := range lifecycleValues {
				key = config.EnvKey(key)
				valueView.Present[key] = true
				valueView.Sensitive[key] = true
				valueView.Values[key] = value
			}
			markSensitiveValueAliases(valueView.Values, valueView.Sensitive)
			for key := range lifecycleValues {
				delete(valueView.Values, config.EnvKey(key))
			}
		}
	}
	entries, err := collectConfigParameters(reg, settings, valueView)
	if err != nil {
		return failuref("registry_invalid", "%s", err.Error())
	}
	if scope != "" {
		filtered := entries[:0:0]
		for _, entry := range entries {
			if entry.Module == scope {
				filtered = append(filtered, entry)
			}
		}
		entries = filtered
	}

	if jsonMode {
		documents := make([]map[string]any, 0, len(entries))
		for _, entry := range entries {
			document := configTargetDocument(configTarget{
				Display: entry.Path, Module: entry.Module, Parameter: entry.Parameter,
			}, entry.Policy)
			document["env_key"] = entry.EnvKey
			defaultValue := entry.Default
			if entry.Policy.Sensitive {
				defaultValue = ""
			}
			addConfigParameterMetadata(document, configTarget{
				Display: entry.Path, Module: entry.Module, Parameter: entry.Parameter,
			}, reg, defaultValue, entry.HasDefault, entry.DefaultSource)
			document["set"] = entry.Set
			if entry.Set && !entry.Policy.Sensitive && !entry.ValueSensitive {
				document["value"] = entry.Value
			}
			documents = append(documents, document)
		}
		return emitOK(map[string]any{"config": cfgPath, "parameters": documents})
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "PATH\tENV\tDEFAULT\tVALUE\tEFFECT")
	for _, entry := range entries {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n",
			entry.Path, entry.EnvKey,
			configDefaultDisplay(entry.Default, entry.HasDefault, entry.Policy.Sensitive),
			configListValue(entry), entry.Policy.Effect)
	}
	return w.Flush()
}

func parameterRequired(module, parameter string, reg map[string]Module) bool {
	return parameterInputRequired(module, parameter, reg)
}

// parameterInputRequired is the external input contract. The legacy JSON
// field `required` remains an alias of this value, while MustResolve describes
// the later invariant after literal, host, inherited, generated and runtime
// defaults have had a chance to provide a value.
func parameterInputRequired(module, parameter string, reg map[string]Module) bool {
	key := parameterEnvKey(module, parameter, reg)
	if module == globalModuleName {
		return contains(globalConfig.InputRequired, key)
	}
	mod, ok := reg[module]
	return ok && contains(mod.InputRequired, key)
}

func parameterMustResolve(module, parameter string, reg map[string]Module) bool {
	key := parameterEnvKey(module, parameter, reg)
	if module == globalModuleName {
		return contains(globalConfig.finalRequirements(), key)
	}
	mod, ok := reg[module]
	return ok && contains(mod.finalRequirements(), key)
}

// parameterDefaultMetadata keeps absence distinct from an explicit empty
// literal and from a value supplied later by the host or runner. `default`
// remains the v1 string projection; has_default says whether a literal exists,
// while default_source says where an omitted input is resolved.
func parameterDefaultMetadata(module, parameter string, reg map[string]Module, globalDefaults map[string]string) (string, bool, string) {
	key := parameterEnvKey(module, parameter, reg)
	spec := parameterType(module, parameter, reg)
	if module == globalModuleName {
		if value, ok := globalConfig.Defaults[key]; ok {
			return value, true, "static"
		}
		if spec.DefaultSource != "" {
			return globalDefaults[key], false, string(spec.DefaultSource)
		}
		return "", false, "none"
	}
	if mod, ok := reg[module]; ok {
		if value, exists := mod.Defaults[key]; exists {
			return value, true, "static"
		}
	}
	if spec.DefaultSource != "" {
		return "", false, string(spec.DefaultSource)
	}
	return "", false, "none"
}

func addConfigParameterMetadata(document map[string]any, target configTarget, reg map[string]Module, defaultValue string, hasDefault bool, defaultSource string) {
	spec := targetParamType(target, reg)
	kind, values := paramTypeDocument(spec)
	document["type"] = kind
	if len(values) > 0 {
		document["allowed_values"] = values
	}
	if constraints := paramConstraintsDocument(spec); len(constraints) > 0 {
		document["constraints"] = constraints
	}
	inputRequired := parameterInputRequired(target.Module, target.Parameter, reg)
	document["required"] = inputRequired // v1 compatibility alias
	document["input_required"] = inputRequired
	document["must_resolve"] = parameterMustResolve(target.Module, target.Parameter, reg)
	document["default"] = defaultValue
	document["has_default"] = hasDefault
	document["default_source"] = defaultSource
}

func paramConstraintsDocument(spec ParamType) map[string]any {
	document := map[string]any{}
	constraints := spec.Constraints
	if constraints.Minimum != nil {
		document["minimum"] = *constraints.Minimum
	}
	if constraints.Maximum != nil {
		document["maximum"] = *constraints.Maximum
	}
	if constraints.MinLength != nil {
		document["min_length"] = *constraints.MinLength
	}
	if constraints.MaxLength != nil {
		document["max_length"] = *constraints.MaxLength
	}
	if constraints.Pattern != "" {
		document["pattern"] = constraints.Pattern
	}
	if constraints.Format != "" {
		document["format"] = constraints.Format
	}
	return document
}

func configListValue(entry configListEntry) string {
	if entry.Policy.Sensitive || entry.ValueSensitive {
		if entry.Set {
			return "<set>"
		}
		return "<unset>"
	}
	if !entry.Set {
		return "-"
	}
	return entry.Value
}

func placeholder(value string) string {
	if strings.TrimSpace(value) == "" {
		return "-"
	}
	return value
}

// configDefaultDisplay preserves the distinction between an absent default
// and a literal empty-string default in human output. JSON already carries
// has_default, but rendering both states as "-" made the text contract lie.
func configDefaultDisplay(value string, hasDefault, sensitive bool) string {
	if sensitive {
		return "-"
	}
	if strings.TrimSpace(value) == "" {
		if hasDefault {
			return fmt.Sprintf("%q", value)
		}
		return "-"
	}
	return value
}
