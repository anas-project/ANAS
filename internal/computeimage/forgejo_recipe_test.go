package computeimage

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestForgejoRunnerPublicConfigParentsIgnorePrivateBuilderUmask(t *testing.T) {
	for _, arch := range []string{"amd64", "arm64"} {
		for _, iface := range []string{"incus_container", "incus_vm"} {
			body, err := ForgejoRunnerRecipe(Target{Architecture: arch, Interface: iface})
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(body), "install -d -o root -g root -m 0755 /etc/forgejo-runner /usr/local/libexec") {
				t.Errorf("%s/%s public config/entrypoint parents depend on builder umask", arch, iface)
			}
		}
	}
}

func TestForgejoRunnerEngineOwnsAllPrivateConfigParents(t *testing.T) {
	const owned = "install -d -o runner-engine -g actions-engine -m 0700 /home/runner-engine/.config /home/runner-engine/.config/systemd /home/runner-engine/.config/systemd/user /home/runner-engine/.config/systemd/user/sockets.target.wants"
	for _, arch := range []string{"amd64", "arm64"} {
		for _, iface := range []string{"incus_container", "incus_vm"} {
			body, err := ForgejoRunnerRecipe(Target{Architecture: arch, Interface: iface})
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(body), owned) {
				t.Errorf("%s/%s engine config parents remain root-owned", arch, iface)
			}
		}
	}
	provision, err := os.ReadFile(filepath.Join("..", "..", "modules", "forgejo", "runner-image", "provision.sh"))
	if err != nil || !strings.Contains(string(provision), owned) {
		t.Fatal("provisioning must assign every engine config parent", err)
	}
}

func TestForgejoRunnerRecipeEnablesOnlyEngineUserLingering(t *testing.T) {
	for _, arch := range []string{"amd64", "arm64"} {
		for _, iface := range []string{"incus_container", "incus_vm"} {
			body, err := ForgejoRunnerRecipe(Target{Architecture: arch, Interface: iface})
			if err != nil {
				t.Fatal(err)
			}
			for _, want := range []string{"        - dbus-user-session\n", "touch /var/lib/systemd/linger/runner-engine", "chmod 0644 /var/lib/systemd/linger/runner-engine"} {
				if !strings.Contains(string(body), want) {
					t.Errorf("%s/%s missing %s", arch, iface, want)
				}
			}
			if strings.Contains(string(body), "linger/runner-agent") {
				t.Fatal("persistent Runner user session enabled")
			}
		}
	}
}

func TestForgejoRunnerRecipeIsDeterministicAndTargeted(t *testing.T) {
	seen := map[string]string{}
	for _, arch := range []string{"amd64", "arm64"} {
		for _, iface := range []string{"incus_container", "incus_vm"} {
			body, err := ForgejoRunnerRecipe(Target{Architecture: arch, Interface: iface})
			if err != nil {
				t.Fatal(err)
			}
			if again, _ := ForgejoRunnerRecipe(Target{Architecture: arch, Interface: iface}); !bytes.Equal(body, again) {
				t.Fatal("recipe is not deterministic")
			}
			var parsed map[string]any
			if err := yaml.Unmarshal(body, &parsed); err != nil {
				t.Fatalf("invalid yaml for %s/%s: %v", arch, iface, err)
			}
			text := string(body)
			if strings.Contains(text, "alias") || strings.Contains(text, "image import") || strings.Contains(text, "latest") {
				t.Fatalf("recipe contains mutable import vocabulary:\n%s", text)
			}
			if strings.Contains(text, "incus-agent") != (iface == "incus_vm") {
				t.Fatalf("vm-specific files mismatch for %s", iface)
			}
			if strings.Contains(text, "generator: fstab") != (iface == "incus_vm") {
				t.Fatalf("vm fstab generator mismatch for %s", iface)
			}
			if strings.Contains(text, "architecture_map: debian") == false || strings.Contains(text, "systemd-networkd.service") == false {
				t.Fatal("recipe is missing Debian architecture mapping or network bootstrap")
			}
			seen[arch+"/"+iface] = text
		}
	}
	if seen["amd64/incus_container"] == seen["arm64/incus_container"] || seen["amd64/incus_container"] == seen["amd64/incus_vm"] {
		t.Fatal("target-specific recipes collapsed to the same bytes")
	}
}

func TestForgejoRunnerRecipeUsesActualOneJobEntrypoint(t *testing.T) {
	source := filepath.Join("..", "..", "modules", "forgejo", "runner-image")
	entrypoint, err := os.ReadFile(filepath.Join(source, "anas-forgejo-one-job"))
	if err != nil {
		t.Fatal(err)
	}
	body, err := ForgejoRunnerRecipeFromSource(Target{Architecture: "amd64", Interface: "incus_container"}, source)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), indentLiteral(string(entrypoint))) {
		t.Fatal("recipe no longer embeds the actual one-job entrypoint source")
	}
}

func TestForgejoRunnerCopyUsesTheFrozenBuildInputDirectory(t *testing.T) {
	for _, iface := range []string{"incus_container", "incus_vm"} {
		body, err := ForgejoRunnerRecipe(Target{Architecture: "amd64", Interface: iface})
		if err != nil {
			t.Fatal(err)
		}
		var recipe struct {
			Files []struct {
				Generator string `yaml:"generator"`
				Source    string `yaml:"source"`
				Path      string `yaml:"path"`
			} `yaml:"files"`
		}
		if err := yaml.Unmarshal(body, &recipe); err != nil {
			t.Fatal(err)
		}
		found := false
		for _, file := range recipe.Files {
			if file.Path != "/usr/local/bin/forgejo-runner" {
				continue
			}
			found = true
			// BuildOnce stages the measured executable under sources/. The
			// copy generator resolves relative to the recipe working directory;
			// --sources-dir controls distribution tarballs, not this generator.
			if file.Generator != "copy" || file.Source != "sources/forgejo-runner" {
				t.Errorf("%s Runner copy does not reference the frozen build input", iface)
			}
		}
		if !found {
			t.Fatal("Runner executable omitted from recipe")
		}
	}
}

func TestForgejoRunnerResolverIsConfiguredAtGuestBootNotInBuildChroot(t *testing.T) {
	for _, arch := range []string{"amd64", "arm64"} {
		for _, iface := range []string{"incus_container", "incus_vm"} {
			body, err := ForgejoRunnerRecipe(Target{Architecture: arch, Interface: iface})
			if err != nil {
				t.Fatal(err)
			}
			text := string(body)
			if strings.Contains(text, "ln -sf /run/systemd/resolve/resolv.conf /etc/resolv.conf") || !strings.Contains(text, "path: /usr/lib/tmpfiles.d/anas-resolver.conf") || !strings.Contains(text, "L+ /etc/resolv.conf - - - - /run/systemd/resolve/resolv.conf") {
				t.Errorf("%s/%s rewrites the builder resolver or lacks guest boot setup", arch, iface)
			}
			if !strings.Contains(text, "        - systemd-sysv\n") {
				t.Errorf("%s/%s has no explicit systemd init provider", arch, iface)
			}
		}
	}
}

func TestForgejoRunnerRootIsTraversableWithoutChangingPrivateArchiveModes(t *testing.T) {
	for _, arch := range []string{"amd64", "arm64"} {
		for _, iface := range []string{"incus_container", "incus_vm"} {
			body, err := ForgejoRunnerRecipe(Target{Architecture: arch, Interface: iface})
			if err != nil {
				t.Fatal(err)
			}
			var recipe struct {
				Actions []struct {
					Trigger string `yaml:"trigger"`
					Action  string `yaml:"action"`
				} `yaml:"actions"`
			}
			if yaml.Unmarshal(body, &recipe) != nil {
				t.Fatal("invalid generated recipe")
			}
			found := false
			for _, action := range recipe.Actions {
				if action.Trigger == "post-files" && strings.Contains(action.Action, "\nchmod 0755 /\n") {
					found = true
				}
			}
			if !found {
				t.Errorf("%s/%s leaves rootfs mode dependent on the private builder umask", arch, iface)
			}
		}
	}
}

func TestForgejoRunnerEngineUsesItsDeclaredPrimaryGroup(t *testing.T) {
	for _, arch := range []string{"amd64", "arm64"} {
		for _, iface := range []string{"incus_container", "incus_vm"} {
			body, err := ForgejoRunnerRecipe(Target{Architecture: arch, Interface: iface})
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(body), "useradd --uid 1002 --gid actions-engine --no-user-group") || strings.Contains(string(body), "SupplementaryGroups=runner-engine") {
				t.Errorf("%s/%s misaligns passwd GID with the systemd service GID", arch, iface)
			}
		}
	}
}

func TestForgejoRunnerRecipeEmbedsTheReviewedSocketActivationFiles(t *testing.T) {
	root := filepath.Join("..", "..", "modules", "forgejo", "runner-image")
	want := map[string]string{}
	for name, destination := range map[string]string{
		"anas-podman.service":   "/usr/lib/systemd/user/anas-podman.service",
		"anas-podman.socket":    "/usr/lib/systemd/user/anas-podman.socket",
		"anas-podman.conf":      "/usr/lib/tmpfiles.d/anas-podman.conf",
		"anas-engine-user.conf": "/etc/systemd/system/user@1002.service.d/anas-engine.conf",
	} {
		body, err := os.ReadFile(filepath.Join(root, name))
		if err != nil {
			t.Fatal(err)
		}
		want[destination] = strings.TrimRight(string(body), "\n")
	}
	for _, arch := range []string{"amd64", "arm64"} {
		for _, iface := range []string{"incus_container", "incus_vm"} {
			body, err := ForgejoRunnerRecipe(Target{Architecture: arch, Interface: iface})
			if err != nil {
				t.Fatal(err)
			}
			var recipe struct {
				Files []struct {
					Path    string `yaml:"path"`
					Content string `yaml:"content"`
				} `yaml:"files"`
			}
			if yaml.Unmarshal(body, &recipe) != nil {
				t.Fatal("invalid recipe")
			}
			seen := map[string]bool{}
			for _, file := range recipe.Files {
				if expected, exists := want[file.Path]; exists {
					if strings.TrimRight(file.Content, "\n") != expected || seen[file.Path] {
						t.Fatal("engine asset duplicated or differs from reviewed source")
					}
					seen[file.Path] = true
				}
			}
			if len(seen) != len(want) || !strings.Contains(string(body), "ln -s /usr/lib/systemd/user/anas-podman.socket /home/runner-engine/.config/systemd/user/sockets.target.wants/anas-podman.socket") || strings.Contains(string(body), "systemctl enable anas-podman") {
				t.Fatal("engine activation asset missing")
			}
		}
	}
}

func TestForgejoRunnerRecipeIncludesEngineAdmissionWithoutTemplateExpansion(t *testing.T) {
	source, err := os.ReadFile(filepath.Join("..", "..", "modules", "forgejo", "runner-image", "anas-forgejo-runner-start"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(source), "{{") || strings.Contains(string(source), "{%") {
		t.Fatal("guest shell contains distrobuilder template delimiters")
	}
	for _, arch := range []string{"amd64", "arm64"} {
		for _, iface := range []string{"incus_container", "incus_vm"} {
			body, err := ForgejoRunnerRecipe(Target{Architecture: arch, Interface: iface})
			if err != nil {
				t.Fatal(err)
			}
			var recipe struct {
				Files []struct {
					Path    string `yaml:"path"`
					Content string `yaml:"content"`
				} `yaml:"files"`
			}
			if err := yaml.Unmarshal(body, &recipe); err != nil {
				t.Fatal(err)
			}
			found := false
			for _, file := range recipe.Files {
				if file.Path != "/usr/local/libexec/anas-forgejo-runner-start" {
					continue
				}
				found = true
				if strings.TrimSpace(file.Content) != strings.TrimSpace(string(source)) {
					t.Fatal("baked admission code differs from the behavioral test input")
				}
				probe := strings.Index(file.Content, "rootless=$(timeout")
				write := strings.Index(file.Content, "install -d")
				if probe < 0 || write < probe {
					t.Fatal("token effect precedes engine admission")
				}
			}
			if !found {
				t.Fatal("Runner starter missing from target recipe")
			}
		}
	}
}
