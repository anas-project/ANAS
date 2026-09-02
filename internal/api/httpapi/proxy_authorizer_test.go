package httpapi

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/anas-project/ANAS/internal/consoleauth"
)

func TestTrustedProxyAuthorizerMapsOnlyFixedUnambiguousAdminIdentity(t *testing.T) {
	now := time.Date(2026, 9, 2, 12, 0, 0, 0, time.UTC)
	store, err := consoleauth.Open(filepath.Join(t.TempDir(), "auth"), consoleauth.AuditSinkFunc(func(context.Context, consoleauth.AuditEvent) error { return nil }), consoleauth.StoreOptions{Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "https://anas.example.test:9000/api/v1/auth/session", nil)
	setTrustedProxyTestHeaders(request, now)
	identity, err := parseTrustedProxyIdentity(request.Header, "https://iam.example.test", "NAS Admins", now)
	if err != nil {
		t.Fatal(err)
	}
	session, err := store.RefreshProxySession(context.Background(), consoleauth.ProxySessionRefreshRequest{Origin: "https://anas.example.test:9000", Identity: identity})
	if err != nil {
		t.Fatal(err)
	}
	authorize, err := TrustedProxyAuthorizer(TrustedProxyOptions{
		Authenticator: store, ExpectedIssuer: "https://iam.example.test",
		ExpectedDirectoryGroup: "NAS Admins", Now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	request = httptest.NewRequest(http.MethodGet, "https://anas.example.test:9000/api/v1/workspaces/main/config", nil)
	setTrustedProxyTestHeaders(request, now)
	request.AddCookie(&http.Cookie{Name: proxySessionCookie, Value: session.Token})
	principal, err := authorize(request, AuthorizationRequest{Policy: RoutePolicy{Permission: PermissionConfigRead}})
	if err != nil {
		t.Fatal(err)
	}
	if principal.Role != "owner" || principal.Source != "oidc_proxy" || principal.SemanticRole != "platform_admin" || principal.DirectoryGroup != "NAS Admins" || principal.Issuer != "https://iam.example.test" || principal.Subject != "subject-123" {
		t.Fatalf("proxy principal = %#v", principal)
	}

	for name, mutate := range map[string]func(*http.Request){
		"duplicate issuer": func(request *http.Request) { request.Header.Add(proxyIssuerHeader, "https://iam.example.test") },
		"comma subject":    func(request *http.Request) { request.Header.Set(proxySubjectHeader, "subject-123,other") },
		"wrong role":       func(request *http.Request) { request.Header.Set(proxyRoleHeader, "owner") },
		"wrong group":      func(request *http.Request) { request.Header.Set(proxyGroupHeader, "Users") },
		"missing issuer":   func(request *http.Request) { request.Header.Del(proxyIssuerHeader) },
	} {
		t.Run(name, func(t *testing.T) {
			invalid := request.Clone(context.Background())
			invalid.Header = request.Header.Clone()
			mutate(invalid)
			if _, err := authorize(invalid, AuthorizationRequest{Policy: RoutePolicy{Permission: PermissionConfigRead}}); !errors.Is(err, ErrUnauthenticated) {
				t.Fatalf("invalid proxy identity error = %v", err)
			}
		})
	}
}

func TestTrustedProxyAuthorizerRequiresBoundSessionExceptAtSessionBootstrap(t *testing.T) {
	now := time.Date(2026, 9, 2, 12, 0, 0, 0, time.UTC)
	store, err := consoleauth.Open(filepath.Join(t.TempDir(), "auth"), consoleauth.AuditSinkFunc(func(context.Context, consoleauth.AuditEvent) error { return nil }), consoleauth.StoreOptions{Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	authorize, err := TrustedProxyAuthorizer(TrustedProxyOptions{Authenticator: store, ExpectedIssuer: "https://iam.example.test", ExpectedDirectoryGroup: "NAS Admins", Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "https://anas.example.test:9000/api/v1/auth/session", nil)
	setTrustedProxyTestHeaders(request, now)
	if _, err := authorize(request, AuthorizationRequest{Policy: RoutePolicy{Permission: PermissionAuthSession}}); err != nil {
		t.Fatalf("session bootstrap identity rejected: %v", err)
	}
	if _, err := authorize(request, AuthorizationRequest{Policy: RoutePolicy{Permission: PermissionConfigRead}}); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("capability route without proxy session error = %v", err)
	}
}

func setTrustedProxyTestHeaders(request *http.Request, now time.Time) {
	request.Header.Set(proxyIssuerHeader, "https://iam.example.test")
	request.Header.Set(proxySubjectHeader, "subject-123")
	request.Header.Set(proxyRoleHeader, "platform_admin")
	request.Header.Set(proxyGroupHeader, "NAS Admins")
	request.Header.Set(proxyAuthTimeHeader, strconv.FormatInt(now.Add(-time.Minute).Unix(), 10))
	request.Header.Set(proxyExpiresHeader, strconv.FormatInt(now.Add(time.Hour).Unix(), 10))
	request.Header.Set(proxyAssertionHeader, "opaque-id-token")
}
