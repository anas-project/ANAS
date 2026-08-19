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

func (a *app) claimUserSecretOwner(key, owner string) {
	if a.envOwner == nil {
		a.envOwner = map[string]string{}
	}
	if current, ok := a.envOwner[key]; !ok || current == config.OwnerUserSecret {
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
	if a.cfg != nil {
		for key := range a.cfg.Secrets {
			out[config.EnvKey(key)] = true
		}
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
		secretValues := map[string]bool{}
		for key, value := range a.secrets.values {
			out[key] = true
			addSensitiveValueForms(secretValues, value)
		}
		for key, value := range a.env {
			if matchesSensitiveValue(secretValues, value) {
				out[key] = true
			}
		}
	}
	markSensitiveValueAliases(a.env, out)
	a.sensitiveKeys = out
	return out
}

// markSensitiveValueAliases extends source provenance across equivalent
// runtime spellings. A secret may be copied into a compatibility alias or a
// selector key before schema validation; redacting only the original key would
// then expose the same plaintext under the alias. Empty values are ignored so
// ordinary unset parameters do not taint one another.
func markSensitiveValueAliases(values map[string]string, sensitive map[string]bool) {
	secretValues := map[string]bool{}
	for key := range sensitive {
		addSensitiveValueForms(secretValues, values[key])
	}
	if len(secretValues) == 0 {
		return
	}
	for key, value := range values {
		if matchesSensitiveValue(secretValues, value) {
			sensitive[key] = true
		}
	}
}

func addSensitiveValueForms(values map[string]bool, value string) {
	if value == "" {
		return
	}
	values[value] = true
	if normalized := strings.TrimSpace(value); normalized != "" {
		values[normalized] = true
	}
}

func matchesSensitiveValue(values map[string]bool, value string) bool {
	if value == "" {
		return false
	}
	if values[value] {
		return true
	}
	normalized := strings.TrimSpace(value)
	return normalized != "" && values[normalized]
}

// hookPatchSensitiveEnv returns a private sensitivity view for validating Hook
// output. Hook patches can introduce a new runtime spelling after the normal
// sensitivity set has already been cached, so rebuild from the post-patch
// environment and carry provenance across equal values. Pending Hook secrets
// are included before they enter the Secret Store so a schema error cannot
// print either the canonical secret or an Env alias containing the same value.
func (a *app) hookPatchSensitiveEnv(values, pendingSecrets map[string]string) map[string]bool {
	// A successful Hook Env merge changes the values from which alias
	// sensitivity is derived. Never reuse a pre-patch cache.
	a.sensitiveKeys = nil
	base := a.sensitiveEnvKeySet()
	sensitive := make(map[string]bool, len(base)+len(pendingSecrets))
	for key := range base {
		sensitive[key] = true
	}
	secretValues := map[string]bool{}
	// The target can be a render-scoped environment. Hooks receive Secret Store
	// values through a separate request field, so the canonical secret key need
	// not be present in values even though the Hook can copy its plaintext into
	// a newly returned env alias. Derive the value provenance from the complete
	// authoritative runtime view, not only from the target map being validated.
	for key := range base {
		addSensitiveValueForms(secretValues, a.env[key])
	}
	if a.secrets != nil {
		for _, value := range a.secrets.values {
			addSensitiveValueForms(secretValues, value)
		}
	}
	if a.cfg != nil {
		for _, raw := range a.cfg.Secrets {
			addSensitiveValueForms(secretValues, config.Scalar(raw))
		}
	}
	for key, value := range pendingSecrets {
		sensitive[key] = true
		addSensitiveValueForms(secretValues, value)
	}
	markSensitiveValueAliases(values, sensitive)
	for key, value := range values {
		if matchesSensitiveValue(secretValues, value) {
			sensitive[key] = true
		}
	}
	return sensitive
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
		if sensitive[key] {
			if owner, tracked := a.envOwner[key]; tracked && owner == name {
				return true
			}
			if owner, _, err := policyOwnerForEnv(key, a.reg); err == nil && owner == name {
				return true
			}
			// A sensitive value owned by another module crosses the boundary
			// only through an explicit claim, regardless of closure or
			// prefix membership.
			return matchEnvPattern(consumes, key)
		}
		if isOwn(key) {
			return true
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
		// Persisted Secret Store ownership is authoritative. In particular, a
		// module Hook cannot make a secret visible to another module merely by
		// choosing that module's prefix. Cross-module delivery remains possible,
		// but only through the consumer's explicit config.consumes declaration.
		if meta, tracked := a.secrets.metadata[k]; tracked && meta.Owner != "" && meta.Owner != runnerScope {
			if meta.Owner == name || matchEnvPattern(a.reg[name].Consumes, k) {
				out[k] = v
			}
			continue
		}
		if scope(k) {
			out[k] = v
		}
	}
	return out
}

// moduleHookMayWriteKey is the common declaration-side authorization for all
// calculate Hook outputs. Env and Secret patches intentionally share this
// rule: a module may write its own namespace, a key it already owns, or a key
// it explicitly publishes through config.exports.
func moduleHookMayWriteKey(mod Module, key string, alreadyOwned bool) bool {
	for _, prefix := range uniqueStrings([]string{mod.EnvPrefix, defaultEnvPrefix(mod.Name)}) {
		if prefix != "" && strings.HasPrefix(key, prefix+"_") {
			return true
		}
	}
	return alreadyOwned || matchEnvPattern(mod.Exports, key)
}

// validateCalculateSecretPatch applies the same write contract as an Env
// patch, then enforces both runtime-env and persisted-secret ownership. It is
// deliberately validation-only so calculate can reject the complete Hook
// response before mutating either destination.
func (a *app) validateCalculateSecretPatch(mod Module, patch map[string]string) error {
	violations := []string{}
	conflicts := []string{}
	keys := make([]string, 0, len(patch))
	for key := range patch {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		envOwner, envTracked := a.envOwner[key]
		meta, secretTracked := a.secrets.metadata[key]
		alreadyOwned := (envTracked && envOwner == mod.Name) || (secretTracked && meta.Owner == mod.Name)
		if !moduleHookMayWriteKey(mod, key, alreadyOwned) {
			violations = append(violations, key)
			continue
		}

		// Empty values and identical existing values are no-ops. Keeping them
		// idempotent avoids changing metadata or leaking ownership information.
		value := patch[key]
		existing, exists := a.secrets.values[key]
		mutates := value != "" && (!exists || existing != value)
		if mutates && envTracked && envOwner != mod.Name {
			conflicts = append(conflicts, key)
		}
	}
	if len(violations) > 0 {
		return fmt.Errorf("module %q calculate hook writes undeclared secret keys: %s (declare them in module.yml config.exports)", mod.Name, strings.Join(violations, ", "))
	}
	if len(conflicts) > 0 {
		return fmt.Errorf("module %q calculate hook tries to write secret keys owned by another source: %s", mod.Name, strings.Join(conflicts, ", "))
	}
	return a.secrets.validateCanonicalHookSecretPatch(mod.Name, patch)
}

// applyCalculatePatch merges a calculate hook's env patch into the global
// environment, records ownership, and enforces the module's write contract: a
// module may only publish keys under its own prefixes, keys it already owns, or
// keys declared in manifest `config.exports`. There is no module exempt from
// this; the deployment-wide values it used to cover are written by the runner,
// which does not go through a hook patch at all.
func (a *app) applyCalculatePatch(mod Module, patch map[string]string) error {
	violations := []string{}
	conflicts := []string{}
	invalidApplicationLists := []string{}
	keys := make([]string, 0, len(patch))
	for k := range patch {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if !envKeyPattern.MatchString(k) {
			continue
		}
		owner, tracked := a.envOwner[k]
		if !moduleHookMayWriteKey(mod, k, tracked && owner == mod.Name) {
			violations = append(violations, k)
			continue
		}
		if k == "APPS_LIST" {
			if !validApplicationListAppend(mod.Name, a.env[k], patch[k]) {
				invalidApplicationLists = append(invalidApplicationLists, k)
			}
			continue
		}
		if tracked && owner != mod.Name {
			conflicts = append(conflicts, k)
		}
	}
	if invalid := invalidHookEnvKeys(patch); len(invalid) > 0 {
		return fmt.Errorf("module %q calculate hook returned invalid env keys: %s", mod.Name, strings.Join(invalid, ", "))
	}
	if len(violations) > 0 {
		return fmt.Errorf("module %q calculate hook writes undeclared env keys: %s (declare them in module.yml config.exports)", mod.Name, strings.Join(violations, ", "))
	}
	if len(invalidApplicationLists) > 0 {
		return fmt.Errorf("module %q calculate hook must preserve APPS_LIST and append only its own module name", mod.Name)
	}
	if len(conflicts) > 0 {
		return fmt.Errorf("module %q calculate hook tries to overwrite env keys owned by another source: %s", mod.Name, strings.Join(conflicts, ", "))
	}
	for _, k := range keys {
		a.env[k] = patch[k]
		if k == "APPS_LIST" {
			// APPS_LIST is a transitional, cooperative launcher protocol. Each
			// publisher may append only itself; the runner owns the aggregate so
			// no one Module can later replace or delete another Module's entry.
			if a.envOwner == nil {
				a.envOwner = map[string]string{}
			}
			a.envOwner[k] = runnerScope
		} else {
			a.setEnvOwner(k, mod.Name)
		}
	}
	if len(keys) > 0 {
		// sensitiveEnvKeySet derives equal-value aliases from a.env. A Hook can
		// add such an alias after the set was cached earlier in calculate.
		a.sensitiveKeys = nil
	}
	return nil
}

func validApplicationListAppend(module, current, next string) bool {
	currentItems, currentOK := applicationListItems(current)
	nextItems, nextOK := applicationListItems(next)
	if !currentOK || !nextOK {
		return false
	}
	want := append([]string{}, currentItems...)
	if !contains(want, module) {
		want = append(want, module)
	}
	if len(want) != len(nextItems) {
		return false
	}
	for i := range want {
		if want[i] != nextItems[i] {
			return false
		}
	}
	return true
}

func applicationListItems(value string) ([]string, bool) {
	items := []string{}
	seen := map[string]bool{}
	for _, raw := range strings.Split(value, ",") {
		item := strings.TrimSpace(raw)
		if item == "" {
			continue
		}
		if seen[item] {
			return nil, false
		}
		seen[item] = true
		items = append(items, item)
	}
	return items, true
}
