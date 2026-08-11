package runner

import (
	"flag"
	"fmt"
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
		return usageErrorf("usage: anas config set|explain|plan|secret ... [--json]")
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
	rootFlag := fs.String("root", "", "project root or cask bundle directory")
	registerJSONFlag(fs)
	positional, err := parseInterspersed(fs, args[1:])
	if err != nil {
		return usageErrorf("%s", err.Error())
	}
	root, err := locateCaskRoot(*rootFlag)
	if err != nil {
		return preconditionErrorf("cask_root_missing", "%s", err.Error())
	}
	reg, err := loadRegistryDir(root)
	if err != nil {
		return preconditionErrorf("cask_root_invalid", "%s", err.Error())
	}
	// `explain` only reads the cask registry, so it stays usable outside a
	// workspace; the other subcommands act on one and must resolve it.
	var workspace, base string
	if subcommand != "explain" {
		workspace, err = resolveWorkspace(*workspaceFlag)
		if err != nil {
			return usageErrorf("%s", err.Error())
		}
		base = stateDir(workspace)
		*cfgPath = absolutePath(configPathFor(workspace, *cfgPath))
	}

	switch subcommand {
	case "set":
		if len(positional) != 2 {
			return usageErrorf("usage: anas config set [-w <workspace>] [-c config.yml] <path> <value> [--json]")
		}
		target, err := resolveConfigTarget(positional[0], reg)
		if err != nil {
			return usageErrorf("%s", err.Error())
		}
		if !exists(*cfgPath) {
			return preconditionErrorf("config_missing", "config %s does not exist", *cfgPath)
		}
		if err := config.SetScalar(*cfgPath, target.YAMLPath, positional[1]); err != nil {
			return failuref("write_failed", "%s", err.Error())
		}
		policy := policyForTarget(target, reg)
		if jsonMode {
			return emitOK(map[string]any{
				"workspace": workspace, "config": *cfgPath,
				"setting": configTargetDocument(target, policy),
			})
		}
		fmt.Printf("updated %s\neffect: %s\napply: %s\n", target.Display, policy.Effect, policy.Apply)
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
			return emitOK(map[string]any{"setting": configTargetDocument(target, policy)})
		}
		fmt.Printf("path: %s\nmodule: %s\nparameter: %s\neffect: %s\napply: %s\nsensitive: %t\n", target.Display, target.Module, target.Parameter, policy.Effect, policy.Apply, policy.Sensitive)
		if policy.Description != "" {
			fmt.Println("description: " + policy.Description)
		}
		return nil
	case "plan":
		if len(positional) != 0 {
			return usageErrorf("usage: anas config plan [-w <workspace>] [-c config.yml] [--json]")
		}
		return reportConfigPlan(workspace, *cfgPath, base, reg, jsonMode)
	default:
		return usageErrorf("unknown config command %q; expected set, explain, plan or secret", subcommand)
	}
}

// configTargetDocument is the one shape `set` and `explain` both report, so a
// caller that can read one can read the other.
func configTargetDocument(target configTarget, policy ChangePolicy) map[string]any {
	return map[string]any{
		"path": target.Display, "module": target.Module, "parameter": target.Parameter,
		"effect": policy.Effect, "apply": policy.Apply,
		"sensitive": policy.Sensitive, "description": policy.Description,
	}
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
		for key := range store.values {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		if jsonMode {
			// Keys only. `list` exists so an operator can discover what was
			// generated; putting the values here would make a routine
			// inventory command leak every secret into whatever captured it.
			return emitOK(map[string]any{"workspace": workspace, "keys": keys})
		}
		for _, key := range keys {
			fmt.Println(key)
		}
		return nil
	case "get":
		if len(positional) != 1 {
			return usageErrorf("usage: anas config secret get <KEY> [-w <workspace>] [--json]")
		}
		value, ok := store.values[positional[0]]
		if !ok {
			return preconditionErrorf("secret_missing",
				"no generated secret %q; use `anas config secret list`", positional[0])
		}
		if jsonMode {
			return emitOK(map[string]any{"workspace": workspace, "key": positional[0], "value": value})
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
	if parts[0] == "global" {
		if len(parts) != 2 {
			return configTarget{}, fmt.Errorf("global config path must have two components")
		}
		return configTarget{YAMLPath: parts, Display: strings.Join(parts, "."), Module: "core", Parameter: strings.ToLower(parts[1])}, nil
	}
	if parts[0] == "env" {
		if len(parts) != 2 {
			return configTarget{}, fmt.Errorf("raw env config path must have two components")
		}
		module, parameter, err := policyOwnerForEnv(parts[1], reg)
		if err != nil {
			return configTarget{}, err
		}
		return configTarget{YAMLPath: parts, Display: strings.Join(parts, "."), Module: module, Parameter: parameter}, nil
	}
	if parts[0] == "services" {
		if len(parts) == 4 && parts[2] == "env" {
			parts = []string{parts[0], parts[1], parts[3]}
		}
		if len(parts) != 3 {
			return configTarget{}, fmt.Errorf("service config path must be services.<module>.<parameter>")
		}
		if _, ok := reg[parts[1]]; !ok {
			return configTarget{}, fmt.Errorf("unknown module %q", parts[1])
		}
		yamlPath := []string{"services", parts[1], "env", parts[2]}
		return configTarget{YAMLPath: yamlPath, Display: parts[1] + "." + parts[2], Module: parts[1], Parameter: strings.ToLower(parts[2])}, nil
	}
	module := parts[0]
	if _, ok := reg[module]; !ok {
		return configTarget{}, fmt.Errorf("unknown module %q", module)
	}
	if len(parts) != 2 {
		return configTarget{}, fmt.Errorf("module config path must have two components")
	}
	parameter := strings.ToLower(parts[1])
	if module == "core" {
		if isGlobalParameter(parameter) {
			return configTarget{YAMLPath: []string{"global", parameter}, Display: "global." + parameter, Module: module, Parameter: parameter}, nil
		}
		key := config.EnvKey(parameter)
		return configTarget{YAMLPath: []string{"env", key}, Display: "env." + key, Module: module, Parameter: parameter}, nil
	}
	return configTarget{YAMLPath: []string{"services", module, "env", parameter}, Display: module + "." + parameter, Module: module, Parameter: parameter}, nil
}

func isGlobalParameter(parameter string) bool {
	switch parameter {
	case "domain", "email", "timezone", "container_prefix", "image_prefix", "network_prefix", "host_ip", "dns_server", "default_service_root_password":
		return true
	default:
		return false
	}
}

func policyOwnerForEnv(key string, reg map[string]Module) (string, string, error) {
	type owner struct{ module, parameter string }
	matches := []owner{}
	names := make([]string, 0, len(reg))
	for name := range reg {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		mod := reg[name]
		parameters := make([]string, 0, len(mod.Changes))
		for parameter := range mod.Changes {
			parameters = append(parameters, parameter)
		}
		sort.Strings(parameters)
		for _, parameter := range parameters {
			if strings.EqualFold(paramEnvKey(name, mod.EnvPrefix, parameter), key) {
				matches = append(matches, owner{name, parameter})
			}
		}
	}
	switch len(matches) {
	case 0:
		return "core", strings.ToLower(key), nil
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
	if mod, ok := reg[target.Module]; ok {
		if policy, ok := mod.Changes[strings.ToLower(target.Parameter)]; ok {
			return policy
		}
	}
	return ChangePolicy{
		Effect: "container_recreate", Apply: "render-and-recreate",
		Description: "No specialized reconciler is declared; recreate the affected container to apply rendered configuration.",
	}
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
	if len(parts) == 4 && parts[0] == "services" && parts[2] == "env" {
		target, _ := resolveConfigTarget("services."+parts[1]+"."+parts[3], reg)
		return target
	}
	return configTarget{Display: path, Module: "core", Parameter: path}
}

func reportConfigPlan(workspace, cfgPath, base string, reg map[string]Module, jsonMode bool) error {
	if !exists(cfgPath) {
		return preconditionErrorf("config_missing", "config %s does not exist", cfgPath)
	}
	settings, err := config.Settings(cfgPath)
	if err != nil {
		return preconditionErrorf("config_invalid", "%s", err.Error())
	}
	state, err := loadAppliedConfig(base)
	if err != nil {
		return preconditionErrorf("state_unreadable", "%s", err.Error())
	}
	keys := map[string]bool{}
	for key := range settings {
		keys[key] = true
	}
	for key := range state.Values {
		keys[key] = true
	}
	changed := []string{}
	for key := range keys {
		if hashSetting(settings[key]) != state.Values[key] {
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
		} else if _, ok := state.Values[key]; !ok {
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
			"applied_at":         nullableString(state.AppliedAt),
			"matches_last_start": len(changes) == 0,
			"changes":            changes,
		})
	}
	if len(changes) == 0 {
		fmt.Println("configuration matches the last successful start")
		return nil
	}
	if state.AppliedAt == "" {
		fmt.Println("no applied snapshot exists; treating this as initial configuration")
	} else {
		fmt.Println("last successful start: " + state.AppliedAt)
	}
	for _, entry := range changes {
		fmt.Printf("%-7s %-48s %-20s %s\n", entry["change"], entry["key"], entry["effect"], entry["apply"])
	}
	return nil
}
