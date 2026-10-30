// TEST_CASES: MAILMOD-T-017
package httpapi

import (
	"context"
	"crypto/tls"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/anas-project/ANAS/internal/application"
	"github.com/anas-project/ANAS/internal/serviceauth"
)

func TestServiceMailOutboundRestrictedRoute(t *testing.T) {
	registry, _ := testRegistry(t, "main")
	allowed, calls := true, 0
	api := &ServiceAPIOptions{
		Authenticate: func(_ context.Context, token, permission, workspace, resource string) (string, error) {
			if token == "bad" {
				return "", serviceauth.ErrUnauthenticated
			}
			if !allowed || token != "outbound-token" || permission != string(PermissionMailOutboundConfigure) || workspace != "main" || resource != "mail" {
				return "", serviceauth.ErrForbidden
			}
			return "mail-service", nil
		},
		Factory: func(string) application.ApplicationCredentialService {
			t.Fatal("credential service exposed")
			return nil
		},
		Audit: func(context.Context, ServiceCredentialAuditEvent) error { return nil },
		MailOutbound: func(ctx context.Context, call ServiceMailOutboundCall) (ServiceMailOutboundResult, error) {
			calls++
			if call.ClientID != "mail-service" || call.WorkspaceID != "main" || call.Request.Password != "synthetic-password" || call.Reauthorize(ctx) != nil {
				t.Fatal("invalid outbound call")
			}
			allowed = false
			if call.Reauthorize(ctx) == nil {
				t.Fatal("revoked grant accepted")
			}
			allowed = true
			return ServiceMailOutboundResult{State: "configured"}, nil
		},
	}
	h, err := New(Options{Registry: registry, Security: SecurityOptions{InitialState: StateFull, HostAllowed: func(*http.Request) bool { return true }}, ServiceAPI: api})
	if err != nil {
		t.Fatal(err)
	}
	h = MailInstallOnly(h)
	path := "/api/v1/service/workspaces/main/applications/mail/mail-outbound-configure"
	valid := `{"actor":"anchor","operation_id":"cfg-0123456789abcdef0123456789abcdef","provider":"smtp","host":"smtp.example.test","port":587,"tls":"starttls","username":"sender","password":"synthetic-password"}`
	for _, tc := range []struct {
		name, path, token, body string
		tls                     bool
		want                    int
	}{
		{"authorized", path, "outbound-token", valid, true, 200},
		{"ingress-only token", path, "ingress-token", valid, true, 403},
		{"bad token", path, "bad", valid, true, 401},
		{"wrong workspace", strings.Replace(path, "/main/", "/other/", 1), "outbound-token", valid, true, 403},
		{"other application", strings.Replace(path, "/mail/", "/other/", 1), "outbound-token", valid, true, 404},
		{"no TLS", path, "outbound-token", valid, false, 404},
		{"duplicate password", path, "outbound-token", strings.Replace(valid, `"password":"synthetic-password"`, `"password":"synthetic-password","password":"other"`, 1), true, 400},
		{"case alias", path, "outbound-token", strings.Replace(valid, `"provider"`, `"Provider"`, 1), true, 400},
		{"unknown field", path, "outbound-token", strings.Replace(valid, `"actor"`, `"unexpected":"x","actor"`, 1), true, 400},
		{"direct with credential", path, "outbound-token", strings.Replace(valid, `"provider":"smtp"`, `"provider":"direct"`, 1), true, 400},
		{"invalid operation", path, "outbound-token", strings.Replace(valid, "cfg-0123456789abcdef0123456789abcdef", "other", 1), true, 400},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before := calls
			r := httptest.NewRequest(http.MethodPost, tc.path, strings.NewReader(tc.body))
			r.Header.Set("Content-Type", "application/json")
			r.Header.Set("Authorization", "Bearer "+tc.token)
			if tc.tls {
				r.TLS = &tls.ConnectionState{}
			}
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != tc.want || strings.Contains(w.Body.String(), "synthetic-password") {
				t.Fatalf("status %d want %d or secret returned", w.Code, tc.want)
			}
			if tc.name == "authorized" {
				before++
			}
			if calls != before {
				t.Fatal("unexpected handler call")
			}
		})
	}
}

func TestServiceMailOutboundHidesUnknownFailure(t *testing.T) {
	registry, _ := testRegistry(t, "main")
	api := &ServiceAPIOptions{Authenticate: func(context.Context, string, string, string, string) (string, error) { return "mail-service", nil }, Factory: func(string) application.ApplicationCredentialService { return nil }, Audit: func(context.Context, ServiceCredentialAuditEvent) error { return nil }, MailOutbound: func(context.Context, ServiceMailOutboundCall) (ServiceMailOutboundResult, error) {
		return ServiceMailOutboundResult{}, errors.New("synthetic-secret")
	}}
	h, err := New(Options{Registry: registry, Security: SecurityOptions{InitialState: StateFull, HostAllowed: func(*http.Request) bool { return true }}, ServiceAPI: api})
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest(http.MethodPost, "/api/v1/service/workspaces/main/applications/mail/mail-outbound-configure", strings.NewReader(`{"actor":"anchor","operation_id":"cfg-0123456789abcdef0123456789abcdef","provider":"direct"}`))
	r.TLS = &tls.ConnectionState{}
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Authorization", "Bearer token")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 503 || strings.Contains(w.Body.String(), "synthetic-secret") {
		t.Fatal("unknown failure was disclosed or accepted")
	}
}

func TestServiceMailOutboundRechecksGrantBeforeSuccess(t *testing.T) {
	for _, tc := range []struct {
		name, finalIdentity string
		want                int
	}{
		{"unchanged", "mail-service", http.StatusOK},
		{"revoked", "", http.StatusServiceUnavailable},
		{"identity changed", "replacement-service", http.StatusServiceUnavailable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			registry, _ := testRegistry(t, "main")
			identity, calls := "mail-service", 0
			api := &ServiceAPIOptions{
				Authenticate: func(context.Context, string, string, string, string) (string, error) {
					if identity == "" {
						return "", serviceauth.ErrForbidden
					}
					return identity, nil
				},
				Factory: func(string) application.ApplicationCredentialService { return nil },
				Audit:   func(context.Context, ServiceCredentialAuditEvent) error { return nil },
				MailOutbound: func(context.Context, ServiceMailOutboundCall) (ServiceMailOutboundResult, error) {
					calls++
					identity = tc.finalIdentity
					return ServiceMailOutboundResult{State: "configured"}, nil
				},
			}
			h, err := New(Options{Registry: registry, Security: SecurityOptions{InitialState: StateFull, HostAllowed: func(*http.Request) bool { return true }}, ServiceAPI: api})
			if err != nil {
				t.Fatal(err)
			}
			r := httptest.NewRequest(http.MethodPost, "/api/v1/service/workspaces/main/applications/mail/mail-outbound-configure", strings.NewReader(`{"actor":"anchor","operation_id":"cfg-0123456789abcdef0123456789abcdef","provider":"direct"}`))
			r.TLS = &tls.ConnectionState{}
			r.Header.Set("Content-Type", "application/json")
			r.Header.Set("Authorization", "Bearer token")
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != tc.want || calls != 1 || (tc.want != http.StatusOK && (!strings.Contains(w.Body.String(), "mail_outbound_unknown") || strings.Contains(w.Body.String(), "configured"))) {
				t.Fatalf("status=%d calls=%d body=%s", w.Code, calls, w.Body.String())
			}
		})
	}
}
