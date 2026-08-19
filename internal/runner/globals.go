package runner

import (
	"bytes"
	_ "embed"
	"fmt"
	"sort"
	"strings"

	"github.com/anas-project/ANAS/internal/localization"
	"gopkg.in/yaml.v3"
)

// globalScope is the module identity of a parameter that belongs to the
// deployment rather than to any module. It is the empty string because that is
// already what the environment scoping rules mean by "global": a key with this
// owner is visible to every module regardless of its dependency closure.
//
// There used to be a second spelling of the same idea -- a module named "core"
// that owned the shared parameters -- and every rule that cared about global
// ownership had to accept both. Hence the string is defined once, here, and
// the two are the same value.
const globalScope = ""

// runnerScope owns derived cross-module topology. Unlike globalScope, these
// values are delivered only to modules that explicitly list them in
// config.consumes, so adding an unrelated module cannot perturb every
// rendered .env and force an otherwise unnecessary Compose recreation.
const runnerScope = "runner"

// globalModuleName is how the same scope is spelled on the command line and in
// the JSON contract, where an empty owner would read as a missing field. It
// used to be reported as "core", the module these parameters were kept in, even
// on paths that had already rewritten themselves to `global.<parameter>`.
const globalModuleName = "global"

//go:embed globals.yml
var globalsYAML []byte

var globalConfig = mustLoadGlobals()

// globalSchema is the declaration side of the deployment's own parameters,
// shaped like a module's config block so the same normalization applies.
type globalSchema struct {
	InputRequired []string
	Required      []string
	MustResolve   []string
	Defaults      map[string]string
	Changes       map[string]ChangePolicy
	Types         map[string]ParamType
	// Parameters is every name this schema declares, in config spelling rather
	// than as env keys. Both are needed and neither derives from the other:
	// paramEnvKey is not injective (timezone and tz both give TZ), so an
	// inventory cannot be recovered by reversing the env keys.
	Parameters []string
}

// finalRequirements is the global equivalent of Module.finalRequirements.
// Globals have no calculate Hook, so all three declaration classes are checked
// together after host/default materialization.
func (s globalSchema) finalRequirements() []string {
	out := append([]string{}, s.InputRequired...)
	out = append(out, s.Required...)
	out = append(out, s.MustResolve...)
	return uniqueStrings(out)
}

func mustLoadGlobals() globalSchema {
	schema, err := loadGlobalSchema(globalsYAML)
	if err != nil {
		// The file is embedded, so this can only be a developer error, and
		// TestGlobalSchemaParses catches it before a build ever ships.
		panic("internal/runner/globals.yml: " + err.Error())
	}
	return schema
}

func loadGlobalSchema(b []byte) (globalSchema, error) {
	var doc struct {
		APIVersion string         `yaml:"api_version"`
		Kind       string         `yaml:"kind"`
		Config     manifestConfig `yaml:"config"`
	}
	dec := yaml.NewDecoder(bytes.NewReader(b))
	dec.KnownFields(true)
	if err := dec.Decode(&doc); err != nil {
		return globalSchema{}, err
	}
	if doc.Kind != "GlobalConfig" {
		return globalSchema{}, fmt.Errorf("unexpected kind %q", doc.Kind)
	}
	if doc.Config.EnvPrefix != "" {
		return globalSchema{}, fmt.Errorf("global parameters have no env prefix")
	}
	types, err := normalizeParamTypes(globalModuleName, doc.Config.Types)
	if err != nil {
		return globalSchema{}, err
	}
	defaults, err := normalizeDefaultsWithTypes(globalModuleName, globalScope, "", nil, doc.Config.Defaults, types)
	if err != nil {
		return globalSchema{}, err
	}
	changes, err := normalizeChangePolicies(globalModuleName, doc.Config.Changes)
	if err != nil {
		return globalSchema{}, err
	}
	if err := validateInputDefaultSemantics(globalModuleName, doc.Config, types); err != nil {
		return globalSchema{}, err
	}
	requirements, err := normalizeConfigRequirements(globalModuleName, globalScope, "", nil, doc.Config)
	if err != nil {
		return globalSchema{}, err
	}
	parameters := declaredParameters(globalModuleName, doc.Config)
	if err := validateDeclaredParameterRuntimeKeys(globalModuleName, globalScope, "", nil, parameters); err != nil {
		return globalSchema{}, err
	}
	return globalSchema{
		InputRequired: requirements.InputRequired,
		Required:      requirements.Required,
		MustResolve:   requirements.MustResolve,
		Defaults:      defaults,
		Changes:       changes,
		Types:         types,
		Parameters:    parameters,
	}, nil
}

// declaredParameters is every addressable parameter a config block names, by
// the spelling it was written with, gathered from input_required, legacy
// required, must_resolve, defaults, types, and changes.
//
// A legacy requirement may instead be a bare runtime environment key. When it
// has neither the module prefix nor an explicit parameter declaration that
// maps back to it, it remains only a runtime requirement. Treating
// EXTERNAL_TOKEN as a parameter named external_token would advertise
// module.external_token while that path actually writes MODULE_EXTERNAL_TOKEN.
// The compatible address for such a value is the existing env.EXTERNAL_TOKEN
// escape hatch, not a fabricated module parameter.
func declaredParameters(owner string, cfg manifestConfig) []string {
	seen := map[string]bool{}
	out := []string{}
	add := func(name string) {
		name = strings.ToLower(strings.TrimSpace(name))
		if name == "" || seen[name] {
			return
		}
		seen[name] = true
		out = append(out, name)
	}
	// Config-name declarations establish the inverse mapping used when a legacy
	// requirement is spelled as an environment key (DEMO_TOKEN). Without this,
	// the inventory would expose a second fictitious parameter `demo_token`.
	for name := range cfg.Defaults {
		add(name)
	}
	for name := range cfg.Types {
		add(name)
	}
	for name := range cfg.Changes {
		add(name)
	}
	prefix := strings.TrimSpace(cfg.EnvPrefix)
	if prefix == "" {
		prefix = defaultEnvPrefix(owner)
	}
	exports := make([]string, 0, len(cfg.Exports))
	for _, export := range cfg.Exports {
		exports = append(exports, strings.TrimSpace(export))
	}
	runtimeKey := func(name string) string {
		name = strings.TrimSpace(name)
		if !isEnvKey(name) {
			name = strings.ToLower(name)
		}
		if owner == globalModuleName {
			return parameterEnvKey(globalModuleName, name, nil)
		}
		return moduleParamEnvKey(owner, prefix, exports, name)
	}
	byRuntimeKey := map[string]string{}
	for _, parameter := range out {
		byRuntimeKey[runtimeKey(parameter)] = parameter
	}
	addRequirement := func(raw string) {
		if parameter, ok := byRuntimeKey[runtimeKey(raw)]; ok {
			add(parameter)
			return
		}
		trimmed := strings.TrimSpace(raw)
		if isEnvKey(trimmed) {
			// An explicitly exported bare key establishes module ownership and
			// therefore a reversible parameter address even without another
			// declaration source.
			if owner != globalModuleName && contains(exports, trimmed) {
				add(trimmed)
				return
			}
			if owner == globalModuleName {
				return
			}
			prefixKey := strings.ToUpper(strings.ReplaceAll(prefix, "-", "_")) + "_"
			if !strings.HasPrefix(trimmed, prefixKey) {
				return
			}
			trimmed = strings.TrimPrefix(trimmed, prefixKey)
		}
		add(trimmed)
	}
	for _, name := range cfg.InputRequired {
		addRequirement(name)
	}
	for _, name := range cfg.Required {
		addRequirement(name)
	}
	for _, name := range cfg.MustResolve {
		addRequirement(name)
	}
	sort.Strings(out)
	return out
}

// policy returns the change policy for a global parameter, addressed either by
// its config name (timezone) or by its env key (TZ).
func (s globalSchema) policy(parameter string) (ChangePolicy, bool) {
	if policy, ok := s.Changes[strings.ToLower(strings.TrimSpace(parameter))]; ok {
		return policy, true
	}
	key := paramEnvKey(globalScope, "", parameter)
	for param := range s.Changes {
		if paramEnvKey(globalScope, "", param) == key {
			return s.Changes[param], true
		}
	}
	return ChangePolicy{}, false
}

// parameterFor returns the global parameter an env key belongs to, so the
// config CLI can report the policy for `env.TZ` as well as for
// `global.timezone`.
func (s globalSchema) parameterFor(key string) (string, bool) {
	for param := range s.Changes {
		if paramEnvKey(globalScope, "", param) == key {
			return param, true
		}
	}
	return "", false
}

// defaultValues resolves host-inherited defaults at operation time. Keeping
// the actual values out of globals.yml prevents a compiled-in locale from
// overriding the machine that owns the workspace.
func (s globalSchema) defaultValues() map[string]string {
	out := make(map[string]string, len(s.Defaults)+3)
	for key, value := range s.Defaults {
		out[key] = value
	}
	system := localization.CurrentSystemDefaults()
	out["TZ"] = system.Timezone
	out["DEFAULT_LANGUAGE"] = system.Language
	out["DEFAULT_LOCALE"] = system.Locale
	return out
}
