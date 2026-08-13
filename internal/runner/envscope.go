package runner

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/anas-project/ANAS/internal/config"
)

// Environment scoping. Every key in the flat environment has an owner:
// globalScope for the deployment's own parameters and everything the runner
// derives about the host, a module name for values that module introduced, or
// config.OwnerUserSecret for user secrets.
// A module's rendered .env — and therefore its containers and its
// render/services/after_start hook input — only receives keys that are
// global, produced inside its dependency closure, matching its own or a
// closure member's env prefix, or explicitly claimed through manifest
// `config.consumes`. The calculate phase intentionally still sees the full
// accumulating environment: it is the derivation stage, and its writes are
// constrained instead (own prefix or manifest `config.exports`).

func (a *app) setEnvOwner(key, owner string) {
	if a.envOwner == nil {
		a.envOwner = map[string]string{}
	}
	if _, ok := a.envOwner[key]; !ok {
		a.envOwner[key] = owner
	}
}

// depClosure returns the transitive dependency closure of a module, including
// the module itself. Globally owned keys are not part of it: they are visible
// through their ownership, not through an edge every module had to be given.
func (a *app) depClosure(name string) map[string]bool {
	out := map[string]bool{name: true}
	var visit func(string)
	visit = func(n string) {
		for _, dep := range a.deps[n] {
			if !out[dep] {
				out[dep] = true
				visit(dep)
			}
		}
	}
	visit(name)
	return out
}

// sensitiveEnvKeySet identifies env keys that must not cross a module boundary
// merely through dependency-closure or prefix membership, even though a
// dependent module can freely read them during its own calculate phase. A key
// is sensitive when either signal holds:
//
//   - its owning module marks the source parameter `sensitive: true` in
//     manifest `config.changes` (covers user-rotatable credentials such as
//     admin passwords that are not necessarily secret-store generated), or
//   - its current value is identical to a value held in the generated secret
//     store (covers TURN_SECRET, database passwords, and any alias of them
//     such as MYSQL_PASSWORD mirroring MARIADB_ROOT_PASSWORD).
//
// A module that genuinely needs a sensitive value belonging to a dependency
// (a real LDAP bind, a real domain join, a real database connection) must
// claim it explicitly through manifest `config.consumes`.
func (a *app) sensitiveEnvKeySet() map[string]bool {
	if a.sensitiveKeys != nil {
		return a.sensitiveKeys
	}
	out := map[string]bool{}
	for key := range a.runnerSensitive {
		out[key] = true
	}
	for param, policy := range globalConfig.Changes {
		if policy.Sensitive {
			out[paramEnvKey(globalScope, "", param)] = true
		}
	}
	for name, mod := range a.reg {
		for param, policy := range mod.Changes {
			if !policy.Sensitive {
				continue
			}
			out[moduleParamEnvKey(name, mod.EnvPrefix, mod.Exports, param)] = true
		}
	}
	if a.secrets != nil {
		values := map[string]bool{}
		for key, v := range a.secrets.values {
			out[key] = true
			if v != "" {
				values[v] = true
			}
		}
		for k, v := range a.env {
			if v != "" && values[v] {
				out[k] = true
			}
		}
	}
	a.sensitiveKeys = out
	return out
}

// wideEnvScopeRequested restores the pre-declaration rule for one run, so a
// deployment can be rendered both ways and diffed. It is a diagnostic, not a
// supported configuration: what a module receives should be a property of its
// manifest, not of the environment the runner happened to start in.
func wideEnvScopeRequested() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("ANAS_WIDE_ENV_SCOPE"))) {
	case "1", "true", "yes":
		return true
	default:
		return false
	}
}

// envScopeFor returns the membership test for one module's environment scope.
func (a *app) envScopeFor(name string) func(key string) bool {
	a.narrowFileScope = !wideEnvScopeRequested()
	sensitive := a.sensitiveEnvKeySet()
	ownPrefixes := []string{}
	if mod, ok := a.reg[name]; ok {
		ownPrefixes = []string{mod.EnvPrefix + "_", defaultEnvPrefix(name) + "_"}
	}
	isOwn := func(key string) bool {
		if owner, tracked := a.envOwner[key]; tracked && owner == name {
			return true
		}
		for _, prefix := range ownPrefixes {
			if strings.HasPrefix(key, prefix) {
				return true
			}
		}
		return false
	}
	closure := a.depClosure(name)
	prefixes := []string{}
	for member := range closure {
		if mod, ok := a.reg[member]; ok {
			prefixes = append(prefixes, mod.EnvPrefix+"_", defaultEnvPrefix(member)+"_")
		}
	}
	consumes := a.reg[name].Consumes
	return func(key string) bool {
		if isOwn(key) {
			return true
		}
		if sensitive[key] {
			// A sensitive value owned by another module crosses the boundary
			// only through an explicit claim, regardless of closure or
			// prefix membership.
			return matchEnvPattern(consumes, key)
		}
		if owner, tracked := a.envOwner[key]; tracked && owner != config.OwnerUserSecret && owner == globalScope {
			return true
		}
		if !a.narrowFileScope {
			// The wide rule: anything owned by, or prefixed like, a member of
			// the dependency closure. It answers "who might be relevant" rather
			// than "who is needed", and the gap between those is most of what a
			// container receives -- collabora reads 19 of the 264 variables this
			// admits. Retained only so a rendering can be compared against it.
			if owner, tracked := a.envOwner[key]; tracked && owner != config.OwnerUserSecret && closure[owner] {
				return true
			}
			for _, prefix := range prefixes {
				if strings.HasPrefix(key, prefix) {
					return true
				}
			}
		}
		return matchEnvPattern(consumes, key)
	}
}

// globalEnv is the deployment-wide environment: every key nobody owns
// privately. It is written next to the rendered modules so artifact
// start/stop/rollback can reconstruct what the release was built with without
// re-reading the config, which is what the "core" module's .env used to be for.
func (a *app) globalEnv() map[string]string {
	out := map[string]string{}
	for k, v := range a.env {
		if owner, tracked := a.envOwner[k]; tracked && owner == globalScope {
			out[k] = v
		}
	}
	return out
}

// scopedEnv filters the full environment down to one module's scope.
func (a *app) scopedEnv(name string) map[string]string {
	scope := a.envScopeFor(name)
	out := map[string]string{}
	for k, v := range a.env {
		if scope(k) {
			out[k] = v
		}
	}
	return out
}

// scopedSecrets filters generated secrets with the same scope rules.
func (a *app) scopedSecrets(name string) map[string]string {
	scope := a.envScopeFor(name)
	out := map[string]string{}
	for k, v := range a.secrets.clone() {
		if scope(k) {
			out[k] = v
		}
	}
	return out
}

// applyModuleRootPassword sets DEFAULT_SERVICE_ROOT_PASSWORD for one module's
// calculation or render. When the user configured a shared password it is
// used unchanged; otherwise each module gets its own generated password,
// persisted as <PREFIX>_DEFAULT_ROOT_PASSWORD in the secret store and
// readable through `anas config secret get`.
func (a *app) applyModuleRootPassword(env map[string]string, name string) error {
	value := a.cfg.Global.DefaultServiceRootPassword
	if value == "" {
		prefix := defaultEnvPrefix(name)
		if mod, ok := a.reg[name]; ok && mod.EnvPrefix != "" {
			prefix = mod.EnvPrefix
		}
		generated, err := a.secrets.Ensure(prefix+"_DEFAULT_ROOT_PASSWORD", func() (string, error) {
			return randomPassword(20)
		})
		if err != nil {
			return err
		}
		value = generated
	}
	env["DEFAULT_SERVICE_ROOT_PASSWORD"] = value
	a.setEnvOwner("DEFAULT_SERVICE_ROOT_PASSWORD", globalScope)
	return nil
}

// applyCalculatePatch merges a calculate hook's env patch into the global
// environment, records ownership, and enforces the module's write contract: a
// module may only publish keys under its own prefixes, keys it already owns, or
// keys declared in manifest `config.exports`. There is no module exempt from
// this; the deployment-wide values it used to cover are written by the runner,
// which does not go through a hook patch at all.
func (a *app) applyCalculatePatch(mod Module, patch map[string]string) error {
	violations := []string{}
	ownPrefixes := []string{mod.EnvPrefix + "_", defaultEnvPrefix(mod.Name) + "_"}
	allowed := func(key string) bool {
		for _, prefix := range ownPrefixes {
			if strings.HasPrefix(key, prefix) {
				return true
			}
		}
		if owner, ok := a.envOwner[key]; ok && owner == mod.Name {
			return true
		}
		return matchEnvPattern(mod.Exports, key)
	}
	keys := make([]string, 0, len(patch))
	for k := range patch {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if !allowed(k) {
			violations = append(violations, k)
			continue
		}
		a.env[k] = patch[k]
		a.setEnvOwner(k, mod.Name)
	}
	if len(violations) > 0 {
		return fmt.Errorf("module %q calculate hook writes undeclared env keys: %s (declare them in module.yml config.exports)", mod.Name, strings.Join(violations, ", "))
	}
	return nil
}
