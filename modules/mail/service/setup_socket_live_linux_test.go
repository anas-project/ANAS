//go:build linux

// TEST_CASES: MAILMOD-T-081
package main

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/anas-project/ANAS/modules/mail/store"
)

// Run only in the disposable container created by the daemon integration
// fixture. The receiving workspace is deliberately missing configuration.
func TestMailInstallerRealDaemonLive(t *testing.T) {
	if os.Getenv("MAIL_SOCKET_CLIENT_LIVE") != "1" {
		t.Skip("requires isolated daemon fixture")
	}
	cert, err := os.ReadFile("/ca.pem")
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(cert) {
		t.Fatal("invalid test certificate")
	}
	t.Setenv("MAIL_SERVICE_API_URL", "https://127.0.0.1:18445")
	t.Setenv("MAIL_SERVICE_API_WORKSPACE", "main")
	t.Setenv("MAIL_SERVICE_API_TOKEN_FILE", "/token/token")
	t.Setenv("MAIL_SERVICE_API_SOCKET_FILE", "/socket/install.sock")
	client, err := configuredMailInstaller([]string{"example.test"}, &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12})
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.Install(t.Context(), workerInstallRequest{Actor: "anchor", Action: "prepare", OperationID: "cfg-eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee", AccountID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Worker: "mail-worker", Bucket: "mail-bucket", ZoneID: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", Domains: []string{"example.test"}, AllowedDomains: []string{"example.test"}, DrainIDs: []string{}, CapacityBytes: 10, TotalCapacityBytes: 10, DeliveredDays: 7, DeploymentToken: "fake-deploy-token-value", RoutingToken: "fake-routing-token-value"})
	if !errors.Is(err, errInstall) {
		t.Fatalf("expected safe unavailable result, got %v", err)
	}
	h, login, _, dir := loginFixture(t)
	h.store = &setupHTTPStore{httpStore: login, state: store.SetupState{Revision: 3, Draft: store.SetupDraft{Inbound: "worker-r2", Domain: "example.test", LimitsConfirmed: true, Limits: store.SetupLimits{CloudBytes: 2048, DeliveredDays: 7}}}}
	h.directory, h.install = &setupHTTPDirectory{httpDirectory: dir, admin: true}, client
	body := `{"revision":3,"action":"prepare","operation_id":"cfg-ffffffffffffffffffffffffffffffff","account_id":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","zone_id":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","worker":"mail-worker","bucket":"mail-bucket","drain_ids":[],"capacity_bytes":10,"deployment_token":"fake-deploy-token-value","routing_token":"fake-routing-token-value"}`
	req := httptest.NewRequest("POST", "https://mail.example.test/api/setup/worker-install", strings.NewReader(body))
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: "session-secret"})
	req.Header.Set("Origin", "https://mail.example.test")
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()
	h.ServeHTTP(resp, req)
	if resp.Code != http.StatusServiceUnavailable || !strings.Contains(resp.Body.String(), "install-unknown") || strings.Contains(resp.Body.String(), "fake-") {
		t.Fatalf("administrator install response %d: %s", resp.Code, resp.Body.String())
	}
}
