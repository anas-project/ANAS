// TEST_CASES: MAILMOD-T-037
package main

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSetupDNSIsBoundedAndAdministratorOnly(t *testing.T) {
	for _, mode := range []string{"observed", "expected-match", "expected-missing", "expected-nxdomain", "expected-unavailable", "expected-invalid", "null-mx", "mixed-null-mx", "invalid-null-preference", "ip-not-found", "ip-failure", "ip-partial", "ip-invalid", "ip-too-many", "ip-duplicate-host", "ip-revoked", "not-found", "failure", "too-many", "invalid", "origin", "revoked", "not-admin", "role-removed"} {
		t.Run(mode, func(t *testing.T) {
			h, login, _, dir := loginFixture(t)
			h.store = &setupHTTPStore{httpStore: login}
			admin := &setupHTTPDirectory{httpDirectory: dir, admin: mode != "not-admin"}
			h.directory = admin
			calls := 0
			h.lookupMX = func(ctx context.Context, domain string) ([]*net.MX, error) {
				calls++
				if _, ok := ctx.Deadline(); !ok || domain != "example.test" {
					t.Fatal("unbounded DNS or unexpected domain")
				}
				switch mode {
				case "not-found", "expected-nxdomain":
					return nil, &net.DNSError{IsNotFound: true}
				case "failure", "expected-unavailable":
					return nil, errors.New("private resolver details")
				case "too-many":
					return make([]*net.MX, 21), nil
				case "null-mx":
					return []*net.MX{{Host: ".", Pref: 0}}, nil
				case "mixed-null-mx":
					return []*net.MX{{Host: ".", Pref: 0}, {Host: "mx.example.test.", Pref: 10}}, nil
				case "invalid-null-preference":
					return []*net.MX{{Host: ".", Pref: 10}}, nil
				case "ip-duplicate-host":
					return []*net.MX{{Host: "mx.example.test.", Pref: 10}, {Host: "mx.example.test.", Pref: 20}}, nil
				case "revoked":
					login.sessionErr = errors.New("revoked during lookup")
				case "role-removed":
					admin.admin = false
				}
				return []*net.MX{{Host: "mx.example.test.", Pref: 10}}, nil
			}
			ipCalls := 0
			h.lookupIP = func(ctx context.Context, family, host string) ([]net.IP, error) {
				ipCalls++
				if _, ok := ctx.Deadline(); !ok || host != "mx.example.test" || family != "ip4" && family != "ip6" {
					t.Fatal("unbounded address lookup or unexpected MX host")
				}
				switch mode {
				case "ip-not-found":
					return nil, &net.DNSError{IsNotFound: true}
				case "ip-failure":
					return nil, errors.New("private address resolver details")
				case "ip-partial":
					if family == "ip6" {
						return nil, errors.New("private address resolver details")
					}
				case "ip-invalid":
					return []net.IP{net.ParseIP("192.0.2.1"), nil}, nil
				case "ip-too-many":
					return make([]net.IP, 33), nil
				case "ip-revoked":
					login.sessionErr = errors.New("revoked during address lookup")
				}
				if family == "ip4" {
					return []net.IP{net.ParseIP("192.0.2.1"), net.ParseIP("192.0.2.1")}, nil
				}
				return []net.IP{net.ParseIP("2001:db8::1")}, nil
			}
			body := `{"domain":"example.test"}`
			if mode == "invalid" {
				body = `{"domain":"https://example.test"}`
			}
			switch mode {
			case "expected-match", "null-mx", "mixed-null-mx", "invalid-null-preference":
				body = `{"domain":"example.test","expected_mx":"mx.example.test"}`
			case "expected-missing", "expected-unavailable", "expected-nxdomain":
				body = `{"domain":"example.test","expected_mx":"different.example.test"}`
			case "expected-invalid":
				body = `{"domain":"example.test","expected_mx":"https://mx.example.test"}`
			}
			r := httptest.NewRequest("POST", "https://mail.example.test/api/setup/dns", strings.NewReader(body))
			r.AddCookie(&http.Cookie{Name: sessionCookie, Value: "session-secret"})
			r.Header.Set("Content-Type", "application/json")
			r.Header.Set("Origin", "https://mail.example.test")
			if mode == "origin" {
				r.Header.Set("Origin", "https://evil.test")
			}
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if mode == "invalid" || mode == "expected-invalid" || mode == "origin" || mode == "not-admin" {
				if w.Code < 400 || calls != 0 {
					t.Fatal("invalid request reached DNS", w.Code, calls)
				}
				return
			}
			if mode == "revoked" || mode == "role-removed" || mode == "ip-revoked" {
				if w.Code < 400 || strings.Contains(w.Body.String(), "mx.example.test") {
					t.Fatal("revoked caller received results")
				}
				return
			}
			if w.Code != 200 || strings.Contains(w.Body.String(), "private resolver details") {
				t.Fatal("unexpected DNS response", w.Code)
			}
			want := map[string]string{"observed": "observed", "expected-match": "observed", "expected-missing": "observed", "expected-nxdomain": "not-found", "expected-unavailable": "unknown", "null-mx": "null-mx", "mixed-null-mx": "invalid-mx", "invalid-null-preference": "invalid-mx", "ip-not-found": "observed", "ip-failure": "observed", "ip-partial": "observed", "ip-invalid": "observed", "ip-too-many": "observed", "ip-duplicate-host": "observed", "not-found": "not-found", "failure": "unknown", "too-many": "unknown"}[mode]
			if !strings.Contains(w.Body.String(), `"status":"`+want+`"`) {
				t.Fatal("incorrect observation", w.Body.String())
			}
			if expected, ok := map[string]string{"expected-match": "observed", "expected-missing": "not-observed", "expected-nxdomain": "not-observed", "expected-unavailable": "unknown", "null-mx": "not-observed", "mixed-null-mx": "unknown", "invalid-null-preference": "unknown"}[mode]; ok && !strings.Contains(w.Body.String(), `"expected_status":"`+expected+`"`) {
				t.Fatal("incorrect expected target observation", w.Body.String())
			}
			if want == "observed" {
				expected := "observed"
				if mode == "ip-not-found" {
					expected = "not-found"
				} else if mode == "ip-failure" || mode == "ip-invalid" || mode == "ip-too-many" {
					expected = "unknown"
				}
				if !strings.Contains(w.Body.String(), `"a_status":"`+expected+`"`) || ipCalls != 2 || strings.Contains(w.Body.String(), "private address resolver details") {
					t.Fatal("incorrect address observation", w.Body.String(), ipCalls)
				}
				v6Expected := expected
				if mode == "ip-partial" {
					v6Expected = "unknown"
				}
				if !strings.Contains(w.Body.String(), `"aaaa_status":"`+v6Expected+`"`) {
					t.Fatal("incorrect IPv6 observation", w.Body.String())
				}
				if expected == "observed" && !strings.Contains(w.Body.String(), `"192.0.2.1"`) || v6Expected == "observed" && !strings.Contains(w.Body.String(), `"2001:db8::1"`) {
					t.Fatal("missing A/AAAA observation", w.Body.String())
				}
			} else if ipCalls != 0 {
				t.Fatal("unexpected address lookup for invalid or unavailable MX", ipCalls)
			}
		})
	}
}

func TestSetupDNSKeepsTemporaryFailuresUnknown(t *testing.T) {
	for _, tc := range []struct {
		name      string
		mxFailure bool
		timeout   bool
	}{
		{"mx-timeout", true, true},
		{"mx-temporary", true, false},
		{"address-timeout", false, true},
		{"address-temporary", false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, login, _, dir := loginFixture(t)
			h.store = &setupHTTPStore{httpStore: login}
			h.directory = &setupHTTPDirectory{httpDirectory: dir, admin: true}
			failure := &net.DNSError{IsNotFound: true, IsTimeout: tc.timeout, IsTemporary: !tc.timeout}
			h.lookupMX = func(context.Context, string) ([]*net.MX, error) {
				if tc.mxFailure {
					return nil, failure
				}
				return []*net.MX{{Host: "mx.example.test.", Pref: 10}}, nil
			}
			h.lookupIP = func(context.Context, string, string) ([]net.IP, error) { return nil, failure }
			r := httptest.NewRequest("POST", "https://mail.example.test/api/setup/dns", strings.NewReader(`{"domain":"example.test","expected_mx":"mx.example.test"}`))
			r.AddCookie(&http.Cookie{Name: sessionCookie, Value: "session-secret"})
			r.Header.Set("Content-Type", "application/json")
			r.Header.Set("Origin", "https://mail.example.test")
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			var result struct {
				Status         string `json:"status"`
				ExpectedStatus string `json:"expected_status"`
				Records        []struct {
					AStatus    string `json:"a_status"`
					AAAAStatus string `json:"aaaa_status"`
				} `json:"records"`
			}
			if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &result) != nil {
				t.Fatal("DNS observation failed", w.Code)
			}
			if tc.mxFailure {
				if result.Status != "unknown" || result.ExpectedStatus != "unknown" || len(result.Records) != 0 {
					t.Fatal("temporary MX failure reported as absent", result)
				}
			} else if result.Status != "observed" || result.ExpectedStatus != "observed" || len(result.Records) != 1 || result.Records[0].AStatus != "unknown" || result.Records[0].AAAAStatus != "unknown" {
				t.Fatal("temporary address failure reported as absent", result)
			}
		})
	}
}

func TestSetupDNSRejectsAmbiguousRequestsBeforeLookup(t *testing.T) {
	for _, tc := range []struct {
		name, body, target string
		contentTypes       []string
	}{
		{"duplicate-domain", `{"domain":"example.test","domain":"other.test"}`, "/api/setup/dns", nil},
		{"escaped-duplicate", `{"domain":"example.test","\u0064omain":"other.test"}`, "/api/setup/dns", nil},
		{"case-variant", `{"domain":"example.test","DOMAIN":"other.test"}`, "/api/setup/dns", nil},
		{"duplicate-expected", `{"domain":"example.test","expected_mx":"mx.example.test","expected_mx":"other.test"}`, "/api/setup/dns", nil},
		{"null-expected", `{"domain":"example.test","expected_mx":null}`, "/api/setup/dns", nil},
		{"duplicate-content-type", `{"domain":"example.test"}`, "/api/setup/dns", []string{"application/json", "application/json"}},
		{"empty-query", `{"domain":"example.test"}`, "/api/setup/dns?", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, login, _, dir := loginFixture(t)
			h.store = &setupHTTPStore{httpStore: login}
			h.directory = &setupHTTPDirectory{httpDirectory: dir, admin: true}
			calls := 0
			h.lookupMX = func(context.Context, string) ([]*net.MX, error) { calls++; return nil, nil }
			r := httptest.NewRequest("POST", "https://mail.example.test"+tc.target, strings.NewReader(tc.body))
			r.AddCookie(&http.Cookie{Name: sessionCookie, Value: "session-secret"})
			r.Header.Set("Origin", "https://mail.example.test")
			if tc.contentTypes == nil {
				r.Header.Set("Content-Type", "application/json")
			} else {
				for _, v := range tc.contentTypes {
					r.Header.Add("Content-Type", v)
				}
			}
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code < 400 || calls != 0 {
				t.Fatalf("ambiguous request reached DNS: status=%d calls=%d", w.Code, calls)
			}
		})
	}
}
