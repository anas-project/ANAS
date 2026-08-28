package runner

import (
	"strings"
	"testing"
)

// The shape that motivated the feature, reduced to three modules: the gateway
// guards the database, needs the identity provider, and the identity provider
// needs that same database.
const (
	fixtureCycleGatewayModule = `api_version: anas.module/v1
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
dependencies:
  requires:
    - directory
`

	fixtureCycleDirectoryModule = `api_version: anas.module/v1
kind: Module
name: directory
version: 1.0.0
revision: 1
status: developing
abi:
  supports: [anas.module-hook/v1]
runtime:
  type: builtin
dependencies:
  requires:
    - db
`

	fixtureCycleConsumerTemplate = `api_version: anas.module/v1
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
%s
config:
  # Required, and more so once the dependency is unordered: dropping the edge
  # also drops the provider from this module's dependency closure, so closure
  # and prefix visibility no longer reach anything it publishes. An explicit
  # claim is the only remaining route.
  consumes:
    - ANAS_FORWARD_AUTH_MIDDLEWARE
`
)

func cycleRegistry(t *testing.T, consumerExtra string) (map[string]Module, error) {
	t.Helper()
	consumer := strings.Replace(fixtureCycleConsumerTemplate, "%s", consumerExtra, 1)
	return loadFixtureRegistry(t, map[string]string{
		"gateway":   fixtureCycleGatewayModule,
		"directory": fixtureCycleDirectoryModule,
		"db":        consumer,
	})
}

// Without the field the arrangement is a cycle, which is the behaviour the
// feature exists to change. Asserting it here keeps the next test honest: it
// shows the ordering edge is what breaks, not the fixture.
func TestOrderedCapabilityDependencyStillCycles(t *testing.T) {
	reg, err := cycleRegistry(t, "")
	if err != nil {
		t.Fatal(err)
	}
	a := newConditionApp(reg, conditionConfig([]string{"db"}, nil))
	_, err = a.resolveOrder(a.cfg.Modules.Order)
	if err == nil || !strings.Contains(err.Error(), "cycle") {
		t.Fatalf("resolveOrder = %v, want a dependency cycle", err)
	}
}

func TestUnorderedCapabilityDependencyBreaksTheCycle(t *testing.T) {
	reg, err := cycleRegistry(t, "      ordering: any")
	if err != nil {
		t.Fatal(err)
	}
	a := newConditionApp(reg, conditionConfig([]string{"db"}, nil))
	order, err := a.resolveOrder(a.cfg.Modules.Order)
	if err != nil {
		t.Fatalf("resolveOrder = %v, want the cycle broken", err)
	}
	// Mandatory, not optional: the provider is still in the deployment.
	if !contains(order, "gateway") {
		t.Fatalf("order = %v, want the gateway still deployed", order)
	}
	// And the edge really is gone, rather than the cycle having been reordered
	// away: the consumer resolves before the provider that guards it.
	if db, gate := indexOf(order, "db"), indexOf(order, "gateway"); db > gate {
		t.Fatalf("order = %v, want db before gateway once the edge is dropped", order)
	}
}

// Dropping the edge must not drop the requirement. This is the line between the
// new field and dependencies.requires[].optional, and it is the one that would
// silently remove a security gateway if it were ever crossed.
func TestUnorderedCapabilityStillFailsWithoutAProvider(t *testing.T) {
	reg, err := loadFixtureRegistry(t, map[string]string{
		"db": strings.Replace(fixtureCycleConsumerTemplate, "%s", "      ordering: any", 1),
	})
	if err != nil {
		t.Fatal(err)
	}
	a := newConditionApp(reg, conditionConfig([]string{"db"}, nil))
	if _, err := a.resolveOrder(a.cfg.Modules.Order); err == nil {
		t.Fatal("resolveOrder = nil, want a failure when nothing provides forward_auth")
	}
}

// Resolution is complete, not skipped: the binding is recorded exactly as an
// ordered dependency records it, so the lock protects it the same way.
func TestUnorderedCapabilityRecordsItsBinding(t *testing.T) {
	reg, err := cycleRegistry(t, "      ordering: any")
	if err != nil {
		t.Fatal(err)
	}
	a := newConditionApp(reg, conditionConfig([]string{"db"}, nil))
	if _, err := a.resolveOrder(a.cfg.Modules.Order); err != nil {
		t.Fatal(err)
	}
	if got := a.resolvedBindings["db"]["forward_auth"]; got != "gateway" {
		t.Fatalf("forward_auth = %q, want gateway", got)
	}
	if got := a.resolvedBindings["db"]["forward_auth.interface"]; got != interfaceHTTP {
		t.Fatalf("forward_auth.interface = %q, want %s", got, interfaceHTTP)
	}
	lock := &moduleLock{Modules: map[string]moduleLockRecord{}}
	if err := a.updateModuleLock(lock, true); err != nil {
		t.Fatal(err)
	}
	if got := lock.Bindings["db"]["forward_auth"]; got != "gateway" {
		t.Fatalf("lock forward_auth = %q, want gateway", got)
	}
}

func TestCapabilityOrderingDefaultsToBefore(t *testing.T) {
	reg, err := loadFixtureRegistry(t, map[string]string{
		"gateway": fixtureGatewayProviderModule,
		"db":      fixtureUnconditionalConsumerModule,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := reg["db"].RequiresCapabilities[0].Ordering; got != orderingBefore {
		t.Fatalf("ordering = %q, want %q when the field is absent", got, orderingBefore)
	}
}

// A closed vocabulary, so a typo cannot be read as the permissive value.
func TestCapabilityOrderingRejectsUnknownValues(t *testing.T) {
	for _, value := range []string{"weak", "none", "after", "true"} {
		t.Run(value, func(t *testing.T) {
			_, err := cycleRegistry(t, "      ordering: "+value)
			if err == nil {
				t.Fatalf("load = nil, want %q rejected", value)
			}
			if !strings.Contains(err.Error(), "must be before or any") {
				t.Fatalf("error = %v, want it to name the accepted values", err)
			}
		})
	}
}

// Ordering is a property of capability dependencies alone. A contract dependency
// or a resource requirement is provisioned by the provider's hook and read by the
// consumer's, so for those the order is the semantics, not a tunable.
func TestOrderingIsRejectedOnContractsAndResources(t *testing.T) {
	for _, tc := range []struct{ name, manifest string }{
		{
			name: "contract dependency",
			manifest: `api_version: anas.module/v1
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
  contracts:
    - name: relational_database
      version: ">=1.0.0 <2.0.0"
      selected_by: db_type
      ordering: any
      interfaces: [postgres]
`,
		},
		{
			name: "resource requirement",
			manifest: `api_version: anas.module/v1
kind: Module
name: db
version: 1.0.0
revision: 1
status: developing
abi:
  supports: [anas.module-hook/v1]
runtime:
  type: builtin
resources:
  requires:
    - id: primary
      contract: relational_database
      binding: db_type
      ordering: any
`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := loadFixtureRegistry(t, map[string]string{"db": tc.manifest}); err == nil {
				t.Fatal("load = nil, want ordering rejected outside requires_capabilities")
			}
		})
	}
}

// The two fields answer different questions -- whether the dependency exists,
// and whether it has to come first -- so they have to compose without either
// one quietly winning.
func TestOrderingComposesWithCondition(t *testing.T) {
	consumer := `api_version: anas.module/v1
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
      ordering: any
      interface_selected_by: forward_auth_interface
      interfaces:
        any_of: [http]
config:
  defaults:
    adminer_enabled: "false"
  types:
    adminer_enabled: bool
`
	reg, err := loadFixtureRegistry(t, map[string]string{
		"gateway":   fixtureCycleGatewayModule,
		"directory": fixtureCycleDirectoryModule,
		"db":        consumer,
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name    string
		values  map[string]any
		gateway bool
	}{
		{name: "condition off drops the dependency entirely", values: nil, gateway: false},
		{name: "condition on keeps it, unordered", values: map[string]any{"adminer_enabled": true}, gateway: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := newConditionApp(reg, conditionConfig([]string{"db"}, tc.values))
			order, err := a.resolveOrder(a.cfg.Modules.Order)
			if err != nil {
				t.Fatalf("resolveOrder = %v, want success in both directions", err)
			}
			if got := contains(order, "gateway"); got != tc.gateway {
				t.Fatalf("gateway in %v = %v, want %v", order, got, tc.gateway)
			}
		})
	}
}

// Keys the unordered provider owns must be gone from the consumer's calculate
// environment, whether it owns them by prefix or by config.exports. A key that
// is sometimes there is worse than one that is never there: it works for whoever
// wrote the hook and fails for whoever deploys it in another topology.
func TestUnorderedProviderKeysAreHiddenFromCalculate(t *testing.T) {
	reg, err := cycleRegistry(t, "      ordering: any")
	if err != nil {
		t.Fatal(err)
	}
	a := newConditionApp(reg, conditionConfig([]string{"db"}, nil))
	if _, err := a.resolveOrder(a.cfg.Modules.Order); err != nil {
		t.Fatal(err)
	}
	a.env["GATEWAY_PRIVATE"] = "x"
	a.setEnvOwner("GATEWAY_PRIVATE", "gateway")
	a.env["ANAS_FORWARD_AUTH_MIDDLEWARE"] = "anas-forward-auth@docker"
	a.setEnvOwner("ANAS_FORWARD_AUTH_MIDDLEWARE", "gateway")
	a.env["DB_OWN"] = "y"
	a.setEnvOwner("DB_OWN", "db")

	hookEnv := a.calculateEnvFor("db")
	for _, key := range []string{"GATEWAY_PRIVATE", "ANAS_FORWARD_AUTH_MIDDLEWARE"} {
		if _, ok := hookEnv[key]; ok {
			t.Fatalf("calculate env still exposes %q from an unordered provider", key)
		}
	}
	if hookEnv["DB_OWN"] != "y" {
		t.Fatal("calculate env dropped the module's own key")
	}

	// The provider itself is unaffected: the hole is only in the consumer's view.
	if a.calculateEnvFor("gateway")["GATEWAY_PRIVATE"] != "x" {
		t.Fatal("the provider lost sight of its own key")
	}
}

// The filter is scoped to calculate. Rendering is a second pass over the fully
// populated env, so the value the consumer could not read while computing is
// exactly the value it can read while rendering -- which is how the Adminer
// middleware label gets filled in at all.
func TestUnorderedProviderKeysRemainVisibleWhenRendering(t *testing.T) {
	reg, err := cycleRegistry(t, "      ordering: any")
	if err != nil {
		t.Fatal(err)
	}
	a := newConditionApp(reg, conditionConfig([]string{"db"}, nil))
	order, err := a.resolveOrder(a.cfg.Modules.Order)
	if err != nil {
		t.Fatal(err)
	}
	a.order = order
	a.env["ANAS_FORWARD_AUTH_MIDDLEWARE"] = "anas-forward-auth@docker"
	a.setEnvOwner("ANAS_FORWARD_AUTH_MIDDLEWARE", "gateway")

	if _, hidden := a.calculateEnvFor("db")["ANAS_FORWARD_AUTH_MIDDLEWARE"]; hidden {
		t.Fatal("precondition: the key should be hidden from calculate")
	}
	if got := a.scopedEnv("db")["ANAS_FORWARD_AUTH_MIDDLEWARE"]; got != "anas-forward-auth@docker" {
		t.Fatalf("render env = %q, want the provider value visible", got)
	}
}

// An ordered dependency keeps the full privileged view; the filter must not
// leak into modules that never asked for it.
func TestOrderedDependencyKeepsTheFullCalculateEnv(t *testing.T) {
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
	a.env["GATEWAY_PRIVATE"] = "x"
	a.setEnvOwner("GATEWAY_PRIVATE", "gateway")
	if a.calculateEnvFor("db")["GATEWAY_PRIVATE"] != "x" {
		t.Fatal("an ordered dependency should still see the provider's keys in calculate")
	}
}

// A capability whose output ABI is projected after its provider's hook cannot be
// made deterministic by the ownership filter, because those keys belong to the
// consumer. Refusing the combination is what keeps that from being a silent hole.
func TestOrderingRejectedOnCapabilitiesWithProjectedOutputs(t *testing.T) {
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
    - name: object_storage
      ordering: any
`
	_, err := loadFixtureRegistry(t, map[string]string{"db": manifest})
	if err == nil {
		t.Fatal("load = nil, want ordering any refused on a capability with a projected output ABI")
	}
	if !strings.Contains(err.Error(), "projected output ABI") {
		t.Fatalf("error = %v, want it to explain why", err)
	}
}
