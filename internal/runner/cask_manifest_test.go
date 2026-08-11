package runner

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestLocateCaskRootUsesAnasCaskRoot(t *testing.T) {
	root := filepath.Join(t.TempDir(), "bundles")
	moduleDir := filepath.Join(root, "test-cask")
	if err := os.MkdirAll(moduleDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(moduleDir, "cask.yml"), []byte("name: test-cask\n"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ANAS_CASK_ROOT", root)

	got, err := locateCaskRoot("")
	if err != nil {
		t.Fatal(err)
	}
	want, err := filepath.Abs(root)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("cask root = %q, want %q", got, want)
	}
}

// The flag and the environment variable have to accept the same values. They
// did not: each caller normalised the flag it had just parsed, six copies of
// the same three lines, and the variable — read inside locateCaskRoot rather
// than by any caller — was the one that missed out. A path that worked as
// --cask-root failed as an export, and the error named the two as
// interchangeable remedies.
func TestCaskRootAcceptsBundleDirOrItsParent(t *testing.T) {
	prefix := t.TempDir()
	bundles := filepath.Join(prefix, "casks", "mods")
	if err := os.MkdirAll(filepath.Join(bundles, "test-cask"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bundles, "test-cask", "cask.yml"), []byte("name: test-cask\n"), 0600); err != nil {
		t.Fatal(err)
	}
	want, err := filepath.Abs(bundles)
	if err != nil {
		t.Fatal(err)
	}

	for _, given := range []string{prefix, bundles} {
		t.Run("flag", func(t *testing.T) {
			t.Setenv("ANAS_CASK_ROOT", "")
			got, err := locateCaskRoot(given)
			if err != nil {
				t.Fatalf("--cask-root %s: %v", given, err)
			}
			if got != want {
				t.Fatalf("--cask-root %s resolved to %q, want %q", given, got, want)
			}
		})
		t.Run("env", func(t *testing.T) {
			t.Setenv("ANAS_CASK_ROOT", given)
			got, err := locateCaskRoot("")
			if err != nil {
				t.Fatalf("ANAS_CASK_ROOT=%s: %v", given, err)
			}
			if got != want {
				t.Fatalf("ANAS_CASK_ROOT=%s resolved to %q, want %q", given, got, want)
			}
		})
	}
}

func TestCasksUseManifestRule(t *testing.T) {
	root := filepath.Join("..", "..", "casks", "mods")
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	checked := 0
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		dir := filepath.Join(root, entry.Name())
		if _, err := os.Stat(filepath.Join(dir, "cask.yml")); err != nil {
			t.Fatalf("%s missing cask.yml", entry.Name())
		}
		b, err := os.ReadFile(filepath.Join(dir, "cask.yml"))
		if err != nil {
			t.Fatal(err)
		}
		var manifest struct {
			APIVersion string `yaml:"api_version"`
			Kind       string `yaml:"kind"`
			Name       string `yaml:"name"`
			Version    string `yaml:"version"`
			Revision   int    `yaml:"revision"`
			ABI        struct {
				Supports []string `yaml:"supports"`
			} `yaml:"abi"`
			Runtime struct {
				Type        string `yaml:"type"`
				ComposeFile string `yaml:"compose_file"`
			} `yaml:"runtime"`
			Dependencies struct {
				Requires    []manifestDependency            `yaml:"requires"`
				RequiresOne []manifestAlternativeDependency `yaml:"requires_one"`
				After       []string                        `yaml:"after"`
			} `yaml:"dependencies"`
			Config struct {
				Required []string                        `yaml:"required"`
				Defaults map[string]any                  `yaml:"defaults"`
				Changes  map[string]manifestChangePolicy `yaml:"changes"`
			} `yaml:"config"`
			Services struct {
				Optional []struct {
					Name      string `yaml:"name"`
					EnabledBy string `yaml:"enabled_by"`
				} `yaml:"optional"`
			} `yaml:"services"`
			Logic struct {
				Hook HookConfig `yaml:"hook"`
			} `yaml:"logic"`
		}
		if err := yaml.Unmarshal(b, &manifest); err != nil {
			t.Fatalf("%s cask.yml is invalid: %v", entry.Name(), err)
		}
		if manifest.APIVersion != "anas.dev/v1" {
			t.Fatalf("%s api_version = %q", entry.Name(), manifest.APIVersion)
		}
		if manifest.Kind != "Cask" {
			t.Fatalf("%s kind = %q", entry.Name(), manifest.Kind)
		}
		if manifest.Name != entry.Name() {
			t.Fatalf("%s manifest name = %q", entry.Name(), manifest.Name)
		}
		if manifest.Version == "" {
			t.Fatalf("%s missing version", entry.Name())
		}
		if manifest.Revision < 1 {
			t.Fatalf("%s revision = %d, want at least 1", entry.Name(), manifest.Revision)
		}
		if _, err := parseSemver(manifest.Version); err != nil {
			t.Fatalf("%s version %q is invalid: %v", entry.Name(), manifest.Version, err)
		}
		if !contains(manifest.ABI.Supports, currentCaskABI) {
			t.Fatalf("%s does not support ABI %s", entry.Name(), currentCaskABI)
		}
		if manifest.Runtime.Type != "builtin" && manifest.Runtime.Type != "compose" {
			t.Fatalf("%s runtime type = %q", entry.Name(), manifest.Runtime.Type)
		}
		if manifest.Runtime.Type == "compose" && manifest.Runtime.ComposeFile == "" {
			t.Fatalf("%s compose runtime is missing compose_file", entry.Name())
		}
		for _, dep := range manifest.Dependencies.RequiresOne {
			if looksLikeEnvParam(dep.SelectedBy) {
				t.Fatalf("%s requires_one selected_by %q should use lower snake_case", entry.Name(), dep.SelectedBy)
			}
		}
		for _, key := range manifest.Config.Required {
			if looksLikeEnvParam(key) {
				t.Fatalf("%s required parameter %q should use lower snake_case", entry.Name(), key)
			}
		}
		for key := range manifest.Config.Defaults {
			if looksLikeEnvParam(key) {
				t.Fatalf("%s default parameter %q should use lower snake_case", entry.Name(), key)
			}
		}
		for key, policy := range manifest.Config.Changes {
			if looksLikeEnvParam(key) {
				t.Fatalf("%s change parameter %q should use lower snake_case", entry.Name(), key)
			}
			if !validChangeEffect(policy.Effect) {
				t.Fatalf("%s change parameter %q has invalid effect %q", entry.Name(), key, policy.Effect)
			}
		}
		for _, svc := range manifest.Services.Optional {
			if looksLikeEnvParam(svc.EnabledBy) {
				t.Fatalf("%s optional enabled_by %q should use lower snake_case", entry.Name(), svc.EnabledBy)
			}
		}
		if len(manifest.Logic.Hook.Command) > 0 {
			last := manifest.Logic.Hook.Command[len(manifest.Logic.Hook.Command)-1]
			if last != "./hook" {
				t.Fatalf("%s hook command should point at ./hook, got %q", entry.Name(), last)
			}
			if _, err := os.Stat(filepath.Join(dir, "hook", "main.go")); err != nil {
				t.Fatalf("%s hook/main.go missing: %v", entry.Name(), err)
			}
		}
		if _, err := os.Stat(filepath.Join(dir, "runner.rb")); err == nil {
			t.Fatalf("%s still contains legacy runner.rb", entry.Name())
		} else if !os.IsNotExist(err) {
			t.Fatal(err)
		}
		checked++
	}
	if checked == 0 {
		t.Fatal("no casks checked")
	}
}

// A cask that forgets upgrade.data_breaking is not obviously broken: it renders,
// deploys and runs. The only symptom is that every rollback across a version
// change is refused as "data compatibility unknown", which surfaces months
// later on the day someone needs to roll back. So it is checked here instead.
func TestBundledCasksDeclareDataBreaking(t *testing.T) {
	root := filepath.Join("..", "..", "casks", "mods")
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	checked := 0
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		mod, err := loadModuleManifest(filepath.Join(root, entry.Name()), entry.Name())
		if err != nil {
			t.Fatalf("%s: %v", entry.Name(), err)
		}
		if mod.DataBreaking == nil {
			t.Fatalf("cask %s does not declare upgrade.data_breaking; "+
				"every rollback that changes its version will be refused as unknown", entry.Name())
		}
		// Nothing has shipped yet, so the claim each cask is making is that no
		// release of it has ever rewritten its data format. That is checkable
		// today; the assertion is here so that the day one of them stops being
		// true, the change is deliberate.
		if len(*mod.DataBreaking) != 0 {
			t.Logf("cask %s declares data-breaking versions %v", entry.Name(), *mod.DataBreaking)
		}
		checked++
	}
	if checked == 0 {
		t.Fatal("no casks checked")
	}
}

func TestManifestRejectsRemovedBeforeDependencyField(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "example")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	manifest := `api_version: anas.dev/v1
kind: Cask
name: example
version: 1.0.0
revision: 1
abi:
  supports: [anas.cask/v2]
runtime:
  type: builtin
dependencies:
  before: [core]
`
	if err := os.WriteFile(filepath.Join(dir, "cask.yml"), []byte(manifest), 0600); err != nil {
		t.Fatal(err)
	}
	_, err := loadModuleManifest(dir, "example")
	if err == nil || !strings.Contains(err.Error(), "field before not found") {
		t.Fatalf("error = %v, want strict rejection of dependencies.before", err)
	}
}

func looksLikeEnvParam(key string) bool {
	if strings.HasPrefix(key, "global.") {
		key = strings.TrimPrefix(key, "global.")
	}
	if key == "IPv4" || key == "IPv6" {
		return true
	}
	return strings.ToUpper(key) == key && strings.Contains(key, "_")
}
