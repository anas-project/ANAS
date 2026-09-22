package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunnerEngineRunsInTheDelegatedUserManager(t *testing.T) {
	root := filepath.Join("..", "runner-image")
	unit, err := os.ReadFile(filepath.Join(root, "anas-podman.service"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(unit)
	if !strings.Contains(text, "--cgroup-manager=systemd") || strings.Contains(text, "\nUser=") || strings.Contains(text, "\nGroup=") || !strings.Contains(text, "WantedBy=default.target") {
		t.Fatal("engine is not a user-manager service with systemd cgroup ownership")
	}
	manager, err := os.ReadFile(filepath.Join(root, "anas-engine-user.conf"))
	if err != nil {
		t.Fatal("fixed UID user manager hardening missing", err)
	}
	for _, required := range []string{"After=systemd-tmpfiles-setup.service", "ProtectSystem=strict", "PrivateTmp=true", "ReadWritePaths=/home/runner-engine /run/anas-podman /run/user/1002"} {
		if !strings.Contains(string(manager), required) {
			t.Errorf("user-manager boundary missing %s", required)
		}
	}
	provision, err := os.ReadFile(filepath.Join(root, "provision.sh"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(provision), "/usr/lib/systemd/user/anas-podman.socket") || !strings.Contains(string(provision), "/home/runner-engine/.config/systemd/user/sockets.target.wants") || strings.Contains(string(provision), "--global enable") {
		t.Fatal("socket must be enabled only for the engine account")
	}
}
