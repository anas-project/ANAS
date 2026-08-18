package runner

import (
	"fmt"
	"sort"
	"strings"

	"github.com/anas-project/ANAS/internal/config"
	"github.com/anas-project/ANAS/internal/dns"
)

// DNS credential materialisation.
//
// A deployment may drive several engines against the same DNS vendor -- lego
// for ACME DNS-01, one or two DDNS implementations for A/AAAA records -- and
// whether they can share one credential depends on the vendor. Tencent Cloud
// uses the same API key for both; Namecheap's DNS API user is a different
// object from its Dynamic DNS password, and the same account cannot produce
// one from the other.
//
// Rather than expose that distinction as a mode the user must choose, both
// spellings are accepted and normalised to the same place:
//
//	secrets:
//	  tencentcloud_secret_id: ...        # shared: every engine that asks for it
//	  ddns_go_namecheap_ddns_password: ... # separate: this engine only
//
// Each engine ends up with <ITS_PREFIX>_<CANONICAL_KEY>, owned by its own
// module. That is deliberately the same shape the env scope rules already use
// for a module's own variables (see envScopeFor), so isolation between engines
// needs no new mechanism and no `config.consumes` entry: a key prefixed
// DDNS_GO_ cannot reach lego, because prefix ownership already says so.
//
// Materialising also means the canonical, unprefixed secret is never delivered
// to any module. It exists only as an input spelling.

// dnsRegistry loads the platform registry once per run.
func (a *app) dnsRegistry() (*dns.Registry, error) {
	if a.dnsReg != nil {
		return a.dnsReg, nil
	}
	reg, err := dns.Load()
	if err != nil {
		return nil, err
	}
	a.dnsReg = reg
	return reg, nil
}

// materializeDNSCredentials resolves each active engine's configured platform
// and publishes that platform's credentials under the engine's env prefix. It
// runs before any hook, so an unknown platform or a half-configured credential
// is a configuration error reported by `plan`, not an authentication failure
// discovered by a container in a restart loop.
func (a *app) materializeDNSCredentials() error {
	engines := []string{}
	for _, engine := range dns.Engines() {
		if contains(a.order, engine) {
			engines = append(engines, engine)
		}
	}
	if len(engines) == 0 {
		return nil
	}
	registry, err := a.dnsRegistry()
	if err != nil {
		return err
	}
	for _, engine := range engines {
		if err := a.materializeEngineCredentials(registry, engine); err != nil {
			return err
		}
	}
	return nil
}

func (a *app) materializeEngineCredentials(registry *dns.Registry, engine string) error {
	prefix := a.envPrefixFor(engine)
	configured := strings.TrimSpace(a.env[prefix+"_DNS_PROVIDER"])
	if configured == "" {
		// Whether a platform is mandatory is the module's own business, declared
		// through its generic config requirement metadata and enforced at the
		// matching resolution stage.
		return nil
	}
	platform, ok := registry.Lookup(configured)
	if !ok {
		return fmt.Errorf("%s: dns_provider %q is not a known DNS platform;\nset services.%s.env.dns_provider to one of: %s",
			engine, configured, engine, strings.Join(registry.NamesFor(engine), ", "))
	}
	if !platform.Supports(engine) {
		return fmt.Errorf("%s: dns_provider %q is a known platform, but %s cannot address it;\nset services.%s.env.dns_provider to one of: %s",
			engine, platform.Name, engine, engine, strings.Join(registry.NamesFor(engine), ", "))
	}
	// Record the resolved canonical name so a hook never has to redo alias
	// resolution, and so `plan` output shows what an alias resolved to.
	a.env[prefix+"_DNS_PLATFORM"] = platform.Name
	a.setEnvOwner(prefix+"_DNS_PLATFORM", engine)

	missing := []string{}
	for _, key := range platform.AllKeys(engine) {
		target := prefix + "_" + key
		if strings.TrimSpace(a.env[target]) != "" {
			// Explicitly given for this engine. Claim ownership so the value
			// is scoped to this module even if it arrived as a bare user secret.
			a.setEnvOwner(target, engine)
			a.markSensitive(target)
			continue
		}
		if shared := strings.TrimSpace(a.env[key]); shared != "" {
			a.env[target] = a.env[key]
			a.setEnvOwner(target, engine)
			a.markSensitive(target)
			continue
		}
		if contains(platform.RequiredKeys(engine), key) {
			missing = append(missing, key)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("%s: dns_provider %q needs %s;\nadd %s under secrets: to share it with every engine using this platform, or %s to give %s its own account",
			engine, platform.Name, strings.Join(missing, ", "),
			strings.ToLower(missing[0]), strings.ToLower(prefix+"_"+missing[0]), engine)
	}
	return nil
}

// markSensitive records a key that must not cross a module boundary through
// dependency-closure or prefix membership alone.
//
// A materialised DNS credential is owned by its engine, and Traefik depends on
// lego, so without this the closure rule would hand Traefik lego's DNS API
// token -- a credential it has no use for and no way to need. The generated
// secret store marks its own values sensitive automatically, but these come
// from the user's config, so nothing else would notice them.
func (a *app) markSensitive(key string) {
	if a.runnerSensitive == nil {
		a.runnerSensitive = map[string]bool{}
	}
	a.runnerSensitive[key] = true
	// The scope set is cached on first use; drop it so a key marked after that
	// point still takes effect rather than silently missing the cache.
	a.sensitiveKeys = nil
}

// envPrefixFor returns the env namespace a module owns.
func (a *app) envPrefixFor(name string) string {
	if mod, ok := a.reg[name]; ok && mod.EnvPrefix != "" {
		return mod.EnvPrefix
	}
	return defaultEnvPrefix(name)
}

// dnsPlanSummary reports each engine's resolved platform and whether the
// engines that are running can work from one credential. Compatibility is only
// interesting when more than one engine is active, so a single-engine
// deployment prints just its own line.
func (a *app) dnsPlanSummary() string {
	registry, err := a.dnsRegistry()
	if err != nil {
		return ""
	}
	type binding struct{ engine, platform string }
	bindings := []binding{}
	for _, engine := range dns.Engines() {
		if !contains(a.order, engine) {
			continue
		}
		if name := a.env[a.envPrefixFor(engine)+"_DNS_PLATFORM"]; name != "" {
			bindings = append(bindings, binding{engine, name})
		}
	}
	if len(bindings) == 0 {
		return ""
	}
	sort.Slice(bindings, func(i, j int) bool { return bindings[i].engine < bindings[j].engine })
	var b strings.Builder
	b.WriteString("\ndns platforms:\n")
	for _, item := range bindings {
		fmt.Fprintf(&b, "  %s -> %s\n", item.engine, item.platform)
	}
	for i := 0; i < len(bindings); i++ {
		for j := i + 1; j < len(bindings); j++ {
			if bindings[i].platform != bindings[j].platform {
				continue
			}
			platform, ok := registry.Lookup(bindings[i].platform)
			if !ok {
				continue
			}
			fmt.Fprintf(&b, "  %s/%s credentials: %s\n", bindings[i].engine, bindings[j].engine,
				platform.Compatibility(bindings[i].engine, bindings[j].engine))
		}
	}
	return b.String()
}

// dnsPlanDocument is the machine-readable form of dnsPlanSummary.
func (a *app) dnsPlanDocument() map[string]any {
	out := map[string]any{}
	for _, engine := range dns.Engines() {
		if !contains(a.order, engine) {
			continue
		}
		if name := a.env[a.envPrefixFor(engine)+"_DNS_PLATFORM"]; name != "" {
			out[engine] = name
		}
	}
	return out
}

// userSecretKeys lists config-supplied secret keys, used by tests to assert a
// canonical secret never leaves the runner under its original name.
func (a *app) userSecretKeys() []string {
	out := []string{}
	for key, owner := range a.envOwner {
		if owner == config.OwnerUserSecret {
			out = append(out, key)
		}
	}
	sort.Strings(out)
	return out
}
