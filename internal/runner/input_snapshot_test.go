package runner

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/anas-project/ANAS/internal/config"
)

func TestWorkspaceDerivedValuesCannotSatisfyInputRequiredAcrossEntrypoints(t *testing.T) {
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
  input_required: [DATA_PATH]
`
	if err := os.WriteFile(filepath.Join(moduleDir, "module.yml"), []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}
	reg, err := loadRegistryDir(moduleRoot)
	if err != nil {
		t.Fatal(err)
	}

	workspace := t.TempDir()
	base := stateDir(workspace)
	if err := os.MkdirAll(base, 0o700); err != nil {
		t.Fatal(err)
	}
	configPath := workspaceConfigPath(workspace)
	configBody := []byte(`modules:
  demo: {}
global:
  base_domain: nas.test
  email: admin@nas.test
`)
	if err := os.WriteFile(configPath, configBody, 0o600); err != nil {
		t.Fatal(err)
	}

	assertMissing := func(t *testing.T, err error) {
		t.Helper()
		if err == nil || !strings.Contains(err.Error(), "DATA_PATH") {
			t.Fatalf("input_required error = %v, want missing DATA_PATH", err)
		}
	}

	t.Run("config plan", func(t *testing.T) {
		assertMissing(t, reportConfigPlan(workspace, configPath, base, reg, true))
	})

	t.Run("legacy apply pipeline", func(t *testing.T) {
		a := &app{
			base: base, cfgPath: configPath, reg: reg, contracts: map[string]Contract{},
			resolvedBindings: map[string]map[string]string{},
		}
		assertMissing(t, a.execute([]string{"render"}))
	})

	t.Run("deployment materialization", func(t *testing.T) {
		_, err := materializeDeployment(prepareOptions{
			workspace:  workspace,
			base:       base,
			cfgPath:    configPath,
			moduleRoot: moduleRoot,
			updateLock: true,
		}, false, false)
		assertMissing(t, err)
	})

	t.Run("resolver keeps derived value but validates original input view", func(t *testing.T) {
		cfg, err := config.Load(configPath)
		if err != nil {
			t.Fatal(err)
		}
		a := &app{
			workspace: workspace,
			reg:       reg,
			cfg:       cfg,
			env: map[string]string{
				"BASE_DOMAIN": "nas.test",
				"EMAIL":       "admin@nas.test",
			},
			resolvedBindings: map[string]map[string]string{},
		}
		a.applyWorkspaceEnv()
		if strings.TrimSpace(a.env["DATA_PATH"]) == "" {
			t.Fatal("workspace environment did not publish DATA_PATH")
		}
		if _, supplied := a.callerInputEnv["DATA_PATH"]; supplied {
			t.Fatal("workspace-derived DATA_PATH leaked into caller input snapshot")
		}
		_, err = a.resolveOrderWithInputValidation([]string{"demo"})
		assertMissing(t, err)
	})
}
