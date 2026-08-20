package runner

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"unicode"

	"github.com/anas-project/ANAS/internal/config"
)

const (
	maxModuleValidationPlanEntries = 64
	maxModuleValidationPlanValue   = 1024
)

// validateDeferredModuleHooks gives `config set --defer` cross-parameter checks
// for every deterministically resolvable Module in effective dependency order,
// without enforcing the separate input_required deployment boundary. Missing
// or ambiguous optional selectors remain deferrable; a pre-existing lock still
// guides alternative/provider choices when one is available.
func validateDeferredModuleHooks(cfg *config.File, reg map[string]Module, store *secretStore, lock *moduleLock) error {
	values, owners := configBaseEnvWithRegistry(cfg, reg)
	validator := &app{
		cfg: cfg, reg: reg, env: values, envOwner: owners, secrets: store, lock: lock,
		resolvedBindings:             map[string]map[string]string{},
		registryOnlyResolution:       true,
		allowUnresolvedInputBindings: true,
	}
	order, err := validator.resolveOrder(cfg.Modules.Order)
	if err != nil {
		return err
	}
	validator.order = order
	validator.applyModuleDefaults()
	pinned := trustedModuleValidationOrder(order, lock)
	if lock != nil && len(lock.Modules) > 0 {
		// A staged config may add a Module that the old lock cannot yet trust.
		// Do not execute that new code here; validate every already-pinned Hook
		// now, then let the explicit `anas lock` trust transition validate the
		// expanded topology before committing its new lock.
		validator.order = pinned
		if err := validateLockedModuleBundles(validator, lock); err != nil {
			return err
		}
	}
	// An empty lock is not implicit trust in every bundle. New workspaces and
	// staged Modules wait until explicit `anas lock` validates the candidate
	// topology before any opt-in Hook is executed.
	validator.order = pinned
	return validator.validateModules()
}

func trustedModuleValidationOrder(order []string, lock *moduleLock) []string {
	if lock == nil || len(lock.Modules) == 0 {
		return []string{}
	}
	pinned := make([]string, 0, len(order))
	for _, name := range order {
		if _, ok := lock.Modules[name]; ok {
			pinned = append(pinned, name)
		}
	}
	return pinned
}

// validateModules runs the opt-in, desired-state-only validation phase for the
// effective topology. Module Hooks are trusted code rather than an OS sandbox,
// but the ABI supplies neither Secret Store plaintext nor a writable deployment
// workdir, and Core refuses every response that could mutate deployment state.
func (a *app) validateModules() error {
	if a.modulesValidated {
		return nil
	}
	if err := normalizeConfiguredParameterEnvWithSensitive(a.env, a.reg, a.sensitiveEnvKeySet()); err != nil {
		return fmt.Errorf("resolved deployment config: %w", err)
	}
	// Normalization can make a public alias equal to a sensitive value (for
	// example after enum case-folding or DNS-name canonicalization). Rebuild
	// the cached set from the normalized environment before scoping any Hook.
	a.sensitiveKeys = nil
	sensitiveKeys := a.sensitiveEnvKeySet()
	root, err := os.MkdirTemp("", "anas-module-validation-*")
	if err != nil {
		return fmt.Errorf("create module validation execution root: %w", err)
	}
	defer os.RemoveAll(root)

	validator := *a
	validator.base = root
	validator.hookBins = nil
	validator.validationBuild = true
	for _, name := range a.order {
		mod, ok := a.reg[name]
		if !ok || !hookSupportsPhase(mod.Hook, "validate") {
			continue
		}
		env := a.scopedEnv(name)
		for key := range sensitiveKeys {
			delete(env, key)
		}
		resp, err := validator.runValidationHook(mod, env)
		if err != nil {
			return fmt.Errorf("%s config validation: %w", name, err)
		}
		if fields := validationMutationFields(resp); len(fields) > 0 {
			return fmt.Errorf("%s config validation: validate hook returned forbidden mutation fields: %s", name, strings.Join(fields, ", "))
		}
		if err := a.recordModuleValidationPlan(name, resp.Plan); err != nil {
			return fmt.Errorf("%s config validation: %w", name, err)
		}
		for _, warning := range resp.Warnings {
			emitWarning(a.jsonMode, "module_validation_warning", "%s config validation: %s", name, warning)
		}
	}
	a.modulesValidated = true
	return nil
}

func validationMutationFields(resp hookResponse) []string {
	fields := []string{}
	if len(resp.Env) > 0 {
		fields = append(fields, "env")
	}
	if len(resp.Secrets) > 0 {
		fields = append(fields, "secrets")
	}
	if len(resp.Files) > 0 {
		fields = append(fields, "files")
	}
	if len(resp.RuntimeFiles) > 0 {
		fields = append(fields, "runtime_files")
	}
	if len(resp.DisableServices) > 0 {
		fields = append(fields, "disable_services")
	}
	if len(resp.DockerCopies) > 0 {
		fields = append(fields, "docker_copies")
	}
	if len(resp.InternalEnv) > 0 {
		fields = append(fields, "internal_env")
	}
	if resp.Credential != nil {
		fields = append(fields, "credential")
	}
	sort.Strings(fields)
	return fields
}

// recordModuleValidationPlan admits only a small, printable, non-sensitive
// metadata map. Hooks are trusted code, not an OS sandbox, but accidental
// publication of a known secret value or sensitive parameter is still refused
// before plan output or a manifest can persist it.
func (a *app) recordModuleValidationPlan(module string, values map[string]string) error {
	if len(values) == 0 {
		return nil
	}
	if len(values) > maxModuleValidationPlanEntries {
		return fmt.Errorf("validate hook returned %d plan fields; maximum is %d", len(values), maxModuleValidationPlanEntries)
	}
	mod := a.reg[module]
	sensitiveKeys := a.sensitiveEnvKeySet()
	sensitiveValues := map[string]bool{}
	addSensitiveValue := func(value string) {
		addSensitiveValueForms(sensitiveValues, value)
	}
	for key := range sensitiveKeys {
		addSensitiveValue(a.env[key])
	}
	if a.secrets != nil {
		for _, value := range a.secrets.values {
			addSensitiveValue(value)
		}
	}

	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	plan := make(map[string]string, len(values))
	for _, rawKey := range keys {
		key := rawKey
		if !configParameterNamePattern.MatchString(key) {
			return fmt.Errorf("validate hook plan key %q must match %s", rawKey, configParameterNamePattern.String())
		}
		moduleKey := moduleParamEnvKey(module, mod.EnvPrefix, mod.Exports, key)
		if sensitiveKeys[config.EnvKey(key)] || sensitiveKeys[moduleKey] {
			return fmt.Errorf("validate hook plan key %q identifies a sensitive parameter", key)
		}
		value := strings.TrimSpace(values[rawKey])
		if value == "" {
			return fmt.Errorf("validate hook plan field %q must not be empty", key)
		}
		if len(value) > maxModuleValidationPlanValue {
			return fmt.Errorf("validate hook plan field %q exceeds %d bytes", key, maxModuleValidationPlanValue)
		}
		if strings.IndexFunc(value, unicode.IsControl) >= 0 {
			return fmt.Errorf("validate hook plan field %q contains control characters", key)
		}
		if matchesSensitiveValue(sensitiveValues, value) {
			return fmt.Errorf("validate hook plan field %q contains a sensitive value", key)
		}
		plan[key] = value
	}
	if a.modulePlans == nil {
		a.modulePlans = map[string]map[string]string{}
	}
	a.modulePlans[module] = plan
	return nil
}

func (a *app) moduleValidationPlanDocument() map[string]map[string]string {
	return cloneNestedMap(a.modulePlans)
}

func (a *app) moduleValidationPlanSummary() string {
	modules := make([]string, 0, len(a.modulePlans))
	for module := range a.modulePlans {
		modules = append(modules, module)
	}
	sort.Strings(modules)
	var out strings.Builder
	for _, module := range modules {
		keys := make([]string, 0, len(a.modulePlans[module]))
		for key := range a.modulePlans[module] {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		fmt.Fprintf(&out, "module plan: %s", module)
		for _, key := range keys {
			fmt.Fprintf(&out, " %s=%s", key, a.modulePlans[module][key])
		}
		out.WriteByte('\n')
	}
	return out.String()
}
