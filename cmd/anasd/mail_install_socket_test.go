// TEST_CASES: MAILMOD-T-081
package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/anas-project/ANAS/internal/api/httpapi"
	"github.com/anas-project/ANAS/internal/application"
	"github.com/anas-project/ANAS/internal/serviceauth"
)

func socketTestDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp(".", ".mail-socket-test-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(dir); err != nil {
			t.Error(err)
		}
	})
	path, err := filepath.Abs(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Chmod(path, 0750); err != nil {
		t.Fatal(err)
	}
	return path
}
func TestMailInstallSocketExclusiveAndRejectsUnknownFiles(t *testing.T) {
	dir := socketTestDir(t)
	gid := uint32(os.Getegid())
	l, err := listenMailInstallSocket(dir, gid)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = listenMailInstallSocket(dir, gid); !errors.Is(err, errMailInstallSocket) {
		t.Fatal("second instance accepted", err)
	}
	if err = l.Close(); err != nil {
		t.Fatal(err)
	}
	if err = l.Close(); err != nil {
		t.Fatal("second close", err)
	}
	if _, err = os.Lstat(filepath.Join(dir, mailInstallSocketName)); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("socket not removed", err)
	}
	path := filepath.Join(dir, mailInstallSocketName)
	if err = os.WriteFile(path, []byte("external"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = listenMailInstallSocket(dir, gid); !errors.Is(err, errMailInstallSocket) {
		t.Fatal("unknown file accepted", err)
	}
	if raw, err := os.ReadFile(path); err != nil || string(raw) != "external" {
		t.Fatal("unknown file overwritten", err)
	}
	if err = os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err = os.Symlink(".lock", path); err != nil {
		t.Fatal(err)
	}
	if _, err = listenMailInstallSocket(dir, gid); !errors.Is(err, errMailInstallSocket) {
		t.Fatal("symlink accepted", err)
	}
	if err = os.Remove(path); err != nil {
		t.Fatal(err)
	}
	other := filepath.Join(dir, "unrelated")
	if err = os.WriteFile(other, []byte("private"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = listenMailInstallSocket(dir, gid); !errors.Is(err, errMailInstallSocket) {
		t.Fatal("unrelated directory content accepted", err)
	}
	if err = os.Remove(other); err != nil {
		t.Fatal(err)
	}
	if err = os.Chmod(dir, 0770); err != nil {
		t.Fatal(err)
	}
	if _, err = listenMailInstallSocket(dir, gid); !errors.Is(err, errMailInstallSocket) {
		t.Fatal("writable directory accepted", err)
	}
}
func TestMailInstallSocketRecoversOnlyOwnedStaleSocket(t *testing.T) {
	dir := socketTestDir(t)
	path := filepath.Join(dir, mailInstallSocketName)
	stale, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	stale.SetUnlinkOnClose(false)
	if err = os.Chmod(path, 0660); err != nil {
		t.Fatal(err)
	}
	if _, err = listenMailInstallSocket(dir, uint32(os.Getegid())); !errors.Is(err, errMailInstallSocket) {
		t.Fatal("active outsider replaced", err)
	}
	if err = stale.Close(); err != nil {
		t.Fatal(err)
	}
	l, err := listenMailInstallSocket(dir, uint32(os.Getegid()))
	if err != nil {
		t.Fatal("stale recovery", err)
	}
	if err = l.Close(); err != nil {
		t.Fatal(err)
	}
}
func TestMailInstallSocketDoesNotDeleteReplacement(t *testing.T) {
	dir := socketTestDir(t)
	l, err := listenMailInstallSocket(dir, uint32(os.Getegid()))
	if err != nil {
		t.Fatal(err)
	}
	socket := filepath.Join(dir, mailInstallSocketName)
	if err = os.Rename(socket, filepath.Join(dir, "moved.sock")); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(socket, []byte("foreign"), 0600); err != nil {
		t.Fatal(err)
	}
	if err = l.Close(); !errors.Is(err, errMailInstallSocket) {
		t.Fatal("replacement not detected", err)
	}
	raw, err := os.ReadFile(socket)
	if err != nil || string(raw) != "foreign" {
		t.Fatal("replacement deleted", err)
	}
}

func TestMailInstallSocketTLSAndRouteBoundary(t *testing.T) {
	dir := socketTestDir(t)
	l, err := listenMailInstallSocket(dir, uint32(os.Getegid()))
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	certServer := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer certServer.Close()
	roots := x509.NewCertPool()
	roots.AddCert(certServer.Certificate())
	workspace := t.TempDir()
	if err = os.Mkdir(filepath.Join(workspace, ".anas"), 0700); err != nil {
		t.Fatal(err)
	}
	registry, err := httpapi.NewRegistry([]httpapi.Workspace{{ID: "main", Path: workspace}})
	if err != nil {
		t.Fatal(err)
	}
	allowed := true
	calls := 0
	api := &httpapi.ServiceAPIOptions{Authenticate: func(_ context.Context, token, permission, ws, resource string) (string, error) {
		if token != "mail-token" {
			return "", serviceauth.ErrUnauthenticated
		}
		if !allowed || permission != "mail.ingress.install" || ws != "main" || resource != "mail" {
			return "", serviceauth.ErrForbidden
		}
		return "mail-service", nil
	}, Factory: func(string) application.ApplicationCredentialService {
		t.Error("credentials route exposed")
		return nil
	}, Audit: func(context.Context, httpapi.ServiceCredentialAuditEvent) error { return nil }, MailInstall: func(ctx context.Context, call httpapi.ServiceMailInstallCall) (httpapi.ServiceMailInstallResult, error) {
		calls++
		if err := call.Reauthorize(ctx); err != nil {
			t.Error("grant reauthorization", err)
		}
		return httpapi.ServiceMailInstallResult{State: "prepared", SourceID: "11111111-1111-4111-8111-111111111111"}, nil
	}}
	h, err := httpapi.New(httpapi.Options{Registry: registry, Security: httpapi.SecurityOptions{InitialState: httpapi.StateFull, HostAllowed: func(r *http.Request) bool { return r.Host == "127.0.0.1" }}, ServiceAPI: api})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- serveMailInstallSocket(ctx, l, h, certServer.TLS.Clone()) }()
	transport := &http.Transport{TLSClientConfig: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}, DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", filepath.Join(dir, mailInstallSocketName))
	}}
	client := &http.Client{Transport: transport, Timeout: 5 * time.Second}
	defer transport.CloseIdleConnections()
	body := `{"actor":"anchor","action":"prepare","operation_id":"cfg-0123456789abcdef0123456789abcdef","account_id":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","worker":"mail-worker","bucket":"mail-bucket","zone_id":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","domains":["example.test"],"allowed_domains":["example.test"],"drain_ids":[],"capacity_bytes":10,"total_capacity_bytes":10,"delivered_days":7,"deployment_token":"private-deploy-token","routing_token":"private-route-token"}`
	for _, tc := range []struct {
		path, token string
		want        int
	}{{"/api/v1/service/workspaces/main/applications/mail/mail-ingress-install", "mail-token", 200}, {"/api/v1/service/workspaces/main/applications/mail/mail-ingress-install", "bad", 401}, {"/api/v1/service/workspaces/main/applications/mail/credentials", "mail-token", 404}, {"/api/v1/system", "mail-token", 404}} {
		r, err := http.NewRequest("POST", "https://127.0.0.1"+tc.path, strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Authorization", "Bearer "+tc.token)
		response, err := client.Do(r)
		if err != nil {
			t.Fatal(err)
		}
		io.Copy(io.Discard, response.Body)
		response.Body.Close()
		if response.StatusCode != tc.want {
			t.Fatalf("%s: %d", tc.path, response.StatusCode)
		}
	}
	allowed = false
	r, err := http.NewRequest("POST", "https://127.0.0.1/api/v1/service/workspaces/main/applications/mail/mail-ingress-install", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Authorization", "Bearer mail-token")
	response, err := client.Do(r)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != 403 || calls != 1 {
		t.Fatal("revoked grant accepted", response.StatusCode, calls)
	}
	bad := transport.Clone()
	bad.TLSClientConfig = &tls.Config{RootCAs: x509.NewCertPool(), MinVersion: tls.VersionTLS12}
	defer bad.CloseIdleConnections()
	if r, err := (&http.Client{Transport: bad, Timeout: 5 * time.Second}).Post("https://127.0.0.1/api/v1/service/workspaces/main/applications/mail/mail-ingress-install", "application/json", nil); err == nil {
		r.Body.Close()
		t.Fatal("untrusted certificate accepted")
	}
	cancel()
	if err = <-done; err != nil {
		t.Fatal(err)
	}
	if err = l.Close(); err != nil {
		t.Fatal(err)
	}
}
