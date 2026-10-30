// TEST_CASES: MAILMOD-T-059
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
	"testing"

	"github.com/anas-project/ANAS/internal/api/httpapi"
	"github.com/anas-project/ANAS/internal/application"
	"github.com/anas-project/ANAS/internal/serviceauth"
	"github.com/anas-project/ANAS/modules/mail/store"
)

type fakeRelayInstaller struct {
	fakeWorkerInstaller
	relayCalls   int
	request      relayInstallRequest
	afterRelay   func()
	resultSource string
}

func (f *fakeRelayInstaller) InstallRelay(_ context.Context, r relayInstallRequest) (workerInstallResult, error) {
	f.relayCalls++
	f.request = r
	if f.afterRelay != nil {
		f.afterRelay()
	}
	state := "prepared"
	if r.Action == "commit" {
		state = "configured"
	}
	sourceID := r.SourceID
	if f.resultSource != "" {
		sourceID = f.resultSource
	}
	return workerInstallResult{State: state, SourceID: sourceID}, nil
}

func TestRelayInstallRequiresCurrentAdministratorAndSavedDraft(t *testing.T) {
	reader, control, notification := strings.Repeat("a", 64), strings.Repeat("b", 64), strings.Repeat("c", 64)
	for _, mode := range []string{"prepare", "commit", "origin", "duplicate", "case-duplicate", "case-only", "not-admin", "revoked-before", "revoked-after", "changed-after", "content-after", "read-after", "wrong-result-source", "stale", "domain", "missing-client", "wrong-provider", "missing-notify", "same-token"} {
		t.Run(mode, func(t *testing.T) {
			h, login, _, dir := loginFixture(t)
			data := &setupHTTPStore{httpStore: login, state: store.SetupState{Revision: 3, Draft: store.SetupDraft{Inbound: "vps-direct", Domain: "example.test", SourceURL: "https://relay.example.test", LimitsConfirmed: true, Limits: store.SetupLimits{CloudBytes: 2 << 30, DeliveredDays: 7}, NotificationURL: "https://mail.example.test"}}}
			d := &setupHTTPDirectory{httpDirectory: dir, admin: true}
			install := &fakeRelayInstaller{}
			h.store, h.directory, h.install = data, d, install
			body := `{"revision":3,"action":"prepare","operation_id":"cfg-0123456789abcdef0123456789abcdef","source_id":"11111111-1111-4111-8111-111111111111","generation":"22222222-2222-4222-8222-222222222222","drain_ids":[],"capacity_bytes":1073741824,"reader_token":"` + reader + `","control_token":"` + control + `","notification_token":"` + notification + `","ipv4_only":false}`
			origin := "https://mail.example.test"
			switch mode {
			case "commit":
				body = strings.Replace(body, `"action":"prepare"`, `"action":"commit"`, 1)
			case "origin":
				origin = "https://evil.test"
			case "duplicate":
				body = strings.Replace(body, `"revision":3`, `"revision":3,"revision":3`, 1)
			case "case-duplicate":
				body = strings.Replace(body, `"revision":3`, `"revision":3,"REVISION":3`, 1)
			case "case-only":
				body = strings.Replace(body, `"revision":3`, `"REVISION":3`, 1)
			case "not-admin":
				d.admin = false
			case "revoked-before":
				dir.afterRead = func() {
					if dir.reads >= 2 {
						d.admin = false
					}
				}
			case "revoked-after":
				install.afterRelay = func() { d.admin = false }
			case "changed-after":
				install.afterRelay = func() { data.state.Revision++ }
			case "content-after":
				install.afterRelay = func() { data.state.Draft.Domain = "changed.test" }
			case "read-after":
				install.afterRelay = func() { data.err = errors.New("private database failure") }
			case "wrong-result-source":
				install.resultSource = "33333333-3333-4333-8333-333333333333"
			case "stale":
				data.state.Revision++
			case "domain":
				data.state.Draft.Domain = "other.test"
			case "missing-client":
				h.install = nil
			case "wrong-provider":
				data.state.Draft.Inbound = "cloud-relay"
			case "missing-notify":
				body = strings.Replace(body, notification, "", 1)
			case "same-token":
				body = strings.Replace(body, control, reader, 1)
			}
			r := httptest.NewRequest("POST", "https://mail.example.test/api/setup/relay-install", strings.NewReader(body))
			r.AddCookie(&http.Cookie{Name: sessionCookie, Value: "session-secret"})
			r.Header.Set("Origin", origin)
			r.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			want := map[string]int{"prepare": 200, "commit": 200, "origin": 403, "duplicate": 400, "case-duplicate": 400, "case-only": 400, "not-admin": 403, "revoked-before": 403, "revoked-after": 403, "changed-after": 503, "content-after": 503, "read-after": 503, "wrong-result-source": 503, "stale": 409, "domain": 409, "missing-client": 503, "wrong-provider": 409, "missing-notify": 409, "same-token": 400}[mode]
			if w.Code != want || strings.Contains(w.Body.String(), reader) || strings.Contains(w.Body.String(), control) || strings.Contains(w.Body.String(), notification) {
				t.Fatalf("guard response: %d", w.Code)
			}
			if mode != "prepare" && mode != "commit" && mode != "revoked-after" && mode != "changed-after" && mode != "content-after" && mode != "read-after" && mode != "wrong-result-source" && install.relayCalls != 0 {
				t.Fatal("denied request invoked installer")
			}
			if mode == "changed-after" || mode == "content-after" || mode == "read-after" || mode == "wrong-result-source" {
				if install.relayCalls != 1 || !strings.Contains(w.Body.String(), "install-unknown") || strings.Contains(w.Body.String(), "private database failure") || strings.Contains(w.Body.String(), "source_id") {
					t.Fatal("post-install uncertainty was not preserved")
				}
			}
			if mode == "prepare" || mode == "commit" {
				if install.relayCalls != 1 || install.request.Actor != fixtureAnchor || install.request.TotalCapacityBytes != 2<<30 || install.request.SourceOrigin != data.state.Draft.SourceURL || install.request.NotificationToken != notification || strings.Contains(w.Body.String(), `"enabled":true`) {
					t.Fatal("bad source binding")
				}
			}
		})
	}
}

func TestRelayInstallClientCallsRestrictedHostRoute(t *testing.T) {
	workspace := t.TempDir()
	if err := os.Mkdir(filepath.Join(workspace, ".anas"), 0700); err != nil {
		t.Fatal(err)
	}
	registry, err := httpapi.NewRegistry([]httpapi.Workspace{{ID: "main", Path: workspace}})
	if err != nil {
		t.Fatal(err)
	}
	reader, control := strings.Repeat("a", 64), strings.Repeat("b", 64)
	calls := 0
	host, err := httpapi.New(httpapi.Options{Registry: registry, Security: httpapi.SecurityOptions{InitialState: httpapi.StateFull, HostAllowed: func(*http.Request) bool { return true }}, ServiceAPI: &httpapi.ServiceAPIOptions{
		Authenticate: func(_ context.Context, token, permission, workspace, resource string) (string, error) {
			if token != "service.synthetic-token" {
				return "", serviceauth.ErrUnauthenticated
			}
			if permission != "mail.ingress.install" || workspace != "main" || resource != "mail" {
				return "", serviceauth.ErrForbidden
			}
			return "mail-service", nil
		},
		Factory: func(string) application.ApplicationCredentialService { return nil }, Audit: func(context.Context, httpapi.ServiceCredentialAuditEvent) error { return nil },
		MailInstall: func(ctx context.Context, call httpapi.ServiceMailInstallCall) (httpapi.ServiceMailInstallResult, error) {
			calls++
			if call.Request.Provider != "cloud-relay" || call.Request.ReaderToken != reader || call.Request.ControlToken != control || call.Reauthorize(ctx) != nil {
				t.Fatal("incorrect restricted request")
			}
			return httpapi.ServiceMailInstallResult{State: "prepared", SourceID: call.Request.SourceID}, nil
		},
	}})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewTLSServer(host)
	defer server.Close()
	roots := x509.NewCertPool()
	roots.AddCert(server.Certificate())
	token := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(token, []byte("service.synthetic-token\n"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MAIL_SERVICE_API_URL", server.URL)
	t.Setenv("MAIL_SERVICE_API_WORKSPACE", "main")
	t.Setenv("MAIL_SERVICE_API_TOKEN_FILE", token)
	configured, err := configuredMailInstaller([]string{"example.test"}, &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12})
	if err != nil {
		t.Fatal(err)
	}
	client, ok := configured.(relayInstaller)
	if !ok {
		t.Fatal("relay client unavailable")
	}
	input := relayInstallRequest{Actor: "anchor", Action: "prepare", OperationID: "cfg-0123456789abcdef0123456789abcdef", SourceID: "11111111-1111-4111-8111-111111111111", Generation: "22222222-2222-4222-8222-222222222222", SourceOrigin: "https://relay.example.test", Domains: []string{"example.test"}, AllowedDomains: []string{"example.test"}, DrainIDs: []string{}, CapacityBytes: 1024, TotalCapacityBytes: 2048, DeliveredDays: 7, ReaderToken: reader, ControlToken: control}
	result, err := client.InstallRelay(t.Context(), input)
	if err != nil || calls != 1 || result.State != "prepared" {
		t.Fatalf("host relay protocol: %v", err)
	}
}
