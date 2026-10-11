package main

import (
	"context"
	"encoding/base64"
	"strings"
	"testing"
)

// INCUS-R-165, R-166: only a lease that declares volumes may attach a disk
// beyond its root disk, and only a managed volume; the capacity is added to
// the daemon-enforced disk limit.
func TestProjectConfigOpensManagedDisksOnlyForDeclaredVolumes(t *testing.T) {
	for _, isolation := range []string{"container", "vm"} {
		l := testLease(t, isolation)
		l.Daemon.StoragePoolAccess = true
		plain := projectConfig(l)
		if plain["restricted.devices.disk"] != "block" || plain["features.storage.volumes"] != "" {
			t.Fatalf("%s without volumes: disk=%q features=%q", isolation, plain["restricted.devices.disk"], plain["features.storage.volumes"])
		}
		l.VolumeCount, l.VolumeGiB = 4, 20
		config := projectConfig(l)
		if config["restricted.devices.disk"] != "managed" || config["features.storage.volumes"] != "true" {
			t.Fatalf("%s with volumes: disk=%q features=%q", isolation, config["restricted.devices.disk"], config["features.storage.volumes"])
		}
		if config["restricted.storage-pools.access"] != l.StoragePool {
			t.Fatalf("%s with volumes: pool access = %q", isolation, config["restricted.storage-pools.access"])
		}
		want := map[string]string{"container": "340GiB", "vm": "352160MiB"}[isolation]
		if config["limits.disk"] != want {
			t.Fatalf("%s limits.disk = %q, want %q", isolation, config["limits.disk"], want)
		}
	}
}

// Without restricted.storage-pools.access a managed disk could name any pool
// of the daemon, so ensure refuses before writing anything and inspect never
// reports such a lease ready.
func TestVolumesNeedStoragePoolRestriction(t *testing.T) {
	d := newFakeDaemon(t)
	l := testLease(t, "container")
	l.VolumeCount, l.VolumeGiB = 1, 10
	if _, err := ensure(context.Background(), d.clientFor(t), l); err == nil || !strings.Contains(err.Error(), "storage pool") {
		t.Fatalf("ensure on a daemon without pool restriction = %v", err)
	}
	if len(d.projects) != 0 {
		t.Fatalf("ensure wrote projects before refusing: %v", d.projects)
	}
	config := projectConfig(l)
	if projectFenceEnforced(config, l) {
		t.Fatal("a volume lease on a daemon without pool restriction must not be ready")
	}
}

func TestEnsureAppliesVolumeFence(t *testing.T) {
	d := newFakeDaemon(t)
	d.apiExtensions = append(d.apiExtensions, "projects_restricted_storage_pool_access")
	l := testLease(t, "container")
	l.VolumeCount, l.VolumeGiB = 2, 10
	result, err := ensure(context.Background(), d.clientFor(t), l)
	if err != nil || !result.Ready {
		t.Fatalf("ensure = %+v %v", result, err)
	}
	config := d.projects[l.Sandbox]
	if config["restricted.devices.disk"] != "managed" || config["features.storage.volumes"] != "true" || config["limits.disk"] != "330GiB" {
		t.Fatalf("project = %v", config)
	}
}

func TestLeaseFromEnvReadsOptionalVolumes(t *testing.T) {
	certPEM, _ := selfSigned(t, "consumer")
	base := map[string]string{
		"ANAS_RESOURCE_CONSUMER":           "ai_agent",
		"ANAS_RESOURCE_SANDBOX":            "anas-ai-agent",
		"ANAS_RESOURCE_INSTANCE_PREFIX":    "anas-ag-",
		"ANAS_RESOURCE_MAX_INSTANCES":      "4",
		"ANAS_RESOURCE_CPU":                "4",
		"ANAS_RESOURCE_MEMORY_MIB":         "8192",
		"ANAS_RESOURCE_DISK_GIB":           "40",
		"ANAS_RESOURCE_IMAGE_ALLOWLIST":    strings.Repeat("a", 64),
		"ANAS_RESOURCE_IMAGE_ARCHITECTURE": "amd64",
		"ANAS_RESOURCE_CLIENT_CERT":        base64.StdEncoding.EncodeToString(certPEM),
		"INCUS_STORAGE_POOL":               "default",
	}
	for name, scenario := range map[string]struct {
		count, size string
		wantCount   int
		wantGiB     int
		fails       bool
	}{
		"absent":          {},
		"zero":            {count: "0", size: "0"},
		"declared":        {count: "8", size: "100", wantCount: 8, wantGiB: 100},
		"count only":      {count: "8", fails: true},
		"size only":       {size: "100", fails: true},
		"count too large": {count: "65", size: "100", fails: true},
		"size too large":  {count: "1", size: "8193", fails: true},
	} {
		t.Run(name, func(t *testing.T) {
			for key, value := range base {
				t.Setenv(key, value)
			}
			t.Setenv("ANAS_RESOURCE_VOLUME_COUNT", scenario.count)
			t.Setenv("ANAS_RESOURCE_VOLUME_GIB", scenario.size)
			l, err := leaseFromEnv("container")
			if scenario.fails {
				if err == nil {
					t.Fatal("should be rejected")
				}
				return
			}
			if err != nil || l.VolumeCount != scenario.wantCount || l.VolumeGiB != scenario.wantGiB {
				t.Fatalf("lease = %d/%d %v", l.VolumeCount, l.VolumeGiB, err)
			}
		})
	}
}
