package runner

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolvedModuleInputRequiredIsEnforcedAtImportAndConfigPlan(t *testing.T) {
	tests := map[string]struct {
		registry func() map[string]Module
		wantKey  string
	}{
		"transitive dependency": {
			wantKey: "DEPENDENCY_TOKEN",
			registry: func() map[string]Module {
				return map[string]Module{
					"consumer": {
						Name: "consumer", EnvPrefix: "CONSUMER",
						Requires: []Dependency{{Name: "dependency"}},
					},
					"dependency": {
						Name: "dependency", EnvPrefix: "DEPENDENCY",
						Parameters: []string{"token"}, InputRequired: []string{"DEPENDENCY_TOKEN"},
						Types: map[string]ParamType{"token": {Kind: "string"}},
					},
				}
			},
		},
		"auto-selected capability provider": {
			wantKey: "PROVIDER_TOKEN",
			registry: func() map[string]Module {
				return map[string]Module{
					"consumer": {
						Name: "consumer", EnvPrefix: "CONSUMER",
						Parameters: []string{"forward_auth_interface"},
						Types:      map[string]ParamType{"forward_auth_interface": {Kind: "string"}},
						RequiresCapabilities: []RequiredCapability{{
							Name: capabilityForwardAuth, InterfaceSelectedBy: "forward_auth_interface",
							AnyOf: []string{interfaceHTTP}, Prefer: []string{interfaceHTTP},
						}},
					},
					"provider": {
						Name: "provider", EnvPrefix: "PROVIDER",
						Parameters: []string{"token"}, InputRequired: []string{"PROVIDER_TOKEN"},
						Types:    map[string]ParamType{"token": {Kind: "string"}},
						Provides: []ProvidedCapability{{Name: capabilityForwardAuth, Interfaces: []string{interfaceHTTP}}},
					},
				}
			},
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			body := []byte(`modules:
  consumer: {}
global:
  base_domain: nas.test
  email: admin@nas.test
`)
			t.Run("import", func(t *testing.T) {
				source := filepath.Join(t.TempDir(), "config.yml")
				if err := os.WriteFile(source, body, 0o600); err != nil {
					t.Fatal(err)
				}
				err := validateConfigImportSource(source, test.registry())
				if err == nil || !strings.Contains(err.Error(), test.wantKey) {
					t.Fatalf("import input_required error = %v, want %s", err, test.wantKey)
				}
			})

			t.Run("config plan", func(t *testing.T) {
				workspace := t.TempDir()
				base := stateDir(workspace)
				if err := os.MkdirAll(base, 0o700); err != nil {
					t.Fatal(err)
				}
				configPath := workspaceConfigPath(workspace)
				if err := os.WriteFile(configPath, body, 0o600); err != nil {
					t.Fatal(err)
				}
				err := reportConfigPlan(workspace, configPath, base, test.registry(), true)
				if err == nil || !strings.Contains(err.Error(), test.wantKey) {
					t.Fatalf("config plan input_required error = %v, want %s", err, test.wantKey)
				}
			})
		})
	}
}

func TestResolvedInputRequiredUsesWorkspaceLockedAlternativeProvider(t *testing.T) {
	registry := func() map[string]Module {
		return map[string]Module{
			"consumer": {
				Name: "consumer", EnvPrefix: "CONSUMER",
				Parameters: []string{"storage_provider"},
				Types:      map[string]ParamType{"storage_provider": {Kind: "string"}},
				RequiresOne: []AlternativeDependency{{
					Capability: "storage", SelectedBy: "storage_provider",
					Providers: []string{"default_provider", "locked_provider"}, Default: "default_provider",
				}},
			},
			"default_provider": {
				Name: "default_provider", EnvPrefix: "DEFAULT_PROVIDER",
				Parameters: []string{"token"}, InputRequired: []string{"DEFAULT_PROVIDER_TOKEN"},
				Types: map[string]ParamType{"token": {Kind: "string"}},
			},
			"locked_provider": {
				Name: "locked_provider", EnvPrefix: "LOCKED_PROVIDER",
				Parameters: []string{"token"}, InputRequired: []string{"LOCKED_PROVIDER_TOKEN"},
				Types: map[string]ParamType{"token": {Kind: "string"}},
			},
		}
	}
	body := []byte(`modules:
  consumer: {}
global:
  base_domain: nas.test
  email: admin@nas.test
env:
  LOCKED_PROVIDER_TOKEN: present
`)
	lock := &moduleLock{
		APIVersion: "anas.module-lock/v1", Modules: map[string]moduleLockRecord{},
		Bindings: map[string]map[string]string{"consumer": {"storage": "locked_provider"}},
	}

	// A first-time validation has no lock and therefore follows the declared
	// default provider. The locked provider token must not accidentally satisfy
	// that different module's input contract.
	source := filepath.Join(t.TempDir(), "config.yml")
	if err := os.WriteFile(source, body, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := validateConfigImportSource(source, registry()); err == nil || !strings.Contains(err.Error(), "DEFAULT_PROVIDER_TOKEN") {
		t.Fatalf("unlocked import error = %v, want default provider input", err)
	}

	for _, boundary := range []string{"reimport", "config plan"} {
		t.Run(boundary, func(t *testing.T) {
			workspace := t.TempDir()
			configPath := workspaceConfigPath(workspace)
			if err := saveModuleLockFile(projectLockPath(configPath), lock); err != nil {
				t.Fatal(err)
			}
			switch boundary {
			case "reimport":
				external := filepath.Join(t.TempDir(), "config.yml")
				if err := os.WriteFile(external, body, 0o600); err != nil {
					t.Fatal(err)
				}
				if _, err := importConfigIntoWorkspace(workspace, external, registry()); err != nil {
					t.Fatalf("reimport ignored locked provider: %v", err)
				}
			case "config plan":
				if err := os.WriteFile(configPath, body, 0o600); err != nil {
					t.Fatal(err)
				}
				base := stateDir(workspace)
				if err := os.MkdirAll(base, 0o700); err != nil {
					t.Fatal(err)
				}
				if _, err := captureRunnerStdout(t, func() error {
					return reportConfigPlan(workspace, configPath, base, registry(), true)
				}); err != nil {
					t.Fatalf("config plan ignored locked provider: %v", err)
				}
			}
		})
	}
}

func TestResolvedInputRequiredUsesWorkspaceLockedAutoCapabilityProvider(t *testing.T) {
	registry := func() map[string]Module {
		reg := forwardAuthRegistry("provider_a", "provider_b")
		for _, name := range []string{"provider_a", "provider_b"} {
			mod := reg[name]
			mod.Parameters = []string{"token"}
			mod.InputRequired = []string{defaultEnvPrefix(name) + "_TOKEN"}
			mod.Types = map[string]ParamType{"token": {Kind: "string"}}
			reg[name] = mod
		}
		return reg
	}
	lock := &moduleLock{
		APIVersion: "anas.module-lock/v1", Modules: map[string]moduleLockRecord{},
		Bindings: map[string]map[string]string{"consumer": {capabilityForwardAuth: "provider_b"}},
	}
	body := func(env string) []byte {
		return []byte("modules:\n  consumer: {}\nglobal:\n  base_domain: nas.test\n  email: admin@nas.test\nenv:\n" + env)
	}

	for _, boundary := range []string{"reimport", "config plan"} {
		for _, test := range []struct {
			name, env, wantError string
		}{
			{name: "locked provider input present", env: "  PROVIDER_B_TOKEN: present\n"},
			{name: "other provider input cannot substitute", env: "  PROVIDER_A_TOKEN: present\n", wantError: "PROVIDER_B_TOKEN"},
		} {
			t.Run(boundary+"/"+test.name, func(t *testing.T) {
				workspace := t.TempDir()
				configPath := workspaceConfigPath(workspace)
				if err := saveModuleLockFile(projectLockPath(configPath), lock); err != nil {
					t.Fatal(err)
				}
				var err error
				switch boundary {
				case "reimport":
					external := filepath.Join(t.TempDir(), "config.yml")
					if writeErr := os.WriteFile(external, body(test.env), 0o600); writeErr != nil {
						t.Fatal(writeErr)
					}
					_, err = importConfigIntoWorkspace(workspace, external, registry())
				case "config plan":
					if writeErr := os.WriteFile(configPath, body(test.env), 0o600); writeErr != nil {
						t.Fatal(writeErr)
					}
					base := stateDir(workspace)
					if mkdirErr := os.MkdirAll(base, 0o700); mkdirErr != nil {
						t.Fatal(mkdirErr)
					}
					_, err = captureRunnerStdout(t, func() error {
						return reportConfigPlan(workspace, configPath, base, registry(), true)
					})
				}
				if test.wantError == "" && err != nil {
					t.Fatalf("locked auto capability provider was not honored: %v", err)
				}
				if test.wantError != "" && (err == nil || !strings.Contains(err.Error(), test.wantError)) {
					t.Fatalf("error = %v, want locked provider input %s", err, test.wantError)
				}
			})
		}
	}
}

func TestResolverGeneratedSelectorCannotSatisfyInputRequired(t *testing.T) {
	reg := map[string]Module{
		"consumer": {
			Name: "consumer", EnvPrefix: "CONSUMER",
			Parameters:    []string{"storage_provider"},
			InputRequired: []string{"CONSUMER_STORAGE_PROVIDER"},
			Types:         map[string]ParamType{"storage_provider": {Kind: "string"}},
			RequiresOne: []AlternativeDependency{{
				Capability: "storage", SelectedBy: "storage_provider",
				Providers: []string{"provider"}, Default: "provider",
			}},
		},
		"provider": {Name: "provider", EnvPrefix: "PROVIDER"},
	}
	body := []byte(`modules:
  consumer: {}
global:
  base_domain: nas.test
  email: admin@nas.test
`)
	source := filepath.Join(t.TempDir(), "config.yml")
	if err := os.WriteFile(source, body, 0o600); err != nil {
		t.Fatal(err)
	}
	err := validateConfigImportSource(source, reg)
	if err == nil || !strings.Contains(err.Error(), "CONSUMER_STORAGE_PROVIDER") {
		t.Fatalf("resolver-generated default satisfied caller input: %v", err)
	}
}

func TestReadOnlyResolutionRejectsExplicitInvalidBindings(t *testing.T) {
	base := func(modules string) []byte {
		return []byte("modules:\n" + modules + "global:\n  base_domain: nas.test\n  email: admin@nas.test\n")
	}
	tests := []struct {
		name     string
		body     []byte
		registry func() map[string]Module
		want     string
	}{
		{
			name: "requires_one invalid selector",
			body: base(`  consumer:
    config: {storage_provider: bogus}
`),
			registry: func() map[string]Module {
				return map[string]Module{
					"consumer": alternativeConsumer(),
					"provider": {Name: "provider", EnvPrefix: "PROVIDER"},
				}
			},
			want: `got "bogus"`,
		},
		{
			name: "requires_one disabled provider",
			body: base(`  consumer:
    config: {storage_provider: provider}
  provider:
    enabled: false
`),
			registry: func() map[string]Module {
				return map[string]Module{
					"consumer": alternativeConsumer(),
					"provider": {Name: "provider", EnvPrefix: "PROVIDER"},
				}
			},
			want: `disabled storage provider "provider"`,
		},
		{
			name: "contract invalid interface",
			body: base(`  consumer:
    config: {database_interface: mysql}
`),
			registry: contractRegistry,
			want:     `got "mysql"`,
		},
		{
			name: "capability invalid interface",
			body: base(`  consumer:
    config: {forward_auth_interface: smtp}
`),
			registry: func() map[string]Module {
				return forwardAuthRegistry("provider")
			},
			want: `supports [http]`,
		},
		{
			name: "capability unknown explicit provider",
			body: []byte(`modules:
  consumer: {}
global:
  base_domain: nas.test
  email: admin@nas.test
identity:
  iam:
    provider: ghost
`),
			registry: iamRegistry,
			want:     `iam.provider "ghost" is not a known module`,
		},
		{
			name: "auto capability with all providers disabled",
			body: base(`  consumer: {}
  provider:
    enabled: false
`),
			registry: func() map[string]Module {
				return forwardAuthRegistry("provider")
			},
			want: `no enabled module provides it`,
		},
		{
			name: "multiple explicitly enabled IAM providers",
			body: base(`  provider_a: {}
  provider_b: {}
`),
			registry: func() map[string]Module {
				return map[string]Module{
					"provider_a": iamProvider("provider_a"),
					"provider_b": iamProvider("provider_b"),
				}
			},
			want: `may run only one IAM`,
		},
		{
			name: "dynamic DNS invalid implementation",
			body: []byte(`modules:
  consumer: {}
global:
  base_domain: nas.test
  email: admin@nas.test
dynamic_dns:
  provider: lego
  dns_provider: cloudflare
`),
			registry: func() map[string]Module {
				return map[string]Module{"consumer": {Name: "consumer", EnvPrefix: "CONSUMER"}}
			},
			want: `not a dynamic DNS implementation`,
		},
		{
			name: "dynamic DNS unsupported platform",
			body: []byte(`modules:
  consumer: {}
global:
  base_domain: nas.test
  email: admin@nas.test
dynamic_dns:
  provider: ddns_updater
  dns_provider: dnspod
`),
			registry: func() map[string]Module {
				return map[string]Module{
					"consumer":     {Name: "consumer", EnvPrefix: "CONSUMER"},
					"ddns_updater": {Name: "ddns_updater", EnvPrefix: "DDNS_UPDATER"},
				}
			},
			want: `cannot update records at`,
		},
		{
			name: "dynamic DNS disabled explicit provider",
			body: []byte(`modules:
  consumer: {}
  ddns_go:
    enabled: false
global:
  base_domain: nas.test
  email: admin@nas.test
dynamic_dns:
  provider: ddns_go
  dns_provider: cloudflare
`),
			registry: func() map[string]Module {
				return map[string]Module{
					"consumer": {Name: "consumer", EnvPrefix: "CONSUMER"},
					"ddns_go":  {Name: "ddns_go", EnvPrefix: "DDNS_GO"},
				}
			},
			want: `dynamic_dns.provider "ddns_go" is disabled`,
		},
		{
			name: "auto dynamic DNS without an available implementation",
			body: []byte(`modules:
  consumer: {}
global:
  base_domain: nas.test
  email: admin@nas.test
dynamic_dns:
  provider: auto
  dns_provider: cloudflare
`),
			registry: func() map[string]Module {
				return map[string]Module{"consumer": {Name: "consumer", EnvPrefix: "CONSUMER"}}
			},
			want: `no dynamic DNS implementation can update records`,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			assertImportAndConfigPlanError(t, test.body, test.registry, test.want)
		})
	}
}

func TestReadOnlyResolutionAllowsOnlyGenuinelyUnresolvedDraftBindings(t *testing.T) {
	tests := []struct {
		name     string
		body     []byte
		registry func() map[string]Module
	}{
		{
			name: "missing explicit IAM selection",
			body: []byte(`modules:
  consumer: {}
global:
  base_domain: nas.test
  email: admin@nas.test
`),
			registry: iamRegistry,
		},
		{
			name: "ambiguous auto capability provider",
			body: []byte(`modules:
  consumer: {}
global:
  base_domain: nas.test
  email: admin@nas.test
`),
			registry: func() map[string]Module {
				return forwardAuthRegistry("provider_a", "provider_b")
			},
		},
		{
			name: "ambiguous auto contract provider",
			body: []byte(`modules:
  consumer: {}
global:
  base_domain: nas.test
  email: admin@nas.test
`),
			registry: contractRegistry,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			assertImportAndConfigPlanSuccess(t, test.body, test.registry)
		})
	}
}

func TestResolvedBindingFailuresRedactValidSensitiveSelectors(t *testing.T) {
	const secret = "topsecretselector"
	const key = "CONSUMER_SELECTOR"
	newApp := func() *app {
		return &app{
			env: map[string]string{key: secret}, runnerSensitive: map[string]bool{key: true},
			reg: map[string]Module{},
		}
	}

	t.Run("missing alternative provider", func(t *testing.T) {
		a := newApp()
		_, err := a.resolveAlternativeDependency("consumer", Module{EnvPrefix: "CONSUMER"}, AlternativeDependency{
			Capability: "storage", SelectedBy: "selector", Providers: []string{secret}, Default: secret,
		})
		assertRedactedResolverError(t, err, secret)
	})

	t.Run("missing contract provider", func(t *testing.T) {
		a := newApp()
		a.contracts = map[string]Contract{"database": {Name: "database", Interfaces: []string{secret}}}
		_, err := a.resolveContractDependency("consumer", Module{EnvPrefix: "CONSUMER"}, ContractDependency{
			Name: "database", SelectedBy: "selector", Interfaces: []string{secret}, Default: secret,
		})
		assertRedactedResolverError(t, err, secret)
	})

	t.Run("incompatible capability interface", func(t *testing.T) {
		a := newApp()
		_, err := a.resolveCapabilityInterface("consumer", Module{EnvPrefix: "CONSUMER"}, RequiredCapability{
			Name: capabilityIAM, InterfaceSelectedBy: "selector", AnyOf: []string{secret}, Prefer: []string{secret},
		}, "iam_provider", ProvidedCapability{Name: capabilityIAM, Interfaces: []string{interfaceOIDC}})
		assertRedactedResolverError(t, err, secret)
	})
}

func alternativeConsumer() Module {
	return Module{
		Name: "consumer", EnvPrefix: "CONSUMER",
		Parameters: []string{"storage_provider"},
		Types:      map[string]ParamType{"storage_provider": {Kind: "string"}},
		RequiresOne: []AlternativeDependency{{
			Capability: "storage", SelectedBy: "storage_provider",
			Providers: []string{"provider"}, Default: "provider",
		}},
	}
}

func contractRegistry() map[string]Module {
	consumer := Module{
		Name: "consumer", EnvPrefix: "CONSUMER",
		Parameters: []string{"database_interface"},
		Types:      map[string]ParamType{"database_interface": {Kind: "string"}},
		RequiresContracts: []ContractDependency{{
			Name: "database", SelectedBy: "database_interface",
			Interfaces: []string{"postgres"}, Default: "postgres",
		}},
	}
	provider := func(name string) Module {
		return Module{
			Name: name, EnvPrefix: defaultEnvPrefix(name),
			ContractProviders: []ContractProvider{{Name: "database", Interface: "postgres"}},
		}
	}
	return map[string]Module{
		"consumer":   consumer,
		"provider_a": provider("provider_a"),
		"provider_b": provider("provider_b"),
	}
}

func forwardAuthRegistry(providers ...string) map[string]Module {
	reg := map[string]Module{
		"consumer": {
			Name: "consumer", EnvPrefix: "CONSUMER",
			Parameters: []string{"forward_auth_interface"},
			Types:      map[string]ParamType{"forward_auth_interface": {Kind: "string"}},
			RequiresCapabilities: []RequiredCapability{{
				Name: capabilityForwardAuth, InterfaceSelectedBy: "forward_auth_interface",
				AnyOf: []string{interfaceHTTP}, Prefer: []string{interfaceHTTP},
			}},
		},
	}
	for _, name := range providers {
		reg[name] = Module{
			Name: name, EnvPrefix: defaultEnvPrefix(name),
			Provides: []ProvidedCapability{{Name: capabilityForwardAuth, Interfaces: []string{interfaceHTTP}}},
		}
	}
	return reg
}

func iamRegistry() map[string]Module {
	return map[string]Module{
		"consumer": {
			Name: "consumer", EnvPrefix: "CONSUMER",
			Parameters: []string{"iam_protocol"},
			Types:      map[string]ParamType{"iam_protocol": {Kind: "string"}},
			RequiresCapabilities: []RequiredCapability{{
				Name: capabilityIAM, InterfaceSelectedBy: "iam_protocol",
				AnyOf: []string{interfaceOIDC}, Prefer: []string{interfaceOIDC},
			}},
		},
		"provider": iamProvider("provider"),
	}
}

func iamProvider(name string) Module {
	return Module{
		Name: name, EnvPrefix: defaultEnvPrefix(name),
		Provides: []ProvidedCapability{{
			Name: capabilityIAM, Interfaces: []string{interfaceOIDC, interfaceSAML},
		}},
	}
}

func assertImportAndConfigPlanError(t *testing.T, body []byte, registry func() map[string]Module, want string) {
	t.Helper()
	for _, boundary := range []string{"import", "config plan"} {
		t.Run(boundary, func(t *testing.T) {
			err := runResolvedBindingBoundary(t, boundary, body, registry())
			if err == nil || !strings.Contains(err.Error(), want) {
				t.Fatalf("%s error = %v, want %q", boundary, err, want)
			}
		})
	}
}

func assertImportAndConfigPlanSuccess(t *testing.T, body []byte, registry func() map[string]Module) {
	t.Helper()
	for _, boundary := range []string{"import", "config plan"} {
		t.Run(boundary, func(t *testing.T) {
			if err := runResolvedBindingBoundary(t, boundary, body, registry()); err != nil {
				t.Fatalf("%s rejected an unresolved draft: %v", boundary, err)
			}
		})
	}
}

func runResolvedBindingBoundary(t *testing.T, boundary string, body []byte, reg map[string]Module) error {
	t.Helper()
	workspace := t.TempDir()
	configPath := workspaceConfigPath(workspace)
	if err := os.WriteFile(configPath, body, 0o600); err != nil {
		t.Fatal(err)
	}
	if boundary == "import" {
		return validateConfigImportSource(configPath, reg)
	}
	base := stateDir(workspace)
	if err := os.MkdirAll(base, 0o700); err != nil {
		t.Fatal(err)
	}
	_, err := captureRunnerStdout(t, func() error {
		return reportConfigPlan(workspace, configPath, base, reg, true)
	})
	return err
}

func TestRemoteModuleLockRejectsMissingInputRequired(t *testing.T) {
	root := t.TempDir()
	moduleRoot := filepath.Join(root, "modules")
	moduleDir := filepath.Join(moduleRoot, "example")
	if err := os.MkdirAll(moduleDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "contracts"), 0o700); err != nil {
		t.Fatal(err)
	}
	manifest := `api_version: anas.module/v1
kind: Module
name: example
version: 1.0.0
revision: 1
status: release
abi:
  supports: [anas.module-hook/v1]
runtime:
  type: builtin
config:
  input_required: [token]
  types: {token: string}
`
	if err := os.WriteFile(filepath.Join(moduleDir, "module.yml"), []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}
	workspace := t.TempDir()
	configPath := workspaceConfigPath(workspace)
	body := "modules:\n  example: {}\nglobal:\n  base_domain: nas.test\n  email: admin@nas.test\n"
	if err := os.WriteFile(configPath, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	lock := &moduleLock{APIVersion: "anas.module-lock/v1", Modules: map[string]moduleLockRecord{}, Bindings: map[string]map[string]string{}}
	_, _, err := resolveRemoteModuleLock(workspace, configPath, moduleRoot, lock)
	if err == nil || !strings.Contains(err.Error(), "EXAMPLE_TOKEN") {
		t.Fatalf("remote lock missing-input error = %v", err)
	}
}
