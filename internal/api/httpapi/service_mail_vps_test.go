// TEST_CASES: MAILMOD-T-072
package httpapi

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/anas-project/ANAS/internal/application"
	"github.com/anas-project/ANAS/internal/serviceauth"
)

func TestMailVPSServiceRestrictedGrantAndInput(t *testing.T) {
	registry, _ := testRegistry(t, "main")
	calls := 0
	allowed := true
	api := &ServiceAPIOptions{Authenticate: func(_ context.Context, token, permission, workspace, resource string) (string, error) {
		if token != "service-token" {
			return "", serviceauth.ErrUnauthenticated
		}
		if !allowed || permission != string(PermissionMailIngressInstall) || workspace != "main" || resource != "mail" {
			return "", serviceauth.ErrForbidden
		}
		return "mail-service", nil
	}, Factory: func(string) application.ApplicationCredentialService { return nil }, Audit: func(context.Context, ServiceCredentialAuditEvent) error { return nil }, MailInstall: func(ctx context.Context, call ServiceMailInstallCall) (ServiceMailInstallResult, error) {
		calls++
		if call.Request.Provider != "vps-ssh" || call.Reauthorize(ctx) != nil {
			t.Fatal("invalid call authority")
		}
		for _, secret := range []string{call.Request.SSHPrivateKey, call.Request.TLSPrivateKey, call.Request.RoutingToken, call.Request.DestinationToken} {
			if secret == "" {
				continue
			}
			if strings.Contains(fmt.Sprintf("%+v", call.Request), secret) {
				t.Fatal("private key leaked")
			}
		}
		allowed = false
		if call.Reauthorize(ctx) == nil {
			t.Fatal("revocation ignored")
		}
		allowed = true
		return ServiceMailInstallResult{State: map[string]string{"publish": "published", "activate": "activated", "prepare": "prepared", "commit": "configured"}[call.Request.Action], SourceID: "11111111-1111-4111-8111-111111111111"}, nil
	}}
	handler, err := New(Options{Registry: registry, Security: SecurityOptions{InitialState: StateFull, HostAllowed: func(*http.Request) bool { return true }}, ServiceAPI: api})
	if err != nil {
		t.Fatal(err)
	}
	input := map[string]any{"actor": "anchor", "action": "publish", "provider": "vps-ssh", "operation_id": "cfg-0123456789abcdef0123456789abcdef", "domains": []string{"example.test"}, "allowed_domains": []string{"example.test"}, "drain_ids": []string{}, "capacity_bytes": 8388608, "total_capacity_bytes": 8388608, "delivered_days": 7, "source_origin": "https://relay.example.test", "ssh_host": "relay.example.test", "ssh_port": 22, "ssh_user": "relaydeploy", "ssh_host_key": "synthetic-host-key", "ssh_private_key": "synthetic-ssh-private-key", "tls_certificate": "synthetic-certificate", "tls_private_key": "synthetic-tls-private-key", "smtp_hostname": "relay.example.test"}
	call := func(body []byte, token string, useTLS bool) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodPost, "/api/v1/service/workspaces/main/applications/mail/mail-ingress-install", bytes.NewReader(body))
		if useTLS {
			r.TLS = &tls.ConnectionState{}
		}
		r.Header.Set("Authorization", "Bearer "+token)
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	for _, action := range []string{"publish", "activate", "prepare", "commit"} {
		input["action"] = action
		body, _ := json.Marshal(input)
		w := call(body, "service-token", true)
		if w.Code != 200 || strings.Contains(w.Body.String(), "synthetic-") {
			t.Fatal(action, w.Code, w.Body.String())
		}
	}
	forward := map[string]any{}
	for k, v := range input {
		forward[k] = v
	}
	forward["account_id"], forward["zone_id"], forward["forward_domain"] = strings.Repeat("1", 32), strings.Repeat("2", 32), "delivery.example.test"
	forward["routing_token"], forward["destination_token"] = strings.Repeat("r", 40), strings.Repeat("d", 40)
	for _, action := range []string{"publish", "activate", "prepare", "commit"} {
		forward["action"] = action
		body, _ := json.Marshal(forward)
		w := call(body, "service-token", true)
		if w.Code != 200 || strings.Contains(w.Body.String(), strings.Repeat("r", 40)) || strings.Contains(w.Body.String(), strings.Repeat("d", 40)) {
			t.Fatal("forward", action, w.Code)
		}
	}
	validCalls := calls
	for _, change := range []struct {
		key   string
		value any
	}{{"action", "arbitrary"}, {"provider", "other"}, {"ssh_port", 0}, {"ssh_private_key", ""}, {"tls_private_key", ""}, {"source_id", "11111111-1111-4111-8111-111111111111"}, {"routing_token", "other"}} {
		copy := map[string]any{}
		for k, v := range input {
			copy[k] = v
		}
		copy[change.key] = change.value
		body, _ := json.Marshal(copy)
		w := call(body, "service-token", true)
		if w.Code != 400 || calls != validCalls {
			t.Fatalf("%s accepted: %d", change.key, w.Code)
		}
	}
	for _, change := range []struct {
		key   string
		value any
	}{{"forward_domain", ""}, {"forward_domain", "Invalid domain"}, {"zone_id", ""}, {"account_id", "invalid"}, {"destination_token", ""}, {"destination_token", strings.Repeat("r", 40)}} {
		copy := map[string]any{}
		for k, v := range forward {
			copy[k] = v
		}
		copy[change.key] = change.value
		body, _ := json.Marshal(copy)
		if w := call(body, "service-token", true); w.Code != 400 || calls != validCalls {
			t.Fatalf("invalid forward %s accepted: %d", change.key, w.Code)
		}
	}
	body, _ := json.Marshal(input)
	if w := call(body, "wrong-token", true); w.Code != 401 {
		t.Fatal("unauthenticated", w.Code)
	}
	if w := call(body, "service-token", false); w.Code != 404 {
		t.Fatal("non-TLS", w.Code)
	}
}
