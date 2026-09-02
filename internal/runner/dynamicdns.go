package runner

import (
	"fmt"
	"sort"
	"strings"

	"github.com/anas-project/ANAS/internal/dns"
)

// Deployment-scoped dynamic DNS.
//
// Unlike IAM, this capability has no consumer module: nothing in a deployment
// declares that it needs its own A record, the deployment as a whole does. So
// there is no dependency edge to resolve it from, and the selected module is
// injected as a root of the module graph instead.
//
// Selecting an implementation is not the same as running one. A DDNS module
// listed in `modules` starts whether or not it was selected -- someone running
// two updaters against different zones is doing something reasonable. What
// selection decides is which one holds the records ANAS itself declares.

const (
	capabilityDynamicDNS = "dynamic_dns"
	// dynamicDNSAuto lets the runner pick among the implementations that can
	// address the configured vendor.
	dynamicDNSAuto = "auto"
	// deploymentBindingKey is the module slot for capabilities that belong to
	// the deployment rather than to a consumer. The "@" cannot occur in a module
	// name, which is what keeps it from colliding with one.
	deploymentBindingKey = "@deployment"
)

// dynamicDNSPreference is the order auto-selection considers when more than
// one implementation can address the vendor.
//
// ddns_go comes first because it discovers the host's IPv6 address rather than
// only its IPv4 one, which is the case a home deployment actually needs; both
// are otherwise equivalent for a vendor they both support. The order is fixed
// here rather than derived from directory listing so that adding a module cannot
// silently change what an existing deployment resolves to.
var dynamicDNSPreference = []string{dns.EngineDDNSGo, dns.EngineDDNSUpdater}

// resolveDynamicDNS picks the implementation that will hold the declared
// records and returns it so the caller can add it to the module roots. An
// empty name means the deployment declares no records.
func (a *app) resolveDynamicDNS() (string, error) {
	if a.cfg == nil || a.cfg.DynamicDNS.Provider == "" {
		return "", nil
	}
	registry, err := a.dnsRegistry()
	if err != nil {
		return "", err
	}
	vendor := a.cfg.DynamicDNS.DNSProvider
	platform, ok := registry.Lookup(vendor)
	if !ok {
		return "", fmt.Errorf("dynamic_dns.dns_provider %q is not a known DNS platform;\nset it to one of: %s",
			vendor, strings.Join(registry.Names(), ", "))
	}

	requested := a.cfg.DynamicDNS.Provider
	if requested != dynamicDNSAuto {
		if !contains(dynamicDNSPreference, requested) {
			return "", fmt.Errorf("dynamic_dns.provider %q is not a dynamic DNS implementation;\nset it to auto, %s",
				requested, strings.Join(dynamicDNSPreference, " or "))
		}
		if _, available := a.reg[requested]; !available {
			return "", fmt.Errorf("dynamic_dns.provider %q is not an available module", requested)
		}
		if !a.moduleEnabled(requested) {
			return "", fmt.Errorf("dynamic_dns.provider %q is disabled", requested)
		}
		if !platform.Supports(requested) {
			return "", fmt.Errorf("dynamic_dns.provider %q cannot update records at %s;\nuse %s, or choose a vendor %s supports: %s",
				requested, platform.Name, strings.Join(supportingEngines(platform), " or "),
				requested, strings.Join(registry.NamesFor(requested), ", "))
		}
		return requested, nil
	}

	// A binding already in the lock wins, so gaining a new implementation --
	// or reordering the preference list in a later release -- cannot move an
	// existing deployment onto a different updater behind the user's back. The
	// lock comparison still reports the change when it stops being valid.
	if locked := a.lockedDynamicDNSProvider(); locked != "" && platform.Supports(locked) {
		if _, available := a.reg[locked]; !available {
			return "", fmt.Errorf("locked dynamic DNS provider %q is not an available module", locked)
		}
		if !a.moduleEnabled(locked) {
			return "", fmt.Errorf("locked dynamic DNS provider %q is disabled", locked)
		}
		return locked, nil
	}
	// An implementation the user already asked to run is preferred over one
	// the runner would have to add.
	for _, name := range dynamicDNSPreference {
		if platform.Supports(name) && contains(a.cfg.Modules.Order, name) && a.moduleEnabled(name) {
			return name, nil
		}
	}
	for _, name := range dynamicDNSPreference {
		if _, available := a.reg[name]; platform.Supports(name) && available && a.moduleEnabled(name) {
			return name, nil
		}
	}
	return "", fmt.Errorf("no dynamic DNS implementation can update records at %s;\nchoose a vendor one of them supports, or set dynamic_dns.provider to \"\" to declare no records",
		platform.Name)
}

func (a *app) lockedDynamicDNSProvider() string {
	if a.lock == nil {
		return ""
	}
	return a.lock.Bindings[deploymentBindingKey][capabilityDynamicDNS]
}

func supportingEngines(platform *dns.Platform) []string {
	out := []string{}
	for _, name := range dynamicDNSPreference {
		if platform.Supports(name) {
			out = append(out, name)
		}
	}
	return out
}

// applyDynamicDNSBinding records the selection and seeds the chosen module's own
// vendor setting. A per-service dns_provider still wins, so a deployment can
// declare its records at one vendor while a second updater it runs by hand
// works at another.
func (a *app) applyDynamicDNSBinding(provider string) {
	if provider == "" {
		return
	}
	if a.resolvedBindings == nil {
		a.resolvedBindings = map[string]map[string]string{}
	}
	if a.resolvedBindings[deploymentBindingKey] == nil {
		a.resolvedBindings[deploymentBindingKey] = map[string]string{}
	}
	a.resolvedBindings[deploymentBindingKey][capabilityDynamicDNS] = provider
	a.dynamicDNSProvider = provider

	key := a.envPrefixFor(provider) + "_DNS_PROVIDER"
	if strings.TrimSpace(a.env[key]) == "" {
		a.env[key] = a.cfg.DynamicDNS.DNSProvider
		a.setEnvOwner(key, provider)
	}
	// Every DDNS module learns whether it is the one holding the declared
	// records, so an unselected one can leave them alone.
	for _, engine := range dns.Engines() {
		if engine == dns.EngineLego {
			continue
		}
		managed := "false"
		if engine == provider {
			managed = "true"
		}
		managedKey := a.envPrefixFor(engine) + "_DYNAMIC_DNS_MANAGED"
		a.env[managedKey] = managed
		a.setEnvOwner(managedKey, engine)
	}
}

// dnsRecordClaim is one record an engine will maintain.
type dnsRecordClaim struct {
	engine   string
	platform string
	family   string
	domain   string
}

// dynamicDNSOverlaps reports records that more than one updater would
// maintain.
//
// This is a warning rather than a refusal. Both updaters discover the address
// the same way -- by asking an outside service what the world sees -- so for
// IPv4 behind one NAT they reach the same answer, each finds the record
// already correct, and neither writes. The overlap is then merely redundant.
//
// It is still worth saying, because the cases where they disagree are not
// exotic: a host with IPv6 privacy extensions has several global addresses and
// the one used for egress varies, and the two engines poll different endpoints,
// which differ behind CGNAT or a second WAN. Disagreement shows up as a record
// that flaps rather than one that is plainly wrong, which is much harder to
// attribute afterwards. Doubling the API calls also matters on a rate-limited
// free tier.
//
// Both engines maintain the base domain and its wildcard by construction, so
// the claims are known before any hook runs.
func (a *app) dynamicDNSOverlaps() []string {
	claims := []dnsRecordClaim{}
	for _, engine := range dns.Engines() {
		if engine == dns.EngineLego || !contains(a.order, engine) {
			continue
		}
		platform := a.env[a.envPrefixFor(engine)+"_DNS_PLATFORM"]
		if platform == "" {
			continue
		}
		base := strings.TrimSpace(a.env["BASE_DOMAIN"])
		if base == "" {
			continue
		}
		families := []string{}
		if a.env["IPv4"] != "false" {
			families = append(families, "A")
		}
		if a.env["IPv6"] != "false" && a.env["HOST_HAS_IPV6"] == "true" {
			families = append(families, "AAAA")
		}
		for _, family := range families {
			for _, domain := range []string{base, "*." + base} {
				claims = append(claims, dnsRecordClaim{engine, platform, family, domain})
			}
		}
	}

	seen := map[string]string{}
	overlaps := []string{}
	for _, claim := range claims {
		identity := claim.platform + " " + claim.family + " " + claim.domain
		if other, ok := seen[identity]; ok && other != claim.engine {
			overlaps = append(overlaps, fmt.Sprintf("%s and %s both maintain %s %s at %s",
				other, claim.engine, claim.family, claim.domain, claim.platform))
			continue
		}
		seen[identity] = claim.engine
	}
	sort.Strings(overlaps)
	return uniqueStrings(overlaps)
}

// reportDynamicDNSOverlaps prints the overlap warning once per run.
func (a *app) reportDynamicDNSOverlaps() {
	overlaps := a.dynamicDNSOverlaps()
	if len(overlaps) == 0 {
		return
	}
	a.warning("dynamic_dns_overlap",
		"more than one dynamic DNS updater maintains these records:\n  %s\n"+
			"they will usually agree and leave the record alone, but they poll different\n"+
			"endpoints, so a host with several global IPv6 addresses can see them disagree\n"+
			"and the record flap. Give them different vendors, or run only one.",
		strings.Join(overlaps, "\n  "))
}

// dynamicDNSPlanSummary reports which implementation holds the declared
// records, so `plan` answers "who owns my DNS" rather than only listing modules.
func (a *app) dynamicDNSPlanSummary() string {
	if a.dynamicDNSProvider == "" {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "\ndynamic dns: %s", a.dynamicDNSProvider)
	if a.cfg != nil && a.cfg.DynamicDNS.Provider == dynamicDNSAuto {
		b.WriteString(" (auto)")
	}
	b.WriteString("\n")
	// Anything else that is running is self-managed, which is worth stating:
	// it explains why only one of them received the declared records.
	for _, engine := range dns.Engines() {
		if engine == dns.EngineLego || engine == a.dynamicDNSProvider || !contains(a.order, engine) {
			continue
		}
		fmt.Fprintf(&b, "  %s runs with its own configuration\n", engine)
	}
	return b.String()
}

func (a *app) dynamicDNSPlanDocument() map[string]any {
	document := map[string]any{"provider": nil, "self_managed": []string{}}
	if a.dynamicDNSProvider == "" {
		return document
	}
	document["provider"] = a.dynamicDNSProvider
	others := []string{}
	for _, engine := range dns.Engines() {
		if engine == dns.EngineLego || engine == a.dynamicDNSProvider || !contains(a.order, engine) {
			continue
		}
		others = append(others, engine)
	}
	sort.Strings(others)
	document["self_managed"] = others
	return document
}
