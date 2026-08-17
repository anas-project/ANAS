package runner

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/anas-project/ANAS/internal/config"
)

func TestInitImportsConfigAndPersistsCNDefaults(t *testing.T) {
	source := filepath.Join(t.TempDir(), "desired.yml")
	raw := `module_source: cn
modules:
  traefik: {}
global:
  base_domain: nas.test
  email: admin@nas.test
  timezone: Asia/Shanghai
`
	if err := os.WriteFile(source, []byte(raw), 0600); err != nil {
		t.Fatal(err)
	}
	workspace := filepath.Join(t.TempDir(), "workspace")
	stdout, _, exit := capture(t,
		"init", workspace,
		"--config", source,
		"--module-root", repoRoot(t),
		"-y", "--json",
	)
	if exit != 0 {
		t.Fatalf("init exit = %d, stdout = %s", exit, stdout)
	}
	body, err := os.ReadFile(workspaceConfigPath(workspace))
	if err != nil {
		t.Fatal(err)
	}
	text := string(body)
	if !strings.Contains(text, "module_source: official-cn") || !strings.Contains(text, "chinese_speedup: true") {
		t.Fatalf("initialized config did not persist CN defaults:\n%s", text)
	}
	cfg, err := config.Load(workspaceConfigPath(workspace))
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.BaseEnv()["CHINESE_SPEEDUP"]; got != "true" {
		t.Fatalf("CHINESE_SPEEDUP = %q", got)
	}
	if err := validateManagedConfig(workspace, workspaceConfigPath(workspace)); err != nil {
		t.Fatalf("initialized config is not managed: %v", err)
	}
	sourceAfter, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	if string(sourceAfter) != raw {
		t.Fatal("init modified the external config source")
	}
}

func TestInitValidatesConfigBeforeCreatingWorkspace(t *testing.T) {
	source := filepath.Join(t.TempDir(), "invalid.yml")
	if err := os.WriteFile(source, []byte("module_source: nowhere\nmodules:\n  traefik: {}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	workspace := filepath.Join(t.TempDir(), "workspace")
	stdout, _, exit := capture(t,
		"init", workspace,
		"--config", source,
		"--module-root", repoRoot(t),
		"-y", "--json",
	)
	if exit != exitPrecondition {
		t.Fatalf("init exit = %d, want %d; stdout = %s", exit, exitPrecondition, stdout)
	}
	if exists(filepath.Join(workspace, workspaceStateDir)) {
		t.Fatal("invalid config created a workspace before validation")
	}
}

func TestInitUsesInstalledCNSourceForNewWorkspace(t *testing.T) {
	preference := filepath.Join(t.TempDir(), "source")
	if err := os.WriteFile(preference, []byte("official-cn\n"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ANAS_SOURCE_CONFIG", preference)
	workspace := filepath.Join(t.TempDir(), "workspace")
	stdout, _, exit := capture(t, "init", workspace, "-y", "--json")
	if exit != 0 {
		t.Fatalf("init exit = %d, stdout = %s", exit, stdout)
	}
	body, err := os.ReadFile(workspaceConfigPath(workspace))
	if err != nil {
		t.Fatal(err)
	}
	text := string(body)
	if !strings.Contains(text, "module_source: official-cn") || !strings.Contains(text, "chinese_speedup: true") {
		t.Fatalf("installer CN preference was not persisted:\n%s", text)
	}
	cfg, err := config.Load(workspaceConfigPath(workspace))
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.BaseEnv()["ANAS_IMAGE_REGISTRY"]; got != "docker.cnb.cool/anas.dev/anas" {
		t.Fatalf("ANAS_IMAGE_REGISTRY = %q", got)
	}
}
