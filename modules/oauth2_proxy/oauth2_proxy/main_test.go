package main

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"reflect"
	"strings"
	"testing"
	"time"
)

func testIdentityToken(t *testing.T, claims map[string]any) string {
	t.Helper()
	payload, err := json.Marshal(claims)
	if err != nil {
		t.Fatal(err)
	}
	return "header." + base64.RawURLEncoding.EncodeToString(payload) + ".signature"
}

func TestVerifiedIdentityClaimsRequireStableIdentityTimestampsAndAdminGroup(t *testing.T) {
	token := testIdentityToken(t, map[string]any{
		"iss": "https://iam.example.test", "sub": "user-123", "auth_time": 1000,
		"exp": 1300, "roles": []string{"Users", "NAS Admins"},
	})
	claims, assertion, err := verifiedIdentityClaims("Bearer "+token, "https://iam.example.test", "NAS Admins")
	if err != nil {
		t.Fatal(err)
	}
	if claims.Subject != "user-123" || claims.AuthTime != 1000 || claims.ExpiresAt != 1300 || assertion != token {
		t.Fatalf("claims = %#v assertion=%q", claims, assertion)
	}
	for name, authorization := range map[string]string{
		"wrong issuer": "Bearer " + testIdentityToken(t, map[string]any{"iss": "https://other.example.test", "sub": "user-123", "auth_time": 1000, "exp": 1300, "roles": []string{"NAS Admins"}}),
		"wrong group":  "Bearer " + testIdentityToken(t, map[string]any{"iss": "https://iam.example.test", "sub": "user-123", "auth_time": 1000, "exp": 1300, "roles": []string{"Users"}}),
		"missing time": "Bearer " + testIdentityToken(t, map[string]any{"iss": "https://iam.example.test", "sub": "user-123", "exp": 1300, "roles": []string{"NAS Admins"}}),
		"not bearer":   token,
	} {
		t.Run(name, func(t *testing.T) {
			if _, _, err := verifiedIdentityClaims(authorization, "https://iam.example.test", "NAS Admins"); err == nil {
				t.Fatal("invalid identity token was accepted")
			}
		})
	}
}

func TestPublicBridgeStripsIdentityAndAuthorizationHeaders(t *testing.T) {
	target, _ := url.Parse("http://127.0.0.1:4181")
	bridge := newOAuth2ReverseProxy(target, "", "")
	request := httptest.NewRequest(http.MethodGet, "http://bridge.test/oauth2/auth", nil)
	request.Header.Set("X-Anas-Identity-Subject", "attacker")
	bridge.Director(request)
	if got := request.Header.Get("X-Anas-Identity-Subject"); got != "" {
		t.Fatalf("upstream received spoofed identity %q", got)
	}
	response := &http.Response{StatusCode: http.StatusAccepted, Header: http.Header{
		"Authorization":           []string{"Bearer secret-token"},
		"X-Anas-Identity-Subject": []string{"spoofed"},
	}}
	if err := bridge.ModifyResponse(response); err != nil {
		t.Fatal(err)
	}
	if response.Header.Get("Authorization") != "" || response.Header.Get("X-Anas-Identity-Subject") != "" {
		t.Fatalf("public bridge leaked identity headers=%v", response.Header)
	}
}

func TestIdentityBridgeEmitsOnlyVerifiedFixedHeaders(t *testing.T) {
	token := testIdentityToken(t, map[string]any{
		"iss": "https://iam.example.test", "sub": "user-123", "auth_time": 1000,
		"exp": 1300, "groups": []string{"NAS Admins"},
	})
	target, _ := url.Parse("http://127.0.0.1:4181")
	bridge := newOAuth2ReverseProxy(target, "https://iam.example.test", "NAS Admins")
	response := &http.Response{StatusCode: http.StatusAccepted, Header: http.Header{
		"Authorization":           []string{"Bearer " + token},
		"X-Anas-Identity-Subject": []string{"spoofed"},
	}}
	if err := bridge.ModifyResponse(response); err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusAccepted || response.Header.Get("Authorization") != "" {
		t.Fatalf("response status=%d headers=%v", response.StatusCode, response.Header)
	}
	want := map[string]string{
		"X-Anas-Identity-Issuer": "https://iam.example.test", "X-Anas-Identity-Subject": "user-123",
		"X-Anas-Identity-Role": "platform_admin", "X-Anas-Identity-Group": "NAS Admins",
		"X-Anas-Identity-Auth-Time": "1000", "X-Anas-Identity-Expires-At": "1300",
		"X-Anas-Identity-Assertion": token,
	}
	for name, value := range want {
		if got := response.Header.Get(name); got != value {
			t.Errorf("%s = %q, want %q", name, got, value)
		}
	}
}

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
