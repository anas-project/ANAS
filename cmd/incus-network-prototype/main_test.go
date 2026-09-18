package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func fixture(t *testing.T) observation {
	t.Helper()
	body, err := os.ReadFile("../../test-env/fixtures/incus-network-prototype/observation.json")
	if err != nil {
		t.Fatal(err)
	}
	var o observation
	if err := json.Unmarshal(body, &o); err != nil {
		t.Fatal(err)
	}
	return o
}

func TestPrototypeBindsOriginTargetAndRevocation(t *testing.T) {
	for _, iface := range []string{"incus_container", "incus_vm"} {
		o := fixture(t)
		o.Interface = iface
		p, err := generate(o)
		if err != nil {
			t.Fatal(err)
		}
		for _, required := range []string{`iifname != "vethlab" drop`, `ip daddr . tcp dport @http_backend`, `ip saddr . tcp sport @http_backend`, `timeout 30s`, `priority -210`, `hook input priority -200`} {
			if !strings.Contains(p.Firewall, required) {
				t.Fatalf("missing %s", required)
			}
		}
		if p.AddRoute[4] != "10.231.1.2/32" || p.AddRoute[len(p.AddRoute)-1] != o.TraefikIP {
			t.Fatalf("route lacks precise target/source: %v", p.AddRoute)
		}
		if !strings.Contains(p.RouteEnv, "http://10.231.1.2:7000") || strings.Contains(p.RouteEnv, "MIDDLEWARES") {
			t.Fatal("unexpected route")
		}
		if p.Conntrack[0] != "conntrack" || !strings.Contains(strings.Join(p.Revoke, " "), "http_backend") {
			t.Fatal("revocation incomplete")
		}
	}
}

func TestPrototypeRejectsUnverifiedOrUnsafeTargets(t *testing.T) {
	for name, mutate := range map[string]func(*observation){
		"wrong project":   func(o *observation) { o.InstanceProject = "other" },
		"wrong owner":     func(o *observation) { o.NetworkOwner = "other" },
		"stopped":         func(o *observation) { o.State = "Stopped" },
		"reassigned ip":   func(o *observation) { o.AllocationIP = "10.231.1.3" },
		"wrong nic":       func(o *observation) { o.AllocationMAC = "00:16:3e:01:02:04" },
		"unapproved port": func(o *observation) { o.GuestPort = 22 },
		"overlap":         func(o *observation) { o.IngressSubnet = o.GuestSubnet },
		"IPv6 backend":    func(o *observation) { o.GuestIP = "fd00::1" },
		"loopback":        func(o *observation) { o.GuestIP = "127.0.0.1"; o.AllocationIP = o.GuestIP },
		"public domain":   func(o *observation) { o.Host = "app.example.com" },
		"injection":       func(o *observation) { o.TraefikVeth = "veth\"; accept" },
	} {
		t.Run(name, func(t *testing.T) {
			o := fixture(t)
			mutate(&o)
			if _, err := generate(o); err == nil {
				t.Fatal("accepted unsafe observation")
			}
		})
	}
}

func TestPrototypeWritesArtifactsWithoutExecutingHostCommands(t *testing.T) {
	out := filepath.Join(t.TempDir(), "plan")
	if err := run([]string{"--input", "../../test-env/fixtures/incus-network-prototype/observation.json", "--out", out}); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"firewall.nft", "traefik.env", "operations.json"} {
		if _, err := os.Stat(filepath.Join(out, name)); err != nil {
			t.Fatal(err)
		}
	}
	if err := run([]string{"--input", "../../test-env/fixtures/incus-network-prototype/observation.json", "--out", out}); err == nil {
		t.Fatal("overwrote existing artifacts")
	}
}
