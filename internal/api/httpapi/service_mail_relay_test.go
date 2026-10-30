// TEST_CASES: MAILMOD-T-059
package httpapi

import (
	"context"
	"crypto/tls"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/anas-project/ANAS/internal/application"
	"github.com/anas-project/ANAS/internal/serviceauth"
)

func TestMailRelayServiceRestrictedGrant(t *testing.T) {
	registry, _ := testRegistry(t, "main")
	allowed, calls := true, 0
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
			if call.Request.Provider != "cloud-relay" || call.Reauthorize(ctx) != nil {
				t.Fatal("relay call missing authority")
			}
			for _, secret := range []string{call.Request.ReaderToken, call.Request.ControlToken, call.Request.NotificationToken} {
				if strings.Contains(fmt.Sprintf("%+v", call.Request), secret) {
					t.Fatal("format leaked secret")
				}
			}
			allowed = false
			if call.Reauthorize(ctx) == nil {
				t.Fatal("revoked service grant remained usable")
			}
			allowed = true
			return ServiceMailInstallResult{State: "prepared", SourceID: call.Request.SourceID}, nil
		},
	}
	handler, err := New(Options{Registry: registry, Security: SecurityOptions{InitialState: StateFull, HostAllowed: func(*http.Request) bool { return true }}, ServiceAPI: api})
	if err != nil {
		t.Fatal(err)
	}
	reader, control, notify := strings.Repeat("a", 64), strings.Repeat("b", 64), strings.Repeat("c", 64)
	body := fmt.Sprintf(`{"actor":"anchor","action":"prepare","provider":"cloud-relay","operation_id":"cfg-0123456789abcdef0123456789abcdef","domains":["example.test"],"allowed_domains":["example.test"],"drain_ids":[],"capacity_bytes":100,"total_capacity_bytes":100,"delivered_days":7,"source_id":"11111111-1111-4111-8111-111111111111","generation":"22222222-2222-4222-8222-222222222222","source_origin":"https://relay.example.test","reader_token":%q,"control_token":%q,"notification_origin":"https://nas.example.test","notification_token":%q}`, reader, control, notify)
	path := "/api/v1/service/workspaces/main/applications/mail/mail-ingress-install"
	call := func(input, token string, useTLS bool) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodPost, path, strings.NewReader(input))
		if useTLS {
			r.TLS = &tls.ConnectionState{}
		}
		r.Header.Set("Authorization", "Bearer "+token)
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	w := call(body, "mail-token", true)
	if w.Code != 200 || calls != 1 || strings.Contains(w.Body.String(), reader) || strings.Contains(w.Body.String(), control) || strings.Contains(w.Body.String(), notify) {
		t.Fatalf("valid relay response: %d", w.Code)
	}
	for _, tc := range []struct {
		input, token string
		tls          bool
		code         int
	}{
		{body, "bad", true, 401},
		{body, "mail-token", false, 404},
		{strings.Replace(body, `"provider":"cloud-relay"`, `"provider":"unknown"`, 1), "mail-token", true, 400},
		{strings.Replace(body, `"control_token":"`+control+`"`, `"control_token":"`+reader+`"`, 1), "mail-token", true, 400},
		{strings.Replace(body, `"source_origin":`, `"worker":"other","source_origin":`, 1), "mail-token", true, 400},
		{strings.Replace(body, `"notification_token":"`+notify+`"`, `"notification_token":""`, 1), "mail-token", true, 400},
		{strings.Replace(body, `"source_id":`, `"source_id":"11111111-1111-4111-8111-111111111111","source_id":`, 1), "mail-token", true, 400},
	} {
		w = call(tc.input, tc.token, tc.tls)
		if w.Code != tc.code || calls != 1 {
			t.Fatalf("invalid input reached installer: %d", w.Code)
		}
	}
}
