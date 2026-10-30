// TEST_CASES: LLNG-READY-T-002
package main

import (
	"context"
	"testing"
	"time"
)

func TestPasskeyEvidenceRequiresVerifiedLLNGAMR(t *testing.T) {
	f := newFixture(t)
	for _, tc := range []struct {
		name, schema    string
		amr             any
		passkey, denied bool
	}{
		{"native-passkey", "llng-account/v1", []string{"pop", "webauthn"}, true, false},
		{"password", "llng-account/v1", []string{"pwd"}, false, false},
		{"proof-of-possession-is-ambiguous", "llng-account/v1", []string{"pop"}, false, false},
		{"missing-method", "llng-account/v1", nil, false, false},
		{"contradictory-method", "llng-account/v1", []string{"pwd", "webauthn"}, false, false},
		{"other-provider", "casdoor-account/v1", []string{"webauthn"}, false, false},
		{"malformed-method", "llng-account/v1", "webauthn", false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f.server.oidc.config.AccountSchema = tc.schema
			f.claims = map[string]any{"iss": f.iam.URL, "aud": "portal", "sub": f.person.Anchor, "nonce": "nonce", "auth_time": time.Now().Unix(), "exp": time.Now().Add(time.Hour).Unix(), "iat": time.Now().Unix(), "amr": tc.amr}
			a, err := f.server.oidc.exchange(context.Background(), Flow{Nonce: "nonce", Verifier: "verifier"}, "code")
			if (err != nil) != tc.denied || a.Passkey != tc.passkey {
				t.Fatalf("passkey=%v, denied=%v", a.Passkey, err != nil)
			}
			// A valid method value must never override the signed token bindings.
			f.claims["nonce"] = "unrelated-flow"
			if _, err := f.server.oidc.exchange(context.Background(), Flow{Nonce: "nonce"}, "code"); err == nil {
				t.Fatal("AMR bypassed token verification")
			}
		})
	}
}

func TestPasskeyPortalSessionAndWritesRespectDirectoryState(t *testing.T) {
	for _, tc := range []struct {
		name    string
		change  func(*Person)
		allowed bool
	}{
		{"normal", func(*Person) {}, true},
		{"must-change-password", func(p *Person) { p.MustChangePassword = true }, true},
		{"password-expired", func(p *Person) { p.PasswordExpired = true }, true},
		{"disabled", func(p *Person) { p.Disabled = true }, false},
		{"locked", func(p *Person) { p.Locked = true }, false},
		{"account-expired", func(p *Person) { p.AccountExpired = true }, false},
		{"state-unavailable", func(p *Person) { p.PasswordStateKnown = false }, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			p := f.person
			tc.change(&p)
			f.directory.people[p.Anchor] = p
			f.session.Passkey = true
			f.server.store.add(f.session)
			response := f.request("GET", "/api/me", "")
			if (response.Code == 200) != tc.allowed {
				t.Fatalf("current session returned %d", response.Code)
			}
			response = f.request("POST", "/api/password/change", `{"old_password":"OldSecret123!","new_password":"NewSecret123!","confirm_password":"NewSecret123!"}`)
			if (response.Code == 200) != tc.allowed || (f.directory.writes == 1) != tc.allowed {
				t.Fatalf("password write returned %d, writes=%d", response.Code, f.directory.writes)
			}
			if (p.MustChangePassword || p.PasswordExpired) && p.canLogin() {
				t.Fatal("password-only checks accepted an expired password")
			}
		})
	}
}
