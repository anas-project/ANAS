package runner

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/anas-project/ANAS/internal/config"
)

// Fixtures for the auto-selected capability path. forward_auth is the first
// capability that is not IAM, so these cover the generic resolver rather than
// the identity-specific policy layered on top of it.
const (
	fixtureForwardAuthProvider = `api_version: anas.module/v1
kind: Module
name: %s
version: 1.0.0
revision: 1
status: release
abi:
  supports: [anas.module-hook/v1]
runtime:
  type: builtin
capabilities:
  provides:
    - name: forward_auth
      interfaces: [http]
`
	fixtureGatedConsumer = `api_version: anas.module/v1
kind: Module
name: ddns_go
version: 1.0.0
revision: 1
status: release
abi:
  supports: [anas.module-hook/v1]
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
	fixtureObjectStorageProvider = `api_version: anas.module/v1
kind: Module
name: versitygw
version: 1.7.0
revision: 1
status: developing
abi:
  supports: [anas.module-hook/v1]
runtime:
  type: builtin
capabilities:
  provides:
    - name: object_storage
      interfaces: [s3]
config:
  exports:
    - ANAS_OBJECT_STORAGE_S3_*
`
	// object_storage/s3 is the single-interface shorthand: a consumer names
	// only the capability and receives a normalized private binding.
	fixtureObjectStorageConsumer = `api_version: anas.module/v1
kind: Module
name: archive
version: 1.0.0
revision: 1
status: developing
abi:
  supports: [anas.module-hook/v1]
runtime:
  type: builtin
dependencies:
  requires_capabilities:
    - name: object_storage
`
)

func forwardAuthApp(t *testing.T, manifests map[string]string, cfg *config.File) *app {
	t.Helper()
	a := &app{
		cfg:              cfg,
		reg:              writeModules(t, manifests),
		env:              map[string]string{},
		envOwner:         map[string]string{},
		lock:             &moduleLock{Bindings: map[string]map[string]string{}},
		resolvedBindings: map[string]map[string]string{},
	}
	a.applyModuleDefaults()
	return a
}

// With one implementation, requiring the capability is enough: the user does
// not have to name a module that has no alternative.
func TestAutoSelectionBindsTheOnlyProvider(t *testing.T) {
	a := forwardAuthApp(t, map[string]string{
		"core":         fixtureCoreModule,
		"oauth2_proxy": strings.Replace(fixtureForwardAuthProvider, "%s", "oauth2_proxy", 1),
		"ddns_go":      fixtureGatedConsumer,
	}, &config.File{Modules: config.NewModuleSelection("ddns_go")})

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

func TestObjectStorageCapabilityNameOnlyBindsAndProjectsS3Outputs(t *testing.T) {
	a := forwardAuthApp(t, map[string]string{
		"core":      fixtureCoreModule,
		"versitygw": fixtureObjectStorageProvider,
		"archive":   fixtureObjectStorageConsumer,
	}, &config.File{Modules: config.NewModuleSelection("archive")})

	order, err := a.resolveOrder([]string{"archive"})
	if err != nil {
		t.Fatal(err)
	}
	if indexOf(order, "versitygw") > indexOf(order, "archive") {
		t.Fatalf("object storage provider ordered after consumer: %v", order)
	}
	if got := a.resolvedBindings["archive"]["object_storage"]; got != "versitygw" {
		t.Fatalf("binding = %q, want versitygw", got)
	}
	if got := a.resolvedBindings["archive"]["object_storage.interface"]; got != "s3" {
		t.Fatalf("interface = %q, want s3", got)
	}

	sources := map[string]string{
		"ANAS_OBJECT_STORAGE_S3_ENDPOINT":          "https://s3.nas.test:443",
		"ANAS_OBJECT_STORAGE_S3_REGION":            "us-east-1",
		"ANAS_OBJECT_STORAGE_S3_ACCESS_KEY_ID":     "ANASROOT",
		"ANAS_OBJECT_STORAGE_S3_SECRET_ACCESS_KEY": "private-secret",
		"ANAS_OBJECT_STORAGE_S3_PATH_STYLE":        "true",
	}
	for key, value := range sources {
		a.env[key] = value
		a.setEnvOwner(key, "versitygw")
	}
	if err := a.publishCapabilityOutputs("versitygw"); err != nil {
		t.Fatal(err)
	}

	want := map[string]string{
		"ANAS_OBJECT_STORAGE_BINDING__ARCHIVE__INTERFACE":         "s3",
		"ANAS_OBJECT_STORAGE_BINDING__ARCHIVE__ENDPOINT":          "https://s3.nas.test:443",
		"ANAS_OBJECT_STORAGE_BINDING__ARCHIVE__REGION":            "us-east-1",
		"ANAS_OBJECT_STORAGE_BINDING__ARCHIVE__ACCESS_KEY_ID":     "ANASROOT",
		"ANAS_OBJECT_STORAGE_BINDING__ARCHIVE__SECRET_ACCESS_KEY": "private-secret",
		"ANAS_OBJECT_STORAGE_BINDING__ARCHIVE__PATH_STYLE":        "true",
	}
	consumerEnv := a.scopedEnv("archive")
	for key, value := range want {
		if consumerEnv[key] != value {
			t.Errorf("consumer binding %s = %q, want %q", key, consumerEnv[key], value)
		}
	}
	if _, leaked := consumerEnv["ANAS_OBJECT_STORAGE_S3_SECRET_ACCESS_KEY"]; leaked {
		t.Fatal("consumer received the provider-side S3 secret namespace")
	}
	if got := a.scopedEnv("core")["ANAS_OBJECT_STORAGE_BINDING__ARCHIVE__SECRET_ACCESS_KEY"]; got != "" {
		t.Fatal("object storage binding secret leaked to an unrelated module")
	}
	if !isRunnerOwnedRuntimeKey("ANAS_OBJECT_STORAGE_BINDING__ARCHIVE__SECRET_ACCESS_KEY", a.reg) {
		t.Fatal("object storage consumer binding is not reserved from caller input")
	}
}

func TestObjectStorageCapabilityRejectsIncompleteProviderOutput(t *testing.T) {
	a := forwardAuthApp(t, map[string]string{
		"core":      fixtureCoreModule,
		"versitygw": fixtureObjectStorageProvider,
		"archive":   fixtureObjectStorageConsumer,
	}, &config.File{Modules: config.NewModuleSelection("archive")})
	if _, err := a.resolveOrder([]string{"archive"}); err != nil {
		t.Fatal(err)
	}
	a.env["ANAS_OBJECT_STORAGE_S3_ENDPOINT"] = "https://s3.nas.test"
	a.setEnvOwner("ANAS_OBJECT_STORAGE_S3_ENDPOINT", "versitygw")
	err := a.publishCapabilityOutputs("versitygw")
	if err == nil || !strings.Contains(err.Error(), "ANAS_OBJECT_STORAGE_S3_REGION") {
		t.Fatalf("error = %v, want missing normalized S3 output", err)
	}
}

func TestObjectStorageCapabilityRejectsSpoofedProviderOutput(t *testing.T) {
	a := forwardAuthApp(t, map[string]string{
		"core":      fixtureCoreModule,
		"versitygw": fixtureObjectStorageProvider,
		"archive":   fixtureObjectStorageConsumer,
	}, &config.File{Modules: config.NewModuleSelection("archive")})
	if _, err := a.resolveOrder([]string{"archive"}); err != nil {
		t.Fatal(err)
	}
	for key, value := range map[string]string{
		"ANAS_OBJECT_STORAGE_S3_ENDPOINT":          "https://attacker.invalid",
		"ANAS_OBJECT_STORAGE_S3_REGION":            "us-east-1",
		"ANAS_OBJECT_STORAGE_S3_ACCESS_KEY_ID":     "attacker",
		"ANAS_OBJECT_STORAGE_S3_SECRET_ACCESS_KEY": "attacker-secret",
		"ANAS_OBJECT_STORAGE_S3_PATH_STYLE":        "true",
	} {
		a.env[key] = value
		a.setEnvOwner(key, globalScope)
	}
	err := a.publishCapabilityOutputs("versitygw")
	if err == nil || !strings.Contains(err.Error(), "does not own required output") {
		t.Fatalf("error = %v, want spoofed provider output rejection", err)
	}
}

// Two implementations is the point at which the deployment does have to
// choose, and the error must say so rather than pick one.
func TestAutoSelectionRefusesToGuessBetweenProviders(t *testing.T) {
	a := forwardAuthApp(t, map[string]string{
		"core":         fixtureCoreModule,
		"oauth2_proxy": strings.Replace(fixtureForwardAuthProvider, "%s", "oauth2_proxy", 1),
		"authelia":     strings.Replace(fixtureForwardAuthProvider, "%s", "authelia", 1),
		"ddns_go":      fixtureGatedConsumer,
	}, &config.File{Modules: config.NewModuleSelection("ddns_go")})

	_, err := a.resolveOrder([]string{"ddns_go"})
	if err == nil {
		t.Fatal("expected an error when two modules provide the capability")
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
		"core":         fixtureCoreModule,
		"oauth2_proxy": strings.Replace(fixtureForwardAuthProvider, "%s", "oauth2_proxy", 1),
		"ddns_go":      fixtureGatedConsumer,
	}, &config.File{
		Modules: config.NewModuleSelection("ddns_go"),
	})
	a.cfg.Modules.Values["oauth2_proxy"] = config.ModuleConfig{Enabled: &disabled}

	_, err := a.resolveOrder([]string{"ddns_go"})
	if err == nil {
		t.Fatal("expected an error when the only provider is disabled")
	}
	if !strings.Contains(err.Error(), "oauth2_proxy") {
		t.Errorf("error %q does not name the provider that could be enabled", err.Error())
	}
}

// Admission applies to every capability, not just IAM: a module that claims to
// provide forward_auth without speaking the exchange is rejected at load,
// before any configuration can depend on it.
func TestForwardAuthProviderMustDeclareItsInterface(t *testing.T) {
	dir := t.TempDir()
	manifest := `api_version: anas.module/v1
kind: Module
name: broken
version: 1.0.0
revision: 1
status: developing
abi:
  supports: [anas.module-hook/v1]
runtime:
  type: builtin
capabilities:
  provides:
    - name: forward_auth
      interfaces: []
`
	if err := os.WriteFile(filepath.Join(dir, "module.yml"), []byte(manifest), 0600); err != nil {
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
