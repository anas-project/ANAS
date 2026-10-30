//go:build linux

// TEST_CASES: MAILMOD-T-037
package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

// This is an explicit read-only observation of the user-authorized test
// domain, from the machine executing this test. It does not test SMTP.
func TestSetupDNSFinanceLive(t *testing.T) {
	if os.Getenv("MAIL_DNS_FINANCE_LIVE") != "1" {
		t.Skip("explicit finance DNS fixture required")
	}
	h, login, _, directory := loginFixture(t)
	h.store = &setupHTTPStore{httpStore: login}
	h.directory = &setupHTTPDirectory{httpDirectory: directory, admin: true}
	r := httptest.NewRequest("POST", "https://mail.example.test/api/setup/dns", strings.NewReader(`{"domain":"mail-poc.bengbu.cc.cd"}`))
	r.AddCookie(&http.Cookie{Name: sessionCookie, Value: "session-secret"})
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Origin", "https://mail.example.test")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	var result struct {
		Domain    string `json:"domain"`
		Status    string `json:"status"`
		CheckedAt string `json:"checked_at"`
		Records   []struct {
			Host       string   `json:"host"`
			A          []string `json:"a"`
			AAAA       []string `json:"aaaa"`
			AStatus    string   `json:"a_status"`
			AAAAStatus string   `json:"aaaa_status"`
		} `json:"records"`
	}
	if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &result) != nil || result.Domain != "mail-poc.bengbu.cc.cd" || result.Status != "observed" || result.CheckedAt == "" || len(result.Records) != 3 {
		t.Fatal("live MX observation unavailable or changed", w.Code, result.Status, len(result.Records))
	}
	wanted := map[string]bool{"route1.mx.cloudflare.net": false, "route2.mx.cloudflare.net": false, "route3.mx.cloudflare.net": false}
	for _, record := range result.Records {
		if _, ok := wanted[record.Host]; !ok || wanted[record.Host] || record.AStatus != "observed" || record.AAAAStatus != "observed" || len(record.A) == 0 || len(record.AAAA) == 0 {
			t.Fatal("live MX host or separate address observation unavailable", record.Host, record.AStatus, record.AAAAStatus)
		}
		wanted[record.Host] = true
	}
	t.Log("read-only finance DNS handler observed three Cloudflare MX hosts and both address families; no SMTP verdict")
}

func TestSetupOutboundDNSFinanceLive(t *testing.T) {
	if os.Getenv("MAIL_DNS_FINANCE_LIVE") != "1" {
		t.Skip("explicit finance DNS fixture required")
	}
	h, login, _, directory := loginFixture(t)
	h.store = &setupHTTPStore{httpStore: login}
	h.directory = &setupHTTPDirectory{httpDirectory: directory, admin: true}
	r := httptest.NewRequest("POST", "https://mail.example.test/api/setup/outbound-dns", strings.NewReader(`{"domain":"mail-poc.hlong.wang","selector":"eusend"}`))
	r.AddCookie(&http.Cookie{Name: sessionCookie, Value: "session-secret"})
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Origin", "https://mail.example.test")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	var result struct{ Domain, Selector, SPF, DMARC, DKIM, CheckedAt string }
	if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &result) != nil || result.Domain != "mail-poc.hlong.wang" || result.Selector != "eusend" {
		t.Fatal("live sending DNS handler failed", w.Code)
	}
	// A missing record is an observation, not an error. Resolver failure is not
	// allowed to masquerade as a completed live check.
	t.Logf("read-only exact sending DNS: SPF=%s DMARC=%s DKIM=%s; no policy, authentication or delivery verdict", result.SPF, result.DMARC, result.DKIM)
	for _, state := range []string{result.SPF, result.DMARC, result.DKIM} {
		if state != "observed" && state != "not-found" && state != "multiple" {
			t.Fatal("live sending DNS incomplete", state)
		}
	}
}
