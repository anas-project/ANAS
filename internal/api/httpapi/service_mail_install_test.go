// TEST_CASES: MAILMOD-T-042
package httpapi

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/anas-project/ANAS/internal/application"
	"github.com/anas-project/ANAS/internal/serviceauth"
)

func TestMailInstallServiceGrantAndReauthorization(t *testing.T) {
	registry, _ := testRegistry(t, "main")
	allowed := true
	calls := 0
	api := &ServiceAPIOptions{
		Authenticate: func(_ context.Context, token, permission, workspace, resource string) (string, error) {
			if token != "mail-token" {
				return "", serviceauth.ErrUnauthenticated
			}
			if !allowed || permission != string(PermissionMailIngressInstall) || workspace != "main" || resource != "mail" {
				return "", serviceauth.ErrForbidden
			}
			return "mail-service", nil
		},
		Factory: func(string) application.ApplicationCredentialService { return nil },
		Audit:   func(context.Context, ServiceCredentialAuditEvent) error { return nil },
		MailInstall: func(ctx context.Context, call ServiceMailInstallCall) (ServiceMailInstallResult, error) {
			calls++
			if call.ClientID != "mail-service" || call.WorkspaceID != "main" || call.Request.Actor != "anchor" || call.Reauthorize(ctx) != nil {
				t.Fatal("wrong service identity or grant")
			}
			if strings.Contains(fmt.Sprintf("%+v", call.Request), "private-route-token") {
				t.Fatal("format leaked token")
			}
			allowed = false
			if call.Reauthorize(ctx) == nil {
				t.Fatal("revoked grant remained usable")
			}
			allowed = true
			return ServiceMailInstallResult{State: "prepared", SourceID: "11111111-1111-4111-8111-111111111111"}, nil
		},
	}
	handler, err := New(Options{Registry: registry, Security: SecurityOptions{InitialState: StateFull, HostAllowed: func(*http.Request) bool { return true }}, ServiceAPI: api})
	if err != nil {
		t.Fatal(err)
	}
	body := `{"actor":"anchor","action":"prepare","operation_id":"cfg-0123456789abcdef0123456789abcdef","account_id":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","worker":"mail-worker","bucket":"mail-bucket","zone_id":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","domains":["example.test"],"allowed_domains":["example.test"],"drain_ids":[],"capacity_bytes":10,"total_capacity_bytes":10,"delivered_days":7,"deployment_token":"private-deploy-token","routing_token":"private-route-token"}`
	path := "/api/v1/service/workspaces/main/applications/mail/mail-ingress-install"
	invoke := func(path, token, input string, tlsEnabled bool) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodPost, path, strings.NewReader(input))
		if tlsEnabled {
			r.TLS = &tls.ConnectionState{}
		}
		r.Header.Set("Authorization", "Bearer "+token)
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	response := invoke(path, "mail-token", body, true)
	if response.Code != 200 || response.Header().Get("Cache-Control") != "no-store" || strings.Contains(response.Body.String(), "private-") || calls != 1 {
		t.Fatal("unexpected response", response.Code, response.Body.String())
	}
	for _, tc := range []struct {
		path, token, body string
		tls               bool
		status            int
	}{
		{path, "bad-token", body, true, 401},
		{strings.Replace(path, "/main/", "/other/", 1), "mail-token", body, true, 403},
		{strings.Replace(path, "/mail/", "/other/", 1), "mail-token", body, true, 403},
		{path, "mail-token", strings.Replace(body, `"actor":"anchor"`, `"actor":"anchor","actor":"other"`, 1), true, 400},
		{path, "mail-token", strings.Replace(body, `"action":"prepare"`, `"action":"prepare","ACTION":"prepare"`, 1), true, 400},
		{path, "mail-token", strings.Replace(body, `"action":"prepare"`, `"ACTION":"prepare"`, 1), true, 400},
		{path, "mail-token", strings.Replace(body, `"action":"prepare"`, `"action":"unknown"`, 1), true, 400},
		{path, "mail-token", body, false, 404},
	} {
		before := calls
		response = invoke(tc.path, tc.token, tc.body, tc.tls)
		if response.Code != tc.status || calls != before || strings.Contains(response.Body.String(), "private-") {
			t.Fatal("invalid request reached installer", response.Code, response.Body.String())
		}
	}
	api.MailInstall = func(context.Context, ServiceMailInstallCall) (ServiceMailInstallResult, error) {
		return ServiceMailInstallResult{}, errors.New("private-route-token")
	}
	// New copies the options; replace the handler to test sanitizing a backend error.
	handler, err = New(Options{Registry: registry, Security: SecurityOptions{InitialState: StateFull, HostAllowed: func(*http.Request) bool { return true }}, ServiceAPI: api})
	if err != nil {
		t.Fatal(err)
	}
	response = invoke(path, "mail-token", body, true)
	if response.Code != 503 || strings.Contains(response.Body.String(), "private-route-token") {
		t.Fatal("provider error leaked", response.Code, response.Body.String())
	}
}

// Keep the strict wire-name allowlist aligned with every request field.
func TestMailInstallFieldNamesMatchRequestType(t *testing.T) {
	ty := reflect.TypeOf(ServiceMailInstallRequest{})
	for i := 0; i < ty.NumField(); i++ {
		name := strings.Split(ty.Field(i).Tag.Get("json"), ",")[0]
		body, _ := json.Marshal(map[string]any{name: nil})
		if name == "" || !mailInstallFields(body) {
			t.Fatalf("missing request field %q", name)
		}
		body, _ = json.Marshal(map[string]any{strings.ToUpper(name): nil})
		if mailInstallFields(body) {
			t.Fatalf("accepted noncanonical field %q", name)
		}
	}
}

func TestMailInstallRechecksServiceGrantBeforeSuccess(t *testing.T) {
	for _, mode := range []string{"valid", "revoked", "identity-changed"} {
		t.Run(mode, func(t *testing.T) {
			registry, _ := testRegistry(t, "main")
			allowed, identity, calls := true, "mail-service", 0
			api := &ServiceAPIOptions{
				Authenticate: func(_ context.Context, token, permission, workspace, resource string) (string, error) {
					if token != "mail-token" || !allowed || permission != string(PermissionMailIngressInstall) || workspace != "main" || resource != "mail" {
						return "", serviceauth.ErrForbidden
					}
					return identity, nil
				},
				Factory: func(string) application.ApplicationCredentialService { return nil },
				Audit:   func(context.Context, ServiceCredentialAuditEvent) error { return nil },
				MailInstall: func(_ context.Context, _ ServiceMailInstallCall) (ServiceMailInstallResult, error) {
					calls++
					if mode == "revoked" {
						allowed = false
					}
					if mode == "identity-changed" {
						identity = "different-service"
					}
					return ServiceMailInstallResult{State: "prepared", SourceID: "11111111-1111-4111-8111-111111111111"}, nil
				},
			}
			handler, err := New(Options{Registry: registry, Security: SecurityOptions{InitialState: StateFull, HostAllowed: func(*http.Request) bool { return true }}, ServiceAPI: api})
			if err != nil {
				t.Fatal(err)
			}
			body := `{"actor":"anchor","action":"prepare","operation_id":"cfg-0123456789abcdef0123456789abcdef","account_id":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","worker":"mail-worker","bucket":"mail-bucket","zone_id":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","domains":["example.test"],"allowed_domains":["example.test"],"drain_ids":[],"capacity_bytes":10,"total_capacity_bytes":10,"delivered_days":7,"deployment_token":"private-deploy-token","routing_token":"private-route-token"}`
			r := httptest.NewRequest(http.MethodPost, "/api/v1/service/workspaces/main/applications/mail/mail-ingress-install", strings.NewReader(body))
			r.TLS = &tls.ConnectionState{}
			r.Header.Set("Authorization", "Bearer mail-token")
			r.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, r)
			if calls != 1 {
				t.Fatal("installer was not called exactly once", calls)
			}
			if mode == "valid" {
				if w.Code != http.StatusOK {
					t.Fatal("valid grant denied", w.Code)
				}
			} else if w.Code != http.StatusServiceUnavailable || !strings.Contains(w.Body.String(), "mail_install_unknown") || strings.Contains(w.Body.String(), "source_id") || strings.Contains(w.Body.String(), "private-") {
				t.Fatal("revoked or replaced grant received a success result", w.Code, w.Body.String())
			}
		})
	}
}
