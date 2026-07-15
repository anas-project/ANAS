package runner

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/whlsxl/anas/internal/config"
)

func TestPlanDoesNotCreateRuntimeState(t *testing.T) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	root := filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
	base := filepath.Join(t.TempDir(), "runtime")
	if err := Main([]string{"plan", "-c", filepath.Join(root, "config.example.yml"), "--root", root, "-b", base}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(base); !os.IsNotExist(err) {
		t.Fatalf("plan created runtime state at %s", base)
	}
}

func TestDisabledModuleIsExcluded(t *testing.T) {
	disabled := false
	a := &app{
		cfg: &config.File{Services: map[string]config.Service{"app": {Enabled: &disabled}}},
		reg: map[string]Module{"core": {Name: "core"}, "app": {Name: "app"}},
	}
	order, err := a.resolveOrder([]string{"app"})
	if err != nil {
		t.Fatal(err)
	}
	if len(order) != 0 {
		t.Fatalf("order = %v, want no disabled modules", order)
	}
}

func TestDisabledRequiredModuleIsRejected(t *testing.T) {
	disabled := false
	a := &app{
		cfg: &config.File{Services: map[string]config.Service{"database": {Enabled: &disabled}}},
		reg: map[string]Module{
			"core": {Name: "core"}, "app": {Name: "app", Deps: []string{"database"}}, "database": {Name: "database"},
		},
	}
	if _, err := a.resolveOrder([]string{"app"}); err == nil {
		t.Fatal("expected disabled dependency error")
	}
}

func TestWriteEnvIsPrivate(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".env")
	if err := os.WriteFile(path, []byte("old"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := writeEnv(path, map[string]string{"PASSWORD": "secret"}); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0600 {
		t.Fatalf(".env mode = %o, want 600", got)
	}
}

func TestPromoteReleaseReplacesOldTree(t *testing.T) {
	base := t.TempDir()
	release := filepath.Join(base, "release")
	staging := filepath.Join(base, "tmp")
	if err := os.MkdirAll(release, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(release, "stale"), []byte("old"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(staging, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(staging, "current"), []byte("new"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := promoteRelease(staging, release); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(release, "current")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(release, "stale")); !os.IsNotExist(err) {
		t.Fatal("stale release file survived promotion")
	}
}
