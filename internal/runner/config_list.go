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
	Path      string
	Module    string
	Parameter string
	EnvKey    string
	Default   string
	Value     string
	Set       bool
	Policy    ChangePolicy
}

// collectConfigParameters builds the inventory from the two declaration sites
// that survive: the embedded global schema and each module's manifest.
//
// Parameters are gathered by env key rather than by config name because the env
// key is what every declaration ultimately produces and what makes two spellings
// of one parameter the same entry. The addressable path is then recovered
// through resolveConfigTarget, so the path printed here is the path `set`
// accepts -- the two cannot drift, because they are the same function.
func collectConfigParameters(reg map[string]Module, settings map[string]string) ([]configListEntry, error) {
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
		if src.module == globalModuleName {
			entry.Default = globalConfig.Defaults[entry.EnvKey]
		} else {
			entry.Default = reg[src.module].Defaults[entry.EnvKey]
		}
		if value, ok := settings[strings.Join(target.YAMLPath, ".")]; ok {
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
func reportConfigList(cfgPath string, reg map[string]Module, scope string, jsonMode bool) error {
	settings := map[string]string{}
	if cfgPath != "" && exists(cfgPath) {
		loaded, err := config.Settings(cfgPath)
		if err != nil {
			return preconditionErrorf("config_invalid", "%s", err.Error())
		}
		settings = loaded
	}
	entries, err := collectConfigParameters(reg, settings)
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
			document["default"] = entry.Default
			document["set"] = entry.Set
			if entry.Set && !entry.Policy.Sensitive {
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
			placeholder(entry.Default), configListValue(entry), entry.Policy.Effect)
	}
	return w.Flush()
}

func configListValue(entry configListEntry) string {
	if entry.Policy.Sensitive {
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
