package identity

import (
	"crypto/rand"
	"crypto/rsa"
	"net/url"
	"testing"
	"time"
)

func TestRPLogoutUsesVerifiedDiscoveryAndFixedCallback(t *testing.T) {
	f, o := newOIDCFixture(t)
	if _, err := o.LogoutURL(f.flow.State, "https://mail.example.test/oidc/logout-callback"); err == nil {
		t.Fatal("missing central logout claimed usable")
	}
	f.mu.Lock()
	f.logoutEndpoint = f.server.URL + "/logout"
	f.mu.Unlock()
	o, err := NewOIDC(t.Context(), o.config, f.server.Client())
	if err != nil {
		t.Fatal(err)
	}
	target, err := o.LogoutURL(f.flow.State, "https://mail.example.test/oidc/logout-callback")
	if err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(target)
	if u.Host != f.server.Listener.Addr().String() || u.Query().Get("client_id") != "mail" || u.Query().Get("state") != f.flow.State {
		t.Fatal("logout target changed", target)
	}
	for _, callback := range []string{"https://foreign.example.test/oidc/logout-callback", "https://mail.example.test/foreign", "http://mail.example.test/oidc/logout-callback"} {
		if _, err = o.LogoutURL(f.flow.State, callback); err == nil {
			t.Fatal("unregistered callback accepted")
		}
	}
	f.mu.Lock()
	f.logoutEndpoint = "https://foreign.example.test/logout"
	f.mu.Unlock()
	if _, err = NewOIDC(t.Context(), o.config, f.server.Client()); err == nil {
		t.Fatal("foreign discovery logout endpoint trusted")
	}
}

func TestOIDCBackchannelLogout(t *testing.T) {
	f, o := newOIDCFixture(t)
	base := func() map[string]any {
		return map[string]any{"iss": f.server.URL, "aud": "mail", "sub": testAnchor, "sid": "one-iam-session", "iat": time.Now().Unix(), "jti": "unique-logout", "events": map[string]any{"http://schemas.openid.net/event/backchannel-logout": map[string]any{}}}
	}
	for _, kind := range []string{"both", "subject", "sid", "optional-expiry"} {
		c := base()
		if kind == "subject" {
			delete(c, "sid")
		}
		if kind == "sid" {
			delete(c, "sub")
		}
		if kind == "optional-expiry" {
			c["exp"] = time.Now().Add(time.Minute).Unix()
		}
		l, err := o.VerifyLogout(t.Context(), signedToken(t, f.key, c))
		if err != nil || l.Issuer != f.server.URL || l.ID != "unique-logout" {
			t.Fatal("valid logout rejected", kind, err)
		}
	}
	changes := map[string]func(map[string]any){
		"issuer":           func(c map[string]any) { c["iss"] = "https://other.test" },
		"audience":         func(c map[string]any) { c["aud"] = "other" },
		"azp":              func(c map[string]any) { c["aud"] = []string{"mail", "other"} },
		"missing target":   func(c map[string]any) { delete(c, "sub"); delete(c, "sid") },
		"editable subject": func(c map[string]any) { c["sub"] = "alice@example.test" },
		"nonce":            func(c map[string]any) { c["nonce"] = "" },
		"null nonce":       func(c map[string]any) { c["nonce"] = nil },
		"missing jti":      func(c map[string]any) { delete(c, "jti") },
		"missing event":    func(c map[string]any) { delete(c, "events") },
		"wrong event":      func(c map[string]any) { c["events"] = map[string]any{"other": map[string]any{}} },
		"null event": func(c map[string]any) {
			c["events"] = map[string]any{"http://schemas.openid.net/event/backchannel-logout": nil}
		},
		"event fields": func(c map[string]any) {
			c["events"] = map[string]any{"http://schemas.openid.net/event/backchannel-logout": map[string]any{"scope": "all"}}
		},
		"old":     func(c map[string]any) { c["iat"] = time.Now().Add(-6 * time.Minute).Unix() },
		"future":  func(c map[string]any) { c["iat"] = time.Now().Add(time.Minute).Unix() },
		"expired": func(c map[string]any) { c["exp"] = time.Now().Add(-time.Second).Unix() },
	}
	for name, change := range changes {
		t.Run(name, func(t *testing.T) {
			c := base()
			change(c)
			if _, err := o.VerifyLogout(t.Context(), signedToken(t, f.key, c)); err != ErrAuthentication {
				t.Fatal("invalid logout accepted", err)
			}
		})
	}
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := o.VerifyLogout(t.Context(), signedToken(t, key, base())); err != ErrAuthentication {
		t.Fatal("untrusted signature accepted")
	}
	// An ordinary login ID token must never become a logout notification.
	if _, err := o.VerifyLogout(t.Context(), signedToken(t, f.key, f.claims)); err != ErrAuthentication {
		t.Fatal("login token used as logout")
	}
}
