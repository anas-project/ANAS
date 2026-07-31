package runner

import (
	"fmt"
	"sort"
	"strings"
)

// Capability binding for IAM. A deployment runs exactly one IAM, chosen
// explicitly through config `iam.provider`; casks may narrow the protocol but
// never the provider. Requiring every IAM provider to serve both OIDC and SAML
// (see requireIAMInterfaces) removes the whole "consumer wants SAML but the
// provider only speaks OIDC" failure class before any resolution happens.
const capabilityIAM = "iam"

const (
	interfaceOIDC = "oidc"
	interfaceSAML = "saml"
)

// knownCapabilityInterfaces lists the protocol identifiers the runner
// understands per capability. An identifier outside this set fails at manifest
// load rather than being ignored, so a typo cannot silently disable SSO.
var knownCapabilityInterfaces = map[string][]string{
	capabilityIAM: {interfaceOIDC, interfaceSAML},
}

// requireIAMInterfaces is the IAM provider admission rule: a cask may only
// register as an IAM provider when it serves both protocols. The check runs at
// manifest load, so whether a given IAM qualifies never depends on the user's
// configuration.
var requireIAMInterfaces = []string{interfaceOIDC, interfaceSAML}

// resolveCapabilityDependency binds one consumer to the deployment's IAM and
// records the protocol it will speak. It returns the provider cask name so the
// caller can add a dependency edge, which is what guarantees the provider's
// calculate hook runs before the consumer's.
func (a *app) resolveCapabilityDependency(moduleName string, mod Module, dep RequiredCapability) (string, error) {
	if dep.Name != capabilityIAM {
		return "", fmt.Errorf("cask %q requires capability %q which the runner cannot bind", moduleName, dep.Name)
	}
	provider := ""
	if a.cfg != nil {
		provider = a.cfg.IAM.Provider
	}
	if provider == "" {
		return "", fmt.Errorf("%s requires IAM capability, but iam.provider is not set;\nset iam.provider to one of: %s",
			moduleName, a.listIAMProviders())
	}
	providerMod, ok := a.reg[provider]
	if !ok {
		return "", fmt.Errorf("iam.provider %q is not a known cask;\navailable providers: %s",
			provider, a.describeIAMProviders())
	}
	capability, ok := providerMod.providedCapability(capabilityIAM)
	if !ok {
		return "", fmt.Errorf("iam.provider %q does not provide capability %q;\navailable providers: %s",
			provider, capabilityIAM, a.describeIAMProviders())
	}
	if !a.moduleEnabled(provider) {
		return "", fmt.Errorf("iam.provider %q is disabled but required by %s", provider, moduleName)
	}
	iface, err := a.resolveCapabilityInterface(moduleName, mod, dep, provider, capability)
	if err != nil {
		return "", err
	}
	a.iamProvider = provider
	if a.iamBindings == nil {
		a.iamBindings = map[string]string{}
	}
	a.iamBindings[moduleName] = iface
	// Reuse the generic binding record so the existing lock comparison
	// protects an IAM or protocol switch the same way it protects a database
	// switch.
	if a.resolvedBindings == nil {
		a.resolvedBindings = map[string]map[string]string{}
	}
	if a.resolvedBindings[moduleName] == nil {
		a.resolvedBindings[moduleName] = map[string]string{}
	}
	a.resolvedBindings[moduleName][capabilityIAM] = provider
	a.resolvedBindings[moduleName][capabilityIAM+".interface"] = iface
	return provider, nil
}

// resolveCapabilityInterface applies the protocol precedence: an explicit cask
// parameter wins, then the deployment default when the cask supports it, then
// the cask's own preference order.
func (a *app) resolveCapabilityInterface(moduleName string, mod Module, dep RequiredCapability, provider string, capability ProvidedCapability) (string, error) {
	key := paramEnvKey(moduleName, mod.EnvPrefix, dep.InterfaceSelectedBy)
	requested := strings.ToLower(strings.TrimSpace(a.env[key]))
	iface := ""
	switch {
	case requested != "" && requested != "auto":
		if !contains(dep.AnyOf, requested) {
			return "", fmt.Errorf("%s.%s is %q, but %s supports [%s];\nset %s.%s to one of: %s, auto",
				moduleName, dep.InterfaceSelectedBy, requested, moduleName, strings.Join(dep.AnyOf, ","),
				moduleName, dep.InterfaceSelectedBy, strings.Join(dep.AnyOf, ", "))
		}
		iface = requested
	default:
		fallback := ""
		if a.cfg != nil {
			fallback = a.cfg.IAM.DefaultProtocol
		}
		if fallback != "" {
			known := knownCapabilityInterfaces[dep.Name]
			if !contains(known, fallback) {
				return "", fmt.Errorf("iam.default_protocol %q is not a known protocol;\nset iam.default_protocol to one of: %s",
					fallback, strings.Join(known, ", "))
			}
			if contains(dep.AnyOf, fallback) {
				iface = fallback
			}
		}
		if iface == "" {
			for _, candidate := range dep.Prefer {
				if contains(dep.AnyOf, candidate) {
					iface = candidate
					break
				}
			}
		}
		if iface == "" && len(dep.AnyOf) > 0 {
			iface = dep.AnyOf[0]
		}
	}
	if iface == "" {
		return "", fmt.Errorf("%s has no usable %s protocol", moduleName, dep.Name)
	}
	// Admission guarantees a qualified provider serves every known protocol,
	// so this only trips on a malformed provider manifest. Keep it as an
	// invariant rather than trusting the admission rule from a distance.
	if !contains(capability.Interfaces, iface) {
		return "", fmt.Errorf("%s requires %s protocol %q, but provider %s offers [%s]",
			moduleName, dep.Name, iface, provider, strings.Join(capability.Interfaces, ","))
	}
	a.env[key] = iface
	return iface, nil
}

// iamProviderNames lists every cask that qualifies as an IAM provider, so an
// error can tell the user what they may actually choose.
func (a *app) iamProviderNames() []string {
	names := []string{}
	for name, mod := range a.reg {
		if _, ok := mod.providedCapability(capabilityIAM); ok {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names
}

func (a *app) listIAMProviders() string {
	names := a.iamProviderNames()
	if len(names) == 0 {
		return "(no cask provides the iam capability)"
	}
	return strings.Join(names, ", ")
}

// describeIAMProviders renders providers with their protocols, e.g.
// "llng[oidc,saml]".
func (a *app) describeIAMProviders() string {
	names := a.iamProviderNames()
	if len(names) == 0 {
		return "(no cask provides the iam capability)"
	}
	out := make([]string, 0, len(names))
	for _, name := range names {
		capability, _ := a.reg[name].providedCapability(capabilityIAM)
		out = append(out, fmt.Sprintf("%s[%s]", name, strings.Join(capability.Interfaces, ",")))
	}
	return strings.Join(out, ", ")
}

// checkSingleIAM rejects a config that would start two IAMs at once. Listing an
// IAM in modules is how a user starts one without any consumer, so the check is
// over explicitly listed casks plus the selected provider.
func (a *app) checkSingleIAM() error {
	if a.cfg == nil {
		return nil
	}
	active := []string{}
	for _, name := range a.cfg.Modules {
		mod, ok := a.reg[name]
		if !ok || !a.moduleEnabled(name) {
			continue
		}
		if _, ok := mod.providedCapability(capabilityIAM); ok && !contains(active, name) {
			active = append(active, name)
		}
	}
	if provider := a.cfg.IAM.Provider; provider != "" {
		if mod, ok := a.reg[provider]; ok {
			if _, ok := mod.providedCapability(capabilityIAM); ok && !contains(active, provider) {
				active = append(active, provider)
			}
		}
	}
	if len(active) > 1 {
		sort.Strings(active)
		return fmt.Errorf("a deployment may run only one IAM, but these are all active: %s;\nremove the extra ones from modules and keep iam.provider as the single choice",
			strings.Join(active, ", "))
	}
	return nil
}

// IAM environment contract. Every value a consumer needs is published under
// its own binding prefix rather than as a deployment-level singleton, because
// whether IdP endpoints vary per application is an implementation difference
// (LemonLDAP::NG and Keycloak share one issuer; authentik mints one per
// application) that the contract has to absorb. A singleton shape is a special
// case of the per-consumer one, not the other way round.
const (
	envIAMProvider    = "ANAS_IAM_PROVIDER"
	envIAMInterfaces  = "ANAS_IAM_INTERFACES"
	envIAMClients     = "ANAS_IAM_CLIENTS"
	envIAMBindingPfx  = "ANAS_IAM_BINDING__"
	envIAMClientsTmpl = "ANAS_IAM_%s_CLIENTS"
)

// requiredEndpointSuffixes lists the per-binding endpoint variables a provider
// must publish for each protocol it is actually bound to. SLO is optional.
var requiredEndpointSuffixes = map[string][]string{
	interfaceOIDC: {"OIDC_ISSUER_URL", "OIDC_DISCOVERY_URL"},
	interfaceSAML: {"SAML_METADATA_URL", "SAML_ENTITY_ID", "SAML_SSO_URL"},
}

func iamBindingKey(consumer, suffix string) string {
	return envIAMBindingPfx + strings.ToUpper(strings.ReplaceAll(consumer, "-", "_")) + "__" + suffix
}

// iamConsumers returns the bound consumers in a stable order.
func (a *app) iamConsumers() []string {
	names := make([]string, 0, len(a.iamBindings))
	for name := range a.iamBindings {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// iamConsumersByInterface partitions the consumer list by resolved protocol.
// The per-protocol lists are a projection of the flat list written from one
// resolution result, which is what makes "this deployment has no SAML
// consumer" a directly testable condition instead of something every provider
// cask has to rediscover by scanning.
func (a *app) iamConsumersByInterface(iface string) []string {
	names := []string{}
	for _, name := range a.iamConsumers() {
		if a.iamBindings[name] == iface {
			names = append(names, name)
		}
	}
	return names
}

// publishIAMEnv writes the resolved binding set before any hook runs. A
// provider whose endpoints are per-application needs the consumer list and
// their protocols during its own calculate phase; all of it is already known
// once ordering succeeds, so no lifecycle reordering is required.
func (a *app) publishIAMEnv() {
	if a.iamProvider == "" || len(a.iamBindings) == 0 {
		return
	}
	if a.env == nil {
		a.env = map[string]string{}
	}
	owner := a.iamProvider
	set := func(key, value string) {
		a.env[key] = value
		a.setEnvOwner(key, owner)
	}
	capability, _ := a.reg[a.iamProvider].providedCapability(capabilityIAM)
	set(envIAMProvider, a.iamProvider)
	set(envIAMInterfaces, strings.Join(capability.Interfaces, ","))
	set(envIAMClients, strings.Join(a.iamConsumers(), ","))
	for _, iface := range knownCapabilityInterfaces[capabilityIAM] {
		key := fmt.Sprintf(envIAMClientsTmpl, strings.ToUpper(iface))
		set(key, strings.Join(a.iamConsumersByInterface(iface), ","))
	}
	for _, consumer := range a.iamConsumers() {
		set(iamBindingKey(consumer, "INTERFACE"), a.iamBindings[consumer])
	}
}

// iamPlanSummary reports the resolved binding for each consumer so `plan`
// answers "which IAM, which protocol" and not only the start order. It returns
// an empty string when nothing consumes the capability.
func (a *app) iamPlanSummary() string {
	if a.iamProvider == "" || len(a.iamBindings) == 0 {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "\niam provider: %s\n", a.iamProvider)
	for _, consumer := range a.iamConsumers() {
		fmt.Fprintf(&b, "  %s -> %s/%s\n", consumer, a.iamProvider, a.iamBindings[consumer])
	}
	return b.String()
}

// iamPlanDocument is the machine-readable form of iamPlanSummary. provider is
// null rather than absent when nothing consumes the capability, so a caller
// always finds the key and never has to distinguish "no IAM" from "this
// version of anas does not report one".
func (a *app) iamPlanDocument() map[string]any {
	document := map[string]any{"provider": nil, "consumers": []map[string]string{}}
	if a.iamProvider == "" || len(a.iamBindings) == 0 {
		return document
	}
	document["provider"] = a.iamProvider
	consumers := make([]map[string]string, 0, len(a.iamBindings))
	for _, consumer := range a.iamConsumers() {
		consumers = append(consumers, map[string]string{
			"cask": consumer, "interface": a.iamBindings[consumer],
		})
	}
	document["consumers"] = consumers
	return document
}

// validateIAMEndpoints runs right after the provider's calculate hook and
// checks only the protocols actually bound. A provider that mints endpoints
// per application has nothing to publish for a protocol no consumer uses, so
// demanding unconditional endpoints would be unsatisfiable. Manifest admission
// covers "this IAM can serve any protocol"; this covers "this deployment
// actually works".
func (a *app) validateIAMEndpoints() error {
	if a.iamProvider == "" {
		return nil
	}
	missing := []string{}
	for _, consumer := range a.iamConsumers() {
		for _, suffix := range requiredEndpointSuffixes[a.iamBindings[consumer]] {
			key := iamBindingKey(consumer, suffix)
			if strings.TrimSpace(a.env[key]) == "" {
				missing = append(missing, key)
			}
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("iam provider %s did not publish required endpoint variables: %s",
			a.iamProvider, strings.Join(missing, ", "))
	}
	return nil
}

func normalizeProvidedCapabilities(cask string, in []manifestProvidedCapability) ([]ProvidedCapability, error) {
	out := []ProvidedCapability{}
	seen := map[string]bool{}
	for _, capability := range in {
		name := strings.ToLower(strings.TrimSpace(capability.Name))
		if name == "" {
			return nil, fmt.Errorf("cask %q has capabilities.provides entry without name", cask)
		}
		known, ok := knownCapabilityInterfaces[name]
		if !ok {
			return nil, fmt.Errorf("cask %q provides unknown capability %q; known capabilities: %s",
				cask, name, strings.Join(knownCapabilityNames(), ", "))
		}
		if seen[name] {
			return nil, fmt.Errorf("cask %q provides capability %q more than once", cask, name)
		}
		seen[name] = true
		interfaces, err := normalizeInterfaceList(cask, "capabilities.provides."+name+".interfaces", known, capability.Interfaces)
		if err != nil {
			return nil, err
		}
		if len(interfaces) == 0 {
			return nil, fmt.Errorf("cask %q provides capability %q without interfaces", cask, name)
		}
		if name == capabilityIAM {
			if missing := missingInterfaces(interfaces, requireIAMInterfaces); len(missing) > 0 {
				return nil, fmt.Errorf(
					"cask %q declares capability %s with interfaces [%s]; an IAM provider must declare both %s",
					cask, name, strings.Join(interfaces, ","), strings.Join(requireIAMInterfaces, " and "))
			}
		}
		out = append(out, ProvidedCapability{Name: name, Interfaces: interfaces})
	}
	return out, nil
}

func normalizeRequiredCapabilities(cask string, in []manifestRequiredCapability) ([]RequiredCapability, error) {
	out := []RequiredCapability{}
	seen := map[string]bool{}
	for _, capability := range in {
		name := strings.ToLower(strings.TrimSpace(capability.Name))
		if name == "" {
			return nil, fmt.Errorf("cask %q has requires_capabilities entry without name", cask)
		}
		known, ok := knownCapabilityInterfaces[name]
		if !ok {
			return nil, fmt.Errorf("cask %q requires unknown capability %q; known capabilities: %s",
				cask, name, strings.Join(knownCapabilityNames(), ", "))
		}
		if seen[name] {
			return nil, fmt.Errorf("cask %q requires capability %q more than once", cask, name)
		}
		seen[name] = true
		selectedBy := strings.TrimSpace(capability.InterfaceSelectedBy)
		if selectedBy == "" {
			return nil, fmt.Errorf("cask %q requires_capabilities %q has no interface_selected_by parameter", cask, name)
		}
		anyOf, err := normalizeInterfaceList(cask, "requires_capabilities."+name+".interfaces.any_of", known, capability.Interfaces.AnyOf)
		if err != nil {
			return nil, err
		}
		if len(anyOf) == 0 {
			return nil, fmt.Errorf("cask %q requires_capabilities %q has an empty any_of", cask, name)
		}
		prefer, err := normalizeInterfaceList(cask, "requires_capabilities."+name+".interfaces.prefer", known, capability.Interfaces.Prefer)
		if err != nil {
			return nil, err
		}
		for _, item := range prefer {
			if !contains(anyOf, item) {
				return nil, fmt.Errorf("cask %q requires_capabilities %q prefers %q which is not in any_of [%s]",
					cask, name, item, strings.Join(anyOf, ","))
			}
		}
		out = append(out, RequiredCapability{
			Name: name, InterfaceSelectedBy: selectedBy, AnyOf: anyOf, Prefer: prefer,
		})
	}
	return out, nil
}

// normalizeInterfaceList lowercases and de-duplicates protocol identifiers,
// rejecting any the runner does not know for this capability.
func normalizeInterfaceList(cask, field string, known, in []string) ([]string, error) {
	out := []string{}
	for _, raw := range in {
		item := strings.ToLower(strings.TrimSpace(raw))
		if item == "" {
			return nil, fmt.Errorf("cask %q %s contains an empty interface", cask, field)
		}
		if !contains(known, item) {
			return nil, fmt.Errorf("cask %q %s contains unknown interface %q; known interfaces: %s",
				cask, field, item, strings.Join(known, ", "))
		}
		if contains(out, item) {
			return nil, fmt.Errorf("cask %q %s lists interface %q more than once", cask, field, item)
		}
		out = append(out, item)
	}
	return out, nil
}

func missingInterfaces(have, want []string) []string {
	missing := []string{}
	for _, item := range want {
		if !contains(have, item) {
			missing = append(missing, item)
		}
	}
	return missing
}

func knownCapabilityNames() []string {
	names := make([]string, 0, len(knownCapabilityInterfaces))
	for name := range knownCapabilityInterfaces {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
