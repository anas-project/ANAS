package computeimage

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestForgejoRecipeFreezesMirrorsForEveryTarget(t *testing.T) {
	for _, arch := range []string{"amd64", "arm64"} {
		for _, iface := range []string{"incus_container", "incus_vm"} {
			target := Target{Architecture: arch, Interface: iface}
			ordinary, err := ForgejoRunnerRecipe(target)
			if err != nil {
				t.Fatal(err)
			}
			body, err := ForgejoRunnerRecipeWithOptions(target, ForgejoRunnerRecipeOptions{ChineseBuildSpeedup: true})
			if err != nil {
				t.Fatal(err)
			}
			var recipe struct {
				Source struct {
					URL              string
					SkipVerification bool `yaml:"skip_verification"`
				}
				Actions []struct{ Trigger, Action string }
			}
			if err := yaml.Unmarshal(body, &recipe); err != nil {
				t.Fatal(err)
			}
			if bytes.Equal(body, ordinary) || recipe.Source.URL != "https://mirrors.aliyun.com/debian" || recipe.Source.SkipVerification {
				t.Fatal("source choice not frozen with signature verification")
			}
			if len(recipe.Actions) < 2 || recipe.Actions[0].Trigger != "post-unpack" {
				t.Fatal("mirror hook runs after package installation")
			}
			script, err := os.ReadFile("../../modules/forgejo/runner-image/configure-build-mirrors")
			if err != nil || strings.TrimSpace(recipe.Actions[0].Action) != strings.TrimSpace(string(script)) {
				t.Fatal("mirror script differs from release input", err)
			}
			if bytes.Contains(ordinary, []byte("mirrors.aliyun.com")) {
				t.Fatal("default recipe changed mirror")
			}
		}
	}
}

// Run the actual embedded transform against disposable APT files. BSD sed
// takes a separate empty backup suffix; the Linux guest uses GNU sed -i.
func TestForgejoMirrorsRewriteAPTFormatsWithoutChangingTrust(t *testing.T) {
	root := t.TempDir()
	apt := filepath.Join(root, "etc/apt")
	if err := os.MkdirAll(filepath.Join(apt, "sources.list.d"), 0700); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		"sources.list":                  "deb [signed-by=/keys/debian.gpg] http://deb.debian.org/debian trixie main\ndeb https://security.debian.org/debian-security trixie-security main\n",
		"sources.list.d/debian.sources": "Types: deb\nURIs: https://deb.debian.org/debian\nSuites: trixie trixie-updates\nComponents: main\nSigned-By: /keys/debian.gpg\n\nTypes: deb\nURIs: http://security.debian.org/debian-security\nSuites: trixie-security\nComponents: main\nSigned-By: /keys/debian.gpg\n",
		"sources.list.d/custom.list":    "deb https://custom.example/debian stable main\n",
	}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(apt, name), []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	script, err := os.ReadFile("../../modules/forgejo/runner-image/configure-build-mirrors")
	if err != nil {
		t.Fatal(err)
	}
	text := strings.ReplaceAll(string(script), "/etc/apt/", apt+"/")
	if runtime.GOOS == "darwin" {
		text = strings.ReplaceAll(text, "sed -i", "sed -i ''")
	}
	for i := 0; i < 2; i++ {
		cmd := exec.Command("sh", "-eu")
		cmd.Stdin = strings.NewReader(text)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("rewrite: %s: %v", out, err)
		}
		for name, original := range files {
			body, err := os.ReadFile(filepath.Join(apt, name))
			want := strings.NewReplacer("http://deb.debian.org/", "https://mirrors.aliyun.com/", "https://deb.debian.org/", "https://mirrors.aliyun.com/", "https://security.debian.org/", "https://mirrors.aliyun.com/", "http://security.debian.org/", "https://mirrors.aliyun.com/").Replace(original)
			if err != nil || string(body) != want {
				t.Fatalf("source trust/suites drifted: %s", name)
			}
		}
	}
}
