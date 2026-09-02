package runner

import (
	"context"
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
	if key, bareRequirement := unboundRequirementEnvKeyForParameter(module, parameter, reg); bareRequirement {
		return fmt.Errorf("%s.%s is a bare runtime requirement, not a Module parameter; set env.%s instead", module, strings.ToLower(strings.TrimSpace(parameter)), key)
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

// unboundRequirementEnvKeyForParameter identifies the legacy manifest form in
// which a requirement names a bare environment key directly. The module path
// for the same lower-case words would add the module prefix and write a
// different, unused key, so it must not be offered as an alias. Other
// undeclared parameters on a legacy Module remain compatible.
func unboundRequirementEnvKeyForParameter(module, parameter string, reg map[string]Module) (string, bool) {
	mod, ok := reg[module]
	if !ok {
		return "", false
	}
	parameter = strings.ToLower(strings.TrimSpace(parameter))
	if parameter == "" || contains(mod.Parameters, parameter) {
		return "", false
	}
	bare := config.EnvKey(parameter)
	if moduleParamEnvKey(module, mod.EnvPrefix, mod.Exports, parameter) == bare {
		return "", false
	}
	for _, key := range mod.finalRequirements() {
		if key == bare {
			return bare, true
		}
	}
	return "", false
}

// validateConfiguredParameters applies the same declaration and type checks to
// a whole imported/loaded config that `config set` applies to one path. Without
// this, hand-written YAML can still create a plausible-looking environment key
// that no runtime component reads, even though the CLI refuses the same path.
func validateConfiguredParameters(cfg *config.File, reg map[string]Module) error {
	return validateConfiguredParameterSchema(cfg, reg)
}

// validateConfiguredParameterDeclarations checks address ownership without
// formatting any value. Production whole-config boundaries run this first and
// then validate the canonical runtime view with source provenance attached;
// validating values here would expose an equal-value alias before the secret
// source has been merged into that view.
func validateConfiguredParameterDeclarations(cfg *config.File, reg map[string]Module) error {
	if cfg == nil {
		return nil
	}
	if err := validateConfiguredRuntimeKeySyntax(cfg, reg); err != nil {
		return err
	}
	for module, selected := range cfg.Modules.Values {
		if strings.TrimSpace(selected.Identity.LoginProtocol) != "" {
			if _, ok := moduleIdentityLoginParameter(module, reg); !ok {
				return fmt.Errorf("modules.%s.identity.login_protocol is not declared by the Module identity contract", module)
			}
		}
		for parameter := range selected.Config {
			if err := validateParameter(module, parameter, reg); err != nil {
				return fmt.Errorf("modules.%s.config.%s: %w", module, parameter, err)
			}
		}
	}
	for key := range cfg.Env {
		key = config.EnvKey(key)
		if isRunnerOwnedRuntimeKey(key, reg) {
			return fmt.Errorf("env.%s is runner-owned and cannot be supplied by config", key)
		}
		if _, _, err := policyOwnerForEnv(key, reg); err != nil {
			return fmt.Errorf("env.%s: %w", key, err)
		}
	}
	for key := range cfg.Secrets {
		key = config.EnvKey(key)
		if isRunnerOwnedRuntimeKey(key, reg) {
			return fmt.Errorf("secrets.%s is runner-owned and cannot be supplied by config", key)
		}
		if _, _, err := policyOwnerForEnv(key, reg); err != nil {
			return fmt.Errorf("secrets.%s: %w", key, err)
		}
	}
	return nil
}

// validateConfiguredRuntimeKeySyntax closes the permissive raw/legacy address
// escape hatch at the actual dotenv boundary. Undeclared legacy parameters are
// still accepted, but the runtime key they produce must be representable as a
// single environment assignment and cannot contain newline, '=' or punctuation.
func validateConfiguredRuntimeKeySyntax(cfg *config.File, reg map[string]Module) error {
	validateRaw := func(section string, values map[string]any) error {
		for raw := range values {
			key := config.EnvKey(raw)
			if !envKeyPattern.MatchString(key) {
				return fmt.Errorf("%s key %q is not a valid environment key", section, key)
			}
		}
		return nil
	}
	if err := validateRaw("env", cfg.Env); err != nil {
		return err
	}
	if err := validateRaw("secrets", cfg.Secrets); err != nil {
		return err
	}
	for module, selected := range cfg.Modules.Values {
		mod, known := reg[module]
		if !known {
			continue
		}
		for parameter := range selected.Config {
			key := moduleParamEnvKey(module, mod.EnvPrefix, mod.Exports, parameter)
			if !envKeyPattern.MatchString(key) {
				return fmt.Errorf("modules.%s.config parameter %q resolves to invalid environment key %q", module, parameter, key)
			}
		}
	}
	return nil
}

// validateConfiguredParameterSchema validates a complete desired-state file
// without running resolution or enforcing required inputs. Source provenance
// is attached before any value is formatted, so an ordinary `secrets:` value
// copied into another typed address cannot be echoed by a schema error.
func validateConfiguredParameterSchema(cfg *config.File, reg map[string]Module, privateTaintSources ...map[string]string) error {
	if err := validateConfiguredParameterDeclarations(cfg, reg); err != nil {
		return err
	}
	values := configBaseEnv(cfg, reg)
	sourceSensitive := map[string]bool{}
	for key := range cfg.Secrets {
		sourceSensitive[config.EnvKey(key)] = true
	}
	markSensitiveValueAliases(values, sourceSensitive)
	markSensitiveValueAliasesFromSources(values, sourceSensitive, privateTaintSources...)
	return normalizeConfiguredParameterEnvWithSensitive(values, reg, sourceSensitive)
}

// markSensitiveValueAliasesFromSources applies confidentiality provenance from
// values that intentionally are not part of the configuration input view. All
// Secret Store record kinds participate here: generated Hook material and local
// administrator credentials may not satisfy input_required, but copying their
// plaintext into a typed config address still must not make a schema error echo
// it. Empty values do not taint ordinary unset parameters.
func markSensitiveValueAliasesFromSources(values map[string]string, sensitive map[string]bool, sources ...map[string]string) {
	privateValues := map[string]bool{}
	for _, source := range sources {
		for _, value := range source {
			addSensitiveValueForms(privateValues, value)
		}
	}
	if len(privateValues) == 0 {
		return
	}
	for key, value := range values {
		if matchesSensitiveValue(privateValues, value) {
			sensitive[config.EnvKey(key)] = true
		}
	}
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

// validateConfigRuntimeKeyCollisions rejects two explicit YAML addresses that
// feed the same runtime environment key. Without this boundary, config set can
// appear to succeed while a pre-existing raw env/secret spelling silently wins
// (or is silently overwritten) according to flattening order.
func validateConfigRuntimeKeyCollisions(path string, reg map[string]Module) error {
	loaded, err := config.Load(path)
	if err != nil {
		return err
	}
	if err := validateConfiguredRuntimeKeySyntax(loaded, reg); err != nil {
		return err
	}
	settings, err := config.Settings(path)
	if err != nil {
		return err
	}
	paths := make([]string, 0, len(settings))
	for settingPath := range settings {
		paths = append(paths, settingPath)
	}
	sort.Strings(paths)
	seen := map[string]string{}
	for _, settingPath := range paths {
		parts := strings.Split(settingPath, ".")
		keys := []string{}
		switch {
		case len(parts) == 2 && (parts[0] == "env" || parts[0] == "secrets"):
			key := config.EnvKey(parts[1])
			if isRunnerOwnedRuntimeKey(key, reg) {
				return fmt.Errorf("%s is runner-owned and cannot be supplied by config", settingPath)
			}
			keys = []string{key}
		case len(parts) == 2 && parts[0] == "global":
			keys = []string{parameterEnvKey(globalModuleName, strings.ToLower(strings.TrimSpace(parts[1])), reg)}
		case len(parts) == 4 && parts[0] == "modules" && parts[2] == "config":
			module := strings.ToLower(strings.TrimSpace(parts[1]))
			mod, ok := reg[module]
			if !ok {
				continue
			}
			keys = []string{moduleParamEnvKey(module, mod.EnvPrefix, mod.Exports, strings.ToLower(strings.TrimSpace(parts[3])))}
		case len(parts) == 4 && parts[0] == "modules" && parts[2] == "identity" && parts[3] == "login_protocol":
			if key, ok := moduleIdentityLoginRuntimeKey(strings.ToLower(strings.TrimSpace(parts[1])), reg); ok {
				keys = []string{key}
			}
		case settingPath == "administration.bootstrap.username":
			keys = bootstrapUsernameRuntimeKeys(reg)
		default:
			continue
		}
		for _, key := range uniqueStrings(keys) {
			if previous, duplicate := seen[key]; duplicate {
				return fmt.Errorf("%s and %s both resolve to runtime key %s", previous, settingPath, key)
			}
			seen[key] = settingPath
		}
	}
	return nil
}

// validateLoadedConfigSchema is the common whole-config boundary used before
// a plan is reported. Without it, plan could claim that an old managed config
// was applicable after a Module tightened its schema, only for apply to reject
// it later.
func validateLoadedConfigSchema(cfg *config.File, reg map[string]Module, lifecycleInputs map[string]string, lock *moduleLock, privateTaintSources ...map[string]string) error {
	_, err := resolveLoadedConfigSchema(cfg, reg, lifecycleInputs, lock, privateTaintSources...)
	return err
}

// resolveLoadedConfigSchema retains the read-only validation app so callers
// that report a plan can expose Module-derived metadata without running the
// privileged calculate phase or rebuilding the effective topology a second
// time.
func resolveLoadedConfigSchema(cfg *config.File, reg map[string]Module, lifecycleInputs map[string]string, lock *moduleLock, privateTaintSources ...map[string]string) (*app, error) {
	if err := validateConfiguredParameterDeclarations(cfg, reg); err != nil {
		return nil, err
	}
	values := configBaseEnv(cfg, reg)
	sourceSensitive := map[string]bool{}
	for key := range cfg.Secrets {
		sourceSensitive[config.EnvKey(key)] = true
	}
	// Lifecycle-managed credentials deliberately do not live in config.yml.
	// Merge their private validation view without mutating the loaded config or
	// projecting values into plan output. The caller is responsible for passing
	// only Secret Store records whose kind is lifecycle_managed.
	for key, value := range lifecycleInputs {
		if strings.TrimSpace(value) != "" {
			key = config.EnvKey(key)
			module, parameter, err := policyOwnerForEnv(key, reg)
			if err != nil {
				return nil, fmt.Errorf("lifecycle secret %s: %w", key, err)
			}
			normalized, err := normalizeImportedSensitiveParameter("lifecycle secret "+key, module, parameter, value, reg)
			if err != nil {
				return nil, err
			}
			values[key] = normalized
			sourceSensitive[key] = true
		}
	}
	markSensitiveValueAliases(values, sourceSensitive)
	markSensitiveValueAliasesFromSources(values, sourceSensitive, privateTaintSources...)
	if err := normalizeConfiguredParameterEnvWithSensitive(values, reg, sourceSensitive); err != nil {
		return nil, err
	}
	filterSources := []map[string]string{lifecycleInputs}
	filterSources = append(filterSources, privateTaintSources...)
	return resolvedInputValidationApp(cfg, reg, values, lock, sourceSensitive, false, filterSources...)
}

// validateResolvedInputRequiredEnv asks the same resolver used by plan/apply
// for the effective module order. That includes transitive dependencies,
// alternative, contract and capability providers, and deployment-scoped
// Dynamic-DNS selection; validating only cfg.Modules.Order would silently skip
// caller inputs required by an auto-selected module.
//
// resolveOrder is read-only with respect to the workspace. It mutates only this
// private environment view to publish resolved bindings, and registry-only mode
// avoids a redundant filesystem admission check without running hooks or any
// lifecycle operation.
func validateResolvedInputRequiredEnv(cfg *config.File, reg map[string]Module, values map[string]string, lock *moduleLock, sourceSensitive map[string]bool, privateFilterValues ...map[string]string) error {
	return validateResolvedInputRequiredEnvContext(context.Background(), cfg, reg, values, lock, sourceSensitive, privateFilterValues...)
}

func validateResolvedInputRequiredEnvContext(ctx context.Context, cfg *config.File, reg map[string]Module, values map[string]string, lock *moduleLock, sourceSensitive map[string]bool, privateFilterValues ...map[string]string) error {
	_, err := resolvedInputValidationAppContext(ctx, cfg, reg, values, lock, sourceSensitive, true, privateFilterValues...)
	return err
}

func resolvedInputValidationApp(cfg *config.File, reg map[string]Module, values map[string]string, lock *moduleLock, sourceSensitive map[string]bool, allowLockExpansion bool, privateFilterValues ...map[string]string) (*app, error) {
	return resolvedInputValidationAppContext(context.Background(), cfg, reg, values, lock, sourceSensitive, allowLockExpansion, privateFilterValues...)
}

func resolvedInputValidationAppContext(ctx context.Context, cfg *config.File, reg map[string]Module, values map[string]string, lock *moduleLock, sourceSensitive map[string]bool, allowLockExpansion bool, privateFilterValues ...map[string]string) (*app, error) {
	_, owners := configBaseEnvWithRegistry(cfg, reg)
	filterStore := &secretStore{values: map[string]string{}}
	for _, source := range privateFilterValues {
		for key, value := range source {
			filterStore.values[config.EnvKey(key)] = value
		}
	}
	resolver := &app{
		cfg: cfg, reg: reg, env: values, envOwner: owners, lock: lock,
		commandContext:               ctx,
		secrets:                      filterStore,
		resolvedBindings:             map[string]map[string]string{},
		registryOnlyResolution:       true,
		allowUnresolvedInputBindings: true,
		runnerSensitive:              sourceSensitive,
	}
	order, err := resolver.resolveOrderWithInputValidation(cfg.Modules.Order)
	if err != nil {
		return nil, err
	}
	resolver.order = order
	resolver.applyModuleDefaults()
	pinned := trustedModuleValidationOrder(order, lock)
	if lock != nil && len(lock.Modules) > 0 {
		if allowLockExpansion {
			resolver.order = pinned
		} else {
			resolver.order = order
		}
		if err := validateLockedModuleBundles(resolver, lock); err != nil {
			return nil, err
		}
	}
	resolver.order = pinned
	if err := resolver.validateModules(); err != nil {
		return nil, err
	}
	resolver.order = order
	return resolver, nil
}

// resolvedValueForError preserves useful diagnostics for ordinary selectors
// while honoring source-based secrecy. A value loaded from config `secrets:`
// or the lifecycle Secret Store stays redacted even if a later manifest no
// longer marks that parameter sensitive.
func (a *app) resolvedValueForError(key, value string) string {
	if a.resolvedValueIsSensitive(key) {
		return "<redacted>"
	}
	return value
}

func (a *app) resolvedValueIsSensitive(key string) bool {
	return a.sensitiveEnvKeySet()[config.EnvKey(key)]
}

// rejectSourceSensitiveSelector keeps secret plaintext out of persistent locks
// and successful plan documents. Structural selectors (provider, interface,
// backend, DNS platform) are necessarily recorded as their canonical public
// identifier, so a value whose source promises secrecy cannot safely be used
// for that job. The caller must move the selector to ordinary configuration;
// actual credentials remain in config secrets or the lifecycle Secret Store.
func (a *app) rejectSourceSensitiveSelector(key, display string) error {
	key = config.EnvKey(key)
	if strings.TrimSpace(a.env[key]) == "" || !a.resolvedValueIsSensitive(key) {
		return nil
	}
	return fmt.Errorf("%s value <redacted> cannot come from secrets or the lifecycle Secret Store because it selects deployment structure; move it to ordinary configuration", display)
}

func (a *app) resolvedBindingValueForError(module, binding, value string) string {
	mod, ok := a.reg[module]
	if !ok {
		return value
	}
	base := strings.TrimSuffix(binding, ".interface")
	for _, dep := range mod.RequiresOne {
		if dep.Capability == base {
			return a.resolvedValueForError(paramEnvKey(module, mod.EnvPrefix, dep.SelectedBy), value)
		}
	}
	for _, dep := range mod.RequiresContracts {
		if dep.Name == base {
			return a.resolvedValueForError(paramEnvKey(module, mod.EnvPrefix, dep.SelectedBy), value)
		}
	}
	for _, dep := range mod.RequiresCapabilities {
		if dep.Name == base {
			return a.resolvedValueForError(paramEnvKey(module, mod.EnvPrefix, dep.InterfaceSelectedBy), value)
		}
	}
	return value
}

// resolveOrderWithInputValidation separates two facts that are easy to blur:
// resolution decides which Modules are active, while input_required describes
// values the caller actually supplied. Resolver defaults and published binding
// values may determine the former, but must never be allowed to satisfy the
// latter. Keep the pre-resolution view and use only the resolved order from the
// mutated environment.
func (a *app) resolveOrderWithInputValidation(modules []string) ([]string, error) {
	inputValues := cloneMap(a.env)
	if a.callerInputEnv != nil {
		inputValues = cloneMap(a.callerInputEnv)
	}
	order, err := a.resolveOrder(modules)
	if err != nil {
		return nil, err
	}
	if err := validateInputRequiredEnv(inputValues, order, a.reg); err != nil {
		return nil, err
	}
	return order, nil
}

// validateInputRequiredEnv checks the unconditional caller-input contract for
// the Module set selected by the generic resolver. values must be the snapshot
// taken before resolver defaults/bindings are published; neither the CLI nor
// anasd needs a branch for any Module name.
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
	return normalizeConfiguredParameterEnvWithSensitive(values, reg, nil)
}

// normalizeConfiguredParameterEnvWithSensitive additionally treats keys as
// secret because of their storage source. Secret Store provenance remains a
// confidentiality boundary even if a later Module manifest accidentally drops
// its sensitive policy while tightening the parameter schema.
func normalizeConfiguredParameterEnvWithSensitive(values map[string]string, reg map[string]Module, sourceSensitive map[string]bool) error {
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
			if sourceSensitive[config.EnvKey(key)] {
				return fmt.Errorf("env.%s does not satisfy its declared type or constraints", key)
			}
			return parameterValidationError("env."+key, module, parameter, err, reg)
		}
		values[key] = normalized
	}
	return nil
}
