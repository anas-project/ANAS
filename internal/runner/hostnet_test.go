package runner

import (
	"net"
	"testing"
)

func hostNetApp(env map[string]string, modules map[string]Module) *app {
	order := []string{}
	for name := range modules {
		order = append(order, name)
	}
	return &app{env: env, envOwner: map[string]string{}, reg: modules, order: order}
}

func TestCalcVLANAcceptsSlash28(t *testing.T) {
	env, err := calcVLAN("192.0.2.2", "28")
	if err != nil {
		t.Fatal(err)
	}
	if env["VLAN_SEGMENT"] != "192.0.2.0/28" {
		t.Fatalf("VLAN_SEGMENT = %q", env["VLAN_SEGMENT"])
	}
}

func TestCalcVLANRejectsSubnetSmallerThanPool(t *testing.T) {
	if _, err := calcVLAN("192.0.2.2", "29"); err == nil {
		t.Fatal("expected /29 to be rejected")
	}
}

func TestApplyHostNetworkUsesExplicitHostNetwork(t *testing.T) {
	a := hostNetApp(map[string]string{
		"HOST_IP":            "10.254.0.2",
		"INTERFACE":          "anas-test-peer",
		"HOST_SUBNET_MASK":   "24",
		"DEFAULT_GATEWAY_IP": "10.254.0.1",
		"LOCAL_DNS_SERVER":   "10.254.0.1",
	}, map[string]Module{"samba_fs": {Name: "samba_fs", UseHostLAN: "required"}})
	if err := a.applyHostNetwork(); err != nil {
		t.Fatal(err)
	}
	if a.env["INTERFACE"] != "anas-test-peer" || a.env["HOST_IP"] != "10.254.0.2" {
		t.Fatalf("explicit network was overwritten: interface=%q host_ip=%q", a.env["INTERFACE"], a.env["HOST_IP"])
	}
	if a.env["VLAN_BRIDGE_IP"] != "10.254.0.241" {
		t.Fatalf("VLAN_BRIDGE_IP = %q", a.env["VLAN_BRIDGE_IP"])
	}
	if a.env["VLAN_GATEWAY_IP"] != "10.254.0.1" {
		t.Fatalf("VLAN_GATEWAY_IP = %q", a.env["VLAN_GATEWAY_IP"])
	}
	if a.env["LOCAL_DNS_SERVER"] != "10.254.0.1" {
		t.Fatalf("LOCAL_DNS_SERVER = %q", a.env["LOCAL_DNS_SERVER"])
	}
}

// The host environment describes the machine, not whoever discovered it, so
// every module sees it regardless of its dependency closure. Recording anything
// else would silently shrink a rendered .env.
func TestApplyHostNetworkPublishesGloballyOwnedKeys(t *testing.T) {
	a := hostNetApp(map[string]string{
		"HOST_IP":          "10.254.0.2",
		"INTERFACE":        "anas-test-peer",
		"HOST_SUBNET_MASK": "24",
	}, map[string]Module{"samba_fs": {Name: "samba_fs", UseHostLAN: "required"}})
	if err := a.applyHostNetwork(); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"HOST_IP", "INTERFACE", "HOST_SUBNET_MASK", "LOCAL_DNS_SERVER", "HOST_SEGMENT", "VLAN_SEGMENT", "VLAN_INTERFACE", "HOST_HAS_IPV6"} {
		owner, tracked := a.envOwner[key]
		if !tracked {
			t.Errorf("%s has no recorded owner", key)
			continue
		}
		if owner != "" {
			t.Errorf("%s is owned by %q, want global ownership", key, owner)
		}
	}
}

// A host with no room for a macvlan pool -- a VPS on a /32, or a /30 -- must
// still render. Nothing in such a deployment attaches to the host LAN, so the
// pool that cannot be carved is also the pool nobody asked for.
func TestApplyHostNetworkSkipsVLANWithoutHostLANConsumer(t *testing.T) {
	env := map[string]string{
		"HOST_IP":          "203.0.113.7",
		"INTERFACE":        "eth0",
		"HOST_SUBNET_MASK": "32",
	}
	a := hostNetApp(env, map[string]Module{"traefik": {Name: "traefik"}})
	if err := a.applyHostNetwork(); err != nil {
		t.Fatalf("render failed on a /32 host with no host-LAN module: %v", err)
	}
	if a.env["VLAN_SEGMENT"] != "" || a.env["HOST_SEGMENT"] != "" {
		t.Fatalf("macvlan plan published without a consumer: segment=%q host=%q",
			a.env["VLAN_SEGMENT"], a.env["HOST_SEGMENT"])
	}

	// The same host does have to fail when a module genuinely needs the bridge:
	// starting samba_fs without one is worse than refusing to render.
	needsLAN := hostNetApp(map[string]string{
		"HOST_IP":          "203.0.113.7",
		"INTERFACE":        "eth0",
		"HOST_SUBNET_MASK": "32",
	}, map[string]Module{"samba_fs": {Name: "samba_fs", UseHostLAN: "required"}})
	if err := needsLAN.applyHostNetwork(); err == nil {
		t.Fatal("expected a /32 host to be rejected once a module requires host LAN")
	}
}

// Host IPv6 detection is published as three values so consumers never repeat
// the probe: whether there is one, which address, and on which interface.
func TestDetectHostIPv6ReportsAbsenceExplicitly(t *testing.T) {
	// An explicitly configured address is authoritative and short-circuits
	// detection, which is also how a test asserts the published shape without
	// depending on the machine it runs on.
	a := hostNetApp(map[string]string{"HOST_IPV6": "2001:db8::1"}, nil)
	a.detectHostIPv6()
	if a.env["HOST_HAS_IPV6"] != "true" {
		t.Fatalf("HOST_HAS_IPV6 = %q, want true", a.env["HOST_HAS_IPV6"])
	}
	if a.env["HOST_IPV6"] != "2001:db8::1" {
		t.Fatalf("configured address was overwritten: %q", a.env["HOST_IPV6"])
	}

	// Detection always sets the flag, so a consumer can distinguish "no IPv6"
	// from "the runner did not look".
	probed := hostNetApp(map[string]string{}, nil)
	probed.detectHostIPv6()
	switch probed.env["HOST_HAS_IPV6"] {
	case "true":
		if probed.env["HOST_IPV6"] == "" {
			t.Error("HOST_HAS_IPV6 is true but no address was published")
		}
		if probed.env["HOST_IPV6_INTERFACE"] == "" {
			t.Error("HOST_HAS_IPV6 is true but no interface was published")
		}
	case "false":
		if probed.env["HOST_IPV6"] != "" {
			t.Errorf("HOST_HAS_IPV6 is false but an address was published: %q", probed.env["HOST_IPV6"])
		}
	default:
		t.Fatalf("HOST_HAS_IPV6 = %q, want an explicit true or false", probed.env["HOST_HAS_IPV6"])
	}
}

// A unique-local address routes nowhere outside the site, so publishing it
// would produce an AAAA record no external client can reach.
func TestUniqueLocalIPv6IsNotAHostAddress(t *testing.T) {
	for _, tc := range []struct {
		addr string
		want bool
	}{
		{"fd00::1", true},
		{"fdce:362c:6319::f", true},
		{"fc00::1", true},
		{"2408:8239:5601:75a::f", false},
		{"2001:db8::1", false},
	} {
		if got := isUniqueLocalIPv6(net.ParseIP(tc.addr)); got != tc.want {
			t.Errorf("isUniqueLocalIPv6(%s) = %v, want %v", tc.addr, got, tc.want)
		}
	}
}

// SERVER_NAME feeds NetBIOS and Kerberos names, which are upper case by
// convention; a configured value is normalized rather than trusted verbatim.
func TestServerNameIsNormalizedToUpperCase(t *testing.T) {
	if got := serverName("nas.example.com"); got != "NAS.EXAMPLE.COM" {
		t.Fatalf("serverName(configured) = %q", got)
	}
	if got := serverName(""); got == "" {
		t.Fatal("serverName fell back to an empty host name")
	}
}

// A configured server name is normalized, not merely accepted: the NetBIOS and
// Kerberos consumers downstream compare against an upper-case name.
func TestApplyHostNetworkNormalizesConfiguredServerName(t *testing.T) {
	a := hostNetApp(map[string]string{
		"SERVER_NAME":      "nas-01",
		"HOST_IP":          "10.254.0.2",
		"INTERFACE":        "anas-test-peer",
		"HOST_SUBNET_MASK": "24",
	}, nil)
	if err := a.applyHostNetwork(); err != nil {
		t.Fatal(err)
	}
	if a.env["SERVER_NAME"] != "NAS-01" {
		t.Fatalf("SERVER_NAME = %q, want NAS-01", a.env["SERVER_NAME"])
	}
}

// HOST_HAS_IPV6 answers "did the runner find a routable address", so a stale
// value carried in from a config or an older render must not survive.
func TestDetectHostIPv6OverwritesStaleFlag(t *testing.T) {
	a := hostNetApp(map[string]string{"HOST_IPV6": "2001:db8::1", "HOST_HAS_IPV6": "false"}, nil)
	a.detectHostIPv6()
	if a.env["HOST_HAS_IPV6"] != "true" {
		t.Fatalf("HOST_HAS_IPV6 = %q, want true", a.env["HOST_HAS_IPV6"])
	}
}
