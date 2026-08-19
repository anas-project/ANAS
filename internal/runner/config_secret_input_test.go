package runner

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/anas-project/ANAS/internal/config"
	"github.com/anas-project/ANAS/internal/configschema"
	"github.com/anas-project/ANAS/internal/dns"
)

func lifecycleInputTestRegistry() map[string]Module {
	minimum := 16
	return map[string]Module{
		"demo": {
			Name: "demo",
			// Deliberately lower-case to exercise the generic manifest-prefix
			// canonicalization at every lifecycle secret boundary.
			EnvPrefix:     "demo",
			Parameters:    []string{"token"},
			InputRequired: []string{"DEMO_TOKEN"},
			Types: map[string]ParamType{
				"token": {Kind: "string", Constraints: configschema.Constraints{MinLength: &minimum}},
			},
			Changes: map[string]ChangePolicy{
				"token": {Effect: "credential_rotate", Sensitive: true, Apply: "demo rotate-token"},
			},
		},
	}
}

func writeLifecycleInputSource(t *testing.T, input string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yml")
	body := "modules:\n  demo:" + input + "\nglobal:\n  base_domain: nas.test\n  email: admin@nas.test\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func captureRunnerStdout(t *testing.T, run func() error) (string, error) {
	t.Helper()
	out, err := os.CreateTemp(t.TempDir(), "stdout")
	if err != nil {
		t.Fatal(err)
	}
	real := os.Stdout
	os.Stdout = out
	defer func() { os.Stdout = real }()
	runErr := run()
	if _, err := out.Seek(0, io.SeekStart); err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(out)
	if err != nil {
		t.Fatal(err)
	}
	if err := out.Close(); err != nil {
		t.Fatal(err)
	}
	return string(body), runErr
}

func TestInputRequiredLifecycleSecretImportsFromEverySupportedPath(t *testing.T) {
	const secret = "correct-horse-battery"
	tests := map[string]string{
		"module config": "\n    config:\n      token: " + secret,
		"raw env":       " {}\nenv:\n  demo_token: " + secret,
		"secrets":       " {}\nsecrets:\n  demo_token: " + secret,
	}
	for name, input := range tests {
		t.Run(name, func(t *testing.T) {
			workspace := t.TempDir()
			result, err := importConfigIntoWorkspace(workspace, writeLifecycleInputSource(t, input), lifecycleInputTestRegistry())
			if err != nil {
				t.Fatalf("importing required lifecycle secret: %v", err)
			}
			if len(result.Secrets) != 1 || result.Secrets[0].Key != "DEMO_TOKEN" || result.Secrets[0].Value != secret {
				t.Fatalf("extracted secrets = %+v", result.Secrets)
			}
			if strings.Contains(string(result.Normalized), secret) {
				t.Fatal("normalized config retained lifecycle secret plaintext")
			}
			store, err := loadSecretStore(stateDir(workspace))
			if err != nil {
				t.Fatal(err)
			}
			if store.values["DEMO_TOKEN"] != secret || store.metadata["DEMO_TOKEN"].Kind != "lifecycle_managed" {
				t.Fatalf("stored lifecycle secret = %q, metadata=%+v", store.values["DEMO_TOKEN"], store.metadata["DEMO_TOKEN"])
			}
		})
	}
}

func TestInputRequiredLifecycleSecretImportFailsBeforeWritingWhenMissing(t *testing.T) {
	workspace := t.TempDir()
	_, err := importConfigIntoWorkspace(workspace, writeLifecycleInputSource(t, " {}"), lifecycleInputTestRegistry())
	if err == nil || !strings.Contains(err.Error(), "DEMO_TOKEN") {
		t.Fatalf("missing lifecycle input error = %v", err)
	}
	if exists(workspaceConfigPath(workspace)) || exists(filepath.Join(stateDir(workspace), "secrets.yml")) {
		t.Fatal("rejected import wrote managed state")
	}
}

func TestInputRequiredLifecycleSecretReimportUsesExistingStoreValue(t *testing.T) {
	workspace := t.TempDir()
	reg := lifecycleInputTestRegistry()
	const secret = "correct-horse-battery"
	if _, err := importConfigIntoWorkspace(workspace,
		writeLifecycleInputSource(t, "\n    config:\n      token: "+secret), reg); err != nil {
		t.Fatal(err)
	}
	// The managed config contains no plaintext token. Reimport/migrate must use
	// the existing lifecycle value while validating its input_required contract.
	if _, err := importConfigIntoWorkspace(workspace, workspaceConfigPath(workspace), reg); err != nil {
		t.Fatalf("reimporting normalized config with existing lifecycle input: %v", err)
	}
	store, err := loadSecretStore(stateDir(workspace))
	if err != nil {
		t.Fatal(err)
	}
	if store.values["DEMO_TOKEN"] != secret || store.metadata["DEMO_TOKEN"].Kind != "lifecycle_managed" {
		t.Fatalf("reimport changed lifecycle value: value=%q metadata=%+v", store.values["DEMO_TOKEN"], store.metadata["DEMO_TOKEN"])
	}
}

func TestLifecycleSecretSchemaErrorsNeverEchoValues(t *testing.T) {
	const rejected = "LEAK-ME"
	for name, input := range map[string]string{
		"module config": "\n    config:\n      token: " + rejected,
		"raw env":       " {}\nenv:\n  DEMO_TOKEN: " + rejected,
		"secrets":       " {}\nsecrets:\n  demo_token: " + rejected,
	} {
		t.Run(name, func(t *testing.T) {
			_, err := normalizeImportedConfig(writeLifecycleInputSource(t, input), lifecycleInputTestRegistry())
			if err == nil || !strings.Contains(err.Error(), "declared string type or constraints") {
				t.Fatalf("schema error = %v", err)
			}
			if strings.Contains(err.Error(), rejected) {
				t.Fatalf("schema error echoed secret value: %v", err)
			}
		})
	}
}

func TestSecretsSourceRedactsSchemaErrorsWithoutSensitivePolicy(t *testing.T) {
	minimum := 16
	reg := map[string]Module{
		"demo": {
			Name: "demo", EnvPrefix: "DEMO", Parameters: []string{"token"},
			Types: map[string]ParamType{
				"token": {Kind: "string", Constraints: configschema.Constraints{MinLength: &minimum}},
			},
			Changes: map[string]ChangePolicy{
				"token": {Effect: "data_migrate", Sensitive: false, Apply: "demo migrate"},
			},
		},
	}
	const rejected = "LEAK-ME"
	_, err := normalizeImportedConfig(writeLifecycleInputSource(t, " {}\nsecrets:\n  DEMO_TOKEN: "+rejected), reg)
	if err == nil || !strings.Contains(err.Error(), "declared string type or constraints") {
		t.Fatalf("secrets schema error = %v", err)
	}
	if strings.Contains(err.Error(), rejected) {
		t.Fatalf("secrets source schema error echoed value: %v", err)
	}
}

func TestImportedSecretCanonicalizationRejectsCollisionsWithoutValues(t *testing.T) {
	for name, body := range map[string]string{
		"normalized secret keys": " {}\nsecrets:\n  demo_token: first-secret-value\n  DEMO_TOKEN: second-secret-value",
		"secret and module":      "\n    config:\n      token: first-secret-value\nsecrets:\n  DEMO_TOKEN: second-secret-value",
		"secret and env":         " {}\nenv:\n  DEMO_TOKEN: first-secret-value\nsecrets:\n  demo_token: second-secret-value",
	} {
		t.Run(name, func(t *testing.T) {
			_, err := normalizeImportedConfig(writeLifecycleInputSource(t, body), lifecycleInputTestRegistry())
			if err == nil || !strings.Contains(err.Error(), "DEMO_TOKEN") {
				t.Fatalf("collision error = %v", err)
			}
			for _, value := range []string{"first-secret-value", "second-secret-value"} {
				if strings.Contains(err.Error(), value) {
					t.Fatalf("collision error echoed secret value: %v", err)
				}
			}
		})
	}
}

func TestConfigPlanUsesOnlyLifecycleManagedSecretInputs(t *testing.T) {
	const validSecret = "correct-horse-battery"
	for _, test := range []struct {
		name, kind, value string
		wantOK            bool
	}{
		{name: "lifecycle secret present", kind: "lifecycle_managed", value: validSecret, wantOK: true},
		{name: "secret absent"},
		{name: "generated secret is not caller input", kind: "generated", value: validSecret},
		{name: "local admin secret is not caller input", kind: "local_admin", value: validSecret},
		{name: "invalid lifecycle secret", kind: "lifecycle_managed", value: "LEAK-ME"},
	} {
		t.Run(test.name, func(t *testing.T) {
			workspace := t.TempDir()
			configPath := writeLifecycleInputSource(t, " {}")
			managedPath := workspaceConfigPath(workspace)
			body, err := os.ReadFile(configPath)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(managedPath, body, 0o600); err != nil {
				t.Fatal(err)
			}
			base := stateDir(workspace)
			if err := os.MkdirAll(base, 0o700); err != nil {
				t.Fatal(err)
			}
			if test.kind != "" {
				store := &secretStore{
					path: filepath.Join(base, "secrets.yml"), values: map[string]string{"DEMO_TOKEN": test.value},
					metadata: map[string]secretMetadata{"DEMO_TOKEN": {Owner: "demo", Kind: test.kind, Provenance: "test"}}, dirty: true,
				}
				if err := store.Save(); err != nil {
					t.Fatal(err)
				}
			}
			stdout, err := captureRunnerStdout(t, func() error {
				return reportConfigPlan(workspace, managedPath, base, lifecycleInputTestRegistry(), true)
			})
			if test.wantOK {
				if err != nil {
					t.Fatalf("plan rejected lifecycle input: %v", err)
				}
				if strings.Contains(stdout, validSecret) {
					t.Fatal("plan output exposed lifecycle secret")
				}
				return
			}
			if err == nil {
				t.Fatalf("plan unexpectedly accepted kind=%q value=%q", test.kind, test.value)
			}
			if test.kind == "lifecycle_managed" {
				if strings.Contains(err.Error(), test.value) || !strings.Contains(err.Error(), "type or constraints") {
					t.Fatalf("invalid lifecycle secret error = %v", err)
				}
			} else if !strings.Contains(err.Error(), "DEMO_TOKEN") {
				t.Fatalf("missing lifecycle input error = %v", err)
			}
		})
	}
}

func TestLifecycleSecretProvenanceRedactsSchemaErrorsAfterPolicyDrift(t *testing.T) {
	const rejected = "LEAK-ME"
	minimum := 16
	reg := map[string]Module{
		"demo": {
			Name: "demo", EnvPrefix: "DEMO", Parameters: []string{"token"},
			InputRequired: []string{"DEMO_TOKEN"},
			Types: map[string]ParamType{
				"token": {Kind: "string", Constraints: configschema.Constraints{MinLength: &minimum}},
			},
			// Simulate a Module upgrade that accidentally loses the sensitive bit.
			// Secret Store provenance must remain authoritative for redaction.
			Changes: map[string]ChangePolicy{
				"token": {Effect: "container_recreate", Sensitive: false},
			},
		},
	}
	workspace := t.TempDir()
	configPath := writeLifecycleInputSource(t, " {}")
	managedPath := workspaceConfigPath(workspace)
	body, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(managedPath, body, 0o600); err != nil {
		t.Fatal(err)
	}
	base := stateDir(workspace)
	if err := os.MkdirAll(base, 0o700); err != nil {
		t.Fatal(err)
	}
	store := &secretStore{
		path: filepath.Join(base, "secrets.yml"), values: map[string]string{"DEMO_TOKEN": rejected},
		metadata: map[string]secretMetadata{
			"DEMO_TOKEN": {Owner: "demo", Kind: "lifecycle_managed", Provenance: "older-manifest"},
		},
		dirty: true,
	}
	if err := store.Save(); err != nil {
		t.Fatal(err)
	}

	_, planErr := captureRunnerStdout(t, func() error {
		return reportConfigPlan(workspace, managedPath, base, reg, true)
	})
	assertRedactedConstraintError(t, planErr, rejected)

	cfg, err := config.Load(managedPath)
	if err != nil {
		t.Fatal(err)
	}
	env, owners := configBaseEnvWithRegistry(cfg, reg)
	a := &app{
		base: base, cfg: cfg, reg: reg, env: env, envOwner: owners,
		registryOnlyResolution: true, resolvedBindings: map[string]map[string]string{},
	}
	if err := a.loadImportedSecrets(); err != nil {
		t.Fatal(err)
	}
	_, applyErr := a.resolveOrderWithInputValidation(cfg.Modules.Order)
	assertRedactedConstraintError(t, applyErr, rejected)
}

func TestSecretSourceAliasesRemainRedactedAtWholeConfigBoundaries(t *testing.T) {
	const rejected = "secret-selector-value"
	reg := map[string]Module{
		"demo": {
			Name: "demo", EnvPrefix: "DEMO", Parameters: []string{"source", "selector"},
			Types: map[string]ParamType{
				"source":   {Kind: "string"},
				"selector": {Kind: "enum", Enum: []string{"allowed"}},
			},
			Changes: map[string]ChangePolicy{
				"source": {Effect: "credential_rotate", Sensitive: true},
			},
		},
	}
	baseConfig := func() *config.File {
		return &config.File{
			Modules: config.NewModuleSelection("demo"),
			Env:     map[string]any{"DEMO_SELECTOR": rejected},
		}
	}

	t.Run("top-level secrets alias", func(t *testing.T) {
		cfg := baseConfig()
		cfg.Secrets = map[string]any{"SOURCE_BLOB": rejected}
		err := validateLoadedConfigSchema(cfg, reg, nil, nil)
		assertRedactedConstraintError(t, err, rejected)
	})

	t.Run("lifecycle store alias", func(t *testing.T) {
		err := validateLoadedConfigSchema(baseConfig(), reg, map[string]string{"DEMO_SOURCE": rejected}, nil)
		assertRedactedConstraintError(t, err, rejected)
	})

	t.Run("padded private source matches trimmed alias", func(t *testing.T) {
		err := validateLoadedConfigSchema(baseConfig(), reg, nil, nil, map[string]string{"PRIVATE_SOURCE": "  " + rejected + "\n"})
		assertRedactedConstraintError(t, err, rejected)
	})

	for _, kind := range []string{"generated", "local_admin"} {
		t.Run(kind+" store alias during apply", func(t *testing.T) {
			base := t.TempDir()
			store := &secretStore{
				path:   filepath.Join(base, "secrets.yml"),
				values: map[string]string{"SOURCE_BLOB": rejected},
				metadata: map[string]secretMetadata{
					"SOURCE_BLOB": {Owner: "demo", Kind: kind, Provenance: "test"},
				},
				dirty: true,
			}
			if err := store.Save(); err != nil {
				t.Fatal(err)
			}
			cfg := baseConfig()
			env, owners := configBaseEnvWithRegistry(cfg, reg)
			a := &app{
				base: base, cfg: cfg, reg: reg, env: env, envOwner: owners,
				registryOnlyResolution: true, resolvedBindings: map[string]map[string]string{},
			}
			if err := a.loadImportedSecrets(); err != nil {
				t.Fatal(err)
			}
			if _, injected := a.env["SOURCE_BLOB"]; injected {
				t.Fatalf("%s Secret Store value was treated as caller input", kind)
			}
			_, err := a.resolveOrderWithInputValidation(cfg.Modules.Order)
			assertRedactedConstraintError(t, err, rejected)
		})
	}
}

func assertRedactedConstraintError(t *testing.T, err error, rejected string) {
	t.Helper()
	if err == nil || !strings.Contains(err.Error(), "type or constraints") {
		t.Fatalf("constraint error = %v", err)
	}
	if strings.Contains(strings.ToLower(err.Error()), strings.ToLower(rejected)) {
		t.Fatalf("constraint error exposed Secret Store value: %v", err)
	}
}

func TestLifecycleSecretProvenanceRedactsResolverSelectorErrors(t *testing.T) {
	const rejected = "TopSecretSelector"
	reg := map[string]Module{
		"consumer": {
			Name: "consumer", EnvPrefix: "CONSUMER", Parameters: []string{"backend"},
			Types: map[string]ParamType{"backend": {Kind: "string"}},
			Changes: map[string]ChangePolicy{
				"backend": {Effect: "container_recreate", Sensitive: false},
			},
			RequiresOne: []AlternativeDependency{{
				Capability: "storage", SelectedBy: "backend",
				Providers: []string{"provider_one", "provider_two"}, Default: "provider_one",
			}},
		},
		"provider_one": {Name: "provider_one", EnvPrefix: "PROVIDER_ONE"},
		"provider_two": {Name: "provider_two", EnvPrefix: "PROVIDER_TWO"},
	}
	workspace := t.TempDir()
	managedPath := workspaceConfigPath(workspace)
	if err := os.WriteFile(managedPath, []byte(`modules:
  consumer: {}
  provider_one: {}
  provider_two: {}
global:
  base_domain: nas.test
  email: admin@nas.test
`), 0o600); err != nil {
		t.Fatal(err)
	}
	base := stateDir(workspace)
	if err := os.MkdirAll(base, 0o700); err != nil {
		t.Fatal(err)
	}
	store := &secretStore{
		path: filepath.Join(base, "secrets.yml"),
		values: map[string]string{
			"CONSUMER_BACKEND": rejected,
		},
		metadata: map[string]secretMetadata{
			"CONSUMER_BACKEND": {Owner: "consumer", Kind: "lifecycle_managed", Provenance: "older-manifest"},
		},
		dirty: true,
	}
	if err := store.Save(); err != nil {
		t.Fatal(err)
	}

	planOutput, planErr := captureRunnerStdout(t, func() error {
		return reportConfigPlan(workspace, managedPath, base, reg, true)
	})
	// Read-only schema resolution deliberately tolerates a binding it cannot
	// settle yet. Whether it reports or defers that error, neither channel may
	// expose a value whose source is the lifecycle Secret Store.
	if strings.Contains(strings.ToLower(planOutput), strings.ToLower(rejected)) ||
		(planErr != nil && strings.Contains(strings.ToLower(planErr.Error()), strings.ToLower(rejected))) {
		t.Fatalf("config plan exposed Secret Store selector: stdout=%q error=%v", planOutput, planErr)
	}

	cfg, err := config.Load(managedPath)
	if err != nil {
		t.Fatal(err)
	}
	env, owners := configBaseEnvWithRegistry(cfg, reg)
	a := &app{
		base: base, cfg: cfg, reg: reg, env: env, envOwner: owners,
		registryOnlyResolution: true, resolvedBindings: map[string]map[string]string{},
	}
	if err := a.loadImportedSecrets(); err != nil {
		t.Fatal(err)
	}
	_, applyErr := a.resolveOrderWithInputValidation(cfg.Modules.Order)
	assertRedactedResolverError(t, applyErr, rejected)
}

func TestEveryEnvironmentSelectorResolverUsesSourceRedaction(t *testing.T) {
	const rejected = "TopSecretSelector"
	key := "CONSUMER_SELECTOR"
	newApp := func() *app {
		return &app{
			env: map[string]string{key: rejected}, runnerSensitive: map[string]bool{key: true},
			cfg: &config.File{}, reg: map[string]Module{},
		}
	}

	t.Run("alternative provider", func(t *testing.T) {
		a := newApp()
		_, err := a.resolveAlternativeDependency("consumer", Module{EnvPrefix: "CONSUMER"}, AlternativeDependency{
			Capability: "storage", SelectedBy: "selector", Providers: []string{"one", "two"}, Default: "one",
		})
		assertRedactedResolverError(t, err, rejected)
	})

	t.Run("allowed alternative is not installed", func(t *testing.T) {
		a := newApp()
		a.env[key] = "ghost_provider"
		_, err := a.resolveAlternativeDependency("consumer", Module{EnvPrefix: "CONSUMER"}, AlternativeDependency{
			Capability: "storage", SelectedBy: "selector", Providers: []string{"ghost_provider"}, Default: "ghost_provider",
		})
		assertRedactedResolverError(t, err, "ghost_provider")
	})

	t.Run("contract interface", func(t *testing.T) {
		a := newApp()
		a.contracts = map[string]Contract{"database": {Name: "database", Interfaces: []string{"sql"}}}
		_, err := a.resolveContractDependency("consumer", Module{EnvPrefix: "CONSUMER"}, ContractDependency{
			Name: "database", SelectedBy: "selector", Interfaces: []string{"sql"}, Default: "sql",
		})
		assertRedactedResolverError(t, err, rejected)
	})

	t.Run("valid contract interface has no provider", func(t *testing.T) {
		a := newApp()
		a.env[key] = "sql"
		a.contracts = map[string]Contract{"database": {Name: "database", Interfaces: []string{"sql"}}}
		_, err := a.resolveContractDependency("consumer", Module{EnvPrefix: "CONSUMER"}, ContractDependency{
			Name: "database", SelectedBy: "selector", Interfaces: []string{"sql"}, Default: "sql",
		})
		assertRedactedResolverError(t, err, "sql")
	})

	t.Run("capability interface", func(t *testing.T) {
		a := newApp()
		_, err := a.resolveCapabilityInterface("consumer", Module{EnvPrefix: "CONSUMER"}, RequiredCapability{
			Name: capabilityIAM, InterfaceSelectedBy: "selector", AnyOf: []string{interfaceOIDC}, Prefer: []string{interfaceOIDC},
		}, "provider", ProvidedCapability{Name: capabilityIAM, Interfaces: []string{interfaceOIDC}})
		assertRedactedResolverError(t, err, rejected)
	})

	t.Run("valid capability interface is absent from provider", func(t *testing.T) {
		a := newApp()
		a.env[key] = interfaceOIDC
		_, err := a.resolveCapabilityInterface("consumer", Module{EnvPrefix: "CONSUMER"}, RequiredCapability{
			Name: capabilityIAM, InterfaceSelectedBy: "selector", AnyOf: []string{interfaceOIDC}, Prefer: []string{interfaceOIDC},
		}, "provider", ProvidedCapability{Name: capabilityIAM, Interfaces: []string{interfaceSAML}})
		assertRedactedResolverError(t, err, interfaceOIDC)
	})
}

func TestSourceSensitiveValuesCannotBecomePersistentBindings(t *testing.T) {
	assertRejected := func(t *testing.T, err error, value string) {
		t.Helper()
		if err == nil || !strings.Contains(err.Error(), "cannot come from secrets") || !strings.Contains(err.Error(), "<redacted>") {
			t.Fatalf("secret-backed selector error = %v", err)
		}
		if strings.Contains(err.Error(), value) {
			t.Fatalf("secret-backed selector error exposed %q: %v", value, err)
		}
	}

	t.Run("alternative provider", func(t *testing.T) {
		const value = "provider_one"
		a := &app{
			env: map[string]string{"CONSUMER_SELECTOR": value}, runnerSensitive: map[string]bool{"CONSUMER_SELECTOR": true},
			cfg: &config.File{Modules: config.NewModuleSelection(value)},
			reg: map[string]Module{value: {Name: value, EnvPrefix: "PROVIDER_ONE"}},
		}
		_, err := a.resolveAlternativeDependency("consumer", Module{EnvPrefix: "CONSUMER"}, AlternativeDependency{
			Capability: "storage", SelectedBy: "selector", Providers: []string{value}, Default: value,
		})
		assertRejected(t, err, value)
	})

	t.Run("contract interface", func(t *testing.T) {
		const value = "sql"
		a := &app{
			env: map[string]string{"CONSUMER_SELECTOR": value}, runnerSensitive: map[string]bool{"CONSUMER_SELECTOR": true},
			cfg:       &config.File{},
			contracts: map[string]Contract{"database": {Name: "database", Interfaces: []string{value}}},
		}
		_, err := a.resolveContractDependency("consumer", Module{EnvPrefix: "CONSUMER"}, ContractDependency{
			Name: "database", SelectedBy: "selector", Interfaces: []string{value}, Default: value,
		})
		assertRejected(t, err, value)
	})

	t.Run("capability interface", func(t *testing.T) {
		const value = interfaceOIDC
		a := &app{
			env: map[string]string{"CONSUMER_SELECTOR": value}, runnerSensitive: map[string]bool{"CONSUMER_SELECTOR": true},
			cfg: &config.File{},
		}
		_, err := a.resolveCapabilityInterface("consumer", Module{EnvPrefix: "CONSUMER"}, RequiredCapability{
			Name: capabilityIAM, InterfaceSelectedBy: "selector", AnyOf: []string{value}, Prefer: []string{value},
		}, "provider", ProvidedCapability{Name: capabilityIAM, Interfaces: []string{value}})
		assertRejected(t, err, value)
	})

	t.Run("dns platform", func(t *testing.T) {
		const value = "cloudflare"
		registry, err := dns.Load()
		if err != nil {
			t.Fatal(err)
		}
		a := &app{
			env: map[string]string{"DDNS_GO_DNS_PROVIDER": value}, runnerSensitive: map[string]bool{"DDNS_GO_DNS_PROVIDER": true},
			reg: map[string]Module{"ddns_go": {Name: "ddns_go", EnvPrefix: "DDNS_GO"}},
		}
		err = a.materializeEngineCredentials(registry, "ddns_go")
		assertRejected(t, err, value)
	})
}

func TestResolvedBindingLockDiagnosticsRedactSelectorDerivedValues(t *testing.T) {
	a := &app{
		reg: map[string]Module{
			"consumer": {
				Name: "consumer", EnvPrefix: "CONSUMER",
				RequiresOne:          []AlternativeDependency{{Capability: "storage", SelectedBy: "backend"}},
				RequiresContracts:    []ContractDependency{{Name: "database", SelectedBy: "database_interface"}},
				RequiresCapabilities: []RequiredCapability{{Name: capabilityIAM, InterfaceSelectedBy: "iam_interface"}},
			},
		},
		runnerSensitive: map[string]bool{
			"CONSUMER_BACKEND":            true,
			"CONSUMER_DATABASE_INTERFACE": true,
			"CONSUMER_IAM_INTERFACE":      true,
		},
	}
	for _, binding := range []string{"storage", "database", "database.interface", capabilityIAM, capabilityIAM + ".interface"} {
		if got := a.resolvedBindingValueForError("consumer", binding, "selector-derived"); got != "<redacted>" {
			t.Errorf("binding %s diagnostic = %q", binding, got)
		}
	}
}

func assertRedactedResolverError(t *testing.T, err error, rejected string) {
	t.Helper()
	if err == nil || !strings.Contains(err.Error(), "<redacted>") {
		t.Fatalf("resolver error = %v", err)
	}
	if strings.Contains(strings.ToLower(err.Error()), strings.ToLower(rejected)) {
		t.Fatalf("resolver error exposed Secret Store value: %v", err)
	}
}

func TestSecretsSettingsKeepDeclaredRiskPolicyInPlanAndStartGuard(t *testing.T) {
	for _, effect := range []string{"data_migrate", "immutable"} {
		t.Run(effect, func(t *testing.T) {
			reg := map[string]Module{
				"demo": {
					Name: "demo", EnvPrefix: "DEMO", Parameters: []string{"token"},
					Types: map[string]ParamType{"token": {Kind: "string"}},
					Changes: map[string]ChangePolicy{
						"token": {Effect: effect, Sensitive: true, Apply: "demo explicit-operation"},
					},
				},
			}
			workspace := t.TempDir()
			base := stateDir(workspace)
			if err := os.MkdirAll(base, 0o700); err != nil {
				t.Fatal(err)
			}
			configPath := workspaceConfigPath(workspace)
			write := func(value string) {
				t.Helper()
				body := "modules:\n  demo: {}\nglobal:\n  base_domain: nas.test\n  email: admin@nas.test\nsecrets:\n  DEMO_TOKEN: " + value + "\n"
				if err := os.WriteFile(configPath, []byte(body), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			const oldValue = "old-sensitive-value"
			const newValue = "new-sensitive-value"
			write(oldValue)
			if err := saveAppliedConfig(base, configPath); err != nil {
				t.Fatal(err)
			}
			write(newValue)

			target := targetForSettingPath("secrets.DEMO_TOKEN", reg)
			if target.Module != "demo" || target.Parameter != "token" || policyForTarget(target, reg).Effect != effect {
				t.Fatalf("secrets target/policy = %+v / %+v", target, policyForTarget(target, reg))
			}
			guardErr := validateOrdinaryStartChanges(base, configPath, reg)
			if guardErr == nil || !strings.Contains(guardErr.Error(), effect) {
				t.Fatalf("ordinary-start guard error = %v", guardErr)
			}
			if strings.Contains(guardErr.Error(), oldValue) || strings.Contains(guardErr.Error(), newValue) {
				t.Fatalf("ordinary-start guard exposed secret: %v", guardErr)
			}

			stdout, err := captureRunnerStdout(t, func() error {
				return reportConfigPlan(workspace, configPath, base, reg, true)
			})
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(stdout, oldValue) || strings.Contains(stdout, newValue) {
				t.Fatalf("config plan exposed secret: %s", stdout)
			}
			var document map[string]any
			if err := json.Unmarshal([]byte(stdout), &document); err != nil {
				t.Fatal(err)
			}
			changes := document["changes"].([]any)
			if len(changes) != 1 || changes[0].(map[string]any)["effect"] != effect || changes[0].(map[string]any)["sensitive"] != true {
				t.Fatalf("config plan changes = %#v", changes)
			}
		})
	}
}

func TestConfigListUsesCanonicalEffectiveInputsAndHidesLifecycleSecrets(t *testing.T) {
	workspace := t.TempDir()
	configPath := workspaceConfigPath(workspace)
	if err := os.WriteFile(configPath, []byte(`modules:
  traefik: {}
  samba_dc: {}
env:
  TRAEFIK_BASE_PORT: 12345
  BASE_DOMAIN: env.nas.test
  EMAIL: admin@nas.test
`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := writeManagedConfigState(workspace, "canonical-list-test"); err != nil {
		t.Fatal(err)
	}
	reg, err := loadRegistry(repoRoot(t))
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := captureRunnerStdout(t, func() error {
		return reportConfigList(configPath, reg, "", true, stateDir(workspace))
	})
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err := json.Unmarshal([]byte(stdout), &document); err != nil {
		t.Fatalf("config list JSON = %q: %v", stdout, err)
	}
	byPath := map[string]map[string]any{}
	for _, raw := range document["parameters"].([]any) {
		entry := raw.(map[string]any)
		byPath[entry["path"].(string)] = entry
	}
	for path, want := range map[string]string{
		"traefik.base_port":  "12345",
		"global.base_domain": "env.nas.test",
	} {
		entry := byPath[path]
		if entry["set"] != true || entry["value"] != want {
			t.Errorf("%s list projection = %#v, want value %q", path, entry, want)
		}
	}

	// Import a lifecycle-managed value through the normal public boundary, then
	// verify both projections expose presence and never plaintext.
	secretSource := filepath.Join(t.TempDir(), "secret.yml")
	const secret = "Directory-Admin-Initial-1!"
	if err := os.WriteFile(secretSource, []byte(`modules:
  samba_dc:
    config:
      admin_password: `+secret+`
global:
  base_domain: nas.test
  email: admin@nas.test
`), 0o600); err != nil {
		t.Fatal(err)
	}
	secretWorkspace := t.TempDir()
	if _, err := importConfigIntoWorkspace(secretWorkspace, secretSource, reg); err != nil {
		t.Fatal(err)
	}
	for _, jsonMode := range []bool{false, true} {
		stdout, err := captureRunnerStdout(t, func() error {
			return reportConfigList(workspaceConfigPath(secretWorkspace), reg, "samba_dc", jsonMode, stateDir(secretWorkspace))
		})
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(stdout, secret) {
			t.Fatalf("config list exposed lifecycle secret (json=%t): %s", jsonMode, stdout)
		}
		if jsonMode {
			var doc map[string]any
			if err := json.Unmarshal([]byte(stdout), &doc); err != nil {
				t.Fatal(err)
			}
			found := false
			for _, raw := range doc["parameters"].([]any) {
				entry := raw.(map[string]any)
				if entry["path"] == "samba_dc.admin_password" {
					found = true
					if entry["set"] != true {
						t.Fatalf("lifecycle secret JSON entry = %#v", entry)
					}
					if _, exposed := entry["value"]; exposed {
						t.Fatalf("lifecycle secret JSON contains value: %#v", entry)
					}
				}
			}
			if !found {
				t.Fatal("JSON list omitted samba_dc.admin_password")
			}
		} else if !strings.Contains(stdout, "samba_dc.admin_password") || !strings.Contains(stdout, "<set>") {
			t.Fatalf("human list did not report lifecycle secret presence:\n%s", stdout)
		}
	}
}

func TestConfigListNeverProjectsEqualValueSecretAliases(t *testing.T) {
	const secret = "list-secret-plaintext"
	reg := map[string]Module{
		"demo": {
			Name: "demo", EnvPrefix: "DEMO", Parameters: []string{"alias"},
			Types:   map[string]ParamType{"alias": {Kind: "string"}},
			Changes: map[string]ChangePolicy{"alias": {Effect: "container_recreate"}},
		},
	}
	writeConfig := func(t *testing.T, withConfigSecret bool) string {
		t.Helper()
		secretBlock := ""
		if withConfigSecret {
			secretBlock = "secrets:\n  SOURCE_SECRET: " + secret + "\n"
		}
		path := filepath.Join(t.TempDir(), "config.yml")
		body := "modules:\n  demo: {}\nenv:\n  DEMO_ALIAS: " + secret + "\n" + secretBlock
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	assertHidden := func(t *testing.T, configPath, base string) {
		t.Helper()
		for _, jsonMode := range []bool{false, true} {
			stdout, err := captureRunnerStdout(t, func() error {
				return reportConfigList(configPath, reg, "demo", jsonMode, base)
			})
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(stdout, secret) {
				t.Fatalf("config list exposed equal-value secret alias (json=%t): %s", jsonMode, stdout)
			}
		}
	}

	t.Run("top-level secrets", func(t *testing.T) {
		assertHidden(t, writeConfig(t, true), "")
	})
	for _, kind := range []string{"lifecycle_managed", "generated", "local_admin"} {
		t.Run(kind+" store", func(t *testing.T) {
			base := t.TempDir()
			store := &secretStore{
				path:   filepath.Join(base, "secrets.yml"),
				values: map[string]string{"SOURCE_SECRET": secret},
				metadata: map[string]secretMetadata{
					"SOURCE_SECRET": {Owner: "demo", Kind: kind, Provenance: "test"},
				},
				dirty: true,
			}
			if err := store.Save(); err != nil {
				t.Fatal(err)
			}
			assertHidden(t, writeConfig(t, false), base)
		})
	}
}
