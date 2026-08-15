package runner

import (
	"fmt"
	"sort"
	"strconv"
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
// Only the named paths are checked. `env.<KEY>` stays deliberately permissive:
// it is the escape hatch for values no manifest declares, and the whole point
// of an escape hatch is that it does not ask permission. That gives an operator
// who genuinely needs an undeclared value somewhere to put it, while keeping
// the spelling of a declared one honest.

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
			if err := validateParameterValue(module, parameter, config.Scalar(raw), reg); err != nil {
				return fmt.Errorf("modules.%s.config.%s: %w", module, parameter, err)
			}
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
	declared, ok := reg[module]
	if !ok {
		return nil
	}
	spec := declared.Types[strings.ToLower(strings.TrimSpace(parameter))]
	if !spec.Declared() {
		return nil
	}
	trimmed := strings.TrimSpace(value)
	switch spec.Kind {
	case "bool":
		switch strings.ToLower(trimmed) {
		case "true", "false":
			return nil
		}
		return fmt.Errorf("%s.%s accepts true or false, not %q", module, parameter, value)
	case "int":
		if _, err := strconv.Atoi(trimmed); err != nil {
			return fmt.Errorf("%s.%s accepts a whole number, not %q", module, parameter, value)
		}
		return nil
	case "enum":
		for _, allowed := range spec.Enum {
			if trimmed == allowed {
				return nil
			}
		}
		// The permitted values are listed rather than merely counted: the reader
		// is choosing one, and a rejection that does not say what is available
		// sends them to the manifest to find out.
		return fmt.Errorf("%s.%s accepts one of %s, not %q",
			module, parameter, strings.Join(spec.Enum, ", "), value)
	}
	return nil
}
