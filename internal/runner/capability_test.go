package runner

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/anas-project/ANAS/internal/config"
)

// Fixture casks for IAM binding. Real casks are still on requires_one for SSO,
// so these stand in for a migrated provider and its consumers.
const (
	fixtureCoreCask = `api_version: anas.dev/v1
kind: Cask
name: core
version: 0.0.0
revision: 1
abi:
  supports: [anas.cask/v2]
runtime:
  type: builtin
`
	// A qualified IAM provider: both protocols, as admission requires.
	fixtureProviderCask = `api_version: anas.dev/v1
kind: Cask
name: %s
version: 1.0.0
revision: 1
abi:
  supports: [anas.cask/v2]
runtime:
  type: builtin
capabilities:
  provides:
    - name: iam
      interfaces: [oidc, saml]
`
	// A consumer that speaks both protocols and prefers OIDC.
	fixtureDualConsumerCask = `api_version: anas.dev/v1
kind: Cask
name: nextcloud
version: 1.0.0
revision: 1
abi:
  supports: [anas.cask/v2]
runtime:
  type: builtin
dependencies:
  requires_capabilities:
    - name: iam
      interface_selected_by: iam_protocol
      interfaces:
        any_of: [oidc, saml]
        prefer: [oidc, saml]
config:
  defaults:
    iam_protocol: auto
`
	// A consumer that only speaks OIDC.
	fixtureOIDCConsumerCask = `api_version: anas.dev/v1
kind: Cask
name: netbird
version: 1.0.0
revision: 1
abi:
  supports: [anas.cask/v2]
runtime:
  type: builtin
dependencies:
  requires_capabilities:
    - name: iam
      interface_selected_by: iam_protocol
      interfaces:
        any_of: [oidc]
        prefer: [oidc]
config:
  defaults:
    iam_protocol: auto
`
)

// writeCasks materializes cask.yml files and loads them as a registry.
func writeCasks(t *testing.T, manifests map[string]string) map[string]Module {
	t.Helper()
	root := t.TempDir()
	for name, body := range manifests {
		dir := filepath.Join(root, name)
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "cask.yml"), []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	reg, err := loadRegistryDir(root)
	if err != nil {
		t.Fatalf("load registry: %v", err)
	}
	return reg
}

// iamFixtureRegistry is the common llng + nextcloud + netbird arrangement.
func iamFixtureRegistry(t *testing.T) map[string]Module {
	t.Helper()
	return writeCasks(t, map[string]string{
		"core":      fixtureCoreCask,
		"llng":      strings.Replace(fixtureProviderCask, "%s", "llng", 1),
		"nextcloud": fixtureDualConsumerCask,
		"netbird":   fixtureOIDCConsumerCask,
	})
}

func newIAMApp(reg map[string]Module, cfg *config.File) *app {
	a := &app{
		cfg:              cfg,
		reg:              reg,
		env:              map[string]string{},
		envOwner:         map[string]string{},
		lock:             &caskLock{Bindings: map[string]map[string]string{}},
		resolvedBindings: map[string]map[string]string{},
	}
	a.applyModuleDefaults()
	return a
}

func iamConfig(modules []string, provider, defaultProtocol string, services map[string]config.Service) *config.File {
	if services == nil {
		services = map[string]config.Service{}
	}
	return &config.File{
		Modules:  modules,
		IAM:      config.IAM{Provider: provider, DefaultProtocol: defaultProtocol},
		Services: services,
	}
}

func TestIAMAutoUsesManifestPreference(t *testing.T) {
	reg := iamFixtureRegistry(t)
	a := newIAMApp(reg, iamConfig([]string{"nextcloud"}, "llng", "", nil))
	order, err := a.resolveOrder(a.cfg.Modules)
	if err != nil {
		t.Fatal(err)
	}
	if got := a.iamBindings["nextcloud"]; got != interfaceOIDC {
		t.Fatalf("nextcloud interface = %q, want oidc", got)
	}
	if !contains(order, "llng") || index(order, "llng") > index(order, "nextcloud") {
		t.Fatalf("order = %v, want llng before nextcloud", order)
	}
}

func TestIAMExplicitProtocolWins(t *testing.T) {
	reg := iamFixtureRegistry(t)
	a := newIAMApp(reg, iamConfig([]string{"nextcloud"}, "llng", "", map[string]config.Service{
		"nextcloud": {Env: map[string]any{"iam_protocol": "saml"}},
	}))
	a.env["NEXTCLOUD_IAM_PROTOCOL"] = "saml"
	if _, err := a.resolveOrder(a.cfg.Modules); err != nil {
		t.Fatal(err)
	}
	if got := a.iamBindings["nextcloud"]; got != interfaceSAML {
		t.Fatalf("nextcloud interface = %q, want saml", got)
	}
}

func TestIAMDefaultProtocolAppliesOnlyWhereSupported(t *testing.T) {
	reg := iamFixtureRegistry(t)
	a := newIAMApp(reg, iamConfig([]string{"nextcloud", "netbird"}, "llng", "saml", nil))
	if _, err := a.resolveOrder(a.cfg.Modules); err != nil {
		t.Fatal(err)
	}
	if got := a.iamBindings["nextcloud"]; got != interfaceSAML {
		t.Fatalf("nextcloud interface = %q, want saml from iam.default_protocol", got)
	}
	// netbird cannot speak SAML, so the deployment default must not drag it
	// outside its own any_of.
	if got := a.iamBindings["netbird"]; got != interfaceOIDC {
		t.Fatalf("netbird interface = %q, want oidc", got)
	}
}

func TestIAMRejectsUnknownDefaultProtocol(t *testing.T) {
	reg := iamFixtureRegistry(t)
	a := newIAMApp(reg, iamConfig([]string{"nextcloud"}, "llng", "ldap", nil))
	_, err := a.resolveOrder(a.cfg.Modules)
	if err == nil || !strings.Contains(err.Error(), "iam.default_protocol") {
		t.Fatalf("error = %v, want rejection of an unknown default protocol", err)
	}
}

func TestIAMExplicitProtocolOutsideAnyOfFails(t *testing.T) {
	reg := iamFixtureRegistry(t)
	a := newIAMApp(reg, iamConfig([]string{"netbird"}, "llng", "", nil))
	a.env["NETBIRD_IAM_PROTOCOL"] = "saml"
	_, err := a.resolveOrder(a.cfg.Modules)
	if err == nil || !strings.Contains(err.Error(), "netbird supports [oidc]") {
		t.Fatalf("error = %v, want netbird to reject saml", err)
	}
}

func TestIAMProviderRequiredAndNeverAutoSelected(t *testing.T) {
	reg := iamFixtureRegistry(t)
	// llng is the only qualified provider; the runner must still refuse to
	// pick it on the user's behalf.
	a := newIAMApp(reg, iamConfig([]string{"nextcloud"}, "", "", nil))
	_, err := a.resolveOrder(a.cfg.Modules)
	if err == nil || !strings.Contains(err.Error(), "iam.provider is not set") {
		t.Fatalf("error = %v, want a missing iam.provider error", err)
	}
	if !strings.Contains(err.Error(), "llng") {
		t.Fatalf("error = %v, want the message to list llng as a choice", err)
	}
}

func TestIAMProviderMustExist(t *testing.T) {
	reg := iamFixtureRegistry(t)
	a := newIAMApp(reg, iamConfig([]string{"nextcloud"}, "foo", "", nil))
	_, err := a.resolveOrder(a.cfg.Modules)
	if err == nil || !strings.Contains(err.Error(), `iam.provider "foo"`) {
		t.Fatalf("error = %v, want an unknown provider error", err)
	}
}

func TestIAMProviderMustProvideCapability(t *testing.T) {
	reg := iamFixtureRegistry(t)
	a := newIAMApp(reg, iamConfig([]string{"nextcloud"}, "netbird", "", nil))
	_, err := a.resolveOrder(a.cfg.Modules)
	if err == nil || !strings.Contains(err.Error(), "does not provide capability") {
		t.Fatalf("error = %v, want a non-provider cask to be rejected", err)
	}
	if !strings.Contains(err.Error(), "llng[oidc,saml]") {
		t.Fatalf("error = %v, want the message to describe available providers", err)
	}
}

func TestIAMProviderMustBeEnabled(t *testing.T) {
	reg := iamFixtureRegistry(t)
	disabled := false
	a := newIAMApp(reg, iamConfig([]string{"nextcloud"}, "llng", "", map[string]config.Service{
		"llng": {Enabled: &disabled},
	}))
	_, err := a.resolveOrder(a.cfg.Modules)
	if err == nil || !strings.Contains(err.Error(), "disabled") {
		t.Fatalf("error = %v, want a disabled provider to be rejected", err)
	}
}

func TestTwoActiveIAMsFail(t *testing.T) {
	reg := writeCasks(t, map[string]string{
		"core":      fixtureCoreCask,
		"llng":      strings.Replace(fixtureProviderCask, "%s", "llng", 1),
		"authentik": strings.Replace(fixtureProviderCask, "%s", "authentik", 1),
		"nextcloud": fixtureDualConsumerCask,
	})
	a := newIAMApp(reg, iamConfig([]string{"nextcloud", "authentik"}, "llng", "", nil))
	_, err := a.resolveOrder(a.cfg.Modules)
	if err == nil || !strings.Contains(err.Error(), "only one IAM") {
		t.Fatalf("error = %v, want two active IAMs to be rejected", err)
	}
}

func TestIAMProviderNotStartedWithoutConsumer(t *testing.T) {
	reg := iamFixtureRegistry(t)
	// iam.provider is set but nothing consumes the capability.
	a := newIAMApp(reg, iamConfig([]string{"core"}, "llng", "", nil))
	order, err := a.resolveOrder(a.cfg.Modules)
	if err != nil {
		t.Fatal(err)
	}
	if contains(order, "llng") {
		t.Fatalf("order = %v, want llng left out when nothing consumes iam", order)
	}
	if a.env[envIAMProvider] != "" {
		t.Fatalf("%s = %q, want no IAM contract without consumers", envIAMProvider, a.env[envIAMProvider])
	}
}

func TestIAMEnvContractAndClientListPartition(t *testing.T) {
	reg := iamFixtureRegistry(t)
	nextcloud := reg["nextcloud"]
	nextcloud.IdentityInterfaces = []string{"ldaps"}
	nextcloud.IdentityAppGroup = true
	reg["nextcloud"] = nextcloud
	netbird := reg["netbird"]
	netbird.IdentityAppGroup = true
	reg["netbird"] = netbird
	a := newIAMApp(reg, iamConfig([]string{"nextcloud", "netbird"}, "llng", "", nil))
	a.env["NEXTCLOUD_IAM_PROTOCOL"] = "saml"
	if _, err := a.resolveOrder(a.cfg.Modules); err != nil {
		t.Fatal(err)
	}
	if got := a.env[envIAMProvider]; got != "llng" {
		t.Fatalf("%s = %q, want llng", envIAMProvider, got)
	}
	if got := a.env[envIAMInterfaces]; got != "oidc,saml" {
		t.Fatalf("%s = %q, want oidc,saml", envIAMInterfaces, got)
	}
	flat := splitList(a.env[envIdentityClients])
	oidc := splitList(a.env["ANAS_IDENTITY_OIDC_CLIENTS"])
	saml := splitList(a.env["ANAS_IDENTITY_SAML_CLIENTS"])
	if len(flat) != 2 || !contains(flat, "netbird") || !contains(flat, "nextcloud") {
		t.Fatalf("%s = %v, want both consumers", envIdentityClients, flat)
	}
	if got := a.env[envIdentityAppClients]; got != "netbird,nextcloud" {
		t.Fatalf("%s = %q, want both application-group consumers", envIdentityAppClients, got)
	}
	if got := a.env[envIdentityClientPfx+"NEXTCLOUD__INTERFACES"]; got != "ldaps,saml" {
		t.Fatalf("nextcloud identity interfaces = %q, want ldaps,saml", got)
	}
	if got := a.envOwner[envIdentityClients]; got != "runner" {
		t.Fatalf("identity contract owner = %q, want runner", got)
	}
	for _, removed := range []string{"USE_LDAP_MODS_NAME", "ANAS_IAM_CLIENTS", "ANAS_IAM_OIDC_CLIENTS", "ANAS_IAM_SAML_CLIENTS"} {
		if _, ok := a.env[removed]; ok {
			t.Fatalf("removed compatibility variable %s was published", removed)
		}
	}
	// The per-protocol lists must partition the flat list: disjoint, and
	// together covering every consumer.
	for _, name := range oidc {
		if contains(saml, name) {
			t.Fatalf("%s appears in both protocol client lists", name)
		}
	}
	if len(oidc)+len(saml) != len(flat) {
		t.Fatalf("oidc=%v saml=%v do not partition %v", oidc, saml, flat)
	}
	for _, name := range flat {
		if !contains(oidc, name) && !contains(saml, name) {
			t.Fatalf("%s is missing from both protocol client lists", name)
		}
	}
	if a.env[iamBindingKey("netbird", "INTERFACE")] != interfaceOIDC {
		t.Fatalf("netbird binding = %q, want oidc", a.env[iamBindingKey("netbird", "INTERFACE")])
	}
	if a.env[iamBindingKey("nextcloud", "INTERFACE")] != interfaceSAML {
		t.Fatalf("nextcloud binding = %q, want saml", a.env[iamBindingKey("nextcloud", "INTERFACE")])
	}
}

func TestIAMEndpointValidationCoversBoundProtocolsOnly(t *testing.T) {
	reg := iamFixtureRegistry(t)
	a := newIAMApp(reg, iamConfig([]string{"netbird"}, "llng", "", nil))
	if _, err := a.resolveOrder(a.cfg.Modules); err != nil {
		t.Fatal(err)
	}
	if err := a.validateIAMEndpoints(); err == nil ||
		!strings.Contains(err.Error(), iamBindingKey("netbird", "OIDC_DISCOVERY_URL")) {
		t.Fatalf("error = %v, want the missing OIDC endpoint reported", err)
	}
	a.env[iamBindingKey("netbird", "OIDC_ISSUER_URL")] = "https://auth.example"
	a.env[iamBindingKey("netbird", "OIDC_DISCOVERY_URL")] = "https://auth.example/.well-known/openid-configuration"
	// No consumer is bound to SAML, so a provider that publishes no SAML
	// endpoint at all must still pass.
	if err := a.validateIAMEndpoints(); err != nil {
		t.Fatalf("validate = %v, want SAML endpoints not required without a SAML consumer", err)
	}
}

func TestIAMEndpointsAreResolvedPerConsumer(t *testing.T) {
	reg := iamFixtureRegistry(t)
	a := newIAMApp(reg, iamConfig([]string{"nextcloud", "netbird"}, "llng", "", nil))
	if _, err := a.resolveOrder(a.cfg.Modules); err != nil {
		t.Fatal(err)
	}
	// Stand in for a provider that mints a different endpoint per
	// application; each consumer must see its own, not a shared singleton.
	for _, name := range []string{"netbird", "nextcloud"} {
		a.env[iamBindingKey(name, "OIDC_ISSUER_URL")] = "https://auth.example/application/o/" + name + "/"
		a.env[iamBindingKey(name, "OIDC_DISCOVERY_URL")] = "https://auth.example/application/o/" + name + "/.well-known/openid-configuration"
	}
	if err := a.validateIAMEndpoints(); err != nil {
		t.Fatal(err)
	}
	netbird := a.env[iamBindingKey("netbird", "OIDC_ISSUER_URL")]
	nextcloud := a.env[iamBindingKey("nextcloud", "OIDC_ISSUER_URL")]
	if netbird == nextcloud {
		t.Fatalf("consumers share issuer %q, want per-application endpoints", netbird)
	}
	if !strings.Contains(netbird, "/netbird/") || !strings.Contains(nextcloud, "/nextcloud/") {
		t.Fatalf("netbird=%q nextcloud=%q, want each consumer to read its own endpoint", netbird, nextcloud)
	}
}

func TestIAMBindingIsRecordedInLock(t *testing.T) {
	reg := iamFixtureRegistry(t)
	a := newIAMApp(reg, iamConfig([]string{"nextcloud", "netbird"}, "llng", "", nil))
	a.env["NEXTCLOUD_IAM_PROTOCOL"] = "saml"
	order, err := a.resolveOrder(a.cfg.Modules)
	if err != nil {
		t.Fatal(err)
	}
	a.order = order
	lock := &caskLock{Bindings: map[string]map[string]string{}}
	if err := a.updateCaskLock(lock, true); err != nil {
		t.Fatal(err)
	}
	if lock.IAM == nil || lock.IAM.Provider != "llng" {
		t.Fatalf("lock.IAM = %+v, want provider llng", lock.IAM)
	}
	if got := lock.Bindings["nextcloud"]["iam.interface"]; got != interfaceSAML {
		t.Fatalf("nextcloud locked interface = %q, want saml", got)
	}
	if got := lock.Bindings["netbird"]["iam.interface"]; got != interfaceOIDC {
		t.Fatalf("netbird locked interface = %q, want oidc", got)
	}
	if got := lock.Bindings["netbird"]["iam"]; got != "llng" {
		t.Fatalf("netbird locked provider = %q, want llng", got)
	}
}

func TestIAMProviderMustDeclareBothProtocols(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "authelia")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	manifest := `api_version: anas.dev/v1
kind: Cask
name: authelia
version: 1.0.0
revision: 1
abi:
  supports: [anas.cask/v2]
runtime:
  type: builtin
capabilities:
  provides:
    - name: iam
      interfaces: [oidc]
`
	if err := os.WriteFile(filepath.Join(dir, "cask.yml"), []byte(manifest), 0600); err != nil {
		t.Fatal(err)
	}
	_, err := loadModuleManifest(dir, "authelia")
	if err == nil || !strings.Contains(err.Error(), "must declare all of: oidc, saml") {
		t.Fatalf("error = %v, want an OIDC-only IAM to be rejected at manifest load", err)
	}
}

func TestCapabilityManifestValidation(t *testing.T) {
	cases := []struct {
		name     string
		manifest string
		want     string
	}{
		{
			name: "unknown provided interface",
			manifest: `capabilities:
  provides:
    - name: iam
      interfaces: [oidc, saml, ldap]
`,
			want: "unknown interface",
		},
		{
			name: "unknown capability",
			manifest: `capabilities:
  provides:
    - name: mail
      interfaces: [oidc]
`,
			want: "unknown capability",
		},
		{
			name: "prefer outside any_of",
			manifest: `dependencies:
  requires_capabilities:
    - name: iam
      interface_selected_by: iam_protocol
      interfaces:
        any_of: [oidc]
        prefer: [saml]
`,
			want: "not in any_of",
		},
		{
			name: "empty any_of",
			manifest: `dependencies:
  requires_capabilities:
    - name: iam
      interface_selected_by: iam_protocol
      interfaces:
        prefer: [oidc]
`,
			want: "empty any_of",
		},
		{
			name: "missing interface_selected_by",
			manifest: `dependencies:
  requires_capabilities:
    - name: iam
      interfaces:
        any_of: [oidc]
`,
			want: "no interface_selected_by",
		},
		{
			// A cask must not be able to pick its own provider: that would
			// mean two IAMs and therefore two logins.
			name: "provider selection field",
			manifest: `dependencies:
  requires_capabilities:
    - name: iam
      selected_by: global.iam.provider
      interface_selected_by: iam_protocol
      interfaces:
        any_of: [oidc]
`,
			want: "field selected_by not found",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "example")
			if err := os.MkdirAll(dir, 0700); err != nil {
				t.Fatal(err)
			}
			body := `api_version: anas.dev/v1
kind: Cask
name: example
version: 1.0.0
revision: 1
abi:
  supports: [anas.cask/v2]
runtime:
  type: builtin
` + tc.manifest
			if err := os.WriteFile(filepath.Join(dir, "cask.yml"), []byte(body), 0600); err != nil {
				t.Fatal(err)
			}
			_, err := loadModuleManifest(dir, "example")
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want it to mention %q", err, tc.want)
			}
		})
	}
}

func TestIAMPlanSummaryReportsBindings(t *testing.T) {
	reg := iamFixtureRegistry(t)
	a := newIAMApp(reg, iamConfig([]string{"nextcloud", "netbird"}, "llng", "", nil))
	a.env["NEXTCLOUD_IAM_PROTOCOL"] = "saml"
	if _, err := a.resolveOrder(a.cfg.Modules); err != nil {
		t.Fatal(err)
	}
	summary := a.iamPlanSummary()
	for _, want := range []string{"iam provider: llng", "netbird -> llng/oidc", "nextcloud -> llng/saml"} {
		if !strings.Contains(summary, want) {
			t.Fatalf("plan summary = %q, want it to contain %q", summary, want)
		}
	}
	empty := newIAMApp(reg, iamConfig([]string{"core"}, "llng", "", nil))
	if _, err := empty.resolveOrder(empty.cfg.Modules); err != nil {
		t.Fatal(err)
	}
	if got := empty.iamPlanSummary(); got != "" {
		t.Fatalf("plan summary = %q, want empty without consumers", got)
	}
}

func splitList(s string) []string {
	out := []string{}
	for _, item := range strings.Split(s, ",") {
		item = strings.TrimSpace(item)
		if item != "" {
			out = append(out, item)
		}
	}
	return out
}
