//go:build linux

// TEST_CASES: MAILMOD-T-017 MAILMOD-T-081
package main

import (
	"crypto/tls"
	"crypto/x509"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/anas-project/ANAS/modules/mail/store"
)

// Runs only inside the disposable, non-root administrator test container.
// The directory and session are synthetic; anasd and its config transaction
// are real and reached through the TLS-protected Unix socket.
func TestMailOutboundAdminSocketClientLive(t *testing.T) {
	if os.Getenv("MAIL_OUTBOUND_ADMIN_CLIENT_LIVE") != "1" {
		t.Skip("isolated administrator fixture required")
	}
	if os.Geteuid() != 65532 || os.Getegid() != 65532 {
		t.Fatal("client did not run as the expected non-root identity")
	}
	cert, err := os.ReadFile("/ca.pem")
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(cert) {
		t.Fatal("test CA invalid")
	}
	t.Setenv("MAIL_SERVICE_API_URL", "https://127.0.0.1:18445")
	t.Setenv("MAIL_SERVICE_API_WORKSPACE", "main")
	t.Setenv("MAIL_SERVICE_API_TOKEN_FILE", "/token/token")
	t.Setenv("MAIL_SERVICE_API_SOCKET_FILE", "/socket/install.sock")
	client, err := configuredMailInstaller([]string{"example.test"}, &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12})
	if err != nil {
		t.Fatal(err)
	}
	h, login, _, dir := loginFixture(t)
	data := &setupHTTPStore{httpStore: login, state: store.SetupState{Revision: 3, Draft: store.SetupDraft{Outbound: store.SetupOutbound{Provider: "direct"}}}}
	h.store, h.directory, h.install = data, &setupHTTPDirectory{httpDirectory: dir, admin: true}, client
	for _, tc := range []struct {
		operation, password string
		draft               store.SetupOutbound
	}{
		{"cfg-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "", store.SetupOutbound{Provider: "direct"}},
		{"cfg-bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", "synthetic-smtp-password", store.SetupOutbound{Provider: "smtp", Host: "smtp.example.test", Port: 587, TLS: "starttls", Username: "sender", SecretRef: "secrets.MAIL_OUTBOUND_PASSWORDS"}},
	} {
		data.state.Draft.Outbound = tc.draft
		body := `{"revision":3,"operation_id":"` + tc.operation + `","password":"` + tc.password + `"}`
		r := httptest.NewRequest(http.MethodPost, "https://mail.example.test/api/setup/outbound-apply", strings.NewReader(body))
		r.AddCookie(&http.Cookie{Name: sessionCookie, Value: "session-secret"})
		r.Header.Set("Origin", "https://mail.example.test")
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 200 || strings.Contains(w.Body.String(), tc.password) && tc.password != "" || strings.Contains(w.Body.String(), `"enabled":true`) {
			t.Fatal("configuration response not confirmed", w.Code)
		}
	}
}
