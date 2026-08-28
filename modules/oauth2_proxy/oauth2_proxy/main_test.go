package main

import (
	"net/http"
	"net/http/httptest"
	"net/netip"
	"reflect"
	"strings"
	"testing"
	"time"
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

// The gate is the one service whose failure is not a degradation: every
// protected route returns 500 rather than falling back, so Compose has to be
// able to tell "running" from "answering".
func TestProbeAcceptsAServingGate(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/ping" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	if err := probe(server.URL+"/ping", time.Second); err != nil {
		t.Fatalf("probe rejected a serving gate: %v", err)
	}
}

func TestProbeRejectsANonServingGate(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer server.Close()

	err := probe(server.URL+"/ping", time.Second)
	if err == nil {
		t.Fatal("probe accepted a 503")
	}
	if !strings.Contains(err.Error(), "503") {
		t.Fatalf("error = %v, want the status in it", err)
	}
}

func TestProbeRejectsAnUnreachableGate(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	url := server.URL + "/ping"
	server.Close()

	if err := probe(url, time.Second); err == nil {
		t.Fatal("probe accepted an unreachable address")
	}
}

// An empty URL means the Compose healthcheck was wired wrong. Reporting healthy
// in that case would be worse than having no healthcheck at all.
func TestProbeRejectsAnEmptyURL(t *testing.T) {
	if err := probe("", time.Second); err == nil {
		t.Fatal("probe accepted an empty URL")
	}
}
