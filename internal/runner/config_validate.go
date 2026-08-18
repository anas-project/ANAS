package runner

import (
	"fmt"
	"sort"
	"strings"

	"github.com/anas-project/ANAS/internal/config"
)

// Rejecting parameters nothing declares.
//
// `anas config set traefik.tls_min_version quiet` used to succeed. It wrote
// modules.traefik.config.tls_min_version, rendered TRAEFIK_TLS_MIN_VERSION, and
// nothing ever read it. The command reported success, the value was in the
// config file where the operator could see it, and the setting simply had no
// effect -- a failure with no symptom other than the thing not working.
//
// Named paths and raw env keys that map to a declared parameter are checked.
// `env.<KEY>` stays deliberately permissive only for values no manifest
// declares: that preserves the escape hatch without letting a second spelling
// bypass a type the owner explicitly declared.

// declaredParametersFor returns the parameters a module accepts by name, and
// whether the module is one whose parameters are known at all.
func declaredParametersFor(module string, reg map[string]Module) ([]string, bool) {
	if module == globalModuleName {
		names := append([]string{}, globalConfig.Parameters...)
		// config.Global's typed fields are settable whether or not the schema
		// gives them a default or a policy; virtual_domain has neither.
		names = append(names, config.GlobalParameters()...)
		return names, true
	}
	mod, ok := reg[module]
	if !ok {
		return nil, false
	}
	return mod.Parameters, true
}

// validateParameter reports an error naming the closest declared parameter when
// the one given is not declared.
func validateParameter(module, parameter string, reg map[string]Module) error {
	declared, known := declaredParametersFor(module, reg)
	if !known {
		return nil
	}
	// A module that declares nothing has nothing to check against. Treating that
	// as "everything is wrong" would make an undeclared-but-working module
	// unconfigurable, which is a worse failure than the one this prevents.
	if len(declared) == 0 {
		return nil
	}
	parameter = strings.ToLower(strings.TrimSpace(parameter))
	for _, name := range declared {
		if name == parameter {
			return nil
		}
	}
	message := fmt.Sprintf("%s has no parameter %q", module, parameter)
	if suggestion := closestParameter(parameter, declared); suggestion != "" {
		message += fmt.Sprintf("; did you mean %s.%s?", module, suggestion)
	}
	return fmt.Errorf("%s\nrun `anas config list %s` to see what it accepts, "+
		"or set env.%s to write an undeclared value", message, module, config.EnvKey(parameter))
}

// validateConfiguredParameters applies the same declaration and type checks to
// a whole imported/loaded config that `config set` applies to one path. Without
// this, hand-written YAML can still create a plausible-looking environment key
// that no runtime component reads, even though the CLI refuses the same path.
func validateConfiguredParameters(cfg *config.File, reg map[string]Module) error {
	if cfg == nil {
		return nil
	}
	for module, selected := range cfg.Modules.Values {
		for parameter, raw := range selected.Config {
			if err := validateParameter(module, parameter, reg); err != nil {
				return fmt.Errorf("modules.%s.config.%s: %w", module, parameter, err)
			}
			path := fmt.Sprintf("modules.%s.config.%s", module, parameter)
			if err := validateParameterValue(module, parameter, config.Scalar(raw), reg); err != nil {
				return parameterValidationError(path, module, parameter, err, reg)
			}
		}
	}
	for key, raw := range cfg.Env {
		key = config.EnvKey(key)
		module, parameter, err := policyOwnerForEnv(key, reg)
		if err != nil {
			return fmt.Errorf("env.%s: %w", key, err)
		}
		path := "env." + key
		if err := validateParameterValue(module, parameter, config.Scalar(raw), reg); err != nil {
			return parameterValidationError(path, module, parameter, err, reg)
		}
	}
	// `secrets:` is an alternative spelling of a runtime environment input, not
	// an escape hatch around the owning Module's schema. Values remain redacted
	// by parameterValidationError whenever the declaration marks them sensitive.
	for key, raw := range cfg.Secrets {
		key = config.EnvKey(key)
		module, parameter, err := policyOwnerForEnv(key, reg)
		if err != nil {
			return fmt.Errorf("secrets.%s: %w", key, err)
		}
		path := "secrets." + key
		if err := validateParameterValue(module, parameter, config.Scalar(raw), reg); err != nil {
			spec := parameterType(module, parameter, reg)
			if spec.Declared() {
				return fmt.Errorf("%s does not satisfy its declared %s type or constraints", path, spec.Kind)
			}
			return fmt.Errorf("%s does not satisfy its declared type or constraints", path)
		}
	}
	return nil
}

// parameterValidationError never includes the rejected value when a manifest
// marks the parameter sensitive. This applies to every change effect, not only
// credential_rotate: lifecycle policy and secrecy are independent axes.
func parameterValidationError(path, module, parameter string, err error, reg map[string]Module) error {
	policy := policyForTarget(configTarget{Display: path, Module: module, Parameter: parameter}, reg)
	if policy.Sensitive {
		return fmt.Errorf("%s does not satisfy its declared type or constraints", path)
	}
	return fmt.Errorf("%s: %w", path, err)
}

func normalizeConfigInputForPolicy(target configTarget, value string, policy ChangePolicy, reg map[string]Module) (string, error) {
	normalized, err := normalizeConfigInputValue(target, value, reg)
	if err != nil && policy.Sensitive {
		return "", fmt.Errorf("%s does not satisfy its declared type or constraints", target.Display)
	}
	return normalized, err
}

// validateLoadedConfigSchema is the common whole-config boundary used before
// a plan is reported. Without it, plan could claim that an old managed config
// was applicable after a Module tightened its schema, only for apply to reject
// it later.
func validateLoadedConfigSchema(cfg *config.File, reg map[string]Module, lifecycleInputs map[string]string) error {
	if err := validateConfiguredParameters(cfg, reg); err != nil {
		return err
	}
	values := cfg.BaseEnv()
	// Lifecycle-managed credentials deliberately do not live in config.yml.
	// Merge their private validation view without mutating the loaded config or
	// projecting values into plan output. The caller is responsible for passing
	// only Secret Store records whose kind is lifecycle_managed.
	for key, value := range lifecycleInputs {
		if strings.TrimSpace(value) != "" {
			key = config.EnvKey(key)
			module, parameter, err := policyOwnerForEnv(key, reg)
			if err != nil {
				return fmt.Errorf("lifecycle secret %s: %w", key, err)
			}
			normalized, err := normalizeImportedSensitiveParameter("lifecycle secret "+key, module, parameter, value, reg)
			if err != nil {
				return err
			}
			values[key] = normalized
		}
	}
	if err := normalizeConfiguredParameterEnv(values, reg); err != nil {
		return err
	}
	order := make([]string, 0, len(cfg.Modules.Order))
	for _, name := range cfg.Modules.Order {
		selected := cfg.Modules.Values[name]
		if selected.Enabled == nil || *selected.Enabled {
			order = append(order, name)
		}
	}
	return validateInputRequiredEnv(values, order, reg)
}

// validateInputRequiredEnv checks the unconditional input contract only after
// the generic resolver has selected the actual enabled Module set and applied
// alternative structured inputs such as deployment-scoped bindings. This is
// deliberately manifest-driven: neither the CLI nor anasd needs a branch for
// any Module name.
func validateInputRequiredEnv(values map[string]string, order []string, reg map[string]Module) error {
	if err := requireKeys(values, globalConfig.InputRequired); err != nil {
		return fmt.Errorf("deployment config input: %w", err)
	}
	for _, name := range order {
		mod, ok := reg[name]
		if !ok {
			continue
		}
		if err := requireKeys(values, mod.InputRequired); err != nil {
			return fmt.Errorf("%s config input: %w", name, err)
		}
	}
	return nil
}

// closestParameter picks the nearest declared name, or "" when nothing is near
// enough to be worth suggesting. A wrong suggestion is worse than none: it
// sends the reader looking for a parameter that has nothing to do with what
// they meant.
func closestParameter(parameter string, declared []string) string {
	candidates := append([]string{}, declared...)
	sort.Strings(candidates)
	// Containment first. A renamed parameter usually keeps its old name inside
	// the new one -- domain became base_domain -- and edit distance ranks that
	// badly: five insertions scores worse than an unrelated word of the same
	// length, so `domain` would be answered with `email`.
	for _, name := range candidates {
		if name != parameter && (strings.Contains(name, parameter) || strings.Contains(parameter, name)) {
			return name
		}
	}
	best, bestDistance := "", 0
	limit := len(parameter)/2 + 1
	for _, name := range candidates {
		distance := editDistance(parameter, name)
		if distance > limit {
			continue
		}
		if best == "" || distance < bestDistance {
			best, bestDistance = name, distance
		}
	}
	return best
}

// editDistance is Levenshtein, iterative with one row of state. Parameter names
// are short and the set is small, so the simple version is the right one.
func editDistance(a, b string) int {
	previous := make([]int, len(b)+1)
	current := make([]int, len(b)+1)
	for j := range previous {
		previous[j] = j
	}
	for i := 1; i <= len(a); i++ {
		current[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			current[j] = min(previous[j]+1, min(current[j-1]+1, previous[j-1]+cost))
		}
		previous, current = current, previous
	}
	return previous[len(b)]
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// validateParameterValue checks a value against what its module declares it
// accepts. It runs when the value is set, which is the only moment the person
// who chose it is still present to be told.
//
// An undeclared parameter accepts anything, as everything did before types
// existed. That is deliberate: making a declaration mandatory would turn every
// module that has not been annotated yet into one that cannot be configured.
func validateParameterValue(module, parameter, value string, reg map[string]Module) error {
	_, err := normalizeParameterValue(module, parameter, value, reg)
	return err
}

// normalizeParameterValue returns the spelling runtime consumers must see.
// Validation alone is insufficient for bools and selectors: many hooks compare
// canonical environment strings, while existing configurations may contain
// harmless case or surrounding-space differences that older selectors accepted.
func normalizeParameterValue(module, parameter, value string, reg map[string]Module) (string, error) {
	spec := parameterType(module, parameter, reg)
	if !spec.Declared() {
		return value, nil
	}
	normalized, err := normalizeValueAgainstParamType(value, spec)
	if err != nil {
		return "", fmt.Errorf("%s.%s %w", module, parameter, err)
	}
	return normalized, nil
}

func parameterType(module, parameter string, reg map[string]Module) ParamType {
	parameter = strings.ToLower(strings.TrimSpace(parameter))
	if module == globalModuleName {
		return globalConfig.Types[parameter]
	}
	if declared, ok := reg[module]; ok {
		return declared.Types[parameter]
	}
	return ParamType{}
}

func validateValueAgainstParamType(value string, spec ParamType) error {
	_, err := normalizeValueAgainstParamType(value, spec)
	return err
}

func normalizeValueAgainstParamType(value string, spec ParamType) (string, error) {
	return spec.Normalize(value)
}

// normalizeConfiguredParameterEnv canonicalizes every environment value whose
// owner declared a type. Unknown raw env keys remain untouched as the explicit
// compatibility escape hatch.
func normalizeConfiguredParameterEnv(values map[string]string, reg map[string]Module) error {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		value := values[key]
		module, parameter, err := policyOwnerForEnv(key, reg)
		if err != nil {
			return fmt.Errorf("env.%s: %w", key, err)
		}
		normalized, err := normalizeParameterValue(module, parameter, value, reg)
		if err != nil {
			return parameterValidationError("env."+key, module, parameter, err, reg)
		}
		values[key] = normalized
	}
	return nil
}
