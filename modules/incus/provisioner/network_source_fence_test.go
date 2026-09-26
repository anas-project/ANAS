package main

import (
	"context"
	"testing"

	"github.com/anas-project/ANAS/internal/computeclient"
)

// A managed bridge or matching subnet does not prove that a guest cannot
// impersonate another source. The host authorizer must also check live devices.
func TestLeaseProfileRequiresSourceFiltering(t *testing.T) {
	for _, isolation := range []string{"container", "vm"} {
		t.Run(isolation, func(t *testing.T) {
			l := testLease(t, isolation)
			p := desiredLeaseProfile(l, computeclient.NetworkName(l.Sandbox))
			for _, key := range []string{"security.mac_filtering", "security.ipv4_filtering", "security.ipv6_filtering"} {
				if p.Devices["eth0"][key] != "true" {
					t.Errorf("lease NIC does not require %s", key)
				}
			}
		})
	}
}

func TestSourceFilterDriftBlocksInspectAndEnsureRepairsOwnedProfile(t *testing.T) {
	for _, key := range []string{"security.mac_filtering", "security.ipv4_filtering", "security.ipv6_filtering"} {
		for _, value := range []string{"", "false"} {
			t.Run(key+"/"+value, func(t *testing.T) {
				d := newFakeDaemon(t)
				l := testLease(t, "container")
				c := d.clientFor(t)
				if _, err := ensure(context.Background(), c, l); err != nil {
					t.Fatal(err)
				}
				p := d.profiles[l.Sandbox+"/"+computeclient.ProfileName]
				if value == "" {
					delete(p.Devices["eth0"], key)
				} else {
					p.Devices["eth0"][key] = value
				}
				result, _ := inspect(context.Background(), c, l)
				if result.Ready {
					t.Fatal("source filtering drift was reported ready")
				}
				if _, err := ensure(context.Background(), c, l); err != nil {
					t.Fatal(err)
				}
				if d.profiles[l.Sandbox+"/"+computeclient.ProfileName].Devices["eth0"][key] != "true" {
					t.Fatal("ensure did not restore the owned source filter")
				}
			})
		}
	}
}
