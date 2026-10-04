package main

import (
	"context"
	"encoding/base64"
	"net/netip"
	"slices"
	"strings"
	"testing"

	"github.com/anas-project/ANAS/internal/computeclient"
	"github.com/anas-project/ANAS/internal/computenet"
)

func publishedLease(t *testing.T, ipv6 bool) lease {
	t.Helper()
	l := testLease(t, "container")
	l.NetworkIPv6 = ipv6
	l.Network = computenet.Network{
		Egress: computenet.EgressInternet, Ingress: computenet.IngressPublished,
		Slots:     []computenet.Slot{{Name: "db", Instance: "anas-fj-db"}, {Name: "dev", Instance: "anas-fj-dev"}},
		HTTPPorts: []uint16{7000, 8080},
		Ports: []computenet.PortBinding{
			{Protocol: "tcp", HostPort: 30432, Slot: "db", GuestPort: 5432},
			{Protocol: "tcp", HostPort: 30022, Slot: "dev", GuestPort: 22},
			{Protocol: "udp", HostPort: 30053, Slot: "dev", GuestPort: 53},
		},
		TraefikPort: 9000,
	}
	return l
}

// INCUS-R-131, R-133: published admits Traefik to the HTTP ports and any
// non-lease source to the slot ports, and drops every lease source first.
func TestPublishedIngressAdmitsOnlyDeclaredPublications(t *testing.T) {
	d := newFakeDaemon(t)
	withTraefikSet(d)
	l := publishedLease(t, true)
	result := ensureReady(t, d, l)
	acl := d.acls[computeclient.NetworkName(l.Sandbox)]
	var traefik, leases bool
	slotRules := map[string]aclRule{}
	for _, r := range acl.Ingress {
		switch {
		case r.Action == "drop" && r.Source == "$"+leasesAddressSet:
			leases = true
		case r.Action == "allow" && r.Source == "$"+traefikAddressSet:
			traefik = r.Protocol == "tcp" && r.DestinationPort == "7000,8080" && r.Destination == ""
		case r.Action == "allow" && r.Source == "":
			slotRules[r.Destination+"/"+r.Protocol] = r
		default:
			t.Fatalf("unexpected ingress rule %+v", r)
		}
	}
	if !leases || !traefik || len(slotRules) != 3 {
		t.Fatalf("ingress rules = %+v", acl.Ingress)
	}
	if result.Network == nil || len(result.Network.Slots) != 2 {
		t.Fatalf("ensure report = %+v", result.Network)
	}
	for _, slot := range result.Network.Slots {
		if slot.IPv6 == "" {
			t.Fatalf("IPv6 lease slot %s has no fixed IPv6", slot.Name)
		}
		key := slot.IPv4 + "/32," + slot.IPv6 + "/128"
		switch slot.Name {
		case "db":
			if r := slotRules[key+"/tcp"]; r.DestinationPort != "5432" {
				t.Fatalf("db slot rule = %+v", r)
			}
		case "dev":
			if slotRules[key+"/tcp"].DestinationPort != "22" || slotRules[key+"/udp"].DestinationPort != "53" {
				t.Fatalf("dev slot rules = %+v", slotRules)
			}
		}
	}
}

func TestIngressNoneHasNoIngressRules(t *testing.T) {
	d := newFakeDaemon(t)
	l := testLease(t, "container")
	ensureReady(t, d, l)
	if rules := d.acls[computeclient.NetworkName(l.Sandbox)].Ingress; len(rules) != 0 {
		t.Fatalf("ingress none carries %+v", rules)
	}
}

// INCUS-R-155: slot addresses sit outside the DHCP range, survive applies,
// and are released when the slot is no longer declared. The profile carries
// the copy the shared client reads (INCUS-R-156).
func TestSlotsGetStableAddressesOutsideDHCP(t *testing.T) {
	d := newFakeDaemon(t)
	withTraefikSet(d)
	c := d.clientFor(t)
	l := publishedLease(t, true)
	first := ensureReady(t, d, l)
	bridge := computeclient.NetworkName(l.Sandbox)
	n := d.networks[bridge]
	subnet := netip.MustParsePrefix(n.Config["ipv4.address"]).Masked()
	ranges := strings.Split(n.Config["ipv4.dhcp.ranges"], "-")
	last := netip.MustParseAddr(ranges[1])
	if n.Config["ipv6.dhcp.stateful"] != "true" || n.Config["ipv6.dhcp.ranges"] == "" {
		t.Fatalf("IPv6 slots need stateful DHCPv6: %v", n.Config)
	}
	addresses := map[string]slotAddress{}
	for _, slot := range first.Network.Slots {
		addr := netip.MustParseAddr(slot.IPv4)
		if !subnet.Contains(addr) || addr.Compare(last) <= 0 {
			t.Fatalf("slot %s address %s is inside the DHCP range ending %s", slot.Name, addr, last)
		}
		if n.Config[slotKeyPrefix+slot.Name+".ipv4"] != slot.IPv4 || n.Config[slotKeyPrefix+slot.Name+".instance"] != slot.Instance {
			t.Fatalf("bridge does not record slot %s: %v", slot.Name, n.Config)
		}
		p := d.profiles[l.Sandbox+"/"+computeclient.ProfileName]
		if p.Config[slotKeyPrefix+slot.Name+".ipv4"] != slot.IPv4 || p.Config[slotKeyPrefix+slot.Name+".ipv6"] != slot.IPv6 {
			t.Fatalf("profile does not mirror slot %s: %v", slot.Name, p.Config)
		}
		addresses[slot.Name] = slot
	}
	if addresses["db"].IPv4 == addresses["dev"].IPv4 {
		t.Fatal("two slots share an address")
	}
	// A second ensure keeps the addresses.
	second := ensureReady(t, d, l)
	if !slices.Equal(first.Network.Slots, second.Network.Slots) {
		t.Fatalf("slot addresses moved: %+v -> %+v", first.Network.Slots, second.Network.Slots)
	}
	// Dropping db releases it; dev keeps its address; a new slot reuses db's.
	l.Network.Slots = []computenet.Slot{{Name: "dev", Instance: "anas-fj-dev"}, {Name: "web", Instance: "anas-fj-web"}}
	l.Network.Ports = l.Network.Ports[1:]
	third := ensureReady(t, d, l)
	got := map[string]slotAddress{}
	for _, slot := range third.Network.Slots {
		got[slot.Name] = slot
	}
	if got["dev"] != addresses["dev"] {
		t.Fatalf("a kept slot moved: %+v -> %+v", addresses["dev"], got["dev"])
	}
	if got["web"].IPv4 != addresses["db"].IPv4 {
		t.Fatalf("the released address was not reused: %+v", got["web"])
	}
	if _, kept := d.networks[bridge].Config[slotKeyPrefix+"db.ipv4"]; kept {
		t.Fatal("a released slot kept its bridge keys")
	}
	if result, err := inspect(context.Background(), c, l); err != nil || !result.Ready {
		t.Fatalf("inspect after slot change = %+v, %v", result, err)
	}
	// A slot address drifted on the bridge is not ready.
	d.networks[bridge].Config[slotKeyPrefix+"dev.ipv4"] = subnet.Addr().Next().Next().String()
	if result, _ := inspect(context.Background(), c, l); result.Ready {
		t.Fatal("a drifted slot address was reported ready")
	}
}

func TestSlotsWithoutIPv6KeepTheDefaultDHCPv6(t *testing.T) {
	d := newFakeDaemon(t)
	withTraefikSet(d)
	l := publishedLease(t, false)
	result := ensureReady(t, d, l)
	n := d.networks[computeclient.NetworkName(l.Sandbox)]
	if _, set := n.Config["ipv6.dhcp.stateful"]; set {
		t.Fatalf("IPv6-off lease set DHCPv6: %v", n.Config)
	}
	for _, slot := range result.Network.Slots {
		if slot.IPv6 != "" {
			t.Fatalf("IPv6-off slot has an IPv6 address: %+v", slot)
		}
	}
}

// INCUS-R-123: intra_lease turns port isolation off; by default it is on.
func TestIntraLeaseControlsPortIsolation(t *testing.T) {
	for _, intra := range []bool{false, true} {
		d := newFakeDaemon(t)
		l := testLease(t, "container")
		l.Network.IntraLease = intra
		ensureReady(t, d, l)
		got := d.profiles[l.Sandbox+"/"+computeclient.ProfileName].Devices["eth0"]["security.port_isolation"]
		if (got == "true") == intra {
			t.Fatalf("intra_lease=%v gave security.port_isolation=%q", intra, got)
		}
	}
}

// INCUS-R-028, R-127: a lease from before the lease* prefix keeps working
// while its old bridge is in use, and the old bridge goes once it is not.
func TestLegacyBridgeIsRetiredOnceUnused(t *testing.T) {
	for name, inUse := range map[string]bool{"unused": false, "in use": true} {
		t.Run(name, func(t *testing.T) {
			d := newFakeDaemon(t)
			l := testLease(t, "container")
			legacy := computeclient.LegacyNetworkName(l.Sandbox)
			d.networks[legacy] = network{Name: legacy, Type: "bridge", Config: map[string]string{
				"user.anas.consumer": l.Consumer, "user.anas.sandbox": l.Sandbox, "ipv4.address": "10.50.0.1/24", "security.acls": legacy,
			}}
			if inUse {
				d.networks[legacy] = network{Name: legacy, Type: "bridge", Config: d.networks[legacy].Config, UsedBy: []string{"/1.0/instances/anas-fj-old?project=" + l.Sandbox}}
			}
			d.acls[legacy] = networkACL{Name: legacy, Egress: []aclRule{}, Ingress: []aclRule{}, Config: map[string]string{
				"user.anas.consumer": l.Consumer, "user.anas.sandbox": l.Sandbox, leaseCredentialKey: l.Credential,
			}}
			d.projects[l.Sandbox] = map[string]string{"restricted": "true", "features.networks": "false", "restricted.networks.access": legacy}
			ensureReady(t, d, l)
			access := d.projects[l.Sandbox]["restricted.networks.access"]
			_, stillThere := d.networks[legacy]
			if inUse {
				if !stillThere || access != computeclient.NetworkName(l.Sandbox)+","+legacy {
					t.Fatalf("an in-use legacy bridge was dropped: present=%v access=%q", stillThere, access)
				}
				return
			}
			if stillThere || d.acls[legacy].Name != "" || access != computeclient.NetworkName(l.Sandbox) {
				t.Fatalf("an unused legacy bridge survived: present=%v access=%q", stillThere, access)
			}
		})
	}
}

// INCUS-R-124: revoke withdraws trust, closes the network and stops the
// lease's running instances; the project and the instances remain.
func TestRevokeClosesTheNetworkAndStopsInstances(t *testing.T) {
	d := newFakeDaemon(t)
	c := d.clientFor(t)
	l := testLease(t, "container")
	ensureReady(t, d, l)
	d.instances[l.Sandbox] = []instanceRecord{{Name: "anas-fj-job1", Status: "Running"}, {Name: "anas-fj-job2", Status: "Stopped"}}
	if err := revoke(context.Background(), c, l); err != nil {
		t.Fatal(err)
	}
	acl := d.acls[computeclient.NetworkName(l.Sandbox)]
	if len(acl.Egress) != 0 || len(acl.Ingress) != 0 || acl.Config["user.anas.sandbox"] != l.Sandbox {
		t.Fatalf("revoked ACL = %+v", acl)
	}
	if !slices.Equal(d.stopped, []string{l.Sandbox + "/anas-fj-job1"}) {
		t.Fatalf("stopped = %v", d.stopped)
	}
	if _, present := d.certificates[l.Credential]; present {
		t.Fatal("certificate survived revoke")
	}
	if _, present := d.projects[l.Sandbox]; !present || len(d.instances[l.Sandbox]) != 2 {
		t.Fatal("revoke removed the project or its instances")
	}
	if err := revoke(context.Background(), c, l); err != nil {
		t.Fatalf("repeated revoke: %v", err)
	}
}

func TestRevokeLeavesAForeignProjectAlone(t *testing.T) {
	d := newFakeDaemon(t)
	l := testLease(t, "container")
	d.projects[l.Sandbox] = map[string]string{"user.anas.consumer": "other"}
	d.instances[l.Sandbox] = []instanceRecord{{Name: "anas-fj-job1", Status: "Running"}}
	if err := revoke(context.Background(), d.clientFor(t), l); err != nil {
		t.Fatal(err)
	}
	if len(d.stopped) != 0 {
		t.Fatal("revoke stopped instances in a project that is not this lease's")
	}
}

func TestLeaseNetworkInputsFromEnvironment(t *testing.T) {
	setLeaseEnv(t)
	n := computenet.Network{Egress: computenet.EgressInternetLAN, Ingress: computenet.IngressNone, IntraLease: true}
	encoded, _ := n.Encode()
	t.Setenv("ANAS_RESOURCE_NETWORK", encoded)
	t.Setenv("ANAS_RESOURCE_LAN_SUBNETS", "192.168.1.0/24, 2001:db8:1::/64")
	t.Setenv("INCUS_LAN_EXTRA_SUBNETS", "10.20.0.0/16,192.168.1.0/24")
	t.Setenv("ANAS_RESOURCE_HOST_ADDRESSES", "192.168.1.10/32,2001:db8:1::10/128")
	l, err := leaseFromEnv("container")
	if err != nil {
		t.Fatal(err)
	}
	if l.Network.Egress != computenet.EgressInternetLAN || !l.Network.IntraLease || len(l.LAN) != 3 || len(l.HostAddresses) != 2 {
		t.Fatalf("lease network inputs = %+v / %v / %v", l.Network, l.LAN, l.HostAddresses)
	}
	for name, env := range map[string][2]string{
		"bad network":        {"ANAS_RESOURCE_NETWORK", `{"egress":"everything"}`},
		"unknown field":      {"ANAS_RESOURCE_NETWORK", `{"egress":"internet","ingress":"none","extra":1}`},
		"loopback LAN":       {"ANAS_RESOURCE_LAN_SUBNETS", "127.0.0.0/8"},
		"default route LAN":  {"INCUS_LAN_EXTRA_SUBNETS", "0.0.0.0/0"},
		"host subnet":        {"ANAS_RESOURCE_HOST_ADDRESSES", "192.168.1.0/24"},
		"link-local address": {"ANAS_RESOURCE_HOST_ADDRESSES", "fe80::1/128"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Setenv(env[0], env[1])
			if _, err := leaseFromEnv("container"); err == nil {
				t.Fatalf("%s accepted", name)
			}
		})
	}
	t.Setenv("ANAS_RESOURCE_NETWORK", "")
	if l, err := leaseFromEnv("container"); err != nil || l.Network.Egress != computenet.EgressInternet {
		t.Fatalf("missing network = %+v, %v", l.Network, err)
	}
}

func setLeaseEnv(t *testing.T) {
	t.Helper()
	certPEM, _ := selfSigned(t, "consumer")
	for key, value := range map[string]string{
		"ANAS_RESOURCE_CONSUMER":           "forgejo",
		"ANAS_RESOURCE_SANDBOX":            "anas-forgejo-runners",
		"INCUS_STORAGE_POOL":               "default",
		"ANAS_RESOURCE_INSTANCE_PREFIX":    "anas-fj-",
		"ANAS_RESOURCE_MAX_INSTANCES":      "8",
		"ANAS_RESOURCE_CPU":                "4",
		"ANAS_RESOURCE_MEMORY_MIB":         "8192",
		"ANAS_RESOURCE_DISK_GIB":           "40",
		"ANAS_RESOURCE_IMAGE_ALLOWLIST":    strings.Repeat("a", 64),
		"ANAS_RESOURCE_IMAGE_ARCHITECTURE": "amd64",
		"ANAS_RESOURCE_CLIENT_CERT":        base64.StdEncoding.EncodeToString(certPEM),
	} {
		t.Setenv(key, value)
	}
}
