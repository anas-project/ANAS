package runner

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

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
			ABI        struct {
				Supports []string `yaml:"supports"`
			} `yaml:"abi"`
			Runtime struct {
				Type        string `yaml:"type"`
				ComposeFile string `yaml:"compose_file"`
			} `yaml:"runtime"`
			Config struct {
				Required []string       `yaml:"required"`
				Defaults map[string]any `yaml:"defaults"`
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

func looksLikeEnvParam(key string) bool {
	if strings.HasPrefix(key, "global.") {
		key = strings.TrimPrefix(key, "global.")
	}
	if key == "IPv4" || key == "IPv6" {
		return true
	}
	return strings.ToUpper(key) == key && strings.Contains(key, "_")
}
