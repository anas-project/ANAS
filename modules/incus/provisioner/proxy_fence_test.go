package main

import (
	"context"
	"testing"
)

// INCUS-R-011: a previously relaxed project must not retain proxy-device
// permission merely because ensure preserves unrelated project settings.
func TestProxyDeviceFenceIsExplicitForBothIsolationTiers(t *testing.T) {
	for _, isolation := range []string{"container", "vm"} {
		t.Run(isolation, func(t *testing.T) {
			l := testLease(t, isolation)
			if got := projectConfig(l)["restricted.devices.proxy"]; got != "block" {
				t.Fatalf("proxy device fence = %q, want explicit block", got)
			}
			for _, value := range []string{"", "allow"} {
				config := projectConfig(l)
				if value == "" {
					delete(config, "restricted.devices.proxy")
				} else {
					config["restricted.devices.proxy"] = value
				}
				if projectFenceEnforced(config, l) {
					t.Fatal("missing or relaxed proxy fence was accepted")
				}
			}
		})
	}
}

func TestEnsureRepairsRelaxedProxyFenceWithoutDroppingUnrelatedSettings(t *testing.T) {
	for _, isolation := range []string{"container", "vm"} {
		t.Run(isolation, func(t *testing.T) {
			d := newFakeDaemon(t)
			l := testLease(t, isolation)
			d.projects[l.Sandbox] = map[string]string{
				"features.networks":        "false",
				"restricted.devices.proxy": "allow",
				"user.operator.note":       "preserve-me",
			}
			result, err := ensure(context.Background(), d.clientFor(t), l)
			if err != nil || !result.Ready {
				t.Fatalf("ensure failed: %+v %v", result, err)
			}
			if d.projects[l.Sandbox]["restricted.devices.proxy"] != "block" {
				t.Fatal("ensure preserved an explicitly relaxed proxy permission")
			}
			if d.projects[l.Sandbox]["user.operator.note"] != "preserve-me" {
				t.Fatal("ensure discarded unrelated operator configuration")
			}
		})
	}
}

func TestInspectRejectsRelaxedProxyFenceWithoutRepairingIt(t *testing.T) {
	d := newFakeDaemon(t)
	l := testLease(t, "container")
	c := d.clientFor(t)
	if _, err := ensure(context.Background(), c, l); err != nil {
		t.Fatal(err)
	}
	d.projects[l.Sandbox]["restricted.devices.proxy"] = "allow"
	posts, puts := len(d.posted), len(d.puts)
	result, _ := inspect(context.Background(), c, l)
	if !result.Exists || result.Ready {
		t.Fatalf("relaxed project reported ready: %+v", result)
	}
	if len(d.posted) != posts || len(d.puts) != puts || d.projects[l.Sandbox]["restricted.devices.proxy"] != "allow" {
		t.Fatal("inspect repaired a project instead of remaining read-only")
	}
}
