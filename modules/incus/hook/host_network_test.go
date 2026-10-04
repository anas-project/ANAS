package main

import (
	"os"
	"path/filepath"
	"testing"
)

// INCUS-R-153, R-164: the hook publishes the range the operator approved, or
// why port bindings cannot work on this host.
func TestPortBindingRangeFollowsTheHost(t *testing.T) {
	writePolicy := func(t *testing.T, dir, body string) string {
		t.Helper()
		path := filepath.Join(dir, "network.json")
		if err := os.WriteFile(path, []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	auto := func(t *testing.T, hostd bool, policy string) map[string]string {
		t.Helper()
		bundle := hostConnectionBundleFixture(t)
		bundlePath := writeHostBundleFixture(t, bundle)
		resetHostBundleTestSeam(t, bundlePath)
		dir, err := filepath.EvalSymlinks(filepath.Dir(bundlePath))
		if err != nil {
			t.Fatal(err)
		}
		hostdPolicyTestPath = filepath.Join(dir, "absent-hostd.json")
		if hostd {
			hostdPolicyTestPath = writePolicy(t, t.TempDir(), "{}")
		}
		hostNetworkTestPath = filepath.Join(dir, "absent-network.json")
		if policy != "" {
			hostNetworkTestPath = writePolicy(t, dir, policy)
		}
		t.Cleanup(func() { hostdPolicyTestPath, hostNetworkTestPath = "", "" })
		env := map[string]string{"NETWORK_PREFIX": "anas_"}
		if err := calculateForTest("incus", env, map[string]string{}); err != nil {
			t.Fatal(err)
		}
		return env
	}
	valid := `{"schema":"anas.incus-host-network/v1","port_range":{"first":30000,"last":32767}}`
	if env := auto(t, true, valid); env["INCUS_PORT_BINDING_RANGE"] != "30000-32767" || env["INCUS_PORT_BINDING_BLOCKER"] != "" {
		t.Fatalf("configured host = %q / %q", env["INCUS_PORT_BINDING_RANGE"], env["INCUS_PORT_BINDING_BLOCKER"])
	}
	if env := auto(t, false, valid); env["INCUS_PORT_BINDING_RANGE"] != "" || env["INCUS_PORT_BINDING_BLOCKER"] != "hostd_missing" {
		t.Fatalf("host without hostd = %q / %q", env["INCUS_PORT_BINDING_RANGE"], env["INCUS_PORT_BINDING_BLOCKER"])
	}
	for name, policy := range map[string]string{
		"missing":       "",
		"other schema":  `{"schema":"anas.incus-host-network/v0","port_range":{"first":30000,"last":32767}}`,
		"inverted":      `{"schema":"anas.incus-host-network/v1","port_range":{"first":32767,"last":30000}}`,
		"privileged":    `{"schema":"anas.incus-host-network/v1","port_range":{"first":22,"last":30000}}`,
		"unknown field": `{"schema":"anas.incus-host-network/v1","port_range":{"first":30000,"last":32767},"extra":1}`,
	} {
		t.Run(name, func(t *testing.T) {
			if env := auto(t, true, policy); env["INCUS_PORT_BINDING_RANGE"] != "" || env["INCUS_PORT_BINDING_BLOCKER"] != "host_not_configured" {
				t.Fatalf("got %q / %q", env["INCUS_PORT_BINDING_RANGE"], env["INCUS_PORT_BINDING_BLOCKER"])
			}
		})
	}
	explicit := validEnv(t)
	if err := calculateForTest("incus", explicit, map[string]string{}); err != nil {
		t.Fatal(err)
	}
	if explicit["INCUS_PORT_BINDING_RANGE"] != "" || explicit["INCUS_PORT_BINDING_BLOCKER"] != "remote_daemon" {
		t.Fatalf("explicit remote daemon = %q / %q", explicit["INCUS_PORT_BINDING_RANGE"], explicit["INCUS_PORT_BINDING_BLOCKER"])
	}
}

func TestLANExtraSubnetsAreValidated(t *testing.T) {
	for _, value := range []string{"", "10.20.0.0/16", "10.20.0.0/16, 2001:db8:9::/48"} {
		if err := validateLANExtraSubnets(value); err != nil {
			t.Errorf("%q: %v", value, err)
		}
	}
	for _, value := range []string{"0.0.0.0/0", "::/0", "127.0.0.0/8", "fe80::/10", "224.0.0.0/4", "10.20.0.0", "lan"} {
		if err := validateLANExtraSubnets(value); err == nil {
			t.Errorf("%q accepted", value)
		}
	}
	env := validEnv(t)
	env["INCUS_LAN_EXTRA_SUBNETS"] = "0.0.0.0/0"
	if err := calculateForTest("incus", env, map[string]string{}); err == nil {
		t.Fatal("calculate accepted a default route as a LAN subnet")
	}
}
