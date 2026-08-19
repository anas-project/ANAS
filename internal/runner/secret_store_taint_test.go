package runner

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/anas-project/ANAS/internal/config"
	"github.com/anas-project/ANAS/internal/configschema"
)

func privateStoreTaintRegistry() map[string]Module {
	minimum := 1
	return map[string]Module{
		"demo": {
			Name: "demo", EnvPrefix: "DEMO",
			Parameters: []string{"target", "stale"},
			Types: map[string]ParamType{
				"target": {Kind: "string"},
				"stale":  {Kind: "int", Constraints: configschema.Constraints{Minimum: &minimum}},
			},
		},
	}
}

func savePrivateStoreValue(t *testing.T, base, kind, key, value string) {
	t.Helper()
	if err := os.MkdirAll(base, 0o700); err != nil {
		t.Fatal(err)
	}
	store := &secretStore{
		path: filepath.Join(base, "secrets.yml"),
		values: map[string]string{
			key: value,
		},
		metadata: map[string]secretMetadata{
			key: {Owner: "demo", Kind: kind, Provenance: "test"},
		},
		dirty: true,
	}
	if err := store.Save(); err != nil {
		t.Fatal(err)
	}
}

func assertPrivateSchemaError(t *testing.T, err error, private string) {
	t.Helper()
	if err == nil || !strings.Contains(err.Error(), "type or constraints") {
		t.Fatalf("private-source schema error = %v", err)
	}
	if strings.Contains(err.Error(), private) {
		t.Fatalf("private-source schema error exposed Secret Store plaintext: %v", err)
	}
}

func TestEverySecretStoreKindTaintsConfigSetAndPlanAliases(t *testing.T) {
	const private = "0"
	reg := privateStoreTaintRegistry()
	for _, kind := range []string{"generated", "local_admin"} {
		t.Run(kind+" config set", func(t *testing.T) {
			workspace := t.TempDir()
			base := stateDir(workspace)
			savePrivateStoreValue(t, base, kind, "PRIVATE_SOURCE", private)
			configPath := workspaceConfigPath(workspace)
			body := []byte("modules:\n  demo:\n    config:\n      target: before\n      stale: 0\nglobal:\n  base_domain: nas.test\n  email: admin@nas.test\n")
			if err := os.WriteFile(configPath, body, 0o600); err != nil {
				t.Fatal(err)
			}
			if err := writeManagedConfigState(workspace, "test"); err != nil {
				t.Fatal(err)
			}
			statePath := managedConfigStatePath(base)
			beforeState, err := os.ReadFile(statePath)
			if err != nil {
				t.Fatal(err)
			}

			err = setManagedConfigScalar(workspace, configPath,
				[]string{"modules", "demo", "config", "target"}, "after", true, reg)
			assertPrivateSchemaError(t, err, private)
			afterConfig, readErr := os.ReadFile(configPath)
			if readErr != nil {
				t.Fatal(readErr)
			}
			afterState, readErr := os.ReadFile(statePath)
			if readErr != nil {
				t.Fatal(readErr)
			}
			if string(afterConfig) != string(body) || string(afterState) != string(beforeState) {
				t.Fatal("rejected config set changed config.yml or its managed digest")
			}
		})

		t.Run(kind+" config plan", func(t *testing.T) {
			workspace := t.TempDir()
			base := stateDir(workspace)
			savePrivateStoreValue(t, base, kind, "PRIVATE_SOURCE", private)
			configPath := workspaceConfigPath(workspace)
			if err := os.WriteFile(configPath, []byte("modules:\n  demo:\n    config:\n      stale: 0\nglobal:\n  base_domain: nas.test\n  email: admin@nas.test\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			assertPrivateSchemaError(t, reportConfigPlan(workspace, configPath, base, reg, true), private)
		})
	}
}

func TestEverySecretStoreKindTaintsImportAndReimportAliases(t *testing.T) {
	const private = "0"
	reg := privateStoreTaintRegistry()
	for _, kind := range []string{"generated", "local_admin"} {
		for _, existingManagedConfig := range []bool{false, true} {
			name := "import"
			if existingManagedConfig {
				name = "reimport"
			}
			t.Run(kind+" "+name, func(t *testing.T) {
				workspace := t.TempDir()
				base := stateDir(workspace)
				savePrivateStoreValue(t, base, kind, "PRIVATE_SOURCE", private)
				storePath := filepath.Join(base, "secrets.yml")
				beforeStore, err := os.ReadFile(storePath)
				if err != nil {
					t.Fatal(err)
				}

				configPath := workspaceConfigPath(workspace)
				statePath := managedConfigStatePath(base)
				var beforeConfig, beforeState []byte
				if existingManagedConfig {
					beforeConfig = []byte("modules:\n  demo:\n    config:\n      target: before\n      stale: 1\nglobal:\n  base_domain: nas.test\n  email: admin@nas.test\n")
					if err := os.WriteFile(configPath, beforeConfig, 0o600); err != nil {
						t.Fatal(err)
					}
					if err := writeManagedConfigState(workspace, "test"); err != nil {
						t.Fatal(err)
					}
					beforeState, err = os.ReadFile(statePath)
					if err != nil {
						t.Fatal(err)
					}
				}

				source := filepath.Join(t.TempDir(), "source.yml")
				if err := os.WriteFile(source, []byte("modules:\n  demo:\n    config:\n      target: after\n      stale: 0\nglobal:\n  base_domain: nas.test\n  email: admin@nas.test\n"), 0o600); err != nil {
					t.Fatal(err)
				}
				_, err = importConfigIntoWorkspace(workspace, source, reg)
				assertPrivateSchemaError(t, err, private)

				afterStore, readErr := os.ReadFile(storePath)
				if readErr != nil {
					t.Fatal(readErr)
				}
				if string(afterStore) != string(beforeStore) {
					t.Fatal("rejected import changed the Secret Store")
				}
				if existingManagedConfig {
					afterConfig, readErr := os.ReadFile(configPath)
					if readErr != nil {
						t.Fatal(readErr)
					}
					afterState, readErr := os.ReadFile(statePath)
					if readErr != nil {
						t.Fatal(readErr)
					}
					if string(afterConfig) != string(beforeConfig) || string(afterState) != string(beforeState) {
						t.Fatal("rejected reimport changed managed config state")
					}
				} else if exists(configPath) || exists(statePath) {
					t.Fatal("rejected initial import created managed config state")
				}
			})
		}
	}
}

type deploymentSchemaFixture struct {
	workspace, base, configPath, moduleRoot, lockPath string
}

func newDeploymentSchemaFixture(t *testing.T, configBlock, secretKind, secretKey, secretValue string) deploymentSchemaFixture {
	t.Helper()
	bundle := t.TempDir()
	moduleRoot := filepath.Join(bundle, "modules")
	moduleDir := filepath.Join(moduleRoot, "demo")
	if err := os.MkdirAll(moduleDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(bundle, "contracts"), 0o700); err != nil {
		t.Fatal(err)
	}
	manifest := `api_version: anas.module/v1
kind: Module
name: demo
version: 1.0.0
revision: 1
status: release
abi:
  supports: [anas.module-hook/v1]
runtime:
  type: builtin
config:
  env_prefix: DEMO
` + configBlock
	if err := os.WriteFile(filepath.Join(moduleDir, "module.yml"), []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}

	workspace := t.TempDir()
	base := stateDir(workspace)
	if err := os.MkdirAll(base, 0o700); err != nil {
		t.Fatal(err)
	}
	configPath := workspaceConfigPath(workspace)
	if err := os.WriteFile(configPath, []byte("modules:\n  demo: {}\nglobal:\n  base_domain: nas.test\n  email: admin@nas.test\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := writeManagedConfigState(workspace, "test"); err != nil {
		t.Fatal(err)
	}
	if secretKind != "" {
		savePrivateStoreValue(t, base, secretKind, secretKey, secretValue)
	}
	lockPath := projectLockPath(configPath)
	if err := saveModuleLockFile(lockPath, &moduleLock{APIVersion: "anas.module-lock/v1", Modules: map[string]moduleLockRecord{}}); err != nil {
		t.Fatal(err)
	}
	return deploymentSchemaFixture{workspace: workspace, base: base, configPath: configPath, moduleRoot: moduleRoot, lockPath: lockPath}
}

func (f deploymentSchemaFixture) run(name string) error {
	switch name {
	case "lock":
		return runLock([]string{"-w", f.workspace, "--root", f.moduleRoot}, false)
	case "plan":
		return runPlan([]string{"-w", f.workspace, "--root", f.moduleRoot}, false)
	case "materialize --update-lock":
		_, err := materializeDeployment(prepareOptions{
			workspace: f.workspace, base: f.base, cfgPath: f.configPath,
			moduleRoot: f.moduleRoot, updateLock: true,
		}, false, false)
		return err
	default:
		return nil
	}
}

func TestDeploymentEntrypointsTaintAllStoreKindsBeforeLockWrites(t *testing.T) {
	const private = "0"
	configBlock := `  types:
    stale:
      kind: int
      constraints:
        minimum: 1
`
	for _, kind := range []string{"generated", "local_admin"} {
		for _, operation := range []string{"lock", "plan", "materialize --update-lock"} {
			t.Run(kind+" "+operation, func(t *testing.T) {
				fixture := newDeploymentSchemaFixture(t, configBlock, kind, "PRIVATE_SOURCE", private)
				// Use a raw env spelling so the fixture exercises the same generic
				// runtime view as structured config without modifying its manifest.
				body := []byte("modules:\n  demo: {}\nenv:\n  DEMO_STALE: 0\nglobal:\n  base_domain: nas.test\n  email: admin@nas.test\n")
				if err := os.WriteFile(fixture.configPath, body, 0o600); err != nil {
					t.Fatal(err)
				}
				if err := writeManagedConfigState(fixture.workspace, "test"); err != nil {
					t.Fatal(err)
				}
				beforeLock, err := os.ReadFile(fixture.lockPath)
				if err != nil {
					t.Fatal(err)
				}
				reg, err := loadRegistryDir(fixture.moduleRoot)
				if err != nil {
					t.Fatal(err)
				}
				cfg, err := config.Load(fixture.configPath)
				if err != nil {
					t.Fatal(err)
				}
				env, owners := configBaseEnvWithRegistry(cfg, reg)
				probe := &app{base: fixture.base, cfg: cfg, reg: reg, env: env, envOwner: owners}
				if err := probe.loadImportedSecrets(); err != nil {
					t.Fatal(err)
				}
				if !probe.sensitiveEnvKeySet()["DEMO_STALE"] {
					t.Fatalf("%s Secret Store value did not taint equal runtime alias; env=%q secrets=%v", kind, env["DEMO_STALE"], probe.secrets.values)
				}

				assertPrivateSchemaError(t, fixture.run(operation), private)
				afterLock, readErr := os.ReadFile(fixture.lockPath)
				if readErr != nil {
					t.Fatal(readErr)
				}
				if string(afterLock) != string(beforeLock) {
					t.Fatalf("%s wrote the lock before rejecting whole-config schema", operation)
				}
			})
		}
	}
}

func TestDeploymentEntrypointsDoNotTreatGeneratedStoreKindsAsRequiredInput(t *testing.T) {
	configBlock := `  input_required: [DEMO_TOKEN]
  types:
    token: string
`
	for _, kind := range []string{"generated", "local_admin"} {
		for _, operation := range []string{"lock", "plan", "materialize --update-lock"} {
			t.Run(kind+" "+operation, func(t *testing.T) {
				fixture := newDeploymentSchemaFixture(t, configBlock, kind, "DEMO_TOKEN", "not-caller-input")
				beforeLock, err := os.ReadFile(fixture.lockPath)
				if err != nil {
					t.Fatal(err)
				}
				err = fixture.run(operation)
				if err == nil || !strings.Contains(err.Error(), "DEMO_TOKEN") {
					t.Fatalf("%s accepted %s Secret Store value as input_required: %v", operation, kind, err)
				}
				if strings.Contains(err.Error(), "not-caller-input") {
					t.Fatalf("%s exposed non-input Secret Store value: %v", operation, err)
				}
				afterLock, readErr := os.ReadFile(fixture.lockPath)
				if readErr != nil {
					t.Fatal(readErr)
				}
				if string(afterLock) != string(beforeLock) {
					t.Fatalf("%s wrote the lock before rejecting input_required", operation)
				}
			})
		}
	}
}
