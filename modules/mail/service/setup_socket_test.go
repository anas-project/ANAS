// TEST_CASES: MAILMOD-T-081
package main

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestMailInstallerUsesUnixSocketWithTLS(t *testing.T) {
	dir, err := os.MkdirTemp(".", ".mail-socket-client-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	dir, err = filepath.Abs(dir)
	if err != nil {
		t.Fatal(err)
	}
	sock := filepath.Join(dir, "install.sock")
	listener, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.TLS == nil || r.Header.Get("Authorization") != "Bearer service.synthetic-token" || r.URL.Path != "/api/v1/service/workspaces/main/applications/mail/mail-ingress-install" {
			t.Error("invalid socket request")
		}
		_ = json.NewEncoder(w).Encode(workerInstallResult{State: "prepared", SourceID: "11111111-1111-4111-8111-111111111111"})
	}))
	server.Listener.Close()
	server.Listener = listener
	server.StartTLS()
	defer server.Close()
	roots := x509.NewCertPool()
	roots.AddCert(server.Certificate())
	token := filepath.Join(dir, "token")
	if err = os.WriteFile(token, []byte("service.synthetic-token\n"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MAIL_SERVICE_API_URL", "https://127.0.0.1")
	t.Setenv("MAIL_SERVICE_API_WORKSPACE", "main")
	t.Setenv("MAIL_SERVICE_API_TOKEN_FILE", token)
	t.Setenv("MAIL_SERVICE_API_SOCKET_FILE", sock)
	client, err := configuredMailInstaller([]string{"example.test"}, &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12})
	if err != nil {
		t.Fatal(err)
	}
	result, err := client.Install(t.Context(), workerInstallRequest{Action: "prepare"})
	if err != nil || result.State != "prepared" || calls != 1 {
		t.Fatal("socket request", err)
	}
	server.CloseClientConnections()
	server.Close()
	if _, err = client.Install(t.Context(), workerInstallRequest{Action: "prepare"}); err == nil {
		t.Fatal("socket failure silently fell back to network")
	}
	t.Setenv("MAIL_SERVICE_API_SOCKET_FILE", "relative")
	if _, err = configuredMailInstaller([]string{"example.test"}, &tls.Config{}); err == nil {
		t.Fatal("relative socket path accepted")
	}
}
