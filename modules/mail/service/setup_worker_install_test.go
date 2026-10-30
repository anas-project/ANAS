// TEST_CASES: MAILMOD-T-042
package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
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

type fakeWorkerInstaller struct {
	calls   int
	request workerInstallRequest
	after   func()
}

func TestWorkerInstallClientCallsActualRestrictedHostRoute(t *testing.T) {
	workspace := t.TempDir()
	if err := os.Mkdir(filepath.Join(workspace, ".anas"), 0700); err != nil {
		t.Fatal(err)
	}
	registry, err := httpapi.NewRegistry([]httpapi.Workspace{{ID: "main", Path: workspace}})
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	serverHandler, err := httpapi.New(httpapi.Options{Registry: registry, Security: httpapi.SecurityOptions{InitialState: httpapi.StateFull, HostAllowed: func(*http.Request) bool { return true }}, ServiceAPI: &httpapi.ServiceAPIOptions{
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
			if call.ClientID != "mail-service" || call.Request.RoutingToken != "private-routing-token" || call.Request.DeploymentToken != "private-deployment-token" || call.Request.Actor != "anchor" || call.Reauthorize(ctx) != nil {
				t.Fatal("host received incorrect restricted request")
			}
			return httpapi.ServiceMailInstallResult{State: "prepared", SourceID: "11111111-1111-4111-8111-111111111111"}, nil
		},
	}})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewTLSServer(serverHandler)
	defer server.Close()
	roots := x509.NewCertPool()
	roots.AddCert(server.Certificate())
	tokenPath := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(tokenPath, []byte("service.synthetic-token\n"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MAIL_SERVICE_API_URL", server.URL)
	t.Setenv("MAIL_SERVICE_API_WORKSPACE", "main")
	t.Setenv("MAIL_SERVICE_API_TOKEN_FILE", tokenPath)
	client, err := configuredMailInstaller([]string{"example.test"}, &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12})
	if err != nil {
		t.Fatal(err)
	}
	input := workerInstallRequest{Actor: "anchor", Action: "prepare", OperationID: "cfg-0123456789abcdef0123456789abcdef", AccountID: strings.Repeat("a", 32), ZoneID: strings.Repeat("b", 32), Worker: "anas-worker", Bucket: "anas-bucket", Domains: []string{"example.test"}, AllowedDomains: []string{"example.test"}, DrainIDs: []string{}, CapacityBytes: 1024, TotalCapacityBytes: 2048, DeliveredDays: 7, DeploymentToken: "private-deployment-token", RoutingToken: "private-routing-token"}
	result, err := client.Install(t.Context(), input)
	if err != nil || calls != 1 || result.State != "prepared" {
		t.Fatal("restricted service protocol failed", err)
	}
}

func (f *fakeWorkerInstaller) Domains() []string { return []string{"example.test"} }
func (f *fakeWorkerInstaller) Install(_ context.Context, r workerInstallRequest) (workerInstallResult, error) {
	f.calls++
	f.request = r
	if f.after != nil {
		f.after()
	}
	state := "prepared"
	if r.Action == "commit" {
		state = "configured"
	}
	return workerInstallResult{State: state, SourceID: "11111111-1111-4111-8111-111111111111"}, nil
}

func TestWorkerInstallRequiresCurrentAdministratorAndSavedDraft(t *testing.T) {
	for _, mode := range []string{"prepare", "commit", "origin", "duplicate", "case-duplicate", "case-only", "not-admin", "revoked-before", "revoked-after", "changed-after", "content-after", "read-after", "stale", "domain", "missing-client"} {
		t.Run(mode, func(t *testing.T) {
			h, login, _, dir := loginFixture(t)
			data := &setupHTTPStore{httpStore: login, state: store.SetupState{Revision: 3, Draft: store.SetupDraft{Inbound: "worker-r2", Domain: "example.test", LimitsConfirmed: true, Limits: store.SetupLimits{CloudBytes: 2 << 30, DeliveredDays: 7}, NotificationURL: "https://mail.example.test"}}}
			d := &setupHTTPDirectory{httpDirectory: dir, admin: true}
			install := &fakeWorkerInstaller{}
			h.store, h.directory, h.install = data, d, install
			body := `{"revision":3,"action":"prepare","operation_id":"cfg-0123456789abcdef0123456789abcdef","account_id":"account","zone_id":"zone","worker":"worker","bucket":"bucket","drain_ids":[],"capacity_bytes":1073741824,"deployment_token":"private-deployment-token","routing_token":"private-routing-token"}`
			origin := "https://mail.example.test"
			switch mode {
			case "commit":
				body = strings.Replace(strings.Replace(body, `"action":"prepare"`, `"action":"commit"`, 1), `,"deployment_token":"private-deployment-token"`, "", 1)
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
				install.after = func() { d.admin = false }
			case "changed-after":
				install.after = func() { data.state.Revision++ }
			case "content-after":
				install.after = func() { data.state.Draft.Domain = "changed.test" }
			case "read-after":
				install.after = func() { data.err = errors.New("private database failure") }
			case "stale":
				data.state.Revision++
			case "domain":
				data.state.Draft.Domain = "other.test"
			case "missing-client":
				h.install = nil
			}
			r := httptest.NewRequest("POST", "https://mail.example.test/api/setup/worker-install", strings.NewReader(body))
			r.AddCookie(&http.Cookie{Name: sessionCookie, Value: "session-secret"})
			r.Header.Set("Origin", origin)
			r.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			want := map[string]int{"prepare": 200, "commit": 200, "origin": 403, "duplicate": 400, "case-duplicate": 400, "case-only": 400, "not-admin": 403, "revoked-before": 403, "revoked-after": 403, "changed-after": 503, "content-after": 503, "read-after": 503, "stale": 409, "domain": 409, "missing-client": 503}[mode]
			if w.Code != want || strings.Contains(w.Body.String(), "private-") {
				t.Fatal("install guard", w.Code, w.Body.String())
			}
			if mode != "prepare" && mode != "commit" && mode != "revoked-after" && mode != "changed-after" && mode != "content-after" && mode != "read-after" && install.calls != 0 {
				t.Fatal("denied request invoked installer")
			}
			if mode == "changed-after" || mode == "content-after" || mode == "read-after" {
				if install.calls != 1 || !strings.Contains(w.Body.String(), "install-unknown") || strings.Contains(w.Body.String(), "private database failure") || strings.Contains(w.Body.String(), "source_id") {
					t.Fatal("post-install uncertainty was not preserved")
				}
			}
			if mode == "prepare" || mode == "commit" {
				if install.calls != 1 || install.request.Actor != fixtureAnchor || install.request.TotalCapacityBytes != 2<<30 || install.request.NotificationOrigin != "https://mail.example.test" || strings.Contains(w.Body.String(), "enabled\":true") {
					t.Fatal("bad installation binding", install.calls)
				}
			}
		})
	}
}

func TestWorkerInstallHostClientValidatesTLSAndRedactsFailures(t *testing.T) {
	requests := 0
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.URL.Path != "/api/v1/service/workspaces/main/applications/mail/mail-ingress-install" || r.Header.Get("Authorization") != "Bearer service.synthetic-token" {
			t.Fatal("wrong service target")
		}
		var input map[string]any
		if json.NewDecoder(r.Body).Decode(&input) != nil || input["routing_token"] != "private-routing-token" {
			t.Fatal("bad private request")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"state":"prepared","source_id":"11111111-1111-4111-8111-111111111111"}`))
	}))
	defer server.Close()
	root := x509.NewCertPool()
	root.AddCert(server.Certificate())
	path := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(path, []byte("service.synthetic-token\n"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MAIL_SERVICE_API_URL", server.URL)
	t.Setenv("MAIL_SERVICE_API_WORKSPACE", "main")
	t.Setenv("MAIL_SERVICE_API_TOKEN_FILE", path)
	client, err := configuredMailInstaller([]string{"example.test"}, &tls.Config{RootCAs: root, MinVersion: tls.VersionTLS12})
	if err != nil {
		t.Fatal(err)
	}
	result, err := client.Install(t.Context(), workerInstallRequest{Action: "prepare", RoutingToken: "private-routing-token"})
	if err != nil || result.State != "prepared" || requests != 1 {
		t.Fatal("TLS host request failed", err)
	}
	if err := os.Chmod(path, 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := client.Install(t.Context(), workerInstallRequest{}); !errors.Is(err, errInstall) || requests != 1 {
		t.Fatal("unsafe service token accepted", err)
	}
	if err := os.Chmod(path, 0600); err != nil {
		t.Fatal(err)
	}
	other, err := configuredMailInstaller([]string{"example.test"}, &tls.Config{RootCAs: x509.NewCertPool(), MinVersion: tls.VersionTLS12})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := other.Install(t.Context(), workerInstallRequest{}); !errors.Is(err, errInstall) || requests != 1 {
		t.Fatal("untrusted TLS accepted", err)
	}
}

func TestMailInstallClientRejectsAmbiguousHostReplies(t *testing.T) {
	const id = "11111111-1111-4111-8111-111111111111"
	const other = "22222222-2222-4222-8222-222222222222"
	for _, tc := range []struct {
		name, body string
		status     int
		relay      bool
		want       error
	}{
		{"worker-valid", `{"state":"prepared","source_id":"` + id + `"}`, 200, false, nil},
		{"duplicate-state", `{"state":"changed","state":"prepared","source_id":"` + id + `"}`, 200, false, errInstall},
		{"case-state", `{"STATE":"prepared","source_id":"` + id + `"}`, 200, false, errInstall},
		{"duplicate-source", `{"state":"prepared","source_id":"` + other + `","source_id":"` + id + `"}`, 200, false, errInstall},
		{"unknown-field", `{"state":"prepared","source_id":"` + id + `","extra":true}`, 200, false, errInstall},
		{"relay-valid", `{"state":"prepared","source_id":"` + id + `"}`, 200, true, nil},
		{"relay-other-source", `{"state":"prepared","source_id":"` + other + `"}`, 200, true, errInstall},
		{"relay-conflict", `{}`, 409, true, errInstallDrift},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer server.Close()
			path := filepath.Join(t.TempDir(), "token")
			if err := os.WriteFile(path, []byte("service.synthetic-token"), 0600); err != nil {
				t.Fatal(err)
			}
			client := &hostWorkerInstaller{origin: server.URL, workspace: "main", tokenFile: path, client: server.Client()}
			var result workerInstallResult
			var err error
			if tc.relay {
				result, err = client.InstallRelay(t.Context(), relayInstallRequest{Action: "prepare", SourceID: id})
			} else {
				result, err = client.Install(t.Context(), workerInstallRequest{Action: "prepare"})
			}
			if tc.want == nil {
				if err != nil || result.SourceID != id {
					t.Fatal("valid reply rejected", err)
				}
			} else if !errors.Is(err, tc.want) || result != (workerInstallResult{}) {
				t.Fatal("ambiguous reply accepted", err)
			}
		})
	}
}
