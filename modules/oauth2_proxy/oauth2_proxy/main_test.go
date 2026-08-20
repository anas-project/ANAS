package main

import (
	"net/netip"
	"reflect"
	"testing"
)

func TestTrustedProxyArgsUseExactTraefikPeer(t *testing.T) {
	got, err := trustedProxyArgs(netip.MustParseAddr("172.21.0.7"), "192.0.2.8, 198.51.100.0/24")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"--trusted-proxy-ip=172.21.0.7/32",
		"--trusted-proxy-ip=192.0.2.8/32",
		"--trusted-proxy-ip=198.51.100.0/24",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("trustedProxyArgs() = %#v, want %#v", got, want)
	}
}

func TestTrustedProxyArgsRejectInvalidValue(t *testing.T) {
	if _, err := trustedProxyArgs(netip.MustParseAddr("2001:db8::7"), "proxy.example.com"); err == nil {
		t.Fatal("trustedProxyArgs accepted a hostname")
	}
}
