package main

import (
	"net"
	"testing"
)

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

func TestCalcCoreUsesExplicitHostNetwork(t *testing.T) {
	env := map[string]string{
		"HOST_IP":            "10.254.0.2",
		"INTERFACE":          "anas-test-peer",
		"HOST_SUBNET_MASK":   "24",
		"DEFAULT_GATEWAY_IP": "10.254.0.1",
		"LOCAL_DNS_SERVER":   "10.254.0.1",
		"DATA_PATH":          "/tmp/anas-test",
		"USERDATA_NAME":      "userdata",
	}
	secrets := &secretStore{values: map[string]string{}}
	if err := calcCore(env, "", secrets); err != nil {
		t.Fatal(err)
	}
	if env["INTERFACE"] != "anas-test-peer" || env["HOST_IP"] != "10.254.0.2" {
		t.Fatalf("explicit network was overwritten: interface=%q host_ip=%q", env["INTERFACE"], env["HOST_IP"])
	}
	if env["VLAN_BRIDGE_IP"] != "10.254.0.241" {
		t.Fatalf("VLAN_BRIDGE_IP = %q", env["VLAN_BRIDGE_IP"])
	}
	if env["VLAN_GATEWAY_IP"] != "10.254.0.1" {
		t.Fatalf("VLAN_GATEWAY_IP = %q", env["VLAN_GATEWAY_IP"])
	}
	if env["LOCAL_DNS_SERVER"] != "10.254.0.1" {
		t.Fatalf("LOCAL_DNS_SERVER = %q", env["LOCAL_DNS_SERVER"])
	}
}

// Host IPv6 detection is published as three values so consumers never repeat
// the probe: whether there is one, which address, and on which interface.
func TestDetectHostIPv6ReportsAbsenceExplicitly(t *testing.T) {
	// An explicitly configured address is authoritative and short-circuits
	// detection, which is also how a test asserts the published shape without
	// depending on the machine it runs on.
	env := map[string]string{"HOST_IPV6": "2001:db8::1"}
	detectHostIPv6(env)
	if env["HOST_HAS_IPV6"] != "true" {
		t.Fatalf("HOST_HAS_IPV6 = %q, want true", env["HOST_HAS_IPV6"])
	}
	if env["HOST_IPV6"] != "2001:db8::1" {
		t.Fatalf("configured address was overwritten: %q", env["HOST_IPV6"])
	}

	// Detection always sets the flag, so a consumer can distinguish "no IPv6"
	// from "core did not look".
	probed := map[string]string{}
	detectHostIPv6(probed)
	switch probed["HOST_HAS_IPV6"] {
	case "true":
		if probed["HOST_IPV6"] == "" {
			t.Error("HOST_HAS_IPV6 is true but no address was published")
		}
		if probed["HOST_IPV6_INTERFACE"] == "" {
			t.Error("HOST_HAS_IPV6 is true but no interface was published")
		}
	case "false":
		if probed["HOST_IPV6"] != "" {
			t.Errorf("HOST_HAS_IPV6 is false but an address was published: %q", probed["HOST_IPV6"])
		}
	default:
		t.Fatalf("HOST_HAS_IPV6 = %q, want an explicit true or false", probed["HOST_HAS_IPV6"])
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
