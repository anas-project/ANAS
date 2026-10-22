// TEST_CASES: PORTALUX-T-002
package main

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func (d *accountFake) VerifyActor(_ Person, password string) string {
	d.verifyCalls++
	if password != "operator-proof" {
		return "credentials_rejected"
	}
	return ""
}

func staleFixture(t *testing.T) (*fixture, *accountFake) {
	t.Helper()
	f, d := adminFixture(t)
	f.session.AuthTime = time.Now().Add(-time.Hour)
	f.server.store.add(f.session)
	return f, d
}

func TestPortalPasswordCheckEstablishesRecentAuthentication(t *testing.T) {
	f, d := staleFixture(t)
	unlock := AdminOperation{Action: "account.unlock", Target: "target-anchor"}
	if r := operationRequest(f, "/api/administration/execute", unlock); r.Code != 403 || !strings.Contains(r.Body.String(), "recent_authentication_required") || d.writes != 0 {
		t.Fatal("stale session wrote", r.Code)
	}
	if r := f.request("POST", "/api/reauth/password", `{"password":"another-person-proof"}`); r.Code != 403 || !strings.Contains(r.Body.String(), "authentication_rejected") {
		t.Fatal("wrong password accepted", r.Code)
	}
	if r := operationRequest(f, "/api/administration/execute", unlock); r.Code != 403 || d.writes != 0 {
		t.Fatal("rejected password made the session recent")
	}
	if r := f.request("POST", "/api/reauth/password", `{"password":"operator-proof"}`); r.Code != 200 {
		t.Fatal("own password rejected", r.Code, r.Body.String())
	}
	if r := operationRequest(f, "/api/administration/execute", unlock); r.Code != 200 || d.writes != 1 {
		t.Fatal("verified session could not write", r.Code)
	}
	if !strings.Contains(f.request("GET", "/api/me", "").Body.String(), `"recent_authentication":true`) {
		t.Fatal("account view does not report recent authentication")
	}
	records, _ := json.Marshal(f.server.audit.list())
	if !strings.Contains(string(records), `"action":"session.reauth"`) || !strings.Contains(string(records), `"directory":"saved"`) || strings.Contains(string(records), "operator-proof") || strings.Contains(string(records), "another-person-proof") {
		t.Fatal("password checks must be audited without passwords", string(records))
	}
	// The window still applies to a portal password check.
	v, _ := f.server.store.get(f.session.ID)
	v.VerifiedAt = time.Now().Add(-f.server.config.RecentWindow - time.Second)
	f.server.store.add(v)
	if r := operationRequest(f, "/api/administration/execute", unlock); r.Code != 403 {
		t.Fatal("expired password check still counted", r.Code)
	}
}

func TestPortalPasswordCheckRefusesInactiveAccountsAlike(t *testing.T) {
	for _, change := range []string{"disabled", "locked", "must_change"} {
		f, d := staleFixture(t)
		p := d.people[f.person.Anchor]
		switch change {
		case "disabled":
			p.Disabled = true
		case "locked":
			p.Locked = true
		case "must_change":
			p.MustChangePassword = true
		}
		d.people[p.Anchor] = p
		r := f.request("POST", "/api/reauth/password", `{"password":"operator-proof"}`)
		if r.Code != 403 && r.Code != 401 || r.Code == 403 && !strings.Contains(r.Body.String(), "authentication_rejected") {
			t.Fatal(change, "account passed the password check", r.Code, r.Body.String())
		}
		if v, _ := f.server.store.get(f.session.ID); !v.VerifiedAt.IsZero() {
			t.Fatal(change, "account was marked recent")
		}
	}
}

func TestPortalPasswordCheckNeedsSessionCSRFAndRateLimit(t *testing.T) {
	f, d := staleFixture(t)
	if r := f.request("POST", "/api/reauth/password", `{"password":"operator-proof","target":"target-anchor"}`); r.Code != 400 {
		t.Fatal("extra field accepted", r.Code)
	}
	csrf := f.session.CSRF
	f.session.CSRF = "forged"
	if r := f.request("POST", "/api/reauth/password", `{"password":"operator-proof"}`); r.Code != 403 || d.verifyCalls != 0 {
		t.Fatal("forged CSRF reached the directory", r.Code)
	}
	f.session.CSRF = csrf
	limited := false
	for i := 0; i < 8; i++ {
		if r := f.request("POST", "/api/reauth/password", `{"password":"wrong"}`); r.Code == 429 {
			limited = true
			break
		}
	}
	if !limited {
		t.Fatal("password checks are not rate limited")
	}
	f.session.ID = "missing-session"
	if r := f.request("POST", "/api/reauth/password", `{"password":"operator-proof"}`); r.Code != 401 {
		t.Fatal("anonymous password check accepted", r.Code)
	}
}
