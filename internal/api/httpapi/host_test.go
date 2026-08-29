package httpapi

import (
	"context"
	"crypto/tls"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestDirectHostPolicyMatchesConnectionLocalAddress(t *testing.T) {
	tests := []struct {
		name    string
		mode    DirectMode
		host    string
		local   string
		dns     []string
		allowed bool
	}{
		{name: "LAN IPv4", mode: DirectModeLAN, host: "192.0.2.20:8080", local: "192.0.2.20:8080", allowed: true},
		{name: "LAN IPv6", mode: DirectModeLAN, host: "[2001:db8::20]:8080", local: "[2001:db8::20]:8080", allowed: true},
		{name: "different local address", mode: DirectModeLAN, host: "192.0.2.21:8080", local: "192.0.2.20:8080"},
		{name: "different port", mode: DirectModeLAN, host: "192.0.2.20:8443", local: "192.0.2.20:8080"},
		{name: "wildcard is not a Host", mode: DirectModeLAN, host: "0.0.0.0:8080", local: "192.0.2.20:8080"},
		{name: "configured DNS", mode: DirectModeLAN, host: "ANAS.EXAMPLE.:8080", local: "192.0.2.20:8080", dns: []string{"anas.example"}, allowed: true},
		{name: "unconfigured DNS", mode: DirectModeLAN, host: "other.example:8080", local: "192.0.2.20:8080", dns: []string{"anas.example"}},
		{name: "loopback", mode: DirectModeLoopback, host: "127.0.0.1:8080", local: "127.0.0.1:8080", allowed: true},
		{name: "loopback rejects LAN", mode: DirectModeLoopback, host: "192.0.2.20:8080", local: "192.0.2.20:8080"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := requestWithLocalAddress(t, test.host, test.local)
			policy := DirectHostPolicy{Mode: test.mode, AllowedDNSHosts: test.dns}
			if got := policy.Allowed(request); got != test.allowed {
				t.Fatalf("Allowed() = %v, want %v", got, test.allowed)
			}
		})
	}
}

func TestDirectHostPolicyRequiresAConcreteLocalAddress(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "http://192.0.2.20:8080/healthz", nil)
	request.Host = "192.0.2.20:8080"
	if (DirectHostPolicy{Mode: DirectModeLAN}).Allowed(request) {
		t.Fatal("request without http.LocalAddrContextKey was allowed")
	}
}

func TestSameOriginUsesExactRequestOrigin(t *testing.T) {
	tests := []struct {
		name   string
		host   string
		origin string
		tls    bool
		want   bool
	}{
		{name: "HTTP", host: "192.0.2.20:8080", origin: "http://192.0.2.20:8080", want: true},
		{name: "HTTPS", host: "anas.example:8443", origin: "https://anas.example:8443", tls: true, want: true},
		{name: "scheme mismatch", host: "anas.example:8443", origin: "http://anas.example:8443", tls: true},
		{name: "host mismatch", host: "anas.example:8443", origin: "https://other.example:8443", tls: true},
		{name: "missing", host: "anas.example:8443", tls: true},
		{name: "null", host: "anas.example:8443", origin: "null", tls: true},
		{name: "multiple", host: "anas.example:8443", origin: "https://anas.example:8443, https://other.example", tls: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, "/", nil)
			request.Host = test.host
			request.Header.Set("Origin", test.origin)
			if test.tls {
				request.TLS = &tls.ConnectionState{}
			}
			if got := sameOrigin(request); got != test.want {
				t.Fatalf("sameOrigin() = %v, want %v", got, test.want)
			}
		})
	}
}

func requestWithLocalAddress(t *testing.T, host, local string) *http.Request {
	t.Helper()
	address, err := net.ResolveTCPAddr("tcp", local)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	request.Host = host
	return request.WithContext(context.WithValue(request.Context(), http.LocalAddrContextKey, address))
}
