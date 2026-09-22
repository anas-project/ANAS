package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestRunnerImageUsesOneJobRootlessPodmanDefaults(t *testing.T) {
	root := filepath.Join("..", "runner-image")
	body, err := os.ReadFile(filepath.Join(root, "config.yml"))
	if err != nil {
		t.Fatal(err)
	}
	var config struct {
		Runner struct {
			Capacity int `yaml:"capacity"`
		} `yaml:"runner"`
		Container struct {
			Privileged   bool     `yaml:"privileged"`
			Options      string   `yaml:"options"`
			ValidVolumes []string `yaml:"valid_volumes"`
			DockerHost   string   `yaml:"docker_host"`
		} `yaml:"container"`
	}
	if err := yaml.Unmarshal(body, &config); err != nil {
		t.Fatal(err)
	}
	if config.Runner.Capacity != 1 || config.Container.Privileged || len(config.Container.ValidVolumes) != 0 {
		t.Fatalf("unsafe Runner defaults: %+v", config)
	}
	for _, required := range []string{"--cpus=", "--memory=", "--pids-limit=", "no-new-privileges"} {
		if !strings.Contains(config.Container.Options, required) {
			t.Errorf("container options are missing %s", required)
		}
	}
	if config.Container.DockerHost != "unix:///run/anas-podman/podman.sock" {
		t.Fatalf("Docker host = %q", config.Container.DockerHost)
	}

	start, err := os.ReadFile(filepath.Join(root, "anas-forgejo-runner-start"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(start)
	for _, required := range []string{"one-job", "--handle", "--wait", "--token-url", "dd of=\"$token_file\"", "systemd-run", "--no-block"} {
		if !strings.Contains(text, required) {
			t.Errorf("Runner starter is missing %q", required)
		}
	}
	for _, forbidden := range []string{"forgejo-runner daemon", ":host", "--privileged"} {
		if strings.Contains(text, forbidden) {
			t.Errorf("Runner starter contains forbidden mode %q", forbidden)
		}
	}
}

func TestRunnerEngineLocalSocketDoesNotWaitForNetworkOnline(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("..", "runner-image", "anas-podman.service"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(body)
	if strings.Contains(text, "network-online.target") || strings.Contains(text, "wait-online") {
		t.Fatal("local engine socket is gated on guest network-online readiness")
	}
	for _, unchanged := range []string{"After=anas-podman.socket", "Delegate=true", "WantedBy=default.target"} {
		if !strings.Contains(text, unchanged) {
			t.Errorf("missing engine boundary: %s", unchanged)
		}
	}
}

func TestRunnerEngineSocketOwnsExplicitSharedGroupPermissions(t *testing.T) {
	root := filepath.Join("..", "runner-image")
	unit, err := os.ReadFile(filepath.Join(root, "anas-podman.service"))
	if err != nil {
		t.Fatal(err)
	}
	socket, err := os.ReadFile(filepath.Join(root, "anas-podman.socket"))
	if err != nil {
		t.Fatal("explicit socket activation unit required", err)
	}
	dirs, err := os.ReadFile(filepath.Join(root, "anas-podman.conf"))
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{"ListenStream=/run/anas-podman/podman.sock", "SocketMode=0660", "RemoveOnStop=true"} {
		if !strings.Contains(string(socket), required) {
			t.Errorf("socket missing %s", required)
		}
	}
	if !strings.Contains(string(unit), "Requires=anas-podman.socket") || !strings.Contains(string(unit), "Delegate=true") || strings.Contains(string(unit), "unix:///run/anas-podman/podman.sock") || strings.Contains(string(unit), "RuntimeDirectory=") {
		t.Fatal("service must inherit the managed socket, not create or unlink its own 0600 socket")
	}
	for _, required := range []string{"d /run/anas-podman 0770 runner-engine actions-engine -", "d /run/anas-podman/xdg 0700 runner-engine actions-engine -"} {
		if !strings.Contains(string(dirs), required) {
			t.Errorf("missing fixed runtime directory %s", required)
		}
	}
}

func TestRunnerEngineAccountPrimaryGroupMatchesServiceIdentity(t *testing.T) {
	root := filepath.Join("..", "runner-image")
	unit, err := os.ReadFile(filepath.Join(root, "anas-podman.service"))
	if err != nil {
		t.Fatal(err)
	}
	provision, err := os.ReadFile(filepath.Join(root, "provision.sh"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(unit), "\nUser=") || strings.Contains(string(unit), "\nGroup=") || strings.Contains(string(unit), "SupplementaryGroups=") {
		t.Fatal("user service must inherit the engine's declared identity without group overrides")
	}
	if !strings.Contains(string(provision), "useradd --uid 1002 --gid actions-engine --no-user-group") {
		t.Fatal("provisioned engine primary group differs from the service's real GID")
	}
}

func TestRunnerEngineRootlessDNSHasARealUserSession(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("..", "runner-image", "anas-podman.service"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Environment=XDG_RUNTIME_DIR=/run/user/1002", "Environment=DBUS_SESSION_BUS_ADDRESS=unix:path=/run/user/1002/bus"} {
		if !strings.Contains(string(body), want) {
			t.Errorf("missing rootless session boundary %s", want)
		}
	}
}

func TestRunnerEngineKeepsOCITasksWithinItsDelegatedCgroup(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("..", "runner-image", "anas-podman.service"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(body)
	for _, want := range []string{"ExecStart=/usr/bin/podman --cgroup-manager=systemd system service --time=0", "Delegate=true", "WantedBy=default.target"} {
		if !strings.Contains(text, want) {
			t.Errorf("missing delegated OCI boundary %s", want)
		}
	}
	for _, forbidden := range []string{"--cgroups=disabled", "--privileged", "ProtectControlGroups=false"} {
		if strings.Contains(text, forbidden) {
			t.Errorf("cgroup recovery bypassed isolation: %s", forbidden)
		}
	}
}
