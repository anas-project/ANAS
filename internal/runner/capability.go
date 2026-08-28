package runner

import (
	"fmt"
	"net/url"
	"sort"
	"strings"

	"github.com/anas-project/ANAS/internal/topologyschema"
)

// Capability binding. A capability names a job some module does for others --
// serving identity, gating HTTP access -- and a consumer declares the job it
// needs rather than the module it wants. What differs between capabilities is
// how the single provider is chosen, and that difference is data here rather
// than a branch: see capabilityDefinitions.
const (
	capabilityIAM           = "iam"
	capabilityForwardAuth   = "forward_auth"
	capabilityObjectStorage = "object_storage"
)

// Ordering vocabulary for a capability dependency. Both values keep the provider
// mandatory; they differ only in whether resolution must place it first.
const (
	orderingBefore = "before"
	orderingAny    = "any"
)

const (
	interfaceOIDC = topologyschema.IAMProtocolOIDC
	interfaceSAML = topologyschema.IAMProtocolSAML
	// interfaceHTTP is Traefik's ForwardAuth exchange: the proxy asks an
	// external endpoint about each request and forwards it only on 2xx.
	interfaceHTTP = "http"
	// interfaceS3 is the AWS Signature Version 4 object-storage HTTP API.
	interfaceS3 = "s3"
)

// Provider selection policies.
const (
	// selectionExplicit refuses to guess. Used where picking wrong is
	// expensive to undo: switching IAM rewrites every client registration.
	selectionExplicit = "explicit"
	// selectionAuto binds the only enabled provider. The choice is still
	// recorded in the lock, so gaining a second implementation later cannot
	// silently move an existing deployment onto it.
	selectionAuto = "auto"
)

// capabilityDefinition is what the runner knows about a capability before any
// manifest or config is read.
type capabilityDefinition struct {
	// Interfaces are the protocol identifiers this capability may use. An
	// identifier outside the set fails at manifest load rather than being
	// ignored, so a typo cannot silently disable SSO.
	Interfaces []string
	// RequireAll is the provider admission rule: a module may only register as
	// a provider when it serves all of these. Checking at manifest load means
	// whether a module qualifies never depends on the user's configuration.
	RequireAll []string
	// Selection is how the provider is chosen.
	Selection string
	// ConfiguredProvider reads the explicitly selected provider from config.
	// Only meaningful for selectionExplicit.
	ConfiguredProvider func(a *app) string
	// DefaultInterface reads the deployment-wide protocol preference, which
	// applies to consumers that left their own choice on "auto".
	DefaultInterface func(a *app) string
	// ConfigKey names the config field in error messages.
	ConfigKey string
	// ImplicitInterface permits the shortest consumer declaration (`name`
	// only) for a capability whose data ABI has exactly one stable interface.
	// Existing multi-interface capabilities keep requiring an explicit
	// interface_selected_by selector and any_of list.
	ImplicitInterface string
	// Outputs declares provider-neutral runtime values that the runner projects
	// into a consumer-owned binding namespace after the selected provider's
	// calculate Hook. Capability outputs are connection facts, not Resources:
	// they do not create buckets or manage per-consumer credentials.
	Outputs map[string]capabilityOutputDefinition
}

type capabilityOutputDefinition struct {
	ProviderPrefix string
	BindingPrefix  string
	Required       []string
	Sensitive      []string
}

var capabilityDefinitions = map[string]capabilityDefinition{
	// Requiring every IAM provider to serve both protocols removes the whole
	// "consumer wants SAML but the provider only speaks OIDC" failure class
	// before any resolution happens.
	capabilityIAM: {
		Interfaces:         []string{interfaceOIDC, interfaceSAML},
		RequireAll:         []string{interfaceOIDC, interfaceSAML},
		Selection:          selectionExplicit,
		ConfigKey:          "iam.provider",
		ConfiguredProvider: func(a *app) string { return a.cfg.IAM.Provider },
		DefaultInterface:   func(a *app) string { return a.cfg.IAM.DefaultProtocol },
	},
	// Forward auth puts an authenticated gate in front of a service that has
	// no login of its own. There is one exchange to speak and, today, one
	// implementation, so demanding the user name it would be ceremony.
	capabilityForwardAuth: {
		Interfaces: []string{interfaceHTTP},
		RequireAll: []string{interfaceHTTP},
		Selection:  selectionAuto,
		ConfigKey:  "forward_auth.provider",
	},
	// Object storage has one currently supported wire interface. Consumers may
	// therefore depend on it with only `name: object_storage`; the selected
	// provider publishes one normalized S3 connection record which is projected
	// into an isolated per-consumer binding namespace.
	capabilityObjectStorage: {
		Interfaces:        []string{interfaceS3},
		RequireAll:        []string{interfaceS3},
		Selection:         selectionAuto,
		ConfigKey:         "modules",
		ImplicitInterface: interfaceS3,
		Outputs: map[string]capabilityOutputDefinition{
			interfaceS3: {
				ProviderPrefix: "ANAS_OBJECT_STORAGE_S3_",
				BindingPrefix:  "ANAS_OBJECT_STORAGE_BINDING__",
				Required: []string{
					"ENDPOINT", "REGION", "ACCESS_KEY_ID", "SECRET_ACCESS_KEY", "PATH_STYLE",
				},
				Sensitive: []string{"SECRET_ACCESS_KEY"},
			},
		},
	},
}

// knownCapabilityInterfaces is the manifest-validation view of the table.
func knownCapabilityInterfaces(name string) ([]string, bool) {
	definition, ok := capabilityDefinitions[name]
	if !ok {
		return nil, false
	}
	return definition.Interfaces, true
}

// resolveCapabilityDependency binds one consumer to the deployment's provider
// for a capability and records the interface it will speak. It returns the
// provider module name so the caller can add a dependency edge, which is what
// guarantees the provider's calculate hook runs before the consumer's.
func (a *app) resolveCapabilityDependency(moduleName string, mod Module, dep RequiredCapability) (string, error) {
	definition, ok := capabilityDefinitions[dep.Name]
	if !ok {
		return "", fmt.Errorf("module %q requires capability %q which the runner cannot bind", moduleName, dep.Name)
	}
	provider, err := a.selectCapabilityProvider(moduleName, dep.Name, definition)
	if err != nil {
		return "", err
	}
	providerMod, ok := a.reg[provider]
	if !ok {
		return "", fmt.Errorf("%s %q is not a known module;\navailable providers: %s",
			definition.ConfigKey, provider, a.describeCapabilityProviders(dep.Name))
	}
	capability, ok := providerMod.providedCapability(dep.Name)
	if !ok {
		return "", fmt.Errorf("%s %q does not provide capability %q;\navailable providers: %s",
			definition.ConfigKey, provider, dep.Name, a.describeCapabilityProviders(dep.Name))
	}
	if !a.moduleEnabled(provider) {
		return "", fmt.Errorf("%s %q is disabled but required by %s", definition.ConfigKey, provider, moduleName)
	}
	iface, err := a.resolveCapabilityInterface(moduleName, mod, dep, provider, capability)
	if err != nil {
		return "", err
	}
	if dep.Name == capabilityIAM {
		a.iamProvider = provider
		if a.iamBindings == nil {
			a.iamBindings = map[string]string{}
		}
		a.iamBindings[moduleName] = iface
	}
	if a.capabilityProviders == nil {
		a.capabilityProviders = map[string]string{}
	}
	a.capabilityProviders[dep.Name] = provider
	// Reuse the generic binding record so the existing lock comparison
	// protects an IAM or protocol switch the same way it protects a database
	// switch.
	if a.resolvedBindings == nil {
		a.resolvedBindings = map[string]map[string]string{}
	}
	if a.resolvedBindings[moduleName] == nil {
		a.resolvedBindings[moduleName] = map[string]string{}
	}
	a.resolvedBindings[moduleName][dep.Name] = provider
	a.resolvedBindings[moduleName][dep.Name+".interface"] = iface
	// A conditional dependency records the switch that produced it, in the same
	// place and the same shape as the interface it resolved to. Without it the
	// only visible effect of flipping an optional service on is that an
	// unrelated module appears in the deployment, and neither `plan` nor a lock
	// error can say which parameter asked for it.
	if dep.EnabledBy != "" {
		a.resolvedBindings[moduleName][dep.Name+".enabled_by"] = dep.EnabledBy
	}
	return provider, nil
}

// calculateEnvFor is the environment a module's calculate Hook may read.
//
// calculate is the privileged stage and normally receives all of a.env
// (internal/runner/hook.go documents why). An unordered dependency punches one
// hole in that: its provider may not have run yet, so anything the provider owns
// is present or absent depending on where resolution happened to place it. A
// Hook reading such a key would work on the author's machine and fail on a
// deployment whose topology sorted differently.
//
// Removing those keys turns the race into a stable absence. The Hook breaks the
// first time it runs instead of the first time someone else runs it, and the
// consumer is pushed to read the value at render time, where the second pass
// makes it deterministic.
//
// Ownership is the right filter rather than the provider's prefix: a provider
// publishes through config.exports as well, and applyCalculatePatch records
// every key it writes under the module's name either way.
func (a *app) calculateEnvFor(name string) map[string]string {
	providers := a.unorderedProvidersFor(name)
	if len(providers) == 0 {
		return a.env
	}
	out := make(map[string]string, len(a.env))
	for key, value := range a.env {
		if owner, tracked := a.envOwner[key]; tracked && providers[owner] {
			continue
		}
		out[key] = value
	}
	return out
}

// unorderedProvidersFor names the modules this one depends on without requiring
// them to resolve first. It reads the binding recorded during resolution rather
// than re-selecting, so it cannot disagree with the order that was produced.
func (a *app) unorderedProvidersFor(name string) map[string]bool {
	out := map[string]bool{}
	for _, dep := range a.reg[name].RequiresCapabilities {
		if dep.Ordering != orderingAny {
			continue
		}
		if provider := a.resolvedBindings[name][dep.Name]; provider != "" {
			out[provider] = true
		}
	}
	return out
}

// conditionalPullReason explains why a module is in the deployment when nothing
// selected it directly: some consumer's optional service asked for a capability
// it provides. It reads the binding record rather than keeping a second map, so
// the explanation cannot drift from the resolution that produced it.
//
// Empty means no conditional dependency accounts for this module, which is the
// ordinary case and must stay silent -- an explanation offered for a module the
// operator chose themselves would be noise at best and wrong at worst.
func (a *app) conditionalPullReason(provider string) string {
	consumers := make([]string, 0, len(a.resolvedBindings))
	for consumer := range a.resolvedBindings {
		consumers = append(consumers, consumer)
	}
	sort.Strings(consumers)
	for _, consumer := range consumers {
		bindings := a.resolvedBindings[consumer]
		capabilities := make([]string, 0, len(bindings))
		for key := range bindings {
			capabilities = append(capabilities, key)
		}
		sort.Strings(capabilities)
		for _, capability := range capabilities {
			if strings.Contains(capability, ".") || bindings[capability] != provider {
				continue
			}
			parameter, ok := bindings[capability+".enabled_by"]
			if !ok {
				continue
			}
			return fmt.Sprintf("%s.%s is on, which requires a %s provider", consumer, parameter, capability)
		}
	}
	return ""
}

// selectCapabilityProvider applies the capability's selection policy.
//
// An explicit policy names the config key that is missing, because "pick one
// yourself" is only actionable if the user is told where to write it. An auto
// policy binds a lone provider and refuses to guess between several, which is
// the point at which a deployment does have to choose.
func (a *app) selectCapabilityProvider(moduleName, capability string, definition capabilityDefinition) (string, error) {
	if definition.Selection == selectionExplicit {
		provider := ""
		if a.cfg != nil && definition.ConfiguredProvider != nil {
			provider = definition.ConfiguredProvider(a)
		}
		if provider == "" {
			return "", unresolvedBindingErrorf("%s requires %s capability, but %s is not set;\nset %s to one of: %s",
				moduleName, capability, definition.ConfigKey, definition.ConfigKey, a.listCapabilityProviders(capability))
		}
		return provider, nil
	}
	candidates := []string{}
	for _, name := range a.capabilityProviderNames(capability) {
		if a.moduleEnabled(name) {
			candidates = append(candidates, name)
		}
	}
	if a.lock != nil {
		if locked := a.lock.Bindings[moduleName][capability]; locked != "" {
			lockedModule, known := a.reg[locked]
			if !known {
				return "", fmt.Errorf("locked %s provider %q is not a known module", capability, locked)
			}
			if _, provides := lockedModule.providedCapability(capability); !provides {
				return "", fmt.Errorf("locked provider %q does not provide capability %q", locked, capability)
			}
			if !a.moduleEnabled(locked) {
				return "", fmt.Errorf("locked %s provider %q is disabled", capability, locked)
			}
			return locked, nil
		}
	}
	switch len(candidates) {
	case 1:
		return candidates[0], nil
	case 0:
		return "", fmt.Errorf("%s requires %s capability, but no enabled module provides it;\nenable one of: %s",
			moduleName, capability, a.listCapabilityProviders(capability))
	default:
		return "", unresolvedBindingErrorf("%s requires %s capability, but %s all provide it;\nset %s to the one this deployment should use",
			moduleName, capability, strings.Join(candidates, ", "), definition.ConfigKey)
	}
}

// resolveCapabilityInterface applies the protocol precedence: an explicit module
// parameter wins, then the deployment default when the module supports it, then
// the module's own preference order.
func (a *app) resolveCapabilityInterface(moduleName string, mod Module, dep RequiredCapability, provider string, capability ProvidedCapability) (string, error) {
	key := ""
	if dep.InterfaceSelectedBy != "" {
		key = paramEnvKey(moduleName, mod.EnvPrefix, dep.InterfaceSelectedBy)
		if err := a.rejectSourceSensitiveSelector(key, moduleName+"."+dep.InterfaceSelectedBy); err != nil {
			return "", err
		}
	}
	requested := strings.ToLower(strings.TrimSpace(a.env[key]))
	iface := ""
	switch {
	case requested != "" && requested != "auto":
		if !contains(dep.AnyOf, requested) {
			return "", fmt.Errorf("%s.%s is %q, but %s supports [%s];\nset %s.%s to one of: %s, auto",
				moduleName, dep.InterfaceSelectedBy, a.resolvedValueForError(key, requested), moduleName, strings.Join(dep.AnyOf, ","),
				moduleName, dep.InterfaceSelectedBy, strings.Join(dep.AnyOf, ", "))
		}
		iface = requested
	default:
		definition := capabilityDefinitions[dep.Name]
		fallback := ""
		if a.cfg != nil && definition.DefaultInterface != nil {
			fallback = definition.DefaultInterface(a)
		}
		if fallback != "" {
			known, _ := knownCapabilityInterfaces(dep.Name)
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
			moduleName, dep.Name, a.resolvedValueForError(key, iface), provider, strings.Join(capability.Interfaces, ","))
	}
	if key != "" {
		a.env[key] = iface
	}
	return iface, nil
}

// capabilityProviderNames lists every module that qualifies as a provider for a
// capability, so an error can tell the user what they may actually choose.
func (a *app) capabilityProviderNames(capability string) []string {
	names := []string{}
	for name, mod := range a.reg {
		if _, ok := mod.providedCapability(capability); ok {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names
}

func (a *app) listCapabilityProviders(capability string) string {
	names := a.capabilityProviderNames(capability)
	if len(names) == 0 {
		return "(no module provides the " + capability + " capability)"
	}
	return strings.Join(names, ", ")
}

// describeCapabilityProviders renders providers with their interfaces, e.g.
// "llng[oidc,saml]".
func (a *app) describeCapabilityProviders(capability string) string {
	names := a.capabilityProviderNames(capability)
	if len(names) == 0 {
		return "(no module provides the " + capability + " capability)"
	}
	out := make([]string, 0, len(names))
	for _, name := range names {
		provided, _ := a.reg[name].providedCapability(capability)
		out = append(out, fmt.Sprintf("%s[%s]", name, strings.Join(provided.Interfaces, ",")))
	}
	return strings.Join(out, ", ")
}

func capabilityOutputBindingKey(definition capabilityOutputDefinition, consumer, suffix string) string {
	return definition.BindingPrefix + defaultEnvPrefix(consumer) + "__" + suffix
}

// publishCapabilityOutputs copies a selected provider's normalized connection
// facts into consumer-owned binding namespaces. It runs immediately after the
// provider calculate Hook, so later consumer Hooks can read only their own
// projection without knowing the provider Module name or private env prefix.
func (a *app) publishCapabilityOutputs(provider string) error {
	capabilityNames := make([]string, 0, len(capabilityDefinitions))
	for name := range capabilityDefinitions {
		capabilityNames = append(capabilityNames, name)
	}
	sort.Strings(capabilityNames)
	consumerNames := make([]string, 0, len(a.resolvedBindings))
	for consumer := range a.resolvedBindings {
		consumerNames = append(consumerNames, consumer)
	}
	sort.Strings(consumerNames)

	for _, capabilityName := range capabilityNames {
		definition := capabilityDefinitions[capabilityName]
		if len(definition.Outputs) == 0 || a.capabilityProviders[capabilityName] != provider {
			continue
		}
		for _, consumer := range consumerNames {
			binding := a.resolvedBindings[consumer]
			if binding[capabilityName] != provider {
				continue
			}
			iface := binding[capabilityName+".interface"]
			output, ok := definition.Outputs[iface]
			if !ok {
				return fmt.Errorf("capability %s/%s has no registered output ABI", capabilityName, iface)
			}
			setBinding := func(key, value string, sensitive bool) error {
				if existing := a.env[key]; existing != "" && existing != value {
					return fmt.Errorf("%s binding output %s conflicts with an existing value", capabilityName, key)
				}
				if owner, tracked := a.envOwner[key]; tracked && owner != consumer {
					return fmt.Errorf("%s binding output %s is owned by another source", capabilityName, key)
				}
				a.env[key] = value
				a.setEnvOwner(key, consumer)
				if sensitive {
					a.markSensitive(key)
				}
				return nil
			}
			interfaceKey := capabilityOutputBindingKey(output, consumer, "INTERFACE")
			if err := setBinding(interfaceKey, iface, false); err != nil {
				return err
			}
			for _, suffix := range output.Required {
				sourceKey := output.ProviderPrefix + suffix
				value := strings.TrimSpace(a.env[sourceKey])
				if value == "" {
					return fmt.Errorf("%s provider %s did not publish required output %s", capabilityName, provider, sourceKey)
				}
				if owner := a.envOwner[sourceKey]; owner != provider {
					return fmt.Errorf("%s provider %s does not own required output %s", capabilityName, provider, sourceKey)
				}
				targetKey := capabilityOutputBindingKey(output, consumer, suffix)
				sensitive := contains(output.Sensitive, suffix)
				if err := setBinding(targetKey, value, sensitive); err != nil {
					return err
				}
				if sensitive {
					a.markSensitive(sourceKey)
				}
			}
		}
	}
	return nil
}

// checkSingleIAM rejects a config that would start two IAMs at once. Listing an
// IAM in modules is how a user starts one without any consumer, so the check is
// over explicitly listed modules plus the selected provider.
//
// The restriction is specific to IAM rather than general to capabilities: two
// identity providers would both claim to be the deployment's source of truth
// for accounts. Two forward-auth gateways in front of different services are
// merely redundant, so nothing rejects them.
func (a *app) checkSingleIAM() error {
	if a.cfg == nil {
		return nil
	}
	active := []string{}
	for _, name := range a.cfg.Modules.Order {
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
	envIAMProvider         = "ANAS_IAM_PROVIDER"
	envIAMInterfaces       = "ANAS_IAM_INTERFACES"
	envIAMBindingPfx       = "ANAS_IAM_BINDING__"
	envIdentityClients     = "ANAS_IDENTITY_CLIENTS"
	envIdentityAppClients  = "ANAS_IDENTITY_APP_CLIENTS"
	envIdentityClientsTmpl = "ANAS_IDENTITY_%s_CLIENTS"
	envIdentityClientPfx   = "ANAS_IDENTITY_CLIENT__"
)

// requiredEndpointSuffixes lists the per-binding endpoint variables a provider
// must publish for each protocol it is actually bound to. SLO is optional.
var requiredEndpointSuffixes = map[string][]string{
	interfaceOIDC: {"OIDC_ISSUER_URL", "OIDC_DISCOVERY_URL"},
	interfaceSAML: {"SAML_METADATA_URL", "SAML_ENTITY_ID", "SAML_SSO_URL"},
}

var iamLogoutValues = map[string]map[string]bool{
	"OIDC_LOGOUT_METHODS": {"backchannel": true, "frontchannel": true},
	"SAML_SLS_BINDINGS":   {"redirect": true, "post": true},
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
// module has to rediscover by scanning.
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
func (a *app) publishIAMEnv(selected []string) {
	if a.env == nil {
		a.env = map[string]string{}
	}
	set := func(key, value, owner string) {
		a.env[key] = value
		a.setEnvOwner(key, owner)
	}
	if a.iamProvider != "" {
		capability, _ := a.reg[a.iamProvider].providedCapability(capabilityIAM)
		set(envIAMProvider, a.iamProvider, a.iamProvider)
		set(envIAMInterfaces, strings.Join(capability.Interfaces, ","), a.iamProvider)
	}
	for _, consumer := range a.iamConsumers() {
		set(iamBindingKey(consumer, "INTERFACE"), a.iamBindings[consumer], a.iamProvider)
	}

	// Identity topology covers every direct and federated authentication
	// protocol. It replaces the LDAP-only USE_LDAP_MODS_NAME and the IAM-only
	// per-protocol client lists with one source of truth.
	clientIfaces := map[string][]string{}
	appClients := []string{}
	for _, name := range selected {
		mod := a.reg[name]
		for _, iface := range mod.IdentityInterfaces {
			clientIfaces[name] = appendUnique(clientIfaces[name], iface)
		}
		if mod.IdentityAppGroup {
			appClients = appendUnique(appClients, name)
		}
	}
	for consumer, iface := range a.iamBindings {
		clientIfaces[consumer] = appendUnique(clientIfaces[consumer], iface)
	}
	clients := make([]string, 0, len(clientIfaces))
	byInterface := map[string][]string{}
	for client, ifaces := range clientIfaces {
		clients = append(clients, client)
		sort.Strings(ifaces)
		for _, iface := range ifaces {
			byInterface[iface] = append(byInterface[iface], client)
		}
	}
	sort.Strings(clients)
	sort.Strings(appClients)
	// The topology is a runner-owned cross-module contract rather than the output
	// of any module, and unlike the deployment's global parameters it is not for
	// everyone to read.
	// Giving it a synthetic owner keeps it out of every dependency closure;
	// only manifests that explicitly consume a key receive it in their .env.
	set(envIdentityClients, strings.Join(clients, ","), runnerScope)
	set(envIdentityAppClients, strings.Join(appClients, ","), runnerScope)
	for iface, names := range byInterface {
		sort.Strings(names)
		set(fmt.Sprintf(envIdentityClientsTmpl, strings.ToUpper(iface)), strings.Join(names, ","), runnerScope)
	}
	for _, client := range clients {
		key := envIdentityClientPfx + strings.ToUpper(strings.ReplaceAll(client, "-", "_")) + "__INTERFACES"
		set(key, strings.Join(clientIfaces[client], ","), runnerScope)
	}
}

func appendUnique(in []string, value string) []string {
	if value == "" || contains(in, value) {
		return in
	}
	return append(in, value)
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
			"module": consumer, "interface": a.iamBindings[consumer],
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

// validateIAMClientRegistrations checks the provider-neutral logout contract
// after every consumer has completed calculate and before the selected IAM
// translates registrations in render_env. Logout support is optional, but a
// consumer that declares it must publish a usable endpoint and known values;
// silently dropping an incomplete declaration would leave an IAM logout with
// a live application session.
func (a *app) validateIAMClientRegistrations() error {
	if a.iamProvider == "" {
		return nil
	}
	for _, consumer := range a.iamConsumers() {
		prefix := "ANAS_IAM_CLIENT__" + strings.ToUpper(strings.ReplaceAll(consumer, "-", "_")) + "__"
		switch a.iamBindings[consumer] {
		case interfaceOIDC:
			if err := rejectIAMClientFields(a.env, consumer, prefix, []string{"SAML_SLS_URL", "SAML_SLS_BINDINGS"}, interfaceOIDC); err != nil {
				return err
			}
			if err := validateIAMHTTPSList(consumer, prefix+"POST_LOGOUT_REDIRECT_URIS", a.env[prefix+"POST_LOGOUT_REDIRECT_URIS"]); err != nil {
				return err
			}
			methods := splitIAMLogoutList(a.env[prefix+"OIDC_LOGOUT_METHODS"])
			uri := strings.TrimSpace(a.env[prefix+"OIDC_LOGOUT_URI"])
			if len(methods) > 0 && uri == "" {
				return fmt.Errorf("iam client %s declares OIDC logout methods but %sOIDC_LOGOUT_URI is empty", consumer, prefix)
			}
			if uri != "" && len(methods) == 0 {
				return fmt.Errorf("iam client %s publishes %sOIDC_LOGOUT_URI but no OIDC_LOGOUT_METHODS", consumer, prefix)
			}
			if err := validateIAMLogoutValues(consumer, prefix+"OIDC_LOGOUT_METHODS", "OIDC_LOGOUT_METHODS", methods); err != nil {
				return err
			}
			if uri != "" {
				if err := validateIAMHTTPSURL(consumer, prefix+"OIDC_LOGOUT_URI", uri); err != nil {
					return err
				}
			}
			if raw := strings.TrimSpace(a.env[prefix+"OIDC_LOGOUT_SESSION_REQUIRED"]); raw != "" {
				if len(methods) == 0 {
					return fmt.Errorf("iam client %s publishes %sOIDC_LOGOUT_SESSION_REQUIRED but no OIDC logout method", consumer, prefix)
				}
				if raw != "true" && raw != "false" {
					return fmt.Errorf("iam client %s publishes invalid %sOIDC_LOGOUT_SESSION_REQUIRED %q; want true or false", consumer, prefix, raw)
				}
			}
		case interfaceSAML:
			if err := rejectIAMClientFields(a.env, consumer, prefix, []string{
				"POST_LOGOUT_REDIRECT_URIS", "OIDC_LOGOUT_URI", "OIDC_LOGOUT_METHODS", "OIDC_LOGOUT_SESSION_REQUIRED",
			}, interfaceSAML); err != nil {
				return err
			}
			bindings := splitIAMLogoutList(a.env[prefix+"SAML_SLS_BINDINGS"])
			uri := strings.TrimSpace(a.env[prefix+"SAML_SLS_URL"])
			if len(bindings) > 0 && uri == "" {
				return fmt.Errorf("iam client %s declares SAML SLS bindings but %sSAML_SLS_URL is empty", consumer, prefix)
			}
			if uri != "" && len(bindings) == 0 {
				return fmt.Errorf("iam client %s publishes %sSAML_SLS_URL but no SAML_SLS_BINDINGS", consumer, prefix)
			}
			if err := validateIAMLogoutValues(consumer, prefix+"SAML_SLS_BINDINGS", "SAML_SLS_BINDINGS", bindings); err != nil {
				return err
			}
			if uri != "" {
				if err := validateIAMHTTPSURL(consumer, prefix+"SAML_SLS_URL", uri); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func rejectIAMClientFields(env map[string]string, consumer, prefix string, suffixes []string, activeInterface string) error {
	for _, suffix := range suffixes {
		key := prefix + suffix
		if strings.TrimSpace(env[key]) != "" {
			return fmt.Errorf("iam client %s publishes stale %s for active %s interface", consumer, key, activeInterface)
		}
	}
	return nil
}

func validateIAMHTTPSList(consumer, key, raw string) error {
	for _, value := range strings.Split(raw, ",") {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if err := validateIAMHTTPSURL(consumer, key, value); err != nil {
			return err
		}
	}
	return nil
}

func validateIAMHTTPSURL(consumer, key, raw string) error {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil {
		return fmt.Errorf("iam client %s publishes invalid %s %q; want an absolute HTTPS URL", consumer, key, raw)
	}
	return nil
}

func validateIAMLogoutValues(consumer, key, vocabulary string, values []string) error {
	allowed := iamLogoutValues[vocabulary]
	for _, value := range values {
		if !allowed[value] {
			return fmt.Errorf("iam client %s publishes unsupported %s value %q", consumer, key, value)
		}
	}
	return nil
}

func splitIAMLogoutList(raw string) []string {
	values := []string{}
	for _, item := range strings.Split(raw, ",") {
		item = strings.ToLower(strings.TrimSpace(item))
		if item != "" && !contains(values, item) {
			values = append(values, item)
		}
	}
	return values
}

func normalizeProvidedCapabilities(module string, in []manifestProvidedCapability) ([]ProvidedCapability, error) {
	out := []ProvidedCapability{}
	seen := map[string]bool{}
	for _, capability := range in {
		name := strings.ToLower(strings.TrimSpace(capability.Name))
		if name == "" {
			return nil, fmt.Errorf("module %q has capabilities.provides entry without name", module)
		}
		known, ok := knownCapabilityInterfaces(name)
		if !ok {
			return nil, fmt.Errorf("module %q provides unknown capability %q; known capabilities: %s",
				module, name, strings.Join(knownCapabilityNames(), ", "))
		}
		if seen[name] {
			return nil, fmt.Errorf("module %q provides capability %q more than once", module, name)
		}
		seen[name] = true
		interfaces, err := normalizeInterfaceList(module, "capabilities.provides."+name+".interfaces", known, capability.Interfaces)
		if err != nil {
			return nil, err
		}
		if len(interfaces) == 0 {
			return nil, fmt.Errorf("module %q provides capability %q without interfaces", module, name)
		}
		// Admission: a provider must serve everything the capability promises
		// its consumers, checked here so qualification never depends on the
		// user's configuration.
		if required := capabilityDefinitions[name].RequireAll; len(required) > 0 {
			if missing := missingInterfaces(interfaces, required); len(missing) > 0 {
				return nil, fmt.Errorf(
					"module %q declares capability %s with interfaces [%s]; a provider of %s must declare all of: %s",
					module, name, strings.Join(interfaces, ","), name, strings.Join(required, ", "))
			}
		}
		out = append(out, ProvidedCapability{Name: name, Interfaces: interfaces})
	}
	return out, nil
}

func normalizeRequiredCapabilities(module string, in []manifestRequiredCapability, types map[string]ParamType) ([]RequiredCapability, error) {
	out := []RequiredCapability{}
	seen := map[string]bool{}
	for _, capability := range in {
		name := strings.ToLower(strings.TrimSpace(capability.Name))
		if name == "" {
			return nil, fmt.Errorf("module %q has requires_capabilities entry without name", module)
		}
		known, ok := knownCapabilityInterfaces(name)
		if !ok {
			return nil, fmt.Errorf("module %q requires unknown capability %q; known capabilities: %s",
				module, name, strings.Join(knownCapabilityNames(), ", "))
		}
		if seen[name] {
			return nil, fmt.Errorf("module %q requires capability %q more than once", module, name)
		}
		seen[name] = true
		anyOf, err := normalizeInterfaceList(module, "requires_capabilities."+name+".interfaces.any_of", known, capability.Interfaces.AnyOf)
		if err != nil {
			return nil, err
		}
		if len(anyOf) == 0 {
			implicit := capabilityDefinitions[name].ImplicitInterface
			if implicit == "" {
				return nil, fmt.Errorf("module %q requires_capabilities %q has an empty any_of", module, name)
			}
			anyOf = []string{implicit}
		}
		selectedBy := strings.TrimSpace(capability.InterfaceSelectedBy)
		if selectedBy == "" && capabilityDefinitions[name].ImplicitInterface == "" {
			return nil, fmt.Errorf("module %q requires_capabilities %q has no interface_selected_by parameter", module, name)
		}
		prefer, err := normalizeInterfaceList(module, "requires_capabilities."+name+".interfaces.prefer", known, capability.Interfaces.Prefer)
		if err != nil {
			return nil, err
		}
		for _, item := range prefer {
			if !contains(anyOf, item) {
				return nil, fmt.Errorf("module %q requires_capabilities %q prefers %q which is not in any_of [%s]",
					module, name, item, strings.Join(anyOf, ","))
			}
		}
		enabledBy, err := normalizeCapabilityCondition(module, name, capability.EnabledBy, types)
		if err != nil {
			return nil, err
		}
		ordering, err := normalizeCapabilityOrdering(module, name, capability.Ordering)
		if err != nil {
			return nil, err
		}
		out = append(out, RequiredCapability{
			Name: name, InterfaceSelectedBy: selectedBy, AnyOf: anyOf, Prefer: prefer,
			EnabledBy: enabledBy, Ordering: ordering,
		})
	}
	return out, nil
}

// normalizeCapabilityCondition admits only a boolean parameter this same module
// declares. The three rejections are not stylistic:
//
// A parameter belonging to another module, or a raw environment key, could not
// be evaluated where this condition is read: the dependency graph is built
// before any other module's values are known.
//
// A parameter absent from config.types would be a name nobody validates, and a
// misspelled name here does not fail loudly -- it silently evaluates to false,
// which for the first consumer of this field means an authentication gateway
// quietly disappearing.
//
// A non-boolean parameter would need a truthiness rule, and every truthiness
// rule turns some legitimate value into an accidental false for the same silent
// consequence.
func normalizeCapabilityCondition(module, capability, declared string, types map[string]ParamType) (string, error) {
	parameter := strings.ToLower(strings.TrimSpace(declared))
	if parameter == "" {
		return "", nil
	}
	if strings.HasPrefix(parameter, "global.") || isEnvKey(declared) {
		return "", fmt.Errorf("module %q requires_capabilities %q enabled_by %q must name a parameter of this module, not a global or environment key",
			module, capability, declared)
	}
	if !configParameterNamePattern.MatchString(parameter) {
		return "", fmt.Errorf("module %q requires_capabilities %q enabled_by %q is not lower-snake-case",
			module, capability, declared)
	}
	declaredType, ok := types[parameter]
	if !ok {
		return "", fmt.Errorf("module %q requires_capabilities %q enabled_by %q is not declared in config.types",
			module, capability, parameter)
	}
	if strings.TrimSpace(declaredType.Kind) != "bool" {
		return "", fmt.Errorf("module %q requires_capabilities %q enabled_by %q must be a bool parameter",
			module, capability, parameter)
	}
	return parameter, nil
}

// normalizeCapabilityOrdering closes the vocabulary to two values so a typo can
// never be read as the permissive one. Absent means orderingBefore, which is why
// every existing declaration keeps its behaviour untouched.
func normalizeCapabilityOrdering(module, capability, declared string) (string, error) {
	value := strings.ToLower(strings.TrimSpace(declared))
	switch value {
	case "":
		return orderingBefore, nil
	case orderingBefore:
		return orderingBefore, nil
	case orderingAny:
	default:
		return "", fmt.Errorf("module %q requires_capabilities %q ordering %q must be %s or %s",
			module, capability, declared, orderingBefore, orderingAny)
	}
	// A capability with a registered output ABI is projected into the consumer's
	// binding namespace only after the provider's calculate Hook, and those keys
	// belong to the consumer rather than the provider -- so dropping the ordering
	// edge would make them present or absent by luck, and the ownership filter
	// that protects the provider's own keys would not notice. Refusing the pair
	// keeps that from being a silent hole; making the projection order-free is a
	// separate piece of work.
	if len(capabilityDefinitions[capability].Outputs) > 0 {
		return "", fmt.Errorf("module %q requires_capabilities %q cannot use ordering %s: %s publishes a projected output ABI, which is only available after its provider resolves",
			module, capability, orderingAny, capability)
	}
	return orderingAny, nil
}

// capabilityRequired evaluates a conditional dependency's condition.
//
// It reads the declared default itself rather than trusting a.env to carry it,
// because it cannot: applyModuleDefaults runs after resolveOrder and iterates
// the resolved order, so at this point a.env holds only values the operator
// wrote down. Reading a.env alone would make an unset parameter look empty --
// false for a switch whose declared default is true, and vice versa once such a
// module exists.
func (a *app) capabilityRequired(moduleName string, mod Module, dep RequiredCapability) bool {
	if dep.EnabledBy == "" {
		return true
	}
	key := paramEnvKey(moduleName, mod.EnvPrefix, dep.EnabledBy)
	value := strings.TrimSpace(a.env[key])
	if value == "" {
		value = strings.TrimSpace(mod.Defaults[key])
	}
	return strings.EqualFold(value, "true")
}

// normalizeInterfaceList lowercases and de-duplicates protocol identifiers,
// rejecting any the runner does not know for this capability.
func normalizeInterfaceList(module, field string, known, in []string) ([]string, error) {
	out := []string{}
	for _, raw := range in {
		item := strings.ToLower(strings.TrimSpace(raw))
		if item == "" {
			return nil, fmt.Errorf("module %q %s contains an empty interface", module, field)
		}
		if !contains(known, item) {
			return nil, fmt.Errorf("module %q %s contains unknown interface %q; known interfaces: %s",
				module, field, item, strings.Join(known, ", "))
		}
		if contains(out, item) {
			return nil, fmt.Errorf("module %q %s lists interface %q more than once", module, field, item)
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
	names := make([]string, 0, len(capabilityDefinitions))
	for name := range capabilityDefinitions {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
