// TEST_CASES: MAILMOD-T-017
package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/anas-project/ANAS/internal/api/httpapi"
	"github.com/anas-project/ANAS/internal/application"
	"github.com/anas-project/ANAS/internal/serviceauth"
	"github.com/anas-project/ANAS/modules/mail/store"
)

type fakeOutboundConfigurer struct {
	fakeWorkerInstaller
	request outboundConfigureRequest
	calls   int
	err     error
	after   func()
}

func (f *fakeOutboundConfigurer) ConfigureOutbound(_ context.Context, request outboundConfigureRequest) error {
	f.calls++
	f.request = request
	if f.after != nil {
		f.after()
	}
	return f.err
}

func TestOutboundApplyRequiresAdministratorAndCurrentSavedDraft(t *testing.T) {
	for _, mode := range []string{"smtp", "direct", "origin", "duplicate", "case-alias", "missing-revision", "not-admin", "revoked-before", "revoked-after", "stale", "changed-after", "changed-content-after", "read-failed-after", "missing-client", "wrong-reference", "bad-password", "direct-password", "conflict", "permission", "unknown"} {
		t.Run(mode, func(t *testing.T) {
			h, login, _, dir := loginFixture(t)
			draft := store.SetupOutbound{Provider: "smtp", Host: "smtp.example.test", Port: 587, TLS: "starttls", Username: "sender", SecretRef: "secrets.MAIL_OUTBOUND_PASSWORDS"}
			data := &setupHTTPStore{httpStore: login, state: store.SetupState{Revision: 3, Draft: store.SetupDraft{Outbound: draft}}}
			d := &setupHTTPDirectory{httpDirectory: dir, admin: true}
			client := &fakeOutboundConfigurer{}
			h.store, h.directory, h.install = data, d, client
			body := `{"revision":3,"operation_id":"cfg-0123456789abcdef0123456789abcdef","password":"synthetic-password"}`
			origin := "https://mail.example.test"
			switch mode {
			case "direct":
				data.state.Draft.Outbound = store.SetupOutbound{Provider: "direct"}
				body = strings.Replace(body, "synthetic-password", "", 1)
			case "origin":
				origin = "https://evil.test"
			case "duplicate":
				body = strings.Replace(body, `"revision":3`, `"revision":3,"revision":3`, 1)
			case "case-alias":
				body = strings.Replace(body, `"revision"`, `"REVISION"`, 1)
			case "missing-revision":
				body = strings.Replace(body, `"revision":3,`, ``, 1)
			case "not-admin":
				d.admin = false
			case "revoked-before":
				dir.afterRead = func() {
					if dir.reads >= 2 {
						d.admin = false
					}
				}
			case "revoked-after":
				client.after = func() { d.admin = false }
			case "changed-after":
				client.after = func() { data.state.Revision++ }
			case "changed-content-after":
				client.after = func() { data.state.Draft.Outbound.Host = "other.example.test" }
			case "read-failed-after":
				client.after = func() { data.err = errors.New("private database error") }
			case "stale":
				data.state.Revision++
			case "missing-client":
				h.install = &fakeWorkerInstaller{}
			case "wrong-reference":
				data.state.Draft.Outbound.SecretRef = "secrets.MAIL_OTHER"
			case "bad-password":
				body = strings.Replace(body, "synthetic-password", "bad password", 1)
			case "direct-password":
				data.state.Draft.Outbound = store.SetupOutbound{Provider: "direct"}
			case "conflict":
				client.err = errInstallDrift
			case "permission":
				client.err = errOutboundPermission
			case "unknown":
				client.err = errors.New("private internal error")
			}
			r := httptest.NewRequest("POST", "https://mail.example.test/api/setup/outbound-apply", strings.NewReader(body))
			r.AddCookie(&http.Cookie{Name: sessionCookie, Value: "session-secret"})
			r.Header.Set("Origin", origin)
			r.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			want := map[string]int{"smtp": 200, "direct": 200, "origin": 403, "duplicate": 400, "case-alias": 400, "missing-revision": 400, "not-admin": 403, "revoked-before": 403, "revoked-after": 403, "stale": 409, "changed-after": 503, "changed-content-after": 503, "read-failed-after": 503, "missing-client": 503, "wrong-reference": 409, "bad-password": 400, "direct-password": 400, "conflict": 409, "permission": 403, "unknown": 503}[mode]
			if w.Code != want || strings.Contains(w.Body.String(), "synthetic-password") || strings.Contains(w.Body.String(), "private internal error") || strings.Contains(w.Body.String(), "private database error") {
				t.Fatal("incorrect or sensitive response", w.Code)
			}
			if (mode == "changed-after" || mode == "changed-content-after" || mode == "read-failed-after") && !strings.Contains(w.Body.String(), "outbound-unknown") {
				t.Fatal("possibly committed change did not report unknown", w.Body.String())
			}
			invoked := mode == "smtp" || mode == "direct" || mode == "revoked-after" || mode == "changed-after" || mode == "changed-content-after" || mode == "read-failed-after" || mode == "conflict" || mode == "permission" || mode == "unknown"
			if (client.calls == 1) != invoked {
				t.Fatal("unexpected host call", client.calls)
			}
			if mode == "smtp" && (client.request.Actor != fixtureAnchor || client.request.Password != "synthetic-password" || client.request.Provider != "smtp" || strings.Contains(w.Body.String(), `"enabled":true`)) {
				t.Fatal("incorrect configured request")
			}
		})
	}
}

func TestOutboundClientCallsRestrictedHostAndRejectsUnsafeToken(t *testing.T) {
	workspace := t.TempDir()
	if err := os.Mkdir(filepath.Join(workspace, ".anas"), 0700); err != nil {
		t.Fatal(err)
	}
	registry, err := httpapi.NewRegistry([]httpapi.Workspace{{ID: "main", Path: workspace}})
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	host, err := httpapi.New(httpapi.Options{Registry: registry, Security: httpapi.SecurityOptions{InitialState: httpapi.StateFull, HostAllowed: func(*http.Request) bool { return true }}, ServiceAPI: &httpapi.ServiceAPIOptions{
		Authenticate: func(_ context.Context, token, permission, ws, resource string) (string, error) {
			if token != "service.synthetic-token" {
				return "", serviceauth.ErrUnauthenticated
			}
			if permission != "mail.outbound.configure" || ws != "main" || resource != "mail" {
				return "", serviceauth.ErrForbidden
			}
			return "mail-service", nil
		}, Factory: func(string) application.ApplicationCredentialService { return nil }, Audit: func(context.Context, httpapi.ServiceCredentialAuditEvent) error { return nil },
		MailOutbound: func(ctx context.Context, call httpapi.ServiceMailOutboundCall) (httpapi.ServiceMailOutboundResult, error) {
			calls++
			if call.Request.Password != "synthetic-password" || call.Request.Actor != "anchor" || call.Reauthorize(ctx) != nil {
				t.Fatal("incorrect restricted request")
			}
			return httpapi.ServiceMailOutboundResult{State: "configured"}, nil
		},
	}})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewTLSServer(host)
	defer server.Close()
	roots := x509.NewCertPool()
	roots.AddCert(server.Certificate())
	path := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(path, []byte("service.synthetic-token\n"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MAIL_SERVICE_API_URL", server.URL)
	t.Setenv("MAIL_SERVICE_API_WORKSPACE", "main")
	t.Setenv("MAIL_SERVICE_API_TOKEN_FILE", path)
	configured, err := configuredMailInstaller([]string{"example.test"}, &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12})
	if err != nil {
		t.Fatal(err)
	}
	client := configured.(outboundConfigurer)
	request := outboundConfigureRequest{Actor: "anchor", OperationID: "cfg-0123456789abcdef0123456789abcdef", Provider: "smtp", Host: "smtp.example.test", Port: 587, TLS: "starttls", Username: "sender", Password: "synthetic-password"}
	if err := client.ConfigureOutbound(t.Context(), request); err != nil || calls != 1 {
		t.Fatal("restricted request failed", err)
	}
	if err := os.Chmod(path, 0644); err != nil {
		t.Fatal(err)
	}
	if err := client.ConfigureOutbound(t.Context(), request); !errors.Is(err, errInstall) || calls != 1 {
		t.Fatal("unsafe token accepted", err)
	}
	if err := os.Chmod(path, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("wrong-token\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := client.ConfigureOutbound(t.Context(), request); !errors.Is(err, errOutboundPermission) || calls != 1 {
		t.Fatal("rejected service credential misreported", err)
	}
}

func TestOutboundClientRejectsAmbiguousResultsWithoutRetry(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		status     int
	}{
		{"duplicate", `{"state":"configured","state":"configured"}`, 200},
		{"case-alias", `{"State":"configured"}`, 200},
		{"extra", `{"state":"configured","secret":"hidden"}`, 200},
		{"wrong-state", `{"state":"enabled"}`, 200},
		{"server-error", `{"error":"private"}`, 503},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls atomic.Int32
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				calls.Add(1)
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer server.Close()
			path := filepath.Join(t.TempDir(), "token")
			if err := os.WriteFile(path, []byte("service.synthetic-token\n"), 0600); err != nil {
				t.Fatal(err)
			}
			client := &hostWorkerInstaller{origin: server.URL, workspace: "main", tokenFile: path, client: server.Client()}
			err := client.ConfigureOutbound(t.Context(), outboundConfigureRequest{Password: "synthetic-password"})
			if !errors.Is(err, errInstall) || calls.Load() != 1 || strings.Contains(err.Error(), "private") {
				t.Fatal("ambiguous response accepted, leaked, or retried", err)
			}
		})
	}
}

func TestOutboundApplyThroughRestrictedServiceAPI(t *testing.T) {
	workspace := t.TempDir()
	if err := os.Mkdir(filepath.Join(workspace, ".anas"), 0700); err != nil {
		t.Fatal(err)
	}
	registry, err := httpapi.NewRegistry([]httpapi.Workspace{{ID: "main", Path: workspace}})
	if err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	host, err := httpapi.New(httpapi.Options{Registry: registry, Security: httpapi.SecurityOptions{InitialState: httpapi.StateFull, HostAllowed: func(*http.Request) bool { return true }}, ServiceAPI: &httpapi.ServiceAPIOptions{
		Authenticate: func(_ context.Context, token, permission, ws, resource string) (string, error) {
			if token != "service.synthetic-token" {
				return "", serviceauth.ErrUnauthenticated
			}
			if permission != "mail.outbound.configure" || ws != "main" || resource != "mail" {
				return "", serviceauth.ErrForbidden
			}
			return "mail-service", nil
		}, Factory: func(string) application.ApplicationCredentialService { return nil }, Audit: func(context.Context, httpapi.ServiceCredentialAuditEvent) error { return nil },
		MailOutbound: func(ctx context.Context, call httpapi.ServiceMailOutboundCall) (httpapi.ServiceMailOutboundResult, error) {
			calls.Add(1)
			if call.Request.Actor != fixtureAnchor || call.Request.Password != "synthetic-password" || call.Request.Provider != "smtp" || call.Reauthorize(ctx) != nil {
				t.Error("incorrect host request")
			}
			return httpapi.ServiceMailOutboundResult{State: "configured"}, nil
		},
	}})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewTLSServer(host)
	defer server.Close()
	roots := x509.NewCertPool()
	roots.AddCert(server.Certificate())
	path := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(path, []byte("service.synthetic-token\n"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MAIL_SERVICE_API_URL", server.URL)
	t.Setenv("MAIL_SERVICE_API_WORKSPACE", "main")
	t.Setenv("MAIL_SERVICE_API_TOKEN_FILE", path)
	client, err := configuredMailInstaller([]string{"example.test"}, &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12})
	if err != nil {
		t.Fatal(err)
	}
	h, login, _, dir := loginFixture(t)
	h.store = &setupHTTPStore{httpStore: login, state: store.SetupState{Revision: 3, Draft: store.SetupDraft{Outbound: store.SetupOutbound{Provider: "smtp", Host: "smtp.example.test", Port: 587, TLS: "starttls", Username: "sender", SecretRef: "secrets.MAIL_OUTBOUND_PASSWORDS"}}}}
	h.directory, h.install = &setupHTTPDirectory{httpDirectory: dir, admin: true}, client
	r := httptest.NewRequest(http.MethodPost, "https://mail.example.test/api/setup/outbound-apply", strings.NewReader(`{"revision":3,"operation_id":"cfg-0123456789abcdef0123456789abcdef","password":"synthetic-password"}`))
	r.AddCookie(&http.Cookie{Name: sessionCookie, Value: "session-secret"})
	r.Header.Set("Origin", "https://mail.example.test")
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 200 || calls.Load() != 1 || strings.Contains(w.Body.String(), "synthetic-password") || strings.Contains(w.Body.String(), `"enabled":true`) {
		t.Fatal("administrator-to-host handoff failed", w.Code)
	}
}
