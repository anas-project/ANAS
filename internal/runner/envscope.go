package runner

import (
	"fmt"
	"sort"
	"strings"

	"github.com/whlsxl/anas/internal/config"
)

// Environment scoping. Every key in the flat environment has an owner: ""
// for global config sections, "core" for the base cask, a cask name for
// values that cask introduced, or config.OwnerUserSecret for user secrets.
// A cask's rendered .env — and therefore its containers and its
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

// depClosure returns the transitive dependency closure of a cask, including
// the cask itself and core.
func (a *app) depClosure(name string) map[string]bool {
	out := map[string]bool{"core": true, name: true}
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

// sensitiveEnvKeySet identifies env keys that must not cross a cask boundary
// merely through dependency-closure or prefix membership, even though a
// dependent cask can freely read them during its own calculate phase. A key
// is sensitive when either signal holds:
//
//   - its owning cask marks the source parameter `sensitive: true` in
//     manifest `config.changes` (covers user-rotatable credentials such as
//     admin passwords that are not necessarily secret-store generated), or
//   - its current value is identical to a value held in the generated secret
//     store (covers TURN_SECRET, database passwords, and any alias of them
//     such as MYSQL_PASSWORD mirroring MARIADB_ROOT_PASSWORD).
//
// A cask that genuinely needs a sensitive value belonging to a dependency
// (a real LDAP bind, a real domain join, a real database connection) must
// claim it explicitly through manifest `config.consumes`.
func (a *app) sensitiveEnvKeySet() map[string]bool {
	if a.sensitiveKeys != nil {
		return a.sensitiveKeys
	}
	out := map[string]bool{}
	for name, mod := range a.reg {
		for param, policy := range mod.Changes {
			if !policy.Sensitive {
				continue
			}
			out[paramEnvKey(name, mod.EnvPrefix, param)] = true
		}
	}
	if a.secrets != nil {
		values := map[string]bool{}
		for _, v := range a.secrets.values {
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

// envScopeFor returns the membership test for one cask's environment scope.
func (a *app) envScopeFor(name string) func(key string) bool {
	sensitive := a.sensitiveEnvKeySet()
	if name == "core" {
		// core has no containers; its .env is the release's global environment
		// snapshot used by artifact start/stop, so it carries exactly the
		// global and core-derived keys.
		return func(key string) bool {
			owner, tracked := a.envOwner[key]
			return tracked && (owner == "" || owner == "core")
		}
	}
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
			// A sensitive value owned by another cask crosses the boundary
			// only through an explicit claim, regardless of closure or
			// prefix membership.
			return matchEnvPattern(consumes, key)
		}
		if owner, tracked := a.envOwner[key]; tracked && owner != config.OwnerUserSecret && (owner == "" || closure[owner]) {
			return true
		}
		for _, prefix := range prefixes {
			if strings.HasPrefix(key, prefix) {
				return true
			}
		}
		return matchEnvPattern(consumes, key)
	}
}

// scopedEnv filters the full environment down to one cask's scope.
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

// applyCaskRootPassword sets DEFAULT_SERVICE_ROOT_PASSWORD for one cask's
// calculation or render. When the user configured a shared password it is
// used unchanged; otherwise each cask gets its own generated password,
// persisted as <PREFIX>_DEFAULT_ROOT_PASSWORD in the secret store and
// readable through `anas config secret get`.
func (a *app) applyCaskRootPassword(env map[string]string, name string) error {
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
	a.setEnvOwner("DEFAULT_SERVICE_ROOT_PASSWORD", "")
	return nil
}

// applyCalculatePatch merges a calculate hook's env patch into the global
// environment, records ownership, and enforces the cask's write contract:
// non-core casks may only publish keys under their own prefixes, keys they
// already own, or keys declared in manifest `config.exports`.
func (a *app) applyCalculatePatch(mod Module, patch map[string]string) error {
	violations := []string{}
	ownPrefixes := []string{mod.EnvPrefix + "_", defaultEnvPrefix(mod.Name) + "_"}
	allowed := func(key string) bool {
		if mod.Name == "core" {
			return true
		}
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
		owner := mod.Name
		if mod.Name == "core" {
			owner = "core"
		}
		a.setEnvOwner(k, owner)
	}
	if len(violations) > 0 {
		return fmt.Errorf("cask %q calculate hook writes undeclared env keys: %s (declare them in cask.yml config.exports)", mod.Name, strings.Join(violations, ", "))
	}
	return nil
}
