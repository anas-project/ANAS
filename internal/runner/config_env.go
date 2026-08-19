package runner

import (
	"sort"
	"strings"

	"github.com/anas-project/ANAS/internal/config"
)

// configBaseEnvWithRegistry adapts the structured config through manifest
// metadata before any schema, resolver or render code consumes it. The config
// package deliberately knows nothing about Module manifests and therefore uses
// the module name as its fallback prefix; this layer remaps those structured
// values to each Module's declared env_prefix without naming any Module.
func configBaseEnvWithRegistry(cfg *config.File, reg map[string]Module) (map[string]string, map[string]string) {
	env, owners := cfg.BaseEnvWithOwnersUsing(func(rawName, rawParameter string) string {
		name := strings.ToLower(strings.TrimSpace(rawName))
		mod, ok := reg[name]
		if !ok {
			return ""
		}
		return moduleParamEnvKey(name, mod.EnvPrefix, mod.Exports, strings.ToLower(strings.TrimSpace(rawParameter)))
	})
	// Reserve config-core derived keys even when their conditional default is
	// currently absent. Hook write admission consults ownership independently of
	// value presence, preventing a Module's otherwise-valid own prefix from
	// manufacturing a global override.
	for _, key := range configCoreReservedRuntimeKeys() {
		if _, tracked := owners[key]; !tracked {
			owners[key] = globalScope
		}
	}
	secretKeys := map[string]bool{}
	for rawKey := range cfg.Secrets {
		secretKeys[config.EnvKey(rawKey)] = true
	}
	for rawKey := range cfg.Env {
		key := config.EnvKey(rawKey)
		if secretKeys[key] {
			continue
		}
		if owner, _, err := policyOwnerForEnv(key, reg); err == nil && owner != globalModuleName {
			owners[key] = owner
		}
	}
	return env, owners
}

func configBaseEnv(cfg *config.File, reg map[string]Module) map[string]string {
	env, _ := configBaseEnvWithRegistry(cfg, reg)
	return env
}

// moduleIdentityLoginParameter connects the structured identity address to the
// manifest selector that owns its runtime key. The config package publishes
// the structured value through its authoritative flattening implementation;
// the runner only accepts the projection when IAM metadata declares the same
// selector and runtime key. No Module name participates in the decision.
func moduleIdentityLoginParameter(module string, reg map[string]Module) (string, bool) {
	module = strings.ToLower(strings.TrimSpace(module))
	mod, ok := reg[module]
	if !ok {
		return "", false
	}
	selection := config.NewModuleSelection(module)
	selected := selection.Values[module]
	selected.Identity.LoginProtocol = "oidc"
	selection.Values[module] = selected
	view := &config.File{Modules: selection}
	env, owners := configBaseEnvWithRegistry(view, reg)
	runtimeKey := ""
	for key, value := range env {
		if value == "oidc" && owners[key] == module {
			runtimeKey = key
			break
		}
	}
	if runtimeKey == "" {
		return "", false
	}
	candidates := []string{}
	if mod.IdentityAuthentication != nil {
		candidates = append(candidates, mod.IdentityAuthentication.SelectedBy)
	}
	for _, requirement := range mod.RequiresCapabilities {
		if requirement.Name == capabilityIAM {
			candidates = append(candidates, requirement.InterfaceSelectedBy)
		}
	}
	candidates = uniqueStrings(candidates)
	sort.Strings(candidates)
	for _, parameter := range candidates {
		parameter = strings.ToLower(strings.TrimSpace(parameter))
		if parameter != "" && moduleParamEnvKey(module, mod.EnvPrefix, mod.Exports, parameter) == runtimeKey {
			return parameter, true
		}
	}
	return "", false
}

func moduleIdentityLoginRuntimeKey(module string, reg map[string]Module) (string, bool) {
	module = strings.ToLower(strings.TrimSpace(module))
	parameter, ok := moduleIdentityLoginParameter(module, reg)
	if !ok {
		return "", false
	}
	mod := reg[module]
	return moduleParamEnvKey(module, mod.EnvPrefix, mod.Exports, parameter), true
}

// bootstrapUsernameRuntimeKeys derives every runtime alias from config's
// flattening table. Today that includes the global bootstrap key and, when the
// configured registry exposes the matching declaration, a directory-admin
// compatibility alias. Keeping this as a projection avoids encoding any
// provider name in collision validation.
func bootstrapUsernameRuntimeKeys(reg map[string]Module) []string {
	const marker = "anas_bootstrap_runtime_key_probe"
	view := &config.File{
		Administration: config.Administration{
			Bootstrap: config.BootstrapAdministrator{Username: marker},
		},
	}
	env, _ := configBaseEnvWithRegistry(view, reg)
	keys := []string{}
	for key, value := range env {
		if value == marker {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	return uniqueStrings(keys)
}
