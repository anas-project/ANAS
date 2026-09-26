package computeimage

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// Ubuntu's userns mediation also applies inside a stacked AppArmor namespace.
// The outer Incus nesting profile already allows userns; an unprofiled Debian
// guest Podman still gets EACCES when that namespace has no fallback profile.
// Ship only a fixed executable-scoped guest policy, never a host sysctl bypass.
func TestRunnerRecipesFreezeGuestPodmanNamespacePolicy(t *testing.T) {
	source := filepath.Join("..", "..", "modules", "forgejo", "runner-image")
	for _, architecture := range []string{"amd64", "arm64"} {
		for _, isolation := range []string{"incus_container", "incus_vm"} {
			t.Run(architecture+"/"+isolation, func(t *testing.T) {
				body, err := ForgejoRunnerRecipeFromSource(Target{Architecture: architecture, Interface: isolation}, source)
				if err != nil {
					t.Fatal(err)
				}
				var recipe struct {
					Packages struct {
						Sets []struct{ Packages []string } `yaml:"sets"`
					} `yaml:"packages"`
					Files []struct{ Path, Mode, UID, GID, Content string } `yaml:"files"`
				}
				if yaml.Unmarshal(body, &recipe) != nil {
					t.Fatal("invalid recipe")
				}
				parser := false
				for _, set := range recipe.Packages.Sets {
					for _, name := range set.Packages {
						parser = parser || name == "apparmor"
					}
				}
				if !parser {
					t.Error("guest policy cannot be loaded without its official parser package")
				}
				for name, target := range map[string]string{
					"anas-forgejo-podman.apparmor":       "/usr/share/anas/forgejo-runner/podman.apparmor",
					"anas-forgejo-podman-policy.service": "/etc/systemd/system/anas-forgejo-podman-policy.service",
					"anas-apparmor-loader.conf":          "/etc/systemd/system/apparmor.service.d/anas-runner.conf",
				} {
					expected, err := os.ReadFile(filepath.Join(source, name))
					if err != nil {
						t.Fatal(err)
					}
					matches := 0
					for _, file := range recipe.Files {
						if file.Path != target {
							continue
						}
						matches++
						if file.Mode != "0644" || file.UID != "0" || file.GID != "0" || strings.TrimSpace(file.Content) != strings.TrimSpace(string(expected)) {
							t.Fatal("reviewed public guest policy changed during recipe projection")
						}
					}
					if matches != 1 {
						t.Error("missing or duplicated guest policy asset", name)
					}
				}
				for _, forbidden := range []string{"apparmor_restrict_unprivileged_userns=0", "unprivileged_userns_clone=0", "lxc.apparmor.profile=unconfined", "security.privileged=true"} {
					if strings.Contains(string(body), forbidden) {
						t.Fatal("recipe disabled host or container isolation", forbidden)
					}
				}
			})
		}
	}
}

func TestRunnerAppArmorAutoloaderUsesOnlyTheFixedProfile(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("..", "..", "modules", "forgejo", "runner-image", "anas-apparmor-loader.conf"))
	if err != nil {
		t.Fatal("installing the parser package must not enable its permissive userns fallback", err)
	}
	text := string(body)
	command := "/usr/sbin/apparmor_parser --replace --skip-cache /usr/share/anas/forgejo-runner/podman.apparmor"
	for _, required := range []string{
		"ConditionPathExists=/proc/sys/kernel/apparmor_restrict_unprivileged_userns\n",
		"ExecStart=\nExecStart=" + command + "\n",
		"ExecReload=\nExecReload=" + command + "\n",
	} {
		if !strings.Contains(text, required) {
			t.Error("package start/reload must load the same fixed guest policy", required)
		}
	}
	for _, line := range strings.Split(text, "\n") {
		if strings.HasPrefix(line, "#") {
			continue
		}
		for _, forbidden := range []string{"/bin/true", "/dev/null", "apparmor.systemd", "/etc/apparmor.d", "--remove", "sysctl", "ExecStop="} {
			if strings.Contains(line, forbidden) {
				t.Fatal("loader disabled AppArmor, unloaded confinement or reintroduced the distribution-wide policy set")
			}
		}
	}
}

func TestRunnerPolicyAppliesOnlyToPodmanAndPrecedesItsUserManager(t *testing.T) {
	root := filepath.Join("..", "..", "modules", "forgejo", "runner-image")
	read := func(name string) string {
		t.Helper()
		body, err := os.ReadFile(filepath.Join(root, name))
		if err != nil {
			t.Fatal(err)
		}
		return string(body)
	}
	policy := read("anas-forgejo-podman.apparmor")
	if !strings.Contains(policy, "profile anas-forgejo-podman /usr/bin/podman flags=(unconfined) {") ||
		!strings.Contains(policy, "\n  userns,\n") || strings.Count(policy, "profile ") != 1 || strings.Contains(policy, "*") {
		t.Fatal("guest policy gained a broad executable attachment or lost its namespace permission")
	}
	unit := read("anas-forgejo-podman-policy.service")
	for _, line := range []string{"ConditionSecurity=apparmor", "ConditionPathExists=/proc/sys/kernel/apparmor_restrict_unprivileged_userns", "Type=oneshot", "RemainAfterExit=yes",
		"ExecStart=/usr/sbin/apparmor_parser --replace --skip-cache /usr/share/anas/forgejo-runner/podman.apparmor"} {
		if !strings.Contains("\n"+unit, "\n"+line+"\n") {
			t.Error("missing fixed guest policy service contract", line)
		}
	}
	if strings.Contains(unit, "ExecStart=-") || strings.Contains(unit, "sysctl") || strings.Contains(unit, "--Complain") {
		t.Fatal("failed policy loading is not permission to start the engine")
	}
	manager := read("anas-engine-user.conf")
	if !strings.Contains(manager, "Requires=anas-forgejo-podman-policy.service\n") ||
		!strings.Contains(manager, "After=anas-forgejo-podman-policy.service\n") {
		t.Fatal("engine user manager may start before policy publication or after it fails")
	}
}
