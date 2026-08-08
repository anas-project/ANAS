package runner

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/whlsxl/anas/internal/config"
)

// Fixtures for the auto-selected capability path. forward_auth is the first
// capability that is not IAM, so these cover the generic resolver rather than
// the identity-specific policy layered on top of it.
const (
	fixtureForwardAuthProvider = `api_version: anas.dev/v1
kind: Cask
name: %s
version: 1.0.0
abi:
  supports: [anas.cask/v2]
runtime:
  type: builtin
capabilities:
  provides:
    - name: forward_auth
      interfaces: [http]
`
	fixtureGatedConsumer = `api_version: anas.dev/v1
kind: Cask
name: ddns_go
version: 1.0.0
abi:
  supports: [anas.cask/v2]
runtime:
  type: builtin
dependencies:
  requires_capabilities:
    - name: forward_auth
      interface_selected_by: auth_interface
      interfaces:
        any_of: [http]
config:
  defaults:
    auth_interface: auto
`
)

func forwardAuthApp(t *testing.T, manifests map[string]string, cfg *config.File) *app {
	t.Helper()
	a := &app{
		cfg:              cfg,
		reg:              writeCasks(t, manifests),
		env:              map[string]string{},
		envOwner:         map[string]string{},
		lock:             &caskLock{Bindings: map[string]map[string]string{}},
		resolvedBindings: map[string]map[string]string{},
	}
	a.applyModuleDefaults()
	return a
}

// With one implementation, requiring the capability is enough: the user does
// not have to name a cask that has no alternative.
func TestAutoSelectionBindsTheOnlyProvider(t *testing.T) {
	a := forwardAuthApp(t, map[string]string{
		"core":         fixtureCoreCask,
		"oauth2_proxy": strings.Replace(fixtureForwardAuthProvider, "%s", "oauth2_proxy", 1),
		"ddns_go":      fixtureGatedConsumer,
	}, &config.File{Modules: []string{"ddns_go"}})

	order, err := a.resolveOrder([]string{"ddns_go"})
	if err != nil {
		t.Fatal(err)
	}
	if !contains(order, "oauth2_proxy") {
		t.Fatalf("provider was not pulled into the order: %v", order)
	}
	// The provider must be started before its consumer, or the consumer's
	// calculate would read endpoints that do not exist yet.
	if indexOf(order, "oauth2_proxy") > indexOf(order, "ddns_go") {
		t.Fatalf("provider ordered after consumer: %v", order)
	}
	if got := a.resolvedBindings["ddns_go"]["forward_auth"]; got != "oauth2_proxy" {
		t.Fatalf("binding = %q, want oauth2_proxy", got)
	}
	if got := a.resolvedBindings["ddns_go"]["forward_auth.interface"]; got != "http" {
		t.Fatalf("interface = %q, want http", got)
	}
	// Auto-selecting forward_auth must not disturb the IAM bookkeeping.
	if a.iamProvider != "" {
		t.Fatalf("iamProvider = %q, want empty", a.iamProvider)
	}
}

// Two implementations is the point at which the deployment does have to
// choose, and the error must say so rather than pick one.
func TestAutoSelectionRefusesToGuessBetweenProviders(t *testing.T) {
	a := forwardAuthApp(t, map[string]string{
		"core":         fixtureCoreCask,
		"oauth2_proxy": strings.Replace(fixtureForwardAuthProvider, "%s", "oauth2_proxy", 1),
		"authelia":     strings.Replace(fixtureForwardAuthProvider, "%s", "authelia", 1),
		"ddns_go":      fixtureGatedConsumer,
	}, &config.File{Modules: []string{"ddns_go"}})

	_, err := a.resolveOrder([]string{"ddns_go"})
	if err == nil {
		t.Fatal("expected an error when two casks provide the capability")
	}
	for _, want := range []string{"authelia", "oauth2_proxy", "forward_auth.provider"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q", err.Error(), want)
		}
	}
}

func TestAutoSelectionReportsWhenNoProviderIsEnabled(t *testing.T) {
	disabled := false
	a := forwardAuthApp(t, map[string]string{
		"core":         fixtureCoreCask,
		"oauth2_proxy": strings.Replace(fixtureForwardAuthProvider, "%s", "oauth2_proxy", 1),
		"ddns_go":      fixtureGatedConsumer,
	}, &config.File{
		Modules:  []string{"ddns_go"},
		Services: map[string]config.Service{"oauth2_proxy": {Enabled: &disabled}},
	})

	_, err := a.resolveOrder([]string{"ddns_go"})
	if err == nil {
		t.Fatal("expected an error when the only provider is disabled")
	}
	if !strings.Contains(err.Error(), "oauth2_proxy") {
		t.Errorf("error %q does not name the provider that could be enabled", err.Error())
	}
}

// Admission applies to every capability, not just IAM: a cask that claims to
// provide forward_auth without speaking the exchange is rejected at load,
// before any configuration can depend on it.
func TestForwardAuthProviderMustDeclareItsInterface(t *testing.T) {
	dir := t.TempDir()
	manifest := `api_version: anas.dev/v1
kind: Cask
name: broken
version: 1.0.0
abi:
  supports: [anas.cask/v2]
runtime:
  type: builtin
capabilities:
  provides:
    - name: forward_auth
      interfaces: []
`
	if err := os.WriteFile(filepath.Join(dir, "cask.yml"), []byte(manifest), 0600); err != nil {
		t.Fatal(err)
	}
	_, err := loadModuleManifest(dir, "broken")
	if err == nil || !strings.Contains(err.Error(), "without interfaces") {
		t.Fatalf("error = %v, want a forward_auth provider with no interfaces to be rejected", err)
	}
}

func indexOf(list []string, want string) int {
	for i, item := range list {
		if item == want {
			return i
		}
	}
	return -1
}
