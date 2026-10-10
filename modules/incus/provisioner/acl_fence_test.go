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
			if listed := listedOtherLeases(acl); len(listed) != 0 {
				t.Fatalf("a lone lease drops other leases %v", listed)
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
		traefik(internet) {
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
		egressRule(hostTier, "drop", "192.168.1.10/32") ||
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

// INCUS-R-125, R-134: drift in the ACL, its attachment or the port isolation
// is not ready, and ensure repairs it.
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
		"own subnet dropped": func(d *fakeDaemon, b, _ string) {
			a := d.acls[b]
			own := rule("drop", otherLeasesDescription)
			own.Destination = netip.MustParsePrefix(d.networks[b].Config["ipv4.address"]).Masked().String()
			a.Egress = append(a.Egress, own)
			d.acls[b] = a
		},
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
			// A host address gives the internet tier a drop rule to lose.
			l.HostAddresses = []netip.Prefix{netip.MustParsePrefix("192.168.1.10/32")}
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

// INCUS-R-136: a lease drops every other lease in both directions, never its
// own subnet. A lease created later is in an older lease's list only after the
// older one's next ensure; until then the newer lease's own rules keep the two
// apart, and the older lease is not reported as drifted.
func TestLeasesDropEachOtherButNotThemselves(t *testing.T) {
	d := newFakeDaemon(t)
	withTraefikSet(d)
	c := d.clientFor(t)
	a := publishedLease(t, true)
	b := publishedLease(t, true)
	b.Consumer, b.Sandbox, b.InstancePrefix = "ai_agent", "anas-ai-agent", "anas-agent-"
	b.Network.Slots = []computenet.Slot{{Name: "db", Instance: "anas-agent-db"}, {Name: "dev", Instance: "anas-agent-dev"}}
	ensureReady(t, d, a)
	ensureReady(t, d, b)
	aSubnets, bSubnets := bridgeSubnets(t, d, a), bridgeSubnets(t, d, b)
	dropsBoth := func(acl networkACL, others []string) bool {
		var egress, ingress bool
		for _, r := range acl.Egress {
			egress = egress || r.Action == "drop" && slices.Equal(normalizedSubjects(split(r.Destination)), normalizedSubjects(others))
		}
		for _, r := range acl.Ingress {
			ingress = ingress || r.Action == "drop" && slices.Equal(normalizedSubjects(split(r.Source)), normalizedSubjects(others))
		}
		return egress && ingress
	}
	if !dropsBoth(d.acls[computeclient.NetworkName(b.Sandbox)], aSubnets) {
		t.Fatalf("the newer lease does not drop the older one both ways: %+v", d.acls[computeclient.NetworkName(b.Sandbox)])
	}
	if listed := listedOtherLeases(d.acls[computeclient.NetworkName(a.Sandbox)]); len(listed) != 0 {
		t.Fatalf("the older lease names %v before its next ensure", listed)
	}
	if result, err := inspect(context.Background(), c, a); err != nil || !result.Ready {
		t.Fatalf("an older lease is not drift because a newer one exists: %+v, %v", result, err)
	}
	ensureReady(t, d, a)
	if !dropsBoth(d.acls[computeclient.NetworkName(a.Sandbox)], bSubnets) {
		t.Fatal("re-ensure did not add the newer lease")
	}
	for _, l := range []lease{a, b} {
		for _, own := range bridgeSubnets(t, d, l) {
			if slices.Contains(listedOtherLeases(d.acls[computeclient.NetworkName(l.Sandbox)]), own) {
				t.Fatalf("%s drops its own subnet %s", l.Sandbox, own)
			}
		}
	}
}

// INCUS-R-117, R-136: modules_only writes no egress drop for other leases, its
// default drop covers them. Ensure must not require one, and with published
// ingress the other leases are read back from the ingress drop instead.
func TestModulesOnlyLeaseNamesOtherLeasesOnlyWhereItDropsThem(t *testing.T) {
	d := newFakeDaemon(t)
	withTraefikSet(d)
	c := d.clientFor(t)
	other := testLease(t, "container")
	other.Consumer, other.Sandbox, other.InstancePrefix = "ai_agent", "anas-ai-agent", "anas-agent-"
	ensureReady(t, d, other)
	closed := testLease(t, "container")
	closed.Network.Egress = computenet.EgressModulesOnly
	ensureReady(t, d, closed)
	if listed := listedOtherLeases(d.acls[computeclient.NetworkName(closed.Sandbox)]); len(listed) != 0 {
		t.Fatalf("a modules_only lease without published ingress names %v", listed)
	}
	published := publishedLease(t, true)
	published.Consumer, published.Sandbox, published.InstancePrefix = "lnmods", "anas-lnmods", "anas-lnmods-"
	published.Network.Egress = computenet.EgressModulesOnly
	ensureReady(t, d, published)
	want := slices.Concat(bridgeSubnets(t, d, other), bridgeSubnets(t, d, closed))
	if listed := listedOtherLeases(d.acls[computeclient.NetworkName(published.Sandbox)]); !slices.Equal(normalizedSubjects(listed), normalizedSubjects(want)) {
		t.Fatalf("published modules_only lease names %v, want %v", listed, want)
	}
	for _, l := range []lease{closed, published} {
		if result, err := inspect(context.Background(), c, l); err != nil || !result.Ready {
			t.Fatalf("%s inspect = %+v, %v", l.Sandbox, result, err)
		}
		ensureReady(t, d, l)
	}
}

// INCUS-R-123: the ACL also filters traffic inside the bridge, so intra_lease
// allows this lease's instance addresses both ways. The gateway, which is the
// host, stays outside the ranges (INCUS-R-113, R-130).
func TestIntraLeaseAllowsInstanceAddressesNotTheGateway(t *testing.T) {
	d := newFakeDaemon(t)
	l := testLease(t, "container")
	l.NetworkIPv6 = true
	l.Network.IntraLease = true
	ensureReady(t, d, l)
	bridge := computeclient.NetworkName(l.Sandbox)
	acl := d.acls[bridge]
	v4 := netip.MustParsePrefix(d.networks[bridge].Config["ipv4.address"])
	v6 := netip.MustParsePrefix(d.networks[bridge].Config["ipv6.address"])
	members := append(memberRanges(v4), memberRanges(v6)...)
	var egress, ingress bool
	for _, r := range acl.Egress {
		egress = egress || r.Action == "allow" && r.Description == intraLeaseDescription && slices.Equal(split(r.Destination), members)
	}
	for _, r := range acl.Ingress {
		ingress = ingress || r.Action == "allow" && r.Description == intraLeaseDescription && slices.Equal(split(r.Source), members)
	}
	if !egress || !ingress {
		t.Fatalf("intra_lease rules missing: %+v", acl)
	}
	for _, rangeText := range members {
		from, to, _ := strings.Cut(rangeText, "-")
		low, high := netip.MustParseAddr(from), netip.MustParseAddr(to)
		for _, gateway := range []netip.Addr{v4.Addr(), v6.Addr()} {
			if gateway.BitLen() == low.BitLen() && low.Compare(gateway) <= 0 && gateway.Compare(high) <= 0 {
				t.Fatalf("range %s includes the gateway %s", rangeText, gateway)
			}
		}
	}
	l.Network.IntraLease = false
	ensureReady(t, d, l)
	for _, r := range append(d.acls[bridge].Egress, d.acls[bridge].Ingress...) {
		if r.Description == intraLeaseDescription {
			t.Fatal("closing intra_lease kept its rules")
		}
	}
}

func TestMemberRangesSkipNetworkGatewayAndBroadcast(t *testing.T) {
	for prefix, want := range map[string][]string{
		"10.1.2.1/24":   {"10.1.2.2-10.1.2.254"},
		"10.1.2.200/24": {"10.1.2.1-10.1.2.199", "10.1.2.201-10.1.2.254"},
		"10.1.2.254/24": {"10.1.2.1-10.1.2.253"},
		"fd42:1::1/64":  {"fd42:1::2-fd42:1::ffff:ffff:ffff:ffff"},
		"10.1.2.1/30":   {"10.1.2.2"},
	} {
		if got := memberRanges(netip.MustParsePrefix(prefix)); !slices.Equal(got, want) {
			t.Errorf("%s = %v, want %v", prefix, got, want)
		}
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
