package runner

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/anas-project/ANAS/internal/config"
)

const (
	fixtureGatewayProviderModule = `api_version: anas.module/v1
kind: Module
name: gateway
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
      interfaces: [http]
`

	// The Adminer arrangement in miniature: an optional admin console that must
	// sit behind the gateway when it exists and must not drag the gateway into
	// deployments that leave it off.
	fixtureConditionalConsumerModule = `api_version: anas.module/v1
kind: Module
name: db
version: 1.0.0
revision: 1
status: developing
abi:
  supports: [anas.module-hook/v1]
runtime:
  type: builtin
dependencies:
  requires_capabilities:
    - name: forward_auth
      enabled_by: adminer_enabled
      interface_selected_by: forward_auth_interface
      interfaces:
        any_of: [http]
config:
  defaults:
    adminer_enabled: "false"
  types:
    adminer_enabled: bool
`

	// Same shape, but the switch defaults to on. Nothing in the repository
	// declares a dependency this way yet; the fixture exists because the
	// evaluator has to read the declared default rather than treat "unset" as
	// false, and a default of false cannot tell those two behaviours apart.
	fixtureDefaultOnConsumerModule = `api_version: anas.module/v1
kind: Module
name: db
version: 1.0.0
revision: 1
status: developing
abi:
  supports: [anas.module-hook/v1]
runtime:
  type: builtin
dependencies:
  requires_capabilities:
    - name: forward_auth
      enabled_by: adminer_enabled
      interface_selected_by: forward_auth_interface
      interfaces:
        any_of: [http]
config:
  defaults:
    adminer_enabled: "true"
  types:
    adminer_enabled: bool
`

	fixtureUnconditionalConsumerModule = `api_version: anas.module/v1
kind: Module
name: db
version: 1.0.0
revision: 1
status: developing
abi:
  supports: [anas.module-hook/v1]
runtime:
  type: builtin
dependencies:
  requires_capabilities:
    - name: forward_auth
      interface_selected_by: forward_auth_interface
      interfaces:
        any_of: [http]
`
)

// loadFixtureRegistry is writeModules without the fatal: the manifest rejections
// below are the behaviour under test, so the error has to come back.
func loadFixtureRegistry(t *testing.T, manifests map[string]string) (map[string]Module, error) {
	t.Helper()
	root := t.TempDir()
	for name, body := range manifests {
		dir := filepath.Join(root, name)
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "module.yml"), []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	return loadRegistryDir(root)
}

// newConditionApp reproduces the environment resolveOrder actually sees: values
// the configuration supplied and nothing else. It deliberately does not call
// applyModuleDefaults, because production does not either until the order has
// already been resolved -- a helper that pre-filled defaults would hide exactly
// the bug these tests exist to catch.
func newConditionApp(reg map[string]Module, cfg *config.File) *app {
	env, owners := configBaseEnvWithRegistry(cfg, reg)
	return &app{
		cfg:              cfg,
		reg:              reg,
		env:              env,
		envOwner:         owners,
		lock:             &moduleLock{Bindings: map[string]map[string]string{}},
		resolvedBindings: map[string]map[string]string{},
	}
}

func conditionConfig(modules []string, values map[string]any) *config.File {
	selection := config.NewModuleSelection(modules...)
	if values != nil {
		selection.Values["db"] = config.ModuleConfig{Config: values}
	}
	return &config.File{Modules: selection}
}

func TestConditionalCapabilityAbsentWhenSwitchIsOff(t *testing.T) {
	reg, err := loadFixtureRegistry(t, map[string]string{
		"gateway": fixtureGatewayProviderModule,
		"db":      fixtureConditionalConsumerModule,
	})
	if err != nil {
		t.Fatal(err)
	}
	a := newConditionApp(reg, conditionConfig([]string{"db"}, nil))
	order, err := a.resolveOrder(a.cfg.Modules.Order)
	if err != nil {
		t.Fatalf("resolveOrder = %v, want the gateway not to be required", err)
	}
	if contains(order, "gateway") {
		t.Fatalf("order = %v, want no gateway while adminer_enabled is false", order)
	}
	if binding, ok := a.resolvedBindings["db"]; ok {
		t.Fatalf("bindings = %v, want no forward_auth binding recorded", binding)
	}
}

func TestConditionalCapabilityOrdersProviderWhenSwitchIsOn(t *testing.T) {
	reg, err := loadFixtureRegistry(t, map[string]string{
		"gateway": fixtureGatewayProviderModule,
		"db":      fixtureConditionalConsumerModule,
	})
	if err != nil {
		t.Fatal(err)
	}
	a := newConditionApp(reg, conditionConfig([]string{"db"}, map[string]any{"adminer_enabled": true}))
	order, err := a.resolveOrder(a.cfg.Modules.Order)
	if err != nil {
		t.Fatal(err)
	}
	gateway, consumer := indexOf(order, "gateway"), indexOf(order, "db")
	if gateway < 0 {
		t.Fatalf("order = %v, want the gateway pulled in", order)
	}
	if gateway > consumer {
		t.Fatalf("order = %v, want the gateway before its consumer", order)
	}
}

// The regression that motivated reading Defaults directly: resolveOrder runs
// before applyModuleDefaults, so an evaluator trusting a.env alone would read an
// unset switch as empty and decide false no matter what the module declared.
func TestConditionalCapabilityUsesDeclaredDefaultBeforeDefaultsAreApplied(t *testing.T) {
	reg, err := loadFixtureRegistry(t, map[string]string{
		"gateway": fixtureGatewayProviderModule,
		"db":      fixtureDefaultOnConsumerModule,
	})
	if err != nil {
		t.Fatal(err)
	}
	a := newConditionApp(reg, conditionConfig([]string{"db"}, nil))
	if got, ok := a.env["DB_ADMINER_ENABLED"]; ok && got != "" {
		t.Fatalf("DB_ADMINER_ENABLED = %q, want it unset before defaults are applied", got)
	}
	order, err := a.resolveOrder(a.cfg.Modules.Order)
	if err != nil {
		t.Fatal(err)
	}
	if !contains(order, "gateway") {
		t.Fatalf("order = %v, want the declared default of true to require the gateway", order)
	}
}

// An explicit false must beat a declared true, or the operator cannot turn the
// switch off once a module ships with it on.
func TestConditionalCapabilityConfiguredValueBeatsDeclaredDefault(t *testing.T) {
	reg, err := loadFixtureRegistry(t, map[string]string{
		"gateway": fixtureGatewayProviderModule,
		"db":      fixtureDefaultOnConsumerModule,
	})
	if err != nil {
		t.Fatal(err)
	}
	a := newConditionApp(reg, conditionConfig([]string{"db"}, map[string]any{"adminer_enabled": false}))
	order, err := a.resolveOrder(a.cfg.Modules.Order)
	if err != nil {
		t.Fatal(err)
	}
	if contains(order, "gateway") {
		t.Fatalf("order = %v, want an explicit false to drop the dependency", order)
	}
}

// The condition decides whether the dependency exists, never how strong it is:
// with the switch on and nothing providing the capability, resolution has to
// fail rather than continue without a gateway.
func TestConditionalCapabilityFailsWhenSwitchIsOnAndNoProviderExists(t *testing.T) {
	reg, err := loadFixtureRegistry(t, map[string]string{"db": fixtureConditionalConsumerModule})
	if err != nil {
		t.Fatal(err)
	}
	a := newConditionApp(reg, conditionConfig([]string{"db"}, map[string]any{"adminer_enabled": true}))
	if _, err := a.resolveOrder(a.cfg.Modules.Order); err == nil {
		t.Fatal("resolveOrder = nil, want a failure when no module provides forward_auth")
	}
}

func TestConditionalCapabilitySucceedsWhenSwitchIsOffAndNoProviderExists(t *testing.T) {
	reg, err := loadFixtureRegistry(t, map[string]string{"db": fixtureConditionalConsumerModule})
	if err != nil {
		t.Fatal(err)
	}
	a := newConditionApp(reg, conditionConfig([]string{"db"}, nil))
	if _, err := a.resolveOrder(a.cfg.Modules.Order); err != nil {
		t.Fatalf("resolveOrder = %v, want success when the dependency does not exist", err)
	}
}

// Omitting the field keeps the declaration unconditional, so ddns_updater and
// every other existing consumer behaves exactly as before.
func TestCapabilityWithoutConditionStaysUnconditional(t *testing.T) {
	reg, err := loadFixtureRegistry(t, map[string]string{"db": fixtureUnconditionalConsumerModule})
	if err != nil {
		t.Fatal(err)
	}
	if got := reg["db"].RequiresCapabilities[0].EnabledBy; got != "" {
		t.Fatalf("enabled_by = %q, want empty", got)
	}
	a := newConditionApp(reg, conditionConfig([]string{"db"}, nil))
	if _, err := a.resolveOrder(a.cfg.Modules.Order); err == nil {
		t.Fatal("resolveOrder = nil, want an unconditional dependency to still fail without a provider")
	}
}

// The condition must reach the same verdict on the configuration-planning path,
// which resolves with registryOnlyResolution and its own environment.
func TestConditionalCapabilityMatchesOnRegistryOnlyResolution(t *testing.T) {
	reg, err := loadFixtureRegistry(t, map[string]string{
		"gateway": fixtureGatewayProviderModule,
		"db":      fixtureConditionalConsumerModule,
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name    string
		values  map[string]any
		gateway bool
	}{
		{name: "off", values: nil, gateway: false},
		{name: "on", values: map[string]any{"adminer_enabled": true}, gateway: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := newConditionApp(reg, conditionConfig([]string{"db"}, tc.values))
			a.registryOnlyResolution = true
			a.allowUnresolvedInputBindings = true
			order, err := a.resolveOrder(a.cfg.Modules.Order)
			if err != nil {
				t.Fatal(err)
			}
			if got := contains(order, "gateway"); got != tc.gateway {
				t.Fatalf("gateway in %v = %v, want %v", order, got, tc.gateway)
			}
		})
	}
}

func TestConditionalCapabilityManifestRejections(t *testing.T) {
	for _, tc := range []struct {
		name    string
		enabled string
		types   string
		wantErr string
	}{
		{
			name:    "undeclared parameter",
			enabled: "adminer_enabled",
			types:   "",
			wantErr: "not declared in config.types",
		},
		{
			name:    "non-boolean parameter",
			enabled: "adminer_enabled",
			types:   "  types:\n    adminer_enabled: string\n",
			wantErr: "must be a bool parameter",
		},
		{
			name:    "global parameter",
			enabled: "global.base_domain",
			types:   "  types:\n    adminer_enabled: bool\n",
			wantErr: "not a global or environment key",
		},
		{
			name:    "environment key",
			enabled: "DB_ADMINER_ENABLED",
			types:   "  types:\n    adminer_enabled: bool\n",
			wantErr: "not a global or environment key",
		},
		{
			name:    "not lower snake case",
			enabled: "adminer-enabled",
			types:   "  types:\n    adminer_enabled: bool\n",
			wantErr: "not lower-snake-case",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			manifest := `api_version: anas.module/v1
kind: Module
name: db
version: 1.0.0
revision: 1
status: developing
abi:
  supports: [anas.module-hook/v1]
runtime:
  type: builtin
dependencies:
  requires_capabilities:
    - name: forward_auth
      enabled_by: ` + tc.enabled + `
      interface_selected_by: forward_auth_interface
      interfaces:
        any_of: [http]
config:
  defaults:
    adminer_enabled: "false"
` + tc.types
			_, err := loadFixtureRegistry(t, map[string]string{"db": manifest})
			if err == nil {
				t.Fatalf("load = nil, want a rejection mentioning %q", tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("error = %v, want it to mention %q", err, tc.wantErr)
			}
		})
	}
}

// A conditional binding records the switch that produced it, in the same place
// as the interface. `plan` and the lock both read this map, so one record
// serves both rather than each growing its own idea of why the dependency
// exists.
func TestConditionalCapabilityBindingRecordsItsSwitch(t *testing.T) {
	reg, err := loadFixtureRegistry(t, map[string]string{
		"gateway": fixtureGatewayProviderModule,
		"db":      fixtureConditionalConsumerModule,
	})
	if err != nil {
		t.Fatal(err)
	}
	a := newConditionApp(reg, conditionConfig([]string{"db"}, map[string]any{"adminer_enabled": true}))
	if _, err := a.resolveOrder(a.cfg.Modules.Order); err != nil {
		t.Fatal(err)
	}
	bindings := a.resolvedBindings["db"]
	if got := bindings["forward_auth"]; got != "gateway" {
		t.Fatalf("forward_auth = %q, want gateway", got)
	}
	if got := bindings["forward_auth.interface"]; got != interfaceHTTP {
		t.Fatalf("forward_auth.interface = %q, want %s", got, interfaceHTTP)
	}
	if got := bindings["forward_auth.enabled_by"]; got != "adminer_enabled" {
		t.Fatalf("forward_auth.enabled_by = %q, want adminer_enabled", got)
	}
}

// An unconditional dependency must not grow the key: an operator reading a
// binding cannot tell "no switch governs this" from "the switch was recorded as
// empty" if both look the same.
func TestUnconditionalCapabilityBindingHasNoSwitchKey(t *testing.T) {
	reg, err := loadFixtureRegistry(t, map[string]string{
		"gateway": fixtureGatewayProviderModule,
		"db":      fixtureUnconditionalConsumerModule,
	})
	if err != nil {
		t.Fatal(err)
	}
	a := newConditionApp(reg, conditionConfig([]string{"db"}, nil))
	if _, err := a.resolveOrder(a.cfg.Modules.Order); err != nil {
		t.Fatal(err)
	}
	if value, ok := a.resolvedBindings["db"]["forward_auth.enabled_by"]; ok {
		t.Fatalf("forward_auth.enabled_by = %q, want the key to be absent", value)
	}
}

// Nothing about the capability may survive in the binding record when the
// switch is off, because a placeholder would make "no gateway" and "a gateway
// that failed to bind" indistinguishable to anything reading this map.
func TestConditionalCapabilityLeavesNoPlaceholderWhenOff(t *testing.T) {
	reg, err := loadFixtureRegistry(t, map[string]string{
		"gateway": fixtureGatewayProviderModule,
		"db":      fixtureConditionalConsumerModule,
	})
	if err != nil {
		t.Fatal(err)
	}
	a := newConditionApp(reg, conditionConfig([]string{"db"}, nil))
	if _, err := a.resolveOrder(a.cfg.Modules.Order); err != nil {
		t.Fatal(err)
	}
	for key := range a.resolvedBindings["db"] {
		if strings.HasPrefix(key, "forward_auth") {
			t.Fatalf("bindings contain %q, want nothing recorded for a dependency that does not exist", key)
		}
	}
	lock := &moduleLock{Modules: map[string]moduleLockRecord{}}
	if err := a.updateModuleLock(lock, true); err != nil {
		t.Fatal(err)
	}
	for key := range lock.Bindings["db"] {
		if strings.HasPrefix(key, "forward_auth") {
			t.Fatalf("lock records %q, want nothing for a dependency that does not exist", key)
		}
	}
}

// Flipping the switch pulls in a module the operator never named. Telling them
// only that the lock is stale reads like a bug; the sentence has to name the
// parameter that asked for it.
func TestConditionalPullExplainsWhyAModuleIsMissingFromTheLock(t *testing.T) {
	reg, err := loadFixtureRegistry(t, map[string]string{
		"gateway": fixtureGatewayProviderModule,
		"db":      fixtureConditionalConsumerModule,
	})
	if err != nil {
		t.Fatal(err)
	}
	a := newConditionApp(reg, conditionConfig([]string{"db"}, map[string]any{"adminer_enabled": true}))
	order, err := a.resolveOrder(a.cfg.Modules.Order)
	if err != nil {
		t.Fatal(err)
	}
	a.order = order

	lock := &moduleLock{Modules: map[string]moduleLockRecord{"db": {}}}
	err = validateLockedModuleBundles(a, lock)
	if err == nil {
		t.Fatal("validateLockedModuleBundles = nil, want the missing gateway reported")
	}
	for _, want := range []string{"gateway", "db.adminer_enabled", "forward_auth", "anas lock"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error = %v, want it to mention %q", err, want)
		}
	}
}

// The explanation is only for modules a condition dragged in. A module the
// operator selected themselves must produce the plain message, or every
// ordinary stale lock grows a sentence about a switch nobody touched.
func TestUnconditionalMissingModuleKeepsThePlainLockMessage(t *testing.T) {
	reg, err := loadFixtureRegistry(t, map[string]string{
		"gateway": fixtureGatewayProviderModule,
		"db":      fixtureUnconditionalConsumerModule,
	})
	if err != nil {
		t.Fatal(err)
	}
	a := newConditionApp(reg, conditionConfig([]string{"db"}, nil))
	order, err := a.resolveOrder(a.cfg.Modules.Order)
	if err != nil {
		t.Fatal(err)
	}
	a.order = order

	lock := &moduleLock{Modules: map[string]moduleLockRecord{"db": {}}}
	err = validateLockedModuleBundles(a, lock)
	if err == nil {
		t.Fatal("validateLockedModuleBundles = nil, want the missing gateway reported")
	}
	if strings.Contains(err.Error(), "entered this deployment because") {
		t.Fatalf("error = %v, want no conditional explanation for an unconditional dependency", err)
	}
}
