package main

import (
	"context"
	"strings"
	"testing"
)

func TestEnsureRejectsUnsupportedQuotaStorageBeforeMutations(t *testing.T) {
	for _, tier := range []string{"container", "vm"} {
		for _, tc := range []struct {
			name string
			pool *storagePool
		}{
			{"missing", nil},
			{"dir", &storagePool{Name: "default", Driver: "dir", Status: "Created"}},
			{"unknown_driver", &storagePool{Name: "default", Driver: "future", Status: "Created"}},
			{"wrong_pool", &storagePool{Name: "other", Driver: "btrfs", Status: "Created"}},
			{"pending", &storagePool{Name: "default", Driver: "btrfs", Status: "Pending"}},
			{"unavailable", &storagePool{Name: "default", Driver: "zfs", Status: "Unavailable"}},
		} {
			t.Run(tier+"/"+tc.name, func(t *testing.T) {
				d := newFakeDaemon(t)
				d.storage = tc.pool
				l := testLease(t, tier)
				// Existing trust/project must remain untouched on a failed preflight.
				d.projects[l.Sandbox] = map[string]string{"user.keep": "yes"}
				d.certificates["existing"] = certificate{Restricted: true, Projects: []string{l.Sandbox}}
				result, err := ensure(context.Background(), d.clientFor(t), l)
				if err == nil || !strings.Contains(err.Error(), "INCUS_STORAGE_POOL") || result.Ready || result.QuotaEnforced {
					t.Fatalf("result=%+v err=%v", result, err)
				}
				if len(d.posted) != 0 || len(d.puts) != 0 || len(d.networks) != 0 || len(d.profiles) != 0 || len(d.certificates) != 1 || d.projects[l.Sandbox]["user.keep"] != "yes" {
					t.Fatal("unsupported storage changed lease resources")
				}
			})
		}
	}
}

func TestInspectStorageDriftDoesNotClaimQuotaEnforcement(t *testing.T) {
	d := newFakeDaemon(t)
	l := testLease(t, "container")
	c := d.clientFor(t)
	if _, err := ensure(context.Background(), c, l); err != nil {
		t.Fatal(err)
	}
	d.storage.Driver = "dir"
	before := len(d.posted) + len(d.puts)
	result, err := inspect(context.Background(), c, l)
	if err != nil || !result.Exists || !result.Restricted || result.Ready || result.QuotaEnforced {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	if len(d.posted)+len(d.puts) != before {
		t.Fatal("inspect mutated resources")
	}
	d.storage = nil
	result, err = inspect(context.Background(), c, l)
	if err != nil || result.QuotaEnforced || result.Ready {
		t.Fatalf("missing pool: %+v %v", result, err)
	}
}

func TestEnsureRechecksQuotaStorageBeforeTrust(t *testing.T) {
	d := newFakeDaemon(t)
	l := testLease(t, "container")
	d.storageReadFilter = func(p *storagePool) {
		if d.storageReads > 1 {
			p.Driver = "dir"
		}
	}
	_, err := ensure(context.Background(), d.clientFor(t), l)
	if err == nil || !strings.Contains(err.Error(), "has no enforced quota") {
		t.Fatalf("expected storage readback refusal: %v", err)
	}
	if len(d.certificates) != 0 || len(d.networks) != 0 {
		t.Fatal("storage drift must fail before network/trust")
	}
}

func TestEnsureAdmitsNativeQuotaStorage(t *testing.T) {
	for _, driver := range []string{"btrfs", "zfs"} {
		for _, tier := range []string{"container", "vm"} {
			t.Run(driver+"/"+tier, func(t *testing.T) {
				d := newFakeDaemon(t)
				d.storage.Driver = driver
				result, err := ensure(context.Background(), d.clientFor(t), testLease(t, tier))
				if err != nil || !result.Ready || !result.QuotaEnforced || d.storageReads < 2 {
					t.Fatalf("result=%+v reads=%d err=%v", result, d.storageReads, err)
				}
			})
		}
	}
}

func TestStorageReadFailureIsNotReportedAsMissingOrReady(t *testing.T) {
	d := newFakeDaemon(t)
	l := testLease(t, "container")
	c := d.clientFor(t)
	d.projects[l.Sandbox] = projectConfig(l)
	d.storageError = 503
	if _, err := ensure(context.Background(), c, l); err == nil || !strings.Contains(err.Error(), "read INCUS_STORAGE_POOL") {
		t.Fatalf("ensure error: %v", err)
	}
	if result, err := inspect(context.Background(), c, l); err == nil || result.Ready || !strings.Contains(err.Error(), "read INCUS_STORAGE_POOL") {
		t.Fatalf("inspect: %+v %v", result, err)
	}
	if len(d.posted) != 0 || len(d.puts) != 0 || len(d.certificates) != 0 {
		t.Fatal("storage read failure mutated resources")
	}
}
