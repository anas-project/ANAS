package runner

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/whlsxl/anas/internal/config"
)

type configTarget struct {
	YAMLPath  []string
	Display   string
	Module    string
	Parameter string
}

func runConfig(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("config requires set, explain, or plan")
	}
	subcommand := args[0]
	fs := flag.NewFlagSet("config "+subcommand, flag.ContinueOnError)
	cfgPath := fs.String("c", "config.yml", "config file")
	fs.StringVar(cfgPath, "config", "config.yml", "config file")
	base := fs.String("b", "", "runtime base path")
	fs.StringVar(base, "base", "", "runtime base path")
	rootFlag := fs.String("root", "", "project root containing casks/mods")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	root, err := locateRoot(*rootFlag)
	if err != nil {
		return err
	}
	reg, err := loadRegistry(root)
	if err != nil {
		return err
	}
	if *base == "" {
		home, _ := os.UserHomeDir()
		*base = filepath.Join(home, ".anas")
	}

	switch subcommand {
	case "set":
		if fs.NArg() != 2 {
			return fmt.Errorf("usage: anas config set [-c config.yml] <path> <value>")
		}
		target, err := resolveConfigTarget(fs.Arg(0), reg)
		if err != nil {
			return err
		}
		if err := config.SetScalar(*cfgPath, target.YAMLPath, fs.Arg(1)); err != nil {
			return err
		}
		policy := policyForTarget(target, reg)
		fmt.Printf("updated %s\neffect: %s\napply: %s\n", target.Display, policy.Effect, policy.Apply)
		if policy.Description != "" {
			fmt.Println(policy.Description)
		}
		return nil
	case "explain":
		if fs.NArg() != 1 {
			return fmt.Errorf("usage: anas config explain <path>")
		}
		target, err := resolveConfigTarget(fs.Arg(0), reg)
		if err != nil {
			return err
		}
		policy := policyForTarget(target, reg)
		fmt.Printf("path: %s\nmodule: %s\nparameter: %s\neffect: %s\napply: %s\nsensitive: %t\n", target.Display, target.Module, target.Parameter, policy.Effect, policy.Apply, policy.Sensitive)
		if policy.Description != "" {
			fmt.Println("description: " + policy.Description)
		}
		return nil
	case "plan":
		if fs.NArg() != 0 {
			return fmt.Errorf("usage: anas config plan [-c config.yml] [-b ~/.anas]")
		}
		return printConfigPlan(*cfgPath, *base, reg)
	default:
		return fmt.Errorf("unknown config command %q", subcommand)
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
		module, parameter := policyOwnerForEnv(parts[1], reg)
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
	case "domain", "email", "data_path", "timezone", "container_prefix", "image_prefix", "network_prefix", "host_ip", "dns_provider", "dns_server", "default_service_root_password":
		return true
	default:
		return false
	}
}

func policyOwnerForEnv(key string, reg map[string]Module) (string, string) {
	for name, mod := range reg {
		for parameter := range mod.Changes {
			if strings.EqualFold(paramEnvKey(name, mod.EnvPrefix, parameter), key) {
				return name, parameter
			}
		}
	}
	return "core", strings.ToLower(key)
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

func printConfigPlan(cfgPath, base string, reg map[string]Module) error {
	settings, err := config.Settings(cfgPath)
	if err != nil {
		return err
	}
	state, err := loadAppliedConfig(base)
	if err != nil {
		return err
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
	if len(changed) == 0 {
		fmt.Println("configuration matches the last successful start")
		return nil
	}
	if state.AppliedAt == "" {
		fmt.Println("no applied snapshot exists; treating this as initial configuration")
	} else {
		fmt.Println("last successful start: " + state.AppliedAt)
	}
	for _, key := range changed {
		target := targetForSettingPath(key, reg)
		policy := policyForTarget(target, reg)
		change := "change"
		if _, ok := settings[key]; !ok {
			change = "remove"
		} else if _, ok := state.Values[key]; !ok {
			change = "add"
		}
		fmt.Printf("%-7s %-48s %-20s %s\n", change, key, policy.Effect, policy.Apply)
	}
	return nil
}
