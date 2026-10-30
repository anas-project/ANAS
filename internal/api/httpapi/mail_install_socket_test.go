// TEST_CASES: MAILMOD-T-081
package httpapi

import (
	"context"
	"crypto/tls"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/anas-project/ANAS/internal/application"
	"github.com/anas-project/ANAS/internal/serviceauth"
)

func TestMailInstallOnlyPreservesGrantAndExcludesAllOtherRoutes(t *testing.T) {
	registry, _ := testRegistry(t, "main")
	calls := 0
	api := &ServiceAPIOptions{Authenticate: func(_ context.Context, token, permission, workspace, resource string) (string, error) {
		if token != "mail-token" {
			return "", serviceauth.ErrUnauthenticated
		}
		if permission != string(PermissionMailIngressInstall) || workspace != "main" || resource != "mail" {
			return "", serviceauth.ErrForbidden
		}
		return "mail-service", nil
	}, Factory: func(string) application.ApplicationCredentialService { t.Fatal("credential API exposed"); return nil }, Audit: func(context.Context, ServiceCredentialAuditEvent) error { return nil }, MailInstall: func(ctx context.Context, call ServiceMailInstallCall) (ServiceMailInstallResult, error) {
		calls++
		if call.Reauthorize(ctx) != nil {
			t.Fatal("grant lost")
		}
		return ServiceMailInstallResult{State: "prepared", SourceID: "11111111-1111-4111-8111-111111111111"}, nil
	}}
	h, err := New(Options{Registry: registry, Security: SecurityOptions{InitialState: StateFull, HostAllowed: func(*http.Request) bool { return true }}, ServiceAPI: api})
	if err != nil {
		t.Fatal(err)
	}
	h = MailInstallOnly(h)
	path := "/api/v1/service/workspaces/main/applications/mail/mail-ingress-install"
	body := `{"actor":"anchor","action":"prepare","operation_id":"cfg-0123456789abcdef0123456789abcdef","account_id":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","worker":"mail-worker","bucket":"mail-bucket","zone_id":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","domains":["example.test"],"allowed_domains":["example.test"],"drain_ids":[],"capacity_bytes":10,"total_capacity_bytes":10,"delivered_days":7,"deployment_token":"private-deploy-token","routing_token":"private-route-token"}`
	for _, tc := range []struct {
		name, method, path, token string
		tls                       bool
		want                      int
	}{
		{"authorized", "POST", path, "mail-token", true, 200},
		{"wrong token", "POST", path, "bad", true, 401},
		{"no TLS", "POST", path, "mail-token", false, 404},
		{"wrong workspace", "POST", strings.Replace(path, "/main/", "/other/", 1), "mail-token", true, 403},
		{"credentials", "POST", strings.Replace(path, "mail-ingress-install", "credentials", 1), "mail-token", true, 404},
		{"other app", "POST", strings.Replace(path, "/mail/", "/other/", 1), "mail-token", true, 404},
		{"console", "GET", "/api/v1/system", "mail-token", true, 404},
		{"wrong method", "GET", path, "mail-token", true, 404},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before := calls
			r := httptest.NewRequest(tc.method, tc.path, strings.NewReader(body))
			r.Header.Set("Content-Type", "application/json")
			r.Header.Set("Authorization", "Bearer "+tc.token)
			if tc.tls {
				r.TLS = &tls.ConnectionState{}
			}
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != tc.want {
				t.Fatalf("status %d want %d", w.Code, tc.want)
			}
			wantCalls := before
			if tc.name == "authorized" {
				wantCalls++
			}
			if calls != wantCalls {
				t.Fatal("unexpected installation")
			}
		})
	}
}

func TestMailInstallSocketHostPolicy(t *testing.T) {
	policy := MailInstallSocketHostPolicy{Port: 8080, AllowedDNSHosts: []string{"anas.example.test"}}
	for _, tc := range []struct {
		host string
		want bool
	}{
		{"127.0.0.1:8080", true}, {"[::1]:8080", true}, {"anas.example.test:8080", true},
		{"ANAS.EXAMPLE.TEST", true}, {"127.0.0.1:8081", false}, {"192.0.2.1:8080", false},
		{"attacker.invalid:8080", false}, {"anas.example.test.attacker.invalid:8080", false},
		{"anas.example.test:invalid", false}, {"", false},
	} {
		t.Run(tc.host, func(t *testing.T) {
			r := &http.Request{Host: tc.host}
			if got := policy.Allowed(r); got != tc.want {
				t.Fatalf("host %q: got %v, want %v", tc.host, got, tc.want)
			}
		})
	}
}
