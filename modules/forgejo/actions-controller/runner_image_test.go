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
