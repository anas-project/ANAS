package runner

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/anas-project/ANAS/internal/config"
)

func TestManifestEnvPrefixIsUsedForSetImportListAndRuntime(t *testing.T) {
	reg := importTestRegistry(t)
	assertRuntime := func(t *testing.T, path, want string) {
		t.Helper()
		loaded, err := config.Load(path)
		if err != nil {
			t.Fatal(err)
		}
		env, owners := configBaseEnvWithRegistry(loaded, reg)
		if env["TURN_PORT"] != want || owners["TURN_PORT"] != "eturnal" {
			t.Fatalf("manifest-prefixed runtime env = %q owner=%q, want %q/eturnal", env["TURN_PORT"], owners["TURN_PORT"], want)
		}
		if value, exists := env["ETURNAL_PORT"]; !exists || value != "raw-compatible" || owners["ETURNAL_PORT"] != "" {
			t.Fatalf("distinct raw fallback-prefix key = %q present=%v owner=%q", value, exists, owners["ETURNAL_PORT"])
		}
	}

	t.Run("set", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "config.yml")
		if err := os.WriteFile(path, []byte("modules:\n  eturnal: {}\nglobal:\n  base_domain: nas.test\n  email: admin@nas.test\nenv:\n  ETURNAL_PORT: raw-compatible\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := config.SetString(path, []string{"modules", "eturnal", "config", "port"}, "4567"); err != nil {
			t.Fatal(err)
		}
		assertRuntime(t, path, "4567")
		stdout, err := captureRunnerStdout(t, func() error { return reportConfigList(path, reg, "eturnal", true) })
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(stdout, `"env_key": "TURN_PORT"`) || !strings.Contains(stdout, `"value": "4567"`) {
			t.Fatalf("config list did not project manifest prefix/value: %s", stdout)
		}
	})

	t.Run("import", func(t *testing.T) {
		source := filepath.Join(t.TempDir(), "source.yml")
		if err := os.WriteFile(source, []byte("modules:\n  eturnal:\n    config:\n      port: 5678\nglobal:\n  base_domain: nas.test\n  email: admin@nas.test\nenv:\n  ETURNAL_PORT: raw-compatible\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		result, err := normalizeImportedConfig(source, reg)
		if err != nil {
			t.Fatal(err)
		}
		normalized := filepath.Join(t.TempDir(), "config.yml")
		if err := os.WriteFile(normalized, result.Normalized, 0o600); err != nil {
			t.Fatal(err)
		}
		assertRuntime(t, normalized, "5678")
	})
}
