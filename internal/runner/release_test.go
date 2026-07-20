package runner

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPromoteReleaseKeepsPreviousForRollback(t *testing.T) {
	base := t.TempDir()
	release := filepath.Join(base, "release")
	staging := filepath.Join(base, "tmp")
	if err := os.MkdirAll(release, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(release, "marker"), []byte("old"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(staging, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(staging, "marker"), []byte("new"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := promoteRelease(staging, release); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(release+".previous", "marker"))
	if err != nil || string(b) != "old" {
		t.Fatalf("previous release not preserved: %v %q", err, b)
	}
	b, err = os.ReadFile(filepath.Join(release, "marker"))
	if err != nil || string(b) != "new" {
		t.Fatalf("release not promoted: %v %q", err, b)
	}
}

func TestReleaseModulesOrdersRemovedCasksLast(t *testing.T) {
	release := t.TempDir()
	for _, name := range []string{"traefik", "lego", "removedapp", "core"} {
		dir := filepath.Join(release, name)
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatal(err)
		}
		if name != "core" {
			if err := os.WriteFile(filepath.Join(dir, "docker-compose.yml"), []byte("services: {}\n"), 0644); err != nil {
				t.Fatal(err)
			}
		}
	}
	a := &app{
		order: []string{"core", "lego", "traefik"},
		reg: map[string]Module{
			"core":    {Name: "core", RuntimeType: "builtin"},
			"lego":    {Name: "lego", RuntimeType: "compose", ComposeFile: "docker-compose.yml"},
			"traefik": {Name: "traefik", RuntimeType: "compose", ComposeFile: "docker-compose.yml"},
		},
	}
	modules := a.releaseModules(release)
	want := []string{"lego", "traefik", "removedapp"}
	if len(modules) != len(want) {
		t.Fatalf("modules = %v, want %v", modules, want)
	}
	for i := range want {
		if modules[i] != want[i] {
			t.Fatalf("modules = %v, want %v", modules, want)
		}
	}
}

func TestCaskEnvPrefersRenderedEnvFile(t *testing.T) {
	dir := t.TempDir()
	if err := writeEnv(filepath.Join(dir, ".env"), map[string]string{"KEY": "rendered"}); err != nil {
		t.Fatal(err)
	}
	a := &app{env: map[string]string{"KEY": "memory"}}
	if got := a.caskEnv(dir)["KEY"]; got != "rendered" {
		t.Fatalf("caskEnv = %q, want rendered value", got)
	}
	if got := a.caskEnv(filepath.Join(dir, "missing"))["KEY"]; got != "memory" {
		t.Fatalf("caskEnv fallback = %q, want in-memory value", got)
	}
}

func TestRenderERBStrictness(t *testing.T) {
	env := map[string]string{"SET_KEY": "value", "FLAG": "true", "EMPTY": ""}
	out, err := renderERB(`a=<%= envs["SET_KEY"] %> e=<%= envs['EMPTY'] %>`, env)
	if err != nil || out != "a=value e=" {
		t.Fatalf("render = %q, %v", out, err)
	}
	// A guarded block referencing missing keys disappears without error.
	out, err = renderERB(`<% if envs['FLAG'] == 'false' %>x=<%= envs["ABSENT"] %><% end %>ok`, env)
	if err != nil || out != "ok" {
		t.Fatalf("guarded render = %q, %v", out, err)
	}
	// An unguarded reference to an absent key fails.
	if _, err := renderERB(`x=<%= envs["ABSENT"] %>`, env); err == nil {
		t.Fatal("expected missing-key error")
	}
	// Unsupported leftover markers fail.
	if _, err := renderERB(`<% weird ruby %>`, env); err == nil {
		t.Fatal("expected unrendered-marker error")
	}
}
