package computeimage

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

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
