package consoleauth

import (
	"net/http"
	"testing"
	"time"
)

func TestNormalizeOrigin(t *testing.T) {
	tests := map[string]string{
		"HTTP://NAS.Example.Test:80/":   "http://nas.example.test",
		"https://NAS.Example.Test:443":  "https://nas.example.test",
		"https://NAS.Example.Test:8443": "https://nas.example.test:8443",
		"http://[0:0:0:0:0:0:0:1]":      "http://[::1]",
		"http://192.0.2.10":             "http://192.0.2.10",
	}
	for input, want := range tests {
		got, err := NormalizeOrigin(input)
		if err != nil || got != want {
			t.Errorf("NormalizeOrigin(%q) = %q, %v, want %q", input, got, err, want)
		}
	}
	for _, input := range []string{
		"", "null", " nas.example", "ftp://nas.example", "http://user@nas.example",
		"http://nas.example/path", "http://nas.example?query=1", "http://nas.example#fragment",
		"http://nas_example", "http://[::1%25lo0]", "http://nas.example:", "http://nas.example:0", "http://nas.example:65536",
	} {
		if got, err := NormalizeOrigin(input); err == nil {
			t.Errorf("NormalizeOrigin(%q) = %q", input, got)
		}
	}
}

func TestSessionCookieSecurityByState(t *testing.T) {
	expires := time.Date(2026, 8, 29, 3, 0, 0, 0, time.UTC)
	bootstrap, err := SessionCookie("anas_bootstrap", "credential", StateBootstrap, expires)
	if err != nil {
		t.Fatal(err)
	}
	if bootstrap.Secure || !bootstrap.HttpOnly || bootstrap.SameSite != http.SameSiteStrictMode || bootstrap.Path != "/" || bootstrap.Domain != "" || !bootstrap.Expires.Equal(expires) {
		t.Fatalf("bootstrap cookie = %#v", bootstrap)
	}
	for _, state := range []ConsoleState{StateEnrollment, StateFull} {
		cookie, err := SessionCookie("anas_session", "credential", state, expires)
		if err != nil {
			t.Fatal(err)
		}
		if !cookie.Secure || !cookie.HttpOnly || cookie.Domain != "" || cookie.Path != "/" || cookie.SameSite != http.SameSiteStrictMode {
			t.Fatalf("%s cookie = %#v", state, cookie)
		}
	}
	expired, err := ExpiredSessionCookie("anas_session", StateFull)
	if err != nil || expired.MaxAge != -1 || !expired.Secure || expired.Domain != "" {
		t.Fatalf("expired cookie = %#v, %v", expired, err)
	}
	if _, err := SessionCookie("bad name", "credential", StateFull, expires); err == nil {
		t.Fatal("invalid cookie name was accepted")
	}
	if _, err := SessionCookie("anas_session", "bad\nvalue", StateFull, expires); err == nil {
		t.Fatal("invalid cookie value was accepted")
	}
	if _, err := SessionCookie(LocalSessionCookieName, "credential", StateBootstrap, expires); err == nil {
		t.Fatal("secure cookie prefix was accepted for bootstrap")
	}
	if _, err := SessionCookie("anas_session", "credential", StateFull, time.Time{}); err == nil {
		t.Fatal("zero cookie expiry was accepted")
	}
	if _, err := SessionCookie("anas_session", "credential", ConsoleState("unknown"), expires); err == nil {
		t.Fatal("invalid cookie state was accepted")
	}
}

func TestEnrollmentCSRFCookieIsHostOnlySecureAndSPAReadable(t *testing.T) {
	expires := time.Date(2026, 8, 29, 3, 15, 0, 0, time.UTC)
	cookie, err := EnrollmentCSRFCookie("csrf_credential", expires)
	if err != nil {
		t.Fatal(err)
	}
	if cookie.Name != EnrollmentCSRFCookieName || cookie.Value != "csrf_credential" ||
		!cookie.Secure || cookie.HttpOnly || cookie.SameSite != http.SameSiteStrictMode ||
		cookie.Path != "/" || cookie.Domain != "" || !cookie.Expires.Equal(expires) {
		t.Fatalf("enrollment CSRF cookie = %#v", cookie)
	}
	expired := ExpiredEnrollmentCSRFCookie()
	if expired.Name != EnrollmentCSRFCookieName || expired.Value != "" || expired.MaxAge != -1 ||
		!expired.Secure || expired.HttpOnly || expired.SameSite != http.SameSiteStrictMode ||
		expired.Path != "/" || expired.Domain != "" {
		t.Fatalf("expired enrollment CSRF cookie = %#v", expired)
	}
	if _, err := EnrollmentCSRFCookie("bad\nvalue", expires); err == nil {
		t.Fatal("invalid enrollment CSRF cookie value was accepted")
	}
	if _, err := EnrollmentCSRFCookie("csrf_credential", time.Time{}); err == nil {
		t.Fatal("zero enrollment CSRF cookie expiry was accepted")
	}
}
