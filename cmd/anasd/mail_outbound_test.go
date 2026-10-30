// TEST_CASES: MAILMOD-T-017
package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/anas-project/ANAS/internal/api/httpapi"
	"github.com/anas-project/ANAS/internal/application"
	"github.com/anas-project/ANAS/internal/runner"
	"github.com/anas-project/ANAS/internal/serviceauth"
	"github.com/anas-project/ANAS/modules/mail/smtpconfig"
)

func mailOutboundWorkspace(t *testing.T) string {
	t.Helper()
	root, workspace := t.TempDir(), t.TempDir()
	module := filepath.Join(root, "mail")
	if err := os.Mkdir(module, 0700); err != nil {
		t.Fatal(err)
	}
	manifest := `api_version: anas.module/v1
kind: Module
name: mail
version: 1.0.0
revision: 1
status: release
abi:
  supports: [anas.module-hook/v1]
runtime:
  type: builtin
config:
  types: {outbound_routing: string, outbound_passwords: string}
  defaults: {outbound_routing: '', outbound_passwords: ''}
  changes:
    outbound_routing: {effect: container_recreate}
    outbound_passwords: {effect: container_recreate, sensitive: true, store_input: true}
`
	if err := os.WriteFile(filepath.Join(module, "module.yml"), []byte(manifest), 0600); err != nil {
		t.Fatal(err)
	}
	state := filepath.Join(workspace, ".anas")
	if err := os.Mkdir(state, 0700); err != nil {
		t.Fatal(err)
	}
	body := []byte("module_source: official\nmodules:\n  mail: {}\nglobal:\n  base_domain: nas.test\n  email: admin@nas.test\n  timezone: UTC\n")
	if err := os.WriteFile(filepath.Join(workspace, "config.yml"), body, 0600); err != nil {
		t.Fatal(err)
	}
	managed := fmt.Sprintf("api_version: anas.config/v1\ndigest: sha256:%x\nvalidator: cfgv-%s\nupdated_by: test\n", sha256.Sum256(body), strings.Repeat("1", 64))
	if err := os.WriteFile(filepath.Join(state, "config-managed.yml"), []byte(managed), 0600); err != nil {
		t.Fatal(err)
	}
	view, _ := json.Marshal(map[string]string{"api_version": "anas.module-view/v1", "module_root": root})
	if err := os.WriteFile(filepath.Join(state, "module-view.json"), view, 0600); err != nil {
		t.Fatal(err)
	}
	return workspace
}

func TestMailOutboundHostReauthorizesAndAuditsBeforeCommit(t *testing.T) {
	workspace := mailOutboundWorkspace(t)
	writer := &recordingConfigAuditAppender{}
	checks := 0
	call := httpapi.ServiceMailOutboundCall{WorkspaceID: "main", WorkspacePath: workspace, ClientID: "mail-service", Request: httpapi.ServiceMailOutboundRequest{
		Actor: "test-anchor", OperationID: "cfg-0123456789abcdef0123456789abcdef", Provider: "smtp", Host: "smtp.example.test", Port: 587, TLS: "starttls", Username: "sender", Password: "private-test-password",
	}, Reauthorize: func(context.Context) error { checks++; return nil }}
	handler := newMailOutboundHandler(writer)
	result, err := handler(t.Context(), call)
	if err != nil || result.State != "configured" || checks != 3 || len(writer.events) != 2 || writer.events[0].Type != "service.mail.outbound.read" || writer.events[1].Type != "service.mail.outbound.config_commit" {
		t.Fatal("authorized transaction failed", err)
	}
	s, err := runner.ReadMailOutboundSnapshot(t.Context(), workspace)
	if err != nil {
		t.Fatal(err)
	}
	c, err := smtpconfig.Parse(s.Routing, s.Passwords)
	if err != nil || c.Profiles[0].Password != call.Request.Password {
		t.Fatal("private SMTP config missing", err)
	}
	public, err := os.ReadFile(filepath.Join(workspace, "config.yml"))
	if err != nil || bytes.Contains(public, []byte(call.Request.Password)) {
		t.Fatal("public config contains password", err)
	}
	encoded, _ := json.Marshal(writer.events)
	for _, value := range []string{string(encoded), fmt.Sprintf("%+v", call.Request), fmt.Sprintf("%#v", call.Request)} {
		if strings.Contains(value, call.Request.Password) {
			t.Fatal("audit or formatting contains password")
		}
	}
}

func TestMailOutboundHostRevokedGrantCannotWrite(t *testing.T) {
	workspace := mailOutboundWorkspace(t)
	writer := &recordingConfigAuditAppender{}
	checks := 0
	call := httpapi.ServiceMailOutboundCall{WorkspaceID: "main", WorkspacePath: workspace, ClientID: "mail-service", Request: httpapi.ServiceMailOutboundRequest{
		Actor: "test-anchor", OperationID: "cfg-0123456789abcdef0123456789abcdef", Provider: "direct",
	}, Reauthorize: func(context.Context) error {
		checks++
		if checks == 3 {
			return errors.New("grant revoked")
		}
		return nil
	}}
	before, _ := os.ReadFile(filepath.Join(workspace, "config.yml"))
	if _, err := newMailOutboundHandler(writer)(t.Context(), call); !errors.Is(err, httpapi.ErrMailOutboundUnknown) {
		t.Fatal("revocation accepted", err)
	}
	after, _ := os.ReadFile(filepath.Join(workspace, "config.yml"))
	if !bytes.Equal(before, after) || len(writer.events) != 1 {
		t.Fatal("revocation changed configuration")
	}
	if _, err := os.Stat(filepath.Join(workspace, ".anas", "secrets.yml")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("revocation wrote secret", err)
	}
}

func TestMailOutboundHostRejectsInvalidOperationOrMissingAudit(t *testing.T) {
	workspace := mailOutboundWorkspace(t)
	call := httpapi.ServiceMailOutboundCall{WorkspaceID: "main", WorkspacePath: workspace, ClientID: "mail-service", Request: httpapi.ServiceMailOutboundRequest{Actor: "test-anchor", OperationID: "bad-operation", Provider: "direct"}, Reauthorize: func(context.Context) error { return nil }}
	if _, err := newMailOutboundHandler(&recordingConfigAuditAppender{})(t.Context(), call); !errors.Is(err, httpapi.ErrMailOutboundUnknown) {
		t.Fatal("invalid operation accepted", err)
	}
	call.Request.OperationID = "cfg-0123456789abcdef0123456789abcdef"
	if _, err := newMailOutboundHandler(nil)(t.Context(), call); !errors.Is(err, httpapi.ErrMailOutboundUnknown) {
		t.Fatal("missing audit accepted", err)
	}
}

func TestMailOutboundHostThroughRestrictedUnixSocket(t *testing.T) {
	workspace := mailOutboundWorkspace(t)
	dir := socketTestDir(t)
	listener, err := listenMailInstallSocket(dir, uint32(os.Getegid()))
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	certServer := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer certServer.Close()
	roots := x509.NewCertPool()
	roots.AddCert(certServer.Certificate())
	registry, err := httpapi.NewRegistry([]httpapi.Workspace{{ID: "main", Path: workspace}})
	if err != nil {
		t.Fatal(err)
	}
	var allowed atomic.Bool
	allowed.Store(true)
	writer := &recordingConfigAuditAppender{}
	api := &httpapi.ServiceAPIOptions{Authenticate: func(_ context.Context, token, permission, ws, resource string) (string, error) {
		if token != "outbound-token" && token != "ingress-token" {
			return "", serviceauth.ErrUnauthenticated
		}
		if !allowed.Load() || token != "outbound-token" || permission != "mail.outbound.configure" || ws != "main" || resource != "mail" {
			return "", serviceauth.ErrForbidden
		}
		return "mail-service", nil
	}, Factory: func(string) application.ApplicationCredentialService {
		t.Fatal("credential service exposed")
		return nil
	},
		Audit: func(context.Context, httpapi.ServiceCredentialAuditEvent) error { return nil }, MailOutbound: newMailOutboundHandler(writer)}
	handler, err := httpapi.New(httpapi.Options{Registry: registry, Security: httpapi.SecurityOptions{InitialState: httpapi.StateFull, HostAllowed: func(r *http.Request) bool { return r.Host == "127.0.0.1" }}, ServiceAPI: api})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- serveMailInstallSocket(ctx, listener, handler, certServer.TLS.Clone()) }()
	defer func() {
		cancel()
		if err := <-done; err != nil {
			t.Error(err)
		}
	}()
	transport := &http.Transport{TLSClientConfig: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}, DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", filepath.Join(dir, mailInstallSocketName))
	}}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 5 * time.Second}
	endpoint := "https://127.0.0.1/api/v1/service/workspaces/main/applications/mail/mail-outbound-configure"
	body := `{"actor":"test-anchor","operation_id":"cfg-0123456789abcdef0123456789abcdef","provider":"smtp","host":"smtp.example.test","port":587,"tls":"starttls","username":"sender","password":"private-test-password"}`
	send := func(token string) (int, string) {
		r, err := http.NewRequest(http.MethodPost, endpoint, strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Authorization", "Bearer "+token)
		response, err := client.Do(r)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		raw, err := io.ReadAll(response.Body)
		if err != nil {
			t.Fatal(err)
		}
		return response.StatusCode, string(raw)
	}
	if status, _ := send("ingress-token"); status != 403 {
		t.Fatal("ingress grant allowed outbound change", status)
	}
	if status, body := send("outbound-token"); status != 200 || strings.Contains(body, "private-test-password") {
		t.Fatal("socket outbound commit failed or disclosed secret", status)
	}
	s, err := runner.ReadMailOutboundSnapshot(t.Context(), workspace)
	if err != nil {
		t.Fatal(err)
	}
	c, err := smtpconfig.Parse(s.Routing, s.Passwords)
	if err != nil || len(c.Profiles) != 1 || c.Profiles[0].Password != "private-test-password" || len(writer.events) != 2 {
		t.Fatal("socket did not commit or audit expected configuration", err)
	}
	allowed.Store(false)
	if status, _ := send("outbound-token"); status != 403 {
		t.Fatal("revoked grant accepted", status)
	}
}
