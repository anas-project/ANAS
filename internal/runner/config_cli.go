package runner

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/anas-project/ANAS/internal/config"
)

type configTarget struct {
	YAMLPath  []string
	Display   string
	Module    string
	Parameter string
}

func runConfig(args []string, jsonMode bool) error {
	if len(args) == 0 || strings.HasPrefix(args[0], "-") {
		// `anas config --json` used to take "--json" as the subcommand name and
		// fail several steps later with an unrelated code. A subcommand is a
		// word, never a flag.
		return usageErrorf("usage: anas config import|migrate|list|set|explain|plan|secret ... [--json]")
	}
	subcommand := args[0]
	if subcommand == "secret" {
		return runConfigSecret(args[1:], jsonMode)
	}
	fs := flag.NewFlagSet("config "+subcommand, flag.ContinueOnError)
	cfgPath := fs.String("c", "", "config file")
	fs.StringVar(cfgPath, "config", "", "config file")
	workspaceFlag := fs.String("w", "", "workspace path")
	fs.StringVar(workspaceFlag, "workspace", "", "workspace path")
	rootFlag := fs.String("root", "", "project root or module bundle directory")
	var deferApply, updateLock bool
	if subcommand == "set" {
		fs.BoolVar(&deferApply, "defer", false, "store desired state without applying it to an active deployment")
		fs.BoolVar(&updateLock, "update-lock", false, "also update the config lock when resolution changes")
	}
	registerJSONFlag(fs)
	positional, err := parseInterspersed(fs, args[1:])
	if err != nil {
		return usageErrorf("%s", err.Error())
	}
	// `explain` only reads the module registry, so it stays usable outside a
	// workspace; the other subcommands act on one and must resolve it.
	//
	// `list` sits between the two: what can be set is a property of the modules,
	// so it answers outside a workspace, and inside one it additionally fills
	// in the current values. Requiring a workspace would deny the question to
	// exactly the person who has not built one yet.
	var workspace, base string
	switch subcommand {
	case "explain":
	case "list":
		if workspace, err = resolveWorkspace(*workspaceFlag); err == nil {
			base = stateDir(workspace)
			*cfgPath = absolutePath(configPathFor(workspace, *cfgPath))
		} else if strings.TrimSpace(*cfgPath) != "" {
			*cfgPath = absolutePath(*cfgPath)
		} else {
			*cfgPath = ""
		}
	default:
		workspace, err = resolveWorkspace(*workspaceFlag)
		if err != nil {
			return usageErrorf("%s", err.Error())
		}
		base = stateDir(workspace)
		*cfgPath = absolutePath(configPathFor(workspace, *cfgPath))
	}
	root, err := locateModuleRootForWorkspace(*rootFlag, workspace)
	if err != nil {
		return preconditionErrorf("module_root_missing", "%s", err.Error())
	}
	reg, err := loadRegistryDir(root)
	if err != nil {
		return preconditionErrorf("module_root_invalid", "%s", err.Error())
	}

	switch subcommand {
	case "import":
		if len(positional) != 1 {
			return usageErrorf("usage: anas config import SOURCE [-w <workspace>] [--json]")
		}
		unlock, lockErr := acquireRuntimeLock(base)
		if lockErr != nil {
			return preconditionErrorf("runtime_lock_failed", "%s", lockErr.Error())
		}
		defer unlock()
		source := absolutePath(positional[0])
		result, err := importConfigIntoWorkspace(workspace, source, reg)
		if err != nil {
			return preconditionErrorf("config_import_failed", "%s", err.Error())
		}
		if jsonMode {
			return emitOK(map[string]any{"workspace": workspace, "source": source, "config": workspaceConfigPath(workspace), "secrets_imported": len(result.Secrets)})
		}
		fmt.Printf("imported %s\nnormalized config: %s\nsecrets imported: %d\n", source, workspaceConfigPath(workspace), len(result.Secrets))
		return nil
	case "migrate":
		if len(positional) != 0 {
			return usageErrorf("usage: anas config migrate [-w <workspace>] [--json]")
		}
		unlock, lockErr := acquireRuntimeLock(base)
		if lockErr != nil {
			return preconditionErrorf("runtime_lock_failed", "%s", lockErr.Error())
		}
		defer unlock()
		source := workspaceConfigPath(workspace)
		result, err := importConfigIntoWorkspace(workspace, source, reg)
		if err != nil {
			return preconditionErrorf("config_import_failed", "%s", err.Error())
		}
		if jsonMode {
			return emitOK(map[string]any{"workspace": workspace, "source": source, "config": source, "secrets_imported": len(result.Secrets)})
		}
		fmt.Printf("migrated %s\nsecrets imported: %d\n", source, len(result.Secrets))
		return nil
	case "list":
		if len(positional) > 1 {
			return usageErrorf("usage: anas config list [global|<module>] [-w <workspace>] [-c config.yml] [--json]")
		}
		scope := ""
		if len(positional) == 1 {
			scope = strings.ToLower(strings.TrimSpace(positional[0]))
			if _, known := declaredParametersFor(scope, reg); !known {
				return usageErrorf("unknown module %q; pass a module name or %q", scope, globalModuleName)
			}
		}
		if workspace != "" && *cfgPath != "" {
			if err := validateManagedConfig(workspace, *cfgPath); err != nil {
				return preconditionErrorf("config_not_managed", "%s", err.Error())
			}
		}
		return reportConfigList(*cfgPath, reg, scope, jsonMode, base)
	case "set":
		if len(positional) != 2 {
			return usageErrorf("usage: anas config set [-w <workspace>] [-c config.yml] <path> <value> [--json]")
		}
		target, err := resolveConfigTarget(positional[0], reg)
		if err != nil {
			return usageErrorf("%s", err.Error())
		}
		policy := policyForTarget(target, reg)
		normalizedValue, err := normalizeConfigInputForPolicy(target, positional[1], policy, reg)
		if err != nil {
			return usageErrorf("%s", err.Error())
		}
		switch policy.Effect {
		case "credential_rotate":
			return usageErrorf("%s is lifecycle-managed; provide its initial value through `anas config import SOURCE` and use the declared credential rotation command afterwards", target.Display)
		case "data_migrate":
			return usageErrorf("%s requires data migration (%s); config set will not write a value it cannot enact", target.Display, policy.Apply)
		case "immutable":
			return usageErrorf("%s is immutable after initialization (%s); use the declared replacement or migration workflow", target.Display, policy.Apply)
		}
		if !exists(*cfgPath) {
			return preconditionErrorf("config_missing", "config %s does not exist", *cfgPath)
		}
		unlock, lockErr := acquireRuntimeLock(base)
		if lockErr != nil {
			return preconditionErrorf("runtime_lock_failed", "%s", lockErr.Error())
		}
		defer unlock()
		rollback, err := captureManagedConfigFiles(*cfgPath, updateLock)
		if err != nil {
			return failuref("state_unreadable", "%s", err.Error())
		}
		// A declared type already interpreted the CLI token. Persist that exact
		// canonical text instead of asking YAML to reinterpret it a second time
		// (where "null", "1.0" or "[x]" would change meaning).
		preserveText := targetParamType(target, reg).Declared()
		if err := setManagedConfigScalar(workspace, *cfgPath, target.YAMLPath, normalizedValue, preserveText, reg); err != nil {
			var lockMismatch *lockedResolutionError
			if errors.As(err, &lockMismatch) {
				// The generic advice is to run `anas lock`, but this command can
				// do it in one step. A parameter that changes which Modules
				// resolve is the case where an operator most needs to be told
				// that, because nothing about setting a value suggests the lock
				// is involved.
				return preconditionErrorf("lock_stale", "%s, or rerun this command with --update-lock", err.Error())
			}
			var lockFile *configCandidateLockFileError
			if errors.As(err, &lockFile) {
				return preconditionErrorf("lock_invalid", "%s", err.Error())
			}
			var secretStore *configCandidateSecretStoreError
			if errors.As(err, &secretStore) {
				return preconditionErrorf("secrets_unreadable", "%s", err.Error())
			}
			var invalid *configCandidateValidationError
			if errors.As(err, &invalid) {
				return preconditionErrorf("config_invalid", "%s", err.Error())
			}
			return failuref("write_failed", "%s", err.Error())
		}
		execution := map[string]any{"status": "stored", "executor": effectExecutor(policy.Effect)}
		active, err := loadActiveState(base)
		if err != nil {
			_ = rollback()
			return preconditionErrorf("state_unreadable", "%s", err.Error())
		}
		if deferApply {
			execution["status"] = "deferred"
		} else if active.ActiveDeployment == "" {
			execution["status"] = "pending_initial_apply"
		} else if active.RuntimeStatus == "stopped" {
			// Do not surprise an operator by starting a deliberately stopped
			// deployment. The value is durably staged and the explicit next apply
			// will materialize it; the status makes that deferral machine-visible.
			execution["status"] = "pending_explicit_apply"
		} else {
			id, applyErr := applyManagedConfigChangeLocked(workspace, base, *cfgPath, root, policy, updateLock, jsonMode)
			if applyErr != nil {
				if restoreErr := rollback(); restoreErr != nil {
					return failuref("config_rollback_failed", "apply failed (%v) and restoring managed config failed: %v", applyErr, restoreErr)
				}
				return applyErr
			}
			execution["status"] = "applied"
			execution["deployment_id"] = id
			execution["previous_deployment"] = active.ActiveDeployment
		}
		if jsonMode {
			return emitOK(map[string]any{
				"workspace": workspace, "config": *cfgPath,
				"setting": configTargetMetadataDocument(target, policy, reg), "execution": execution,
			})
		}
		fmt.Printf("updated %s\neffect: %s\napply: %s\n", target.Display, policy.Effect, policy.Apply)
		fmt.Printf("execution: %s", execution["status"])
		if id, ok := execution["deployment_id"].(string); ok {
			fmt.Printf(" (%s)", id)
		}
		fmt.Println()
		if policy.Description != "" {
			fmt.Println(policy.Description)
		}
		return nil
	case "explain":
		if len(positional) != 1 {
			return usageErrorf("usage: anas config explain <path> [--json]")
		}
		target, err := resolveConfigTarget(positional[0], reg)
		if err != nil {
			return usageErrorf("%s", err.Error())
		}
		policy := policyForTarget(target, reg)
		if jsonMode {
			document := configTargetMetadataDocument(target, policy, reg)
			return emitOK(map[string]any{"setting": document})
		}
		spec := targetParamType(target, reg)
		kind, values := paramTypeDocument(spec)
		defaultValue, hasDefault, defaultSource := parameterDefaultMetadata(
			target.Module, target.Parameter, reg, globalConfig.defaultValues(),
		)
		if policy.Sensitive {
			defaultValue = ""
		}
		fmt.Printf("path: %s\nmodule: %s\nparameter: %s\ntype: %s\nenv: %s\nrequired: %t\ninput required: %t\nmust resolve: %t\nhas default: %t\ndefault source: %s\ndefault: %s\neffect: %s\nexecutor: %s\napply: %s\nsensitive: %t\n",
			target.Display, target.Module, target.Parameter, kind,
			parameterEnvKey(target.Module, target.Parameter, reg),
			parameterInputRequired(target.Module, target.Parameter, reg),
			parameterInputRequired(target.Module, target.Parameter, reg),
			parameterMustResolve(target.Module, target.Parameter, reg),
			hasDefault, defaultSource,
			configDefaultDisplay(defaultValue, hasDefault, policy.Sensitive), policy.Effect,
			effectExecutor(policy.Effect), policy.Apply, policy.Sensitive)
		if len(values) > 0 {
			fmt.Println("allowed: " + strings.Join(values, ", "))
		}
		if constraints := paramConstraintsDocument(spec); len(constraints) > 0 {
			fmt.Println("constraints: " + formatConfigConstraints(constraints))
		}
		if policy.Description != "" {
			fmt.Println("description: " + policy.Description)
		}
		return nil
	case "plan":
		if len(positional) != 0 {
			return usageErrorf("usage: anas config plan [-w <workspace>] [-c config.yml] [--json]")
		}
		if err := validateManagedConfig(workspace, *cfgPath); err != nil {
			return preconditionErrorf("config_not_managed", "%s", err.Error())
		}
		return reportConfigPlan(workspace, *cfgPath, base, reg, jsonMode)
	default:
		return usageErrorf("unknown config command %q; expected import, migrate, list, set, explain, plan or secret", subcommand)
	}
}

// captureManagedConfigFiles makes config set transactional with deployment
// activation. Runtime activation already compensates by restarting the prior
// deployment when the new one fails; this restores the matching desired state
// and managed digest so a later apply cannot retry a rejected value silently.
func captureManagedConfigFiles(configPath string, includeLock bool) (func() error, error) {
	type saved struct {
		path    string
		data    []byte
		mode    os.FileMode
		existed bool
	}
	paths := []string{configPath, managedConfigStatePath(stateDir(filepath.Dir(configPath)))}
	if includeLock {
		paths = append(paths, projectLockPath(configPath))
	}
	savedFiles := make([]saved, 0, len(paths))
	for _, path := range paths {
		info, err := os.Stat(path)
		if err != nil {
			if os.IsNotExist(err) && path == projectLockPath(configPath) {
				savedFiles = append(savedFiles, saved{path: path})
				continue
			}
			return nil, err
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		savedFiles = append(savedFiles, saved{path: path, data: body, mode: info.Mode().Perm(), existed: true})
	}
	return func() error {
		files := make([]importFile, 0, len(savedFiles))
		for _, file := range savedFiles {
			if !file.existed {
				continue
			}
			files = append(files, importFile{path: file.path, data: file.data, mode: file.mode})
		}
		if err := commitImportedFiles(files); err != nil {
			return err
		}
		for _, file := range savedFiles {
			if !file.existed {
				if err := os.Remove(file.path); err != nil && !os.IsNotExist(err) {
					return err
				}
			}
		}
		return nil
	}, nil
}

func effectExecutor(effect string) string {
	switch effect {
	case "image_rebuild":
		return "deployment_build_apply"
	case "credential_rotate":
		return "credential_lifecycle_command"
	case "data_migrate":
		return "migration_command"
	case "immutable":
		return "replacement_workflow"
	default:
		// Until a Module implements the future config_apply hook, rendering and
		// activating an immutable deployment is the conservative executor for
		// reload/reconcile/restart effects. It may recreate the affected
		// container, but it never reports a value as applied without an action.
		return "deployment_apply_fallback"
	}
}

func applyManagedConfigChangeLocked(workspace, base, cfgPath, moduleRoot string, policy ChangePolicy, updateLock, jsonMode bool) (string, error) {
	opts := prepareOptions{
		workspace: workspace, base: base, cfgPath: cfgPath,
		moduleRoot: moduleRoot, updateLock: updateLock,
	}
	id, err := materializeDeployment(opts, policy.Effect == "image_rebuild", jsonMode)
	if err != nil {
		return "", err
	}
	if err := activateDeployment(base, id, activateOptions{json: jsonMode}); err != nil {
		return "", err
	}
	return id, nil
}

// configTargetDocument is the one shape `set` and `explain` both report, so a
// caller that can read one can read the other.
func configTargetDocument(target configTarget, policy ChangePolicy) map[string]any {
	editable, editCommand := configEditability(policy)
	return map[string]any{
		"path": target.Display, "module": target.Module, "parameter": target.Parameter,
		"effect": policy.Effect, "apply": policy.Apply,
		"sensitive": policy.Sensitive, "description": policy.Description,
		"editable": editable, "edit_command": editCommand,
		"executor":          effectExecutor(policy.Effect),
		"declared_executor": nullableString(policy.Executor),
		"verification":      nullableString(policy.Verify),
	}
}

func configTargetMetadataDocument(target configTarget, policy ChangePolicy, reg map[string]Module) map[string]any {
	document := configTargetDocument(target, policy)
	document["env_key"] = parameterEnvKey(target.Module, target.Parameter, reg)
	defaultValue, hasDefault, defaultSource := parameterDefaultMetadata(
		target.Module, target.Parameter, reg, globalConfig.defaultValues(),
	)
	if policy.Sensitive {
		defaultValue = ""
	}
	addConfigParameterMetadata(document, target, reg, defaultValue, hasDefault, defaultSource)
	return document
}

func normalizeConfigInputValue(target configTarget, value string, reg map[string]Module) (string, error) {
	normalized, err := normalizeParameterValue(target.Module, target.Parameter, value, reg)
	if err != nil {
		return "", err
	}
	if parameterInputRequired(target.Module, target.Parameter, reg) && strings.TrimSpace(normalized) == "" {
		return "", fmt.Errorf("%s is required input and cannot be set to an empty value", target.Display)
	}
	return normalized, nil
}

func formatConfigConstraints(constraints map[string]any) string {
	order := []string{"minimum", "maximum", "min_length", "max_length", "pattern", "format"}
	parts := make([]string, 0, len(constraints))
	for _, key := range order {
		if value, ok := constraints[key]; ok {
			parts = append(parts, fmt.Sprintf("%s=%v", key, value))
		}
	}
	return strings.Join(parts, ", ")
}

func configEditability(policy ChangePolicy) (bool, string) {
	switch policy.Effect {
	case "credential_rotate":
		return false, policy.Apply
	case "data_migrate":
		return false, policy.Apply
	case "immutable":
		return false, policy.Apply
	default:
		return true, "anas config set"
	}
}

func targetParamType(target configTarget, reg map[string]Module) ParamType {
	return parameterType(target.Module, target.Parameter, reg)
}

func paramTypeDocument(spec ParamType) (string, []string) {
	if !spec.Declared() {
		return "unknown", nil
	}
	if len(spec.Enum) > 0 {
		return "enum", append([]string{}, spec.Enum...)
	}
	return spec.Kind, nil
}

func runConfigSecret(args []string, jsonMode bool) error {
	if len(args) == 0 || strings.HasPrefix(args[0], "-") {
		return usageErrorf("usage: anas config secret list | anas config secret get <KEY>")
	}
	action := args[0]
	fs := flag.NewFlagSet("config secret "+action, flag.ContinueOnError)
	workspaceFlag := fs.String("w", "", "workspace path")
	fs.StringVar(workspaceFlag, "workspace", "", "workspace path")
	registerJSONFlag(fs)
	// parseInterspersed, not fs.Parse: the natural `config secret get KEY -w
	// <workspace>` stops the standard parser at KEY, so -w was silently
	// dropped and the command read the secrets of whatever workspace the
	// current directory or ANAS_WORKSPACE happened to name. Reading one
	// deployment's secrets while the operator believes they asked for
	// another's is not a thing to leave to argument order.
	positional, err := parseInterspersed(fs, args[1:])
	if err != nil {
		return usageErrorf("%s", err.Error())
	}
	workspace, err := resolveWorkspace(*workspaceFlag)
	if err != nil {
		return usageErrorf("%s", err.Error())
	}
	store, err := loadSecretStore(stateDir(workspace))
	if err != nil {
		return preconditionErrorf("secrets_unreadable", "%s", err.Error())
	}
	switch action {
	case "list":
		if len(positional) != 0 {
			return usageErrorf("usage: anas config secret list [-w <workspace>] [--json]")
		}
		keys := make([]string, 0, len(store.values))
		sources := map[string]string{}
		for key := range store.values {
			keys = append(keys, key)
			sources[key] = store.metadata[key].Kind
		}
		sort.Strings(keys)
		if jsonMode {
			// Keys only. `list` exists so an operator can discover what was
			// generated; putting the values here would make a routine
			// inventory command leak every secret into whatever captured it.
			items := make([]map[string]string, 0, len(keys))
			for _, key := range keys {
				items = append(items, map[string]string{"key": key, "source": sources[key]})
			}
			return emitOK(map[string]any{"workspace": workspace, "keys": keys, "secrets": items})
		}
		for _, key := range keys {
			fmt.Printf("%s\t%s\n", sources[key], key)
		}
		return nil
	case "get":
		if len(positional) != 1 {
			return usageErrorf("usage: anas config secret get <KEY> [-w <workspace>] [--json]")
		}
		value, ok := store.values[positional[0]]
		source := store.metadata[positional[0]].Kind
		if !ok {
			return preconditionErrorf("secret_missing",
				"no generated secret %q; use `anas config secret list`", positional[0])
		}
		if jsonMode {
			return emitOK(map[string]any{"workspace": workspace, "key": positional[0], "source": source, "value": value})
		}
		fmt.Println(value)
		return nil
	default:
		return usageErrorf("usage: anas config secret list | anas config secret get <KEY>")
	}
}

func resolveConfigTarget(path string, reg map[string]Module) (configTarget, error) {
	parts := strings.Split(strings.TrimSpace(path), ".")
	if len(parts) < 2 {
		return configTarget{}, fmt.Errorf("config path must be <module>.<parameter> or global.<parameter>")
	}
	for i := range parts {
		parts[i] = strings.TrimSpace(parts[i])
	}
	namespace := strings.ToLower(parts[0])
	if namespace == "global" {
		if len(parts) != 2 {
			return configTarget{}, fmt.Errorf("global config path must have two components")
		}
		return resolveGlobalTarget(strings.ToLower(parts[1]), reg)
	}
	if namespace == "env" {
		if len(parts) != 2 {
			return configTarget{}, fmt.Errorf("raw env config path must have two components")
		}
		key := config.EnvKey(parts[1])
		if !envKeyPattern.MatchString(key) {
			return configTarget{}, fmt.Errorf("env key %q is not a valid environment key", key)
		}
		if isRunnerOwnedRuntimeKey(key, reg) {
			return configTarget{}, fmt.Errorf("env.%s is runner-owned and cannot be supplied by config", key)
		}
		module, parameter, err := policyOwnerForEnv(key, reg)
		if err != nil {
			return configTarget{}, err
		}
		return configTarget{YAMLPath: []string{"env", key}, Display: "env." + key, Module: module, Parameter: parameter}, nil
	}
	if namespace == "modules" {
		if len(parts) == 4 && strings.EqualFold(parts[2], "config") {
			parts = []string{namespace, parts[1], parts[3]}
		} else if len(parts) == 4 && strings.EqualFold(parts[2], "identity") && strings.EqualFold(parts[3], "login_protocol") {
			module := strings.ToLower(parts[1])
			parameter, ok := moduleIdentityLoginParameter(module, reg)
			if !ok {
				return configTarget{}, fmt.Errorf("module %q does not declare an identity login protocol selector", module)
			}
			return resolveModuleTarget(module, parameter, reg)
		}
		if len(parts) != 3 {
			return configTarget{}, fmt.Errorf("module config path must be modules.<module>.<parameter>")
		}
		return resolveModuleTarget(strings.ToLower(parts[1]), strings.ToLower(parts[2]), reg)
	}
	module := namespace
	if _, ok := reg[module]; !ok && module != globalModuleName {
		return configTarget{}, fmt.Errorf("unknown module %q", module)
	}
	if len(parts) != 2 {
		return configTarget{}, fmt.Errorf("module config path must have two components")
	}
	parameter := strings.ToLower(parts[1])
	if module == globalModuleName {
		return resolveGlobalTarget(parameter, reg)
	}
	return resolveModuleTarget(module, parameter, reg)
}

func resolveModuleTarget(module, parameter string, reg map[string]Module) (configTarget, error) {
	module = strings.ToLower(strings.TrimSpace(module))
	parameter = strings.ToLower(strings.TrimSpace(parameter))
	mod, ok := reg[module]
	if !ok {
		return configTarget{}, fmt.Errorf("unknown module %q", module)
	}
	if err := validateParameter(module, parameter, reg); err != nil {
		return configTarget{}, err
	}
	if key := moduleParamEnvKey(module, mod.EnvPrefix, mod.Exports, parameter); !envKeyPattern.MatchString(key) {
		return configTarget{}, fmt.Errorf("%s.%s resolves to invalid environment key %q", module, parameter, key)
	}
	// A parameter the module declares under a bare env name is set in the top
	// level `env:` block: every key under `modules.<module>.config` acquires the
	// module prefix, which would write a variant nothing reads.
	if key, ok := mod.bareEnvParameter(parameter); ok {
		return configTarget{YAMLPath: []string{"env", key}, Display: "env." + key, Module: module, Parameter: parameter}, nil
	}
	if identityParameter, ok := moduleIdentityLoginParameter(module, reg); ok && parameter == identityParameter {
		return configTarget{
			YAMLPath: []string{"modules", module, "identity", "login_protocol"},
			Display:  module + "." + parameter, Module: module, Parameter: parameter,
		}, nil
	}
	return configTarget{YAMLPath: []string{"modules", module, "config", parameter}, Display: module + "." + parameter, Module: module, Parameter: parameter}, nil
}

// resolveGlobalTarget is the single answer to "where does a deployment-wide
// parameter get written", shared by `global.<parameter>` and the module path
// spelled with the global module name. They used to disagree: the former wrote
// into the `global:` block unconditionally, so a parameter with no field on
// config.Global produced a config that KnownFields
// then refused to load, breaking every later command rather than the `set`
// that caused it.
func resolveGlobalTarget(parameter string, reg map[string]Module) (configTarget, error) {
	if err := validateParameter(globalModuleName, parameter, reg); err != nil {
		return configTarget{}, err
	}
	if isGlobalParameter(parameter) {
		return configTarget{
			YAMLPath: []string{"global", parameter}, Display: "global." + parameter,
			Module: globalModuleName, Parameter: parameter,
		}, nil
	}
	// Anything else lands in the top level `env:` block, and the key there may
	// well belong to a module that publishes it under a bare name. Naming it
	// through the global path must not lose that module's change policy.
	key := config.EnvKey(parameter)
	owner, ownerParameter, err := policyOwnerForEnv(key, reg)
	if err != nil {
		return configTarget{}, err
	}
	return configTarget{
		YAMLPath: []string{"env", key}, Display: "env." + key,
		Module: owner, Parameter: ownerParameter,
	}, nil
}

// globalParameterSet is config.Global's own field list. Deriving it is what
// keeps `anas config list` honest: it prints `global.<parameter>` as the way
// to address these, so a name here that config.Global does not have would be
// advertising a path that corrupts the config.
var globalParameterSet = func() map[string]bool {
	out := map[string]bool{}
	for _, parameter := range config.GlobalParameters() {
		out[parameter] = true
	}
	return out
}()

func isGlobalParameter(parameter string) bool {
	return globalParameterSet[strings.ToLower(strings.TrimSpace(parameter))]
}

func policyOwnerForEnv(key string, reg map[string]Module) (string, string, error) {
	type owner struct{ module, parameter string }
	matches := []owner{}
	seen := map[owner]bool{}
	add := func(module, parameter string) {
		match := owner{module: module, parameter: strings.ToLower(strings.TrimSpace(parameter))}
		if match.parameter == "" || seen[match] {
			return
		}
		seen[match] = true
		matches = append(matches, match)
	}
	globalParameters, _ := declaredParametersFor(globalModuleName, reg)
	for _, parameter := range globalParameters {
		if strings.EqualFold(parameterEnvKey(globalModuleName, parameter, reg), key) {
			add(globalModuleName, parameter)
		}
	}
	names := make([]string, 0, len(reg))
	for name := range reg {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		mod := reg[name]
		parameters := append([]string{}, mod.Parameters...)
		sort.Strings(parameters)
		for _, parameter := range parameters {
			if strings.EqualFold(moduleParamEnvKey(name, mod.EnvPrefix, mod.Exports, parameter), key) {
				add(name, parameter)
			}
		}
	}
	switch len(matches) {
	case 0:
		return globalModuleName, strings.ToLower(key), nil
	case 1:
		return matches[0].module, matches[0].parameter, nil
	default:
		owners := make([]string, 0, len(matches))
		for _, m := range matches {
			owners = append(owners, m.module+"."+m.parameter)
		}
		return "", "", fmt.Errorf("env key %q maps to multiple module parameters (%s); use the module path instead", key, strings.Join(owners, ", "))
	}
}

func policyForTarget(target configTarget, reg map[string]Module) ChangePolicy {
	if target.Module == globalModuleName {
		if policy, ok := globalConfig.policy(target.Parameter); ok {
			return policy
		}
	}
	if mod, ok := reg[target.Module]; ok {
		if policy, ok := mod.Changes[strings.ToLower(target.Parameter)]; ok {
			return policy
		}
	}
	if imageBuildEnvKeys[config.EnvKey(target.Parameter)] {
		return ChangePolicy{
			Effect: "image_rebuild", Apply: "apply-with-build",
			Description: "This value is consumed while building an image; run anas apply --build to regenerate it.",
		}
	}
	return ChangePolicy{
		Effect: "container_recreate", Apply: "render-and-recreate",
		Description: "No specialized reconciler is declared; recreate the affected container to apply rendered configuration.",
	}
}

var imageBuildEnvKeys = map[string]bool{
	"APT_MIRROR_URL":                     true,
	"APK_MIRROR_URL":                     true,
	"NPM_REGISTRY_URL":                   true,
	"GOPROXY_URL":                        true,
	"BUILD_GITHUB_DOWNLOAD_PROXY_PREFIX": true,
	"DOCKER_HUB_REGISTRY":                true,
	"LLNG_DOCKER_HUB_REGISTRY":           true,
	"GHCR_REGISTRY":                      true,
	"NEXTCLOUD_APT_MIRROR_URL":           true,
	"LAM_APT_MIRROR_URL":                 true,
	"LAM_DOWNLOAD_URL":                   true,
}

func targetForSettingPath(path string, reg map[string]Module) configTarget {
	parts := strings.Split(path, ".")
	if len(parts) == 2 && parts[0] == "global" {
		target, _ := resolveConfigTarget(path, reg)
		return target
	}
	if len(parts) == 2 && parts[0] == "env" {
		target, _ := resolveConfigTarget(path, reg)
		return target
	}
	if len(parts) == 2 && parts[0] == "secrets" {
		key := config.EnvKey(parts[1])
		module, parameter, err := policyOwnerForEnv(key, reg)
		if err == nil {
			return configTarget{Display: path, Module: module, Parameter: parameter}
		}
	}
	if len(parts) == 4 && parts[0] == "modules" && parts[2] == "config" {
		target, _ := resolveConfigTarget("modules."+parts[1]+"."+parts[3], reg)
		return target
	}
	if len(parts) == 4 && parts[0] == "modules" && parts[2] == "identity" && parts[3] == "login_protocol" {
		target, _ := resolveConfigTarget(path, reg)
		return target
	}
	return configTarget{Display: path, Module: globalModuleName, Parameter: path}
}

func reportConfigPlan(workspace, cfgPath, base string, reg map[string]Module, jsonMode bool) error {
	if !exists(cfgPath) {
		return preconditionErrorf("config_missing", "config %s does not exist", cfgPath)
	}
	loaded, err := config.Load(cfgPath)
	if err != nil {
		return preconditionErrorf("config_invalid", "%s", err.Error())
	}
	if err := validateConfigRuntimeKeyCollisions(cfgPath, reg); err != nil {
		return preconditionErrorf("config_invalid", "%s", err.Error())
	}
	lock, err := loadModuleLockFile(projectLockPath(cfgPath))
	if err != nil {
		return preconditionErrorf("lock_invalid", "module lock: %s", err.Error())
	}
	store, err := loadSecretStore(base)
	if err != nil {
		return preconditionErrorf("secrets_unreadable", "%s", err.Error())
	}
	validation, err := resolveLoadedConfigSchema(loaded, reg, store.lifecycleManagedValues(), lock, store.values)
	if err != nil {
		var lockMismatch *lockedResolutionError
		if errors.As(err, &lockMismatch) {
			return preconditionErrorf("lock_stale", "%s", err.Error())
		}
		return preconditionErrorf("config_invalid", "%s", err.Error())
	}
	settings, err := config.Settings(cfgPath)
	if err != nil {
		return preconditionErrorf("config_invalid", "%s", err.Error())
	}
	appliedAt, appliedValues, err := appliedSettingFingerprints(base)
	if err != nil {
		return preconditionErrorf("state_unreadable", "%s", err.Error())
	}
	keys := map[string]bool{}
	for key := range settings {
		keys[key] = true
	}
	for key := range appliedValues {
		keys[key] = true
	}
	changed := []string{}
	for key := range keys {
		if hashSetting(settings[key]) != appliedValues[key] {
			changed = append(changed, key)
		}
	}
	sort.Strings(changed)

	// change is an enumeration, not a verb chosen for the sentence it appears
	// in: add, remove and change are what a caller branches on.
	changes := make([]map[string]any, 0, len(changed))
	for _, key := range changed {
		target := targetForSettingPath(key, reg)
		policy := policyForTarget(target, reg)
		change := "change"
		if _, ok := settings[key]; !ok {
			change = "remove"
		} else if _, ok := appliedValues[key]; !ok {
			change = "add"
		}
		entry := configTargetDocument(target, policy)
		entry["key"] = key
		entry["change"] = change
		changes = append(changes, entry)
	}

	if jsonMode {
		return emitOK(map[string]any{
			"workspace": workspace, "config": cfgPath,
			"applied_at":         nullableString(appliedAt),
			"matches_last_start": len(changes) == 0,
			"changes":            changes,
			"module_plans":       validation.moduleValidationPlanDocument(),
		})
	}
	if len(changes) == 0 {
		fmt.Println("configuration matches the last successful start")
		fmt.Print(validation.moduleValidationPlanSummary())
		return nil
	}
	if appliedAt == "" {
		fmt.Println("no applied snapshot exists; treating this as initial configuration")
	} else {
		fmt.Println("last successful activation: " + appliedAt)
	}
	for _, entry := range changes {
		fmt.Printf("%-7s %-48s %-20s %s\n", entry["change"], entry["key"], entry["effect"], entry["apply"])
	}
	fmt.Print(validation.moduleValidationPlanSummary())
	return nil
}

// appliedSettingFingerprints reads the active immutable deployment first. The
// legacy config-applied.yml snapshot is only a compatibility fallback for old
// tests/workspaces; a successful deployment activation is now the authority on
// what configuration the runtime has observed.
func appliedSettingFingerprints(base string) (string, map[string]string, error) {
	active, err := loadActiveState(base)
	if err != nil {
		return "", nil, err
	}
	if active.ActiveDeployment != "" {
		manifest, err := loadDeploymentManifest(filepath.Join(base, "deployments", active.ActiveDeployment))
		if err != nil {
			return "", nil, err
		}
		values := make(map[string]string, len(manifest.Settings))
		for key, setting := range manifest.Settings {
			values[key] = setting.Fingerprint
		}
		at := active.VerifiedAt
		if at == "" {
			at = active.ActivatedAt
		}
		return at, values, nil
	}
	legacy, err := loadAppliedConfig(base)
	if err != nil {
		return "", nil, err
	}
	return legacy.AppliedAt, legacy.Values, nil
}
