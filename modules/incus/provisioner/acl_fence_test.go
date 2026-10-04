package main

import (
	"context"
	"net/netip"
	"slices"
	"strings"
	"testing"

	"github.com/anas-project/ANAS/internal/computeclient"
	"github.com/anas-project/ANAS/internal/computenet"
)

func bridgeSubnets(t *testing.T, d *fakeDaemon, l lease) []string {
	t.Helper()
	n := d.networks[computeclient.NetworkName(l.Sandbox)]
	var want []string
	for _, family := range []string{"ipv4", "ipv6"} {
		if value := n.Config[family+".address"]; value != "none" && value != "" {
			want = append(want, netip.MustParsePrefix(value).Masked().String())
		}
	}
	slices.Sort(want)
	return want
}

func split(value string) []string {
	if value == "" {
		return nil
	}
	parts := strings.Split(value, ",")
	slices.Sort(parts)
	return parts
}

func ensureReady(t *testing.T, d *fakeDaemon, l lease) ensureResult {
	t.Helper()
	result, err := ensure(context.Background(), d.clientFor(t), l)
	if err != nil || !result.Ready {
		t.Fatalf("ensure = %+v, %v", result, err)
	}
	return result
}

func withTraefikSet(d *fakeDaemon) {
	d.addressSets[traefikAddressSet] = addressSet{Name: traefikAddressSet, Addresses: []string{"172.30.0.2/32"}, Config: map[string]string{}}
}

// INCUS-R-044, R-112, R-130: every allow rule is fenced to the bridge's own
// subnets, egress and ingress both default to drop.
func TestLeaseBridgeCarriesNetworkPolicyACL(t *testing.T) {
	for name, ipv6 := range map[string]bool{"ipv6 on": true, "ipv6 off": false} {
		t.Run(name, func(t *testing.T) {
			d := newFakeDaemon(t)
			l := testLease(t, "container")
			l.NetworkIPv6 = ipv6
			ensureReady(t, d, l)
			bridge := computeclient.NetworkName(l.Sandbox)
			if !strings.HasPrefix(bridge, "lease") || strings.HasPrefix(bridge, "anas") {
				t.Fatalf("lease bridge %s must use the dedicated prefix (INCUS-R-127)", bridge)
			}
			n := d.networks[bridge]
			if n.Config["security.acls"] != bridge || n.Config["security.acls.default.egress.action"] != "drop" ||
				n.Config["security.acls.default.ingress.action"] != "drop" {
				t.Fatalf("bridge ACL attachment = %v", n.Config)
			}
			want := bridgeSubnets(t, d, l)
			if len(want) != map[bool]int{true: 2, false: 1}[ipv6] {
				t.Fatalf("subnets = %v for ipv6=%v", want, ipv6)
			}
			acl := d.acls[bridge]
			allows := 0
			for _, r := range acl.Egress {
				if r.Action == "allow" {
					allows++
					if !slices.Equal(split(r.Source), want) {
						t.Fatalf("allow rule source = %q, want the bridge subnets %v", r.Source, want)
					}
				}
				if !ipv6 && strings.Contains(r.Destination, "::") {
					t.Fatalf("IPv6-off lease has an IPv6 destination: %+v", r)
				}
			}
			if allows == 0 || len(acl.Ingress) != 0 {
				t.Fatalf("default lease ACL = %+v", acl)
			}
			if acl.Config["user.anas.consumer"] != l.Consumer || acl.Config["user.anas.sandbox"] != l.Sandbox || acl.Config[leaseCredentialKey] != l.Credential {
				t.Fatalf("ACL ownership = %v", acl.Config)
			}
			set := d.addressSets[leasesAddressSet]
			for _, subnet := range want {
				if !slices.Contains(set.Addresses, subnet) {
					t.Fatalf("lease subnet set %v lacks %s", set.Addresses, subnet)
				}
			}
		})
	}
}

func egressRule(acl networkACL, action, destination string) bool {
	for _, r := range acl.Egress {
		if r.Action == action && slices.Contains(split(r.Destination), destination) {
			return true
		}
	}
	return false
}

// INCUS-R-113--R-117, R-120: each tier allows and drops what its row in the
// requirement table says.
func TestEgressTiersWriteTheirRules(t *testing.T) {
	lan := []netip.Prefix{netip.MustParsePrefix("192.168.1.0/24"), netip.MustParsePrefix("203.0.113.0/24")}
	host := []netip.Prefix{netip.MustParsePrefix("192.168.1.10/32"), netip.MustParsePrefix("203.0.113.7/32")}
	base := func(t *testing.T, egress string, moduleAccess bool) networkACL {
		d := newFakeDaemon(t)
		withTraefikSet(d)
		l := testLease(t, "container")
		l.LAN, l.HostAddresses = lan, host
		l.Network = computenet.Network{Egress: egress, ModuleAccess: moduleAccess, Ingress: computenet.IngressNone}
		if l.Network.NeedsTraefik() {
			l.Network.TraefikPort = 9000
		}
		ensureReady(t, d, l)
		return d.acls[computeclient.NetworkName(l.Sandbox)]
	}
	traefik := func(acl networkACL) bool {
		for _, r := range acl.Egress {
			if r.Action == "allow" && r.Destination == "$"+traefikAddressSet && r.Protocol == "tcp" && r.DestinationPort == "9000" {
				return true
			}
		}
		return false
	}

	internet := base(t, computenet.EgressInternet, false)
	if !egressRule(internet, "allow", "1.0.0.0/8") || egressRule(internet, "allow", "192.168.0.0/16") ||
		!egressRule(internet, "drop", "203.0.113.0/24") || !egressRule(internet, "drop", "203.0.113.7/32") ||
		!egressRule(internet, "drop", "$"+leasesAddressSet) || traefik(internet) {
		t.Fatalf("internet tier = %+v", internet.Egress)
	}
	if !traefik(base(t, computenet.EgressInternet, true)) {
		t.Fatal("module_access did not reach Traefik")
	}
	internetLAN := base(t, computenet.EgressInternetLAN, false)
	if !egressRule(internetLAN, "allow", "192.168.1.0/24") || !egressRule(internetLAN, "drop", "192.168.1.10/32") ||
		egressRule(internetLAN, "allow", "10.0.0.0/8") {
		t.Fatalf("internet_lan tier = %+v", internetLAN.Egress)
	}
	hostTier := base(t, computenet.EgressInternetLANHost, false)
	if !egressRule(hostTier, "allow", "192.168.1.10/32") || !egressRule(hostTier, "allow", "172.16.0.0/12") ||
		egressRule(hostTier, "drop", "192.168.1.10/32") || !egressRule(hostTier, "drop", "$"+leasesAddressSet) ||
		egressRule(hostTier, "allow", "169.254.0.0/16") {
		t.Fatalf("internet_lan_host tier = %+v", hostTier.Egress)
	}
	only := base(t, computenet.EgressModulesOnly, false)
	if len(only.Egress) != 1 || !traefik(only) {
		t.Fatalf("modules_only tier = %+v", only.Egress)
	}
}

func TestPublicIPv4ComplementsReservedRanges(t *testing.T) {
	public := make([]netip.Prefix, len(publicIPv4))
	for i, cidr := range publicIPv4 {
		public[i] = netip.MustParsePrefix(cidr)
	}
	for _, reserved := range reservedIPv4 {
		r := netip.MustParsePrefix(reserved)
		for _, p := range public {
			if p.Overlaps(r) {
				t.Fatalf("public %s overlaps reserved %s", p, r)
			}
		}
	}
	covered := func(addr string) bool {
		a := netip.MustParseAddr(addr)
		for _, p := range public {
			if p.Contains(a) {
				return true
			}
		}
		return false
	}
	for _, addr := range []string{"1.1.1.1", "8.8.8.8", "100.63.255.255", "100.128.0.0", "169.253.255.255", "169.255.0.1",
		"172.15.255.255", "172.32.0.0", "192.167.255.255", "192.169.0.0", "223.255.255.255", "198.51.100.7"} {
		if !covered(addr) {
			t.Errorf("%s is public but not allowed", addr)
		}
	}
	for _, addr := range []string{"10.1.2.3", "100.64.0.1", "127.0.0.1", "169.254.1.1", "172.16.0.1", "192.168.1.1", "224.0.0.1", "255.255.255.255", "0.1.2.3"} {
		if covered(addr) {
			t.Errorf("%s is reserved but allowed", addr)
		}
	}
}

// INCUS-R-125, R-134: drift in the ACL, its attachment, the shared lease
// subnet set or the port isolation is not ready, and ensure repairs it.
func TestNetworkPolicyDriftBlocksInspectAndEnsureRepairs(t *testing.T) {
	for name, drift := range map[string]func(*fakeDaemon, string, string){
		"widened source": func(d *fakeDaemon, b, _ string) {
			a := d.acls[b]
			a.Egress[0].Source += ",::/0"
			d.acls[b] = a
		},
		"extra egress rule": func(d *fakeDaemon, b, _ string) {
			a := d.acls[b]
			a.Egress = append(a.Egress, aclRule{Action: "allow", State: "enabled"})
			d.acls[b] = a
		},
		"missing drop rule": func(d *fakeDaemon, b, _ string) {
			a := d.acls[b]
			a.Egress = slices.DeleteFunc(slices.Clone(a.Egress), func(r aclRule) bool { return r.Action == "drop" })
			d.acls[b] = a
		},
		"ingress rule": func(d *fakeDaemon, b, _ string) {
			a := d.acls[b]
			a.Ingress = []aclRule{{Action: "allow", State: "enabled"}}
			d.acls[b] = a
		},
		"disabled rule":        func(d *fakeDaemon, b, _ string) { a := d.acls[b]; a.Egress[0].State = "disabled"; d.acls[b] = a },
		"detached from bridge": func(d *fakeDaemon, b, _ string) { delete(d.networks[b].Config, "security.acls") },
		"egress default allow": func(d *fakeDaemon, b, _ string) {
			d.networks[b].Config["security.acls.default.egress.action"] = "allow"
		},
		"ingress default allow": func(d *fakeDaemon, b, _ string) {
			d.networks[b].Config["security.acls.default.ingress.action"] = "allow"
		},
		"ACL deleted": func(d *fakeDaemon, b, _ string) { delete(d.acls, b); delete(d.networks[b].Config, "security.acls") },
		"lease set shrunk": func(d *fakeDaemon, _, _ string) {
			s := d.addressSets[leasesAddressSet]
			s.Addresses = nil
			d.addressSets[leasesAddressSet] = s
		},
		"lease set deleted":  func(d *fakeDaemon, _, _ string) { delete(d.addressSets, leasesAddressSet) },
		"DHCP range widened": func(d *fakeDaemon, b, _ string) { delete(d.networks[b].Config, "ipv4.dhcp.ranges") },
		"port isolation off": func(d *fakeDaemon, _, project string) {
			delete(d.profiles[project+"/"+computeclient.ProfileName].Devices["eth0"], "security.port_isolation")
		},
	} {
		t.Run(name, func(t *testing.T) {
			d := newFakeDaemon(t)
			c := d.clientFor(t)
			l := testLease(t, "vm")
			l.NetworkIPv6 = true
			ensureReady(t, d, l)
			bridge := computeclient.NetworkName(l.Sandbox)
			drift(d, bridge, l.Sandbox)
			if result, err := inspect(context.Background(), c, l); err != nil || result.Ready {
				t.Fatalf("drifted network policy reported %+v, %v", result, err)
			}
			ensureReady(t, d, l)
		})
	}
}

func TestForeignNetworkACLIsNotAdopted(t *testing.T) {
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
				t.Fatal("certificate registered before the network policy was proven")
			}
		})
	}
}

func TestForeignLeaseSubnetSetIsNotAdopted(t *testing.T) {
	d := newFakeDaemon(t)
	d.addressSets[leasesAddressSet] = addressSet{Name: leasesAddressSet, Addresses: []string{"10.0.0.0/8"}, Config: map[string]string{}}
	if _, err := ensure(context.Background(), d.clientFor(t), testLease(t, "container")); err == nil {
		t.Fatal("ensure rewrote an address set ANAS does not manage")
	}
	if got := d.addressSets[leasesAddressSet].Addresses; len(got) != 1 || got[0] != "10.0.0.0/8" {
		t.Fatalf("foreign set changed: %v", got)
	}
}

// One shared set excludes every lease from every other: the second lease's
// ensure adds its subnet, so the first lease is protected without re-ensure.
func TestLeaseSubnetSetCoversEveryLease(t *testing.T) {
	d := newFakeDaemon(t)
	c := d.clientFor(t)
	a := testLease(t, "container")
	b := testLease(t, "container")
	b.Consumer, b.Sandbox, b.InstancePrefix = "ai_agent", "anas-ai-agent", "anas-agent-"
	ensureReady(t, d, a)
	ensureReady(t, d, b)
	set := d.addressSets[leasesAddressSet].Addresses
	for _, l := range []lease{a, b} {
		for _, subnet := range bridgeSubnets(t, d, l) {
			if !slices.Contains(set, subnet) {
				t.Fatalf("set %v lacks %s", set, subnet)
			}
		}
	}
	if result, err := inspect(context.Background(), c, a); err != nil || !result.Ready {
		t.Fatalf("first lease after the second was ensured = %+v, %v", result, err)
	}
}

func TestDaemonWithoutAddressSetsIsRefused(t *testing.T) {
	d := newFakeDaemon(t)
	d.apiExtensions = slices.DeleteFunc(d.apiExtensions, func(e string) bool { return e == "network_address_set" })
	if _, err := ensure(context.Background(), d.clientFor(t), testLease(t, "container")); err == nil || !strings.Contains(err.Error(), "network_address_set") {
		t.Fatalf("ensure on a daemon without address sets = %v", err)
	}
}

// INCUS-R-119: the Provider never creates the Traefik set; a lease that needs
// it fails until hostd has.
func TestTraefikSetIsRequiredNotCreated(t *testing.T) {
	d := newFakeDaemon(t)
	c := d.clientFor(t)
	l := testLease(t, "container")
	l.Network = computenet.Network{Egress: computenet.EgressModulesOnly, Ingress: computenet.IngressNone, TraefikPort: 9000}
	if _, err := ensure(context.Background(), c, l); err == nil || !strings.Contains(err.Error(), traefikAddressSet) {
		t.Fatalf("ensure without the Traefik set = %v", err)
	}
	if _, created := d.addressSets[traefikAddressSet]; created {
		t.Fatal("the Provider created the Traefik set")
	}
	withTraefikSet(d)
	ensureReady(t, d, l)
	delete(d.addressSets, traefikAddressSet)
	if result, _ := inspect(context.Background(), c, l); result.Ready {
		t.Fatal("a lease that needs the Traefik set was ready without it")
	}
}

func TestSourceFenceFollowsIPv6PostureChange(t *testing.T) {
	d := newFakeDaemon(t)
	l := testLease(t, "container")
	l.NetworkIPv6 = true
	ensureReady(t, d, l)
	if got := bridgeSubnets(t, d, l); len(got) != 2 {
		t.Fatalf("IPv6 lease subnets = %v", got)
	}
	l.NetworkIPv6 = false
	ensureReady(t, d, l)
	for _, r := range d.acls[computeclient.NetworkName(l.Sandbox)].Egress {
		if strings.Contains(r.Source, ":") || strings.Contains(r.Destination, "::") {
			t.Fatalf("IPv6-off lease still carries IPv6: %+v", r)
		}
	}
}
