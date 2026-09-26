package main

import (
	"context"
	"slices"
	"strings"
	"testing"
)

// incus60Restrictions is every restriction key accepted by the Incus 6.0 LTS
// daemons in the first-tier distribution table (6.0.0 through 6.0.5, from
// projectConfigKeys in cmd/incusd/api_project.go).
var incus60Restrictions = []string{
	"restricted.backups",
	"restricted.cluster.groups",
	"restricted.cluster.target",
	"restricted.containers.interception",
	"restricted.containers.nesting",
	"restricted.containers.lowlevel",
	"restricted.containers.privilege",
	"restricted.virtual-machines.lowlevel",
	"restricted.devices.unix-char",
	"restricted.devices.unix-block",
	"restricted.devices.unix-hotplug",
	"restricted.devices.infiniband",
	"restricted.devices.gpu",
	"restricted.devices.usb",
	"restricted.devices.pci",
	"restricted.devices.proxy",
	"restricted.devices.nic",
	"restricted.devices.disk",
	"restricted.devices.disk.paths",
	"restricted.idmap.uid",
	"restricted.idmap.gid",
	"restricted.networks.access",
	"restricted.networks.integrations",
	"restricted.networks.uplinks",
	"restricted.networks.subnets",
	"restricted.networks.zones",
	"restricted.snapshots",
}

// INCUS-R-011: ensure merges existing project configuration, so a restriction
// left to restricted=true's default keeps whatever an older project held.
func TestProjectConfigOwnsEveryIncus60Restriction(t *testing.T) {
	for _, isolation := range []string{"container", "vm"} {
		config := projectConfig(testLease(t, isolation))
		for _, key := range incus60Restrictions {
			_, written := config[key]
			if written == slices.Contains(clearedRestrictions, key) {
				t.Errorf("%s: %s must be either written or cleared, not both or neither", isolation, key)
			}
		}
		for key := range config {
			if strings.HasPrefix(key, "restricted.") && !slices.Contains(incus60Restrictions, key) {
				t.Errorf("%s: %s is not accepted by an Incus 6.0 LTS daemon", isolation, key)
			}
		}
	}
}

// INCUS-R-008, INCUS-R-052: the daemon, not the shared client's --vm flag,
// decides which instance type a lease may create.
func TestIsolationTierIsEnforcedByProjectInstanceTypeLimits(t *testing.T) {
	for isolation, want := range map[string][2]string{
		"vm":        {"0", "8"},
		"container": {"8", "0"},
	} {
		config := projectConfig(testLease(t, isolation))
		if config["limits.containers"] != want[0] || config["limits.virtual-machines"] != want[1] {
			t.Errorf("%s tier limits.containers=%q limits.virtual-machines=%q, want %v",
				isolation, config["limits.containers"], config["limits.virtual-machines"], want)
		}
	}
}

func TestEnsureReplacesStaleTierLimitFromAnotherTier(t *testing.T) {
	d := newFakeDaemon(t)
	c := d.clientFor(t)
	container := testLease(t, "container")
	vm := container
	vm.Isolation = "vm"
	if _, err := ensure(context.Background(), c, container); err != nil {
		t.Fatal(err)
	}
	result, err := ensure(context.Background(), c, vm)
	if err != nil || !result.Ready {
		t.Fatalf("ensure after tier change: %+v %v", result, err)
	}
	config := d.projects[vm.Sandbox]
	if config["limits.virtual-machines"] != "8" || config["limits.containers"] != "0" {
		t.Fatalf("tier change kept a stale limit: containers=%q virtual-machines=%q",
			config["limits.containers"], config["limits.virtual-machines"])
	}
}

func TestEnsureTightensEveryLegacyRestrictionOnAdoptedProject(t *testing.T) {
	for _, isolation := range []string{"container", "vm"} {
		t.Run(isolation, func(t *testing.T) {
			d := newFakeDaemon(t)
			l := testLease(t, isolation)
			legacy := map[string]string{
				"features.networks":                  "false",
				"restricted":                         "true",
				"restricted.backups":                 "allow",
				"restricted.cluster.target":          "allow",
				"restricted.containers.interception": "full",
				"restricted.containers.privilege":    "allow",
				"restricted.devices.infiniband":      "allow",
				"restricted.devices.unix-hotplug":    "allow",
				"restricted.snapshots":               "allow",
				"limits.containers":                  "8",
				"limits.virtual-machines":            "8",
				"user.operator.note":                 "preserve-me",
			}
			for _, key := range clearedRestrictions {
				legacy[key] = "0-65535"
			}
			d.projects[l.Sandbox] = legacy
			result, err := ensure(context.Background(), d.clientFor(t), l)
			if err != nil || !result.Ready {
				t.Fatalf("ensure: %+v %v", result, err)
			}
			config := d.projects[l.Sandbox]
			for key, want := range projectConfig(l) {
				if config[key] != want {
					t.Errorf("%s = %q, want %q", key, config[key], want)
				}
			}
			for _, key := range clearedRestrictions {
				if _, present := config[key]; present {
					t.Errorf("%s survived ensure", key)
				}
			}
			if config["user.operator.note"] != "preserve-me" {
				t.Error("ensure discarded unrelated operator configuration")
			}
		})
	}
}

func TestEnsureRefusesUnmanagedRestrictionBeforeAnyWrite(t *testing.T) {
	d := newFakeDaemon(t)
	l := testLease(t, "vm")
	original := map[string]string{
		"features.networks":                   "false",
		"restricted":                          "true",
		"restricted.virtual-machines.nesting": "allow",
		"restricted.storage-pools.access":     "secret-pool-name",
	}
	d.projects[l.Sandbox] = original
	_, err := ensure(context.Background(), d.clientFor(t), l)
	if err == nil || !strings.Contains(err.Error(), "restricted.storage-pools.access, restricted.virtual-machines.nesting") {
		t.Fatalf("expected refusal naming both keys, got %v", err)
	}
	if strings.Contains(err.Error(), "secret-pool-name") || strings.Contains(err.Error(), "allow") {
		t.Fatal("refusal echoed restriction values")
	}
	if len(d.puts) != 0 || len(d.posted) != 0 || len(d.certificates) != 0 {
		t.Fatal("refused project was modified or trusted")
	}
}

func TestInspectRejectsLooseOrUnmanagedRestrictionsWithoutRepair(t *testing.T) {
	for name, mutate := range map[string]func(map[string]string){
		"vm privilege":         func(config map[string]string) { config["restricted.containers.privilege"] = "allow" },
		"interception":         func(config map[string]string) { config["restricted.containers.interception"] = "allow" },
		"idmap range":          func(config map[string]string) { config["restricted.idmap.uid"] = "0-65535" },
		"container tier limit": func(config map[string]string) { delete(config, "limits.containers") },
		"unmanaged key":        func(config map[string]string) { config["restricted.virtual-machines.nesting"] = "block" },
		"image server list":    func(config map[string]string) { config["restricted.images.servers"] = "images.example" },
	} {
		t.Run(name, func(t *testing.T) {
			d := newFakeDaemon(t)
			l := testLease(t, "vm")
			c := d.clientFor(t)
			if _, err := ensure(context.Background(), c, l); err != nil {
				t.Fatal(err)
			}
			mutate(d.projects[l.Sandbox])
			posts, puts := len(d.posted), len(d.puts)
			result, _ := inspect(context.Background(), c, l)
			if !result.Exists || result.Ready {
				t.Fatalf("loose project reported ready: %+v", result)
			}
			if len(d.posted) != posts || len(d.puts) != puts {
				t.Fatal("inspect repaired a project instead of remaining read-only")
			}
		})
	}
}
