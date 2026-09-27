package main

import (
	"context"
	stdnet "net"
	"slices"
	"strings"
	"testing"

	"github.com/anas-project/ANAS/internal/computeclient"
)

func leaseACLSources(t *testing.T, d *fakeDaemon, l lease) []string {
	t.Helper()
	acl, ok := d.acls[computeclient.NetworkName(l.Sandbox)]
	if !ok || len(acl.Egress) != 1 {
		t.Fatalf("lease has no single-rule source fence ACL: %+v", acl)
	}
	sources := strings.Split(acl.Egress[0].Source, ",")
	slices.Sort(sources)
	return sources
}

// INCUS-R-044: a forged off-subnet source must not leave the bridge unNATed.
// The ACL allows exactly the bridge's own subnets and drops the rest.
func TestLeaseBridgeCarriesSourceFenceACL(t *testing.T) {
	for name, ipv6 := range map[string]bool{"ipv6 on": true, "ipv6 off": false} {
		t.Run(name, func(t *testing.T) {
			d := newFakeDaemon(t)
			l := testLease(t, "container")
			l.NetworkIPv6 = ipv6
			if result, err := ensure(context.Background(), d.clientFor(t), l); err != nil || !result.Ready {
				t.Fatalf("ensure = %+v, %v", result, err)
			}
			bridge := computeclient.NetworkName(l.Sandbox)
			n := d.networks[bridge]
			if n.Config["security.acls"] != bridge || n.Config["security.acls.default.egress.action"] != "drop" ||
				n.Config["security.acls.default.ingress.action"] != "allow" {
				t.Fatalf("bridge ACL attachment = %v", n.Config)
			}
			var want []string
			for _, family := range []string{"ipv4", "ipv6"} {
				if value := n.Config[family+".address"]; value != "none" && value != "" {
					_, prefix, err := stdnet.ParseCIDR(value)
					if err != nil {
						t.Fatal(err)
					}
					want = append(want, prefix.String())
				}
			}
			slices.Sort(want)
			if got := leaseACLSources(t, d, l); !slices.Equal(got, want) {
				t.Fatalf("ACL sources = %v, want the bridge subnets %v", got, want)
			}
			if len(want) != map[bool]int{true: 2, false: 1}[ipv6] {
				t.Fatalf("subnets = %v for ipv6=%v", want, ipv6)
			}
			acl := d.acls[bridge]
			if acl.Config["user.anas.consumer"] != l.Consumer || acl.Config["user.anas.sandbox"] != l.Sandbox || acl.Config[leaseCredentialKey] != l.Credential {
				t.Fatalf("ACL ownership = %v", acl.Config)
			}
		})
	}
}

func TestSourceFenceDriftBlocksInspectAndEnsureRepairs(t *testing.T) {
	for name, drift := range map[string]func(*fakeDaemon, string){
		"widened source": func(d *fakeDaemon, b string) { a := d.acls[b]; a.Egress[0].Source += ",::/0"; d.acls[b] = a },
		"extra egress rule": func(d *fakeDaemon, b string) {
			a := d.acls[b]
			a.Egress = append(a.Egress, aclRule{Action: "allow", State: "enabled"})
			d.acls[b] = a
		},
		"ingress rule": func(d *fakeDaemon, b string) {
			a := d.acls[b]
			a.Ingress = []aclRule{{Action: "drop", State: "enabled"}}
			d.acls[b] = a
		},
		"disabled rule":        func(d *fakeDaemon, b string) { a := d.acls[b]; a.Egress[0].State = "disabled"; d.acls[b] = a },
		"detached from bridge": func(d *fakeDaemon, b string) { delete(d.networks[b].Config, "security.acls") },
		"egress default allow": func(d *fakeDaemon, b string) { d.networks[b].Config["security.acls.default.egress.action"] = "allow" },
		"ingress default drop": func(d *fakeDaemon, b string) { d.networks[b].Config["security.acls.default.ingress.action"] = "drop" },
		"ACL deleted":          func(d *fakeDaemon, b string) { delete(d.acls, b); delete(d.networks[b].Config, "security.acls") },
	} {
		t.Run(name, func(t *testing.T) {
			d := newFakeDaemon(t)
			c := d.clientFor(t)
			l := testLease(t, "vm")
			l.NetworkIPv6 = true
			if _, err := ensure(context.Background(), c, l); err != nil {
				t.Fatal(err)
			}
			bridge := computeclient.NetworkName(l.Sandbox)
			drift(d, bridge)
			if result, err := inspect(context.Background(), c, l); err != nil || result.Ready {
				t.Fatalf("drifted source fence reported %+v, %v", result, err)
			}
			if result, err := ensure(context.Background(), c, l); err != nil || !result.Ready {
				t.Fatalf("ensure did not repair the owned source fence: %+v, %v", result, err)
			}
		})
	}
}

func TestForeignSourceFenceACLIsNotAdopted(t *testing.T) {
	for name, config := range map[string]map[string]string{
		"unmarked":           {},
		"other consumer":     {"user.anas.consumer": "other", "user.anas.sandbox": "anas-forgejo-runners"},
		"other lease secret": {"user.anas.consumer": "forgejo", "user.anas.sandbox": "anas-forgejo-runners", leaseCredentialKey: strings.Repeat("0", 64)},
	} {
		t.Run(name, func(t *testing.T) {
			d := newFakeDaemon(t)
			l := testLease(t, "container")
			bridge := computeclient.NetworkName(l.Sandbox)
			foreign := networkACL{Name: bridge, Egress: []aclRule{{Action: "allow", Source: "0.0.0.0/0", State: "enabled"}}, Ingress: []aclRule{}, Config: config}
			d.acls[bridge] = foreign
			if _, err := ensure(context.Background(), d.clientFor(t), l); err == nil {
				t.Fatal("ensure adopted an ACL that belongs to someone else")
			}
			if got := d.acls[bridge]; got.Egress[0].Source != "0.0.0.0/0" {
				t.Fatal("ensure rewrote a foreign ACL")
			}
			if d.networks[bridge].Config["security.acls"] != "" {
				t.Fatal("ensure attached a foreign ACL to the lease bridge")
			}
			if _, present := d.certificates[l.Credential]; present {
				t.Fatal("certificate registered before the source fence was proven")
			}
		})
	}
}

func TestSourceFenceFollowsIPv6PostureChange(t *testing.T) {
	d := newFakeDaemon(t)
	c := d.clientFor(t)
	l := testLease(t, "container")
	l.NetworkIPv6 = true
	if _, err := ensure(context.Background(), c, l); err != nil {
		t.Fatal(err)
	}
	if got := leaseACLSources(t, d, l); len(got) != 2 {
		t.Fatalf("IPv6 lease sources = %v", got)
	}
	l.NetworkIPv6 = false
	if result, err := ensure(context.Background(), c, l); err != nil || !result.Ready {
		t.Fatalf("ensure after turning IPv6 off = %+v, %v", result, err)
	}
	got := leaseACLSources(t, d, l)
	if len(got) != 1 || strings.Contains(got[0], ":") {
		t.Fatalf("IPv6-off lease still allows IPv6 sources: %v", got)
	}
}
