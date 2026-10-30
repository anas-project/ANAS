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
	"sync/atomic"
	"testing"
	"time"
)

func TestOutboundDNSChecksExactNamesAndDoesNotClaimAuthentication(t *testing.T) {
	for _, mode := range []string{"observed", "missing-selector", "missing", "multiple", "partial-failure", "ambiguous-error", "too-many", "oversized", "malformed-version", "invalid-domain", "invalid-selector", "duplicate-key", "origin", "not-admin", "revoked"} {
		t.Run(mode, func(t *testing.T) {
			h, login, _, dir := loginFixture(t)
			h.store = &setupHTTPStore{httpStore: login}
			admin := &setupHTTPDirectory{httpDirectory: dir, admin: mode != "not-admin"}
			h.directory = admin
			var calls atomic.Int32
			h.lookupTXT = func(ctx context.Context, name string) ([]string, error) {
				calls.Add(1)
				if _, ok := ctx.Deadline(); !ok {
					t.Error("DNS query has no deadline")
				}
				switch name {
				case "send.example.test":
					if mode == "revoked" {
						login.sessionErr = errors.New("revoked")
					}
					if mode == "multiple" {
						return []string{"v=spf1 -all", "V=SPF1 +all"}, nil
					}
					if mode == "too-many" {
						return make([]string, 21), nil
					}
					if mode == "oversized" {
						return []string{strings.Repeat("x", 8193)}, nil
					}
					if mode == "ambiguous-error" {
						return nil, &net.DNSError{IsNotFound: true, IsTimeout: true}
					}
					if mode == "malformed-version" {
						return []string{"v=spf10 -all"}, nil
					}
					if mode == "missing" {
						return []string{"unrelated TXT"}, nil
					}
					return []string{"v=spf1 include:private.example -all", "unrelated TXT"}, nil
				case "_dmarc.send.example.test":
					if mode == "partial-failure" {
						return nil, errors.New("private resolver detail")
					}
					if mode == "missing" {
						return nil, &net.DNSError{IsNotFound: true}
					}
					return []string{"v=DMARC1; p=none"}, nil
				case "eusend._domainkey.send.example.test":
					return []string{"v=DKIM1; p=public-key"}, nil
				default:
					t.Errorf("unexpected query: %s", name)
					return nil, errors.New("unexpected")
				}
			}
			body := `{"domain":"send.example.test","selector":"eusend"}`
			switch mode {
			case "missing-selector":
				body = `{"domain":"send.example.test","selector":""}`
			case "invalid-domain":
				body = `{"domain":"https://send.example.test","selector":"eusend"}`
			case "invalid-selector":
				body = `{"domain":"send.example.test","selector":"evil/path"}`
			case "duplicate-key":
				body = `{"domain":"send.example.test","Domain":"evil.test","selector":"eusend"}`
			}
			r := httptest.NewRequest("POST", "https://mail.example.test/api/setup/outbound-dns", strings.NewReader(body))
			r.AddCookie(&http.Cookie{Name: sessionCookie, Value: "session-secret"})
			r.Header.Set("Content-Type", "application/json")
			r.Header.Set("Origin", "https://mail.example.test")
			if mode == "origin" {
				r.Header.Set("Origin", "https://evil.test")
			}
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if mode == "invalid-domain" || mode == "invalid-selector" || mode == "duplicate-key" || mode == "origin" || mode == "not-admin" {
				if w.Code < 400 || calls.Load() != 0 {
					t.Fatal("invalid request reached DNS", w.Code, calls.Load())
				}
				return
			}
			if mode == "revoked" {
				if w.Code < 400 || strings.Contains(w.Body.String(), "send.example.test") {
					t.Fatal("revoked caller received DNS")
				}
				return
			}
			var result struct {
				Domain    string `json:"domain"`
				Selector  string `json:"selector"`
				SPF       string `json:"spf"`
				DMARC     string `json:"dmarc"`
				DKIM      string `json:"dkim"`
				CheckedAt string `json:"checked_at"`
			}
			if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &result) != nil || result.Domain != "send.example.test" || result.CheckedAt == "" || strings.Contains(w.Body.String(), "private") || strings.Contains(w.Body.String(), "public-key") {
				t.Fatal("invalid or sensitive response", w.Code, w.Body.String())
			}
			spf, dmarc, dkim := "observed", "observed", "observed"
			switch mode {
			case "missing-selector":
				dkim = "not-configured"
			case "missing":
				spf = "not-found"
				dmarc = "not-found"
			case "multiple":
				spf = "multiple"
			case "partial-failure":
				dmarc = "unknown"
			case "too-many", "oversized", "ambiguous-error":
				spf = "unknown"
			case "malformed-version":
				spf = "not-found"
			}
			if result.SPF != spf || result.DMARC != dmarc || result.DKIM != dkim {
				t.Fatal("incorrect observations", result)
			}
			wantCalls := int32(3)
			if mode == "missing-selector" {
				wantCalls = 2
			}
			if calls.Load() != wantCalls {
				t.Fatal("unexpected query count", calls.Load())
			}
		})
	}
}

func TestOutboundDNSDoesNotWaitPastRequestDeadline(t *testing.T) {
	h, login, _, dir := loginFixture(t)
	h.store = &setupHTTPStore{httpStore: login}
	h.directory = &setupHTTPDirectory{httpDirectory: dir, admin: true}
	h.lookupTXT = func(ctx context.Context, _ string) ([]string, error) { <-ctx.Done(); return nil, ctx.Err() }
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	r := httptest.NewRequest("POST", "https://mail.example.test/api/setup/outbound-dns", strings.NewReader(`{"domain":"send.example.test","selector":"eusend"}`)).WithContext(ctx)
	r.AddCookie(&http.Cookie{Name: sessionCookie, Value: "session-secret"})
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Origin", "https://mail.example.test")
	w := httptest.NewRecorder()
	started := time.Now()
	h.ServeHTTP(w, r)
	if time.Since(started) > time.Second || w.Code != 200 || !strings.Contains(w.Body.String(), `"spf":"unknown"`) || !strings.Contains(w.Body.String(), `"dkim":"unknown"`) {
		t.Fatal("deadline did not return unknown observations", w.Code, w.Body.String())
	}
}
