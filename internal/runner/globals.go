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
	Required []string
	Defaults map[string]string
	Changes  map[string]ChangePolicy
	// Parameters is every name this schema declares, in config spelling rather
	// than as env keys. Both are needed and neither derives from the other:
	// paramEnvKey is not injective (timezone and tz both give TZ), so an
	// inventory cannot be recovered by reversing the env keys.
	Parameters []string
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
	changes := map[string]ChangePolicy{}
	for key, policy := range doc.Config.Changes {
		if !validChangeEffect(policy.Effect) {
			return globalSchema{}, fmt.Errorf("config.changes.%s has invalid effect %q", key, policy.Effect)
		}
		changes[strings.ToLower(strings.TrimSpace(key))] = ChangePolicy{
			Effect: policy.Effect, Apply: policy.Apply,
			Description: policy.Description, Sensitive: policy.Sensitive,
		}
	}
	return globalSchema{
		Required:   normalizeRequired(globalScope, "", nil, doc.Config.Required),
		Defaults:   normalizeDefaults(globalScope, "", nil, doc.Config.Defaults),
		Changes:    changes,
		Parameters: declaredParameters(doc.Config),
	}, nil
}

// declaredParameters is every parameter a config block names, by the spelling
// it was written with, gathered from all three lists rather than from defaults
// alone. default_service_root_password is why: it appears only under `changes`,
// so an inventory built from defaults would omit the one parameter operators
// most need to be told about.
func declaredParameters(cfg manifestConfig) []string {
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
	for _, name := range cfg.Required {
		add(name)
	}
	for name := range cfg.Defaults {
		add(name)
	}
	for name := range cfg.Changes {
		add(name)
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
