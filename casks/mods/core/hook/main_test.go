package main

import (
	"strings"
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
	if !strings.HasPrefix(env["SSH_RSA_PRIVATE"], "ssh-rsa ") {
		t.Fatal("expected generated SSH public key")
	}
}
