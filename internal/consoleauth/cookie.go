package consoleauth

import (
	"errors"
	"net/http"
	"regexp"
	"strings"
	"time"
)

var cookieNamePattern = regexp.MustCompile(`^[!#$%&'*+\-.^_` + "`" + `|~0-9A-Za-z]+$`)
var cookieValuePattern = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

const (
	BootstrapSessionCookieName  = "anas_bootstrap_session"
	EnrollmentSessionCookieName = "__Host-anas_enrollment_session"
	EnrollmentCSRFCookieName    = "__Host-anas_enrollment_csrf"
	LocalSessionCookieName      = "__Host-anas_local_session"
	ProxySessionCookieName      = "__Host-anas_proxy_session"
)

// SessionCookie returns a host-only, HttpOnly, SameSite=Strict session cookie.
// Bootstrap is the sole state allowed without Secure; enrollment and full are
// always Secure. Domain is intentionally left empty and Path is always root.
func SessionCookie(name, value string, state ConsoleState, expiresAt time.Time) (*http.Cookie, error) {
	if !cookieNamePattern.MatchString(name) || !cookieValuePattern.MatchString(value) || expiresAt.IsZero() {
		return nil, errors.New("session cookie name and value are required")
	}
	secure, err := cookieSecure(state)
	if err != nil {
		return nil, err
	}
	if !secure && (strings.HasPrefix(name, "__Host-") || strings.HasPrefix(name, "__Secure-")) {
		return nil, errors.New("secure cookie prefix requires enrollment or full state")
	}
	return &http.Cookie{
		Name:     name,
		Value:    value,
		Path:     "/",
		Expires:  expiresAt.UTC(),
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteStrictMode,
	}, nil
}

// ExpiredSessionCookie returns a cookie deletion directive with the same
// security attributes as SessionCookie.
func ExpiredSessionCookie(name string, state ConsoleState) (*http.Cookie, error) {
	if !cookieNamePattern.MatchString(name) {
		return nil, errors.New("session cookie name is required")
	}
	secure, err := cookieSecure(state)
	if err != nil {
		return nil, err
	}
	if !secure && (strings.HasPrefix(name, "__Host-") || strings.HasPrefix(name, "__Secure-")) {
		return nil, errors.New("secure cookie prefix requires enrollment or full state")
	}
	return &http.Cookie{
		Name:     name,
		Value:    "",
		Path:     "/",
		Expires:  time.Unix(1, 0).UTC(),
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteStrictMode,
	}, nil
}

// EnrollmentCSRFCookie returns the TLS-only, host-only double-submit cookie
// used by the enrollment SPA. Unlike a session cookie it is intentionally
// readable by same-origin JavaScript so the SPA can copy its value into the
// X-CSRF-Token header. The server still validates the value against the digest
// bound to the HttpOnly enrollment session.
func EnrollmentCSRFCookie(value string, expiresAt time.Time) (*http.Cookie, error) {
	if !cookieValuePattern.MatchString(value) || expiresAt.IsZero() {
		return nil, errors.New("enrollment CSRF cookie value and expiry are required")
	}
	return &http.Cookie{
		Name:     EnrollmentCSRFCookieName,
		Value:    value,
		Path:     "/",
		Expires:  expiresAt.UTC(),
		Secure:   true,
		HttpOnly: false,
		SameSite: http.SameSiteStrictMode,
	}, nil
}

// ExpiredEnrollmentCSRFCookie returns a deletion directive with the same
// scope and security attributes as EnrollmentCSRFCookie.
func ExpiredEnrollmentCSRFCookie() *http.Cookie {
	return &http.Cookie{
		Name:     EnrollmentCSRFCookieName,
		Value:    "",
		Path:     "/",
		Expires:  time.Unix(1, 0).UTC(),
		MaxAge:   -1,
		Secure:   true,
		HttpOnly: false,
		SameSite: http.SameSiteStrictMode,
	}
}

func cookieSecure(state ConsoleState) (bool, error) {
	switch state {
	case StateBootstrap:
		return false, nil
	case StateEnrollment, StateFull:
		return true, nil
	default:
		return false, errors.New("session cookie state is invalid")
	}
}
