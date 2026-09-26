package main

import (
	"os"
	"strings"
	"testing"
)

func TestRelayUnitProjectsOnlyItsPublicConfiguration(t *testing.T) {
	body, err := os.ReadFile("../../../packaging/systemd/anas-incus-control-relay.service")
	if err != nil {
		t.Fatal(err)
	}
	unit := string(body)
	for _, line := range []string{
		"User=anas-incus-relay", "Group=anas-incus-relay",
		"ExecStart=/usr/local/lib/anas/anas-incus-control-relay --config /run/anas-incus-control-relay.json",
		"BindReadOnlyPaths=/etc/anas/incus-control-relay.json:/run/anas-incus-control-relay.json",
		"RestrictAddressFamilies=AF_INET AF_NETLINK",
		"CapabilityBoundingSet=", "AmbientCapabilities=", "NoNewPrivileges=true",
		"ProtectSystem=strict", "ProtectHome=true", "PrivateDevices=true",
	} {
		if !strings.Contains("\n"+unit, "\n"+line+"\n") {
			t.Errorf("relay packaging lacks exact constraint %q", line)
		}
	}
	for _, line := range strings.Split(unit, "\n") {
		if strings.HasPrefix(line, "BindReadOnlyPaths=") && line != "BindReadOnlyPaths=/etc/anas/incus-control-relay.json:/run/anas-incus-control-relay.json" {
			t.Fatal("relay may not inherit a private directory, credentials or sockets")
		}
		if strings.HasPrefix(line, "BindPaths=") || strings.HasPrefix(line, "ReadWritePaths=") {
			t.Fatal("relay does not need writable host projections")
		}
	}
}

func TestInstallerRetainsProjectedRelayConfiguration(t *testing.T) {
	body, err := os.ReadFile("../../../install.sh")
	if err != nil {
		t.Fatal(err)
	}
	installer := string(body)
	if !strings.Contains(installer, `print "ExecStart=" binary " --config /run/anas-incus-control-relay.json"`) ||
		strings.Contains(installer, `print "ExecStart=" binary " --config /etc/anas/incus-control-relay.json"`) {
		t.Fatal("installer rewrites the relay unit back to a non-traversable private source path")
	}
}
