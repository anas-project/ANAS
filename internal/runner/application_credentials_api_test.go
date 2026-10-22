package runner

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/anas-project/ANAS/internal/api/httpapi"
	"github.com/anas-project/ANAS/internal/application"
	"github.com/anas-project/ANAS/internal/serviceauth"
)

// Exercise the actual service grant, HTTP policy and active-deployment reader
// together. No owner session or replacement credential reader is installed.
func TestApplicationCredentialAPIIntegratesCurrentValuesAndHostRevocation(t *testing.T) {
	f := newApplicationCredentialFixture(t, true)
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	config := serviceauth.Config{Directory: filepath.Join(root, "clients"), Clients: []serviceauth.Client{{
		ID: "portal", Enabled: true, UID: os.Geteuid(), GID: os.Getegid(), Grants: []serviceauth.Grant{{Permission: "application.credentials.read", Workspace: "main", Resources: []string{"core-admin"}}},
	}}}
	manager, err := serviceauth.New(serviceauth.Options{LoadConfig: func() (serviceauth.Config, error) { return config, nil }})
	if err != nil {
		t.Fatal(err)
	}
	if err = manager.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	token, err := os.ReadFile(filepath.Join(config.Directory, "portal", "token"))
	if err != nil {
		t.Fatal(err)
	}
	registry, err := httpapi.NewRegistry([]httpapi.Workspace{{ID: "main", Path: f.workspace}})
	if err != nil {
		t.Fatal(err)
	}
	audits := 0
	handler, err := httpapi.New(httpapi.Options{Registry: registry, Security: httpapi.SecurityOptions{InitialState: httpapi.StateFull, HostAllowed: func(*http.Request) bool { return true }}, ServiceAPI: &httpapi.ServiceAPIOptions{
		Authenticate: manager.Authenticate, Factory: NewWorkspaceApplicationCredentialService,
		Audit: func(_ context.Context, event httpapi.ServiceCredentialAuditEvent) error {
			if event.ClientID != "portal" || event.Application != "core-admin" {
				t.Error("wrong audit identity")
			}
			audits++
			return nil
		},
	}})
	if err != nil {
		t.Fatal(err)
	}
	request := func(applicationID string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodPost, "https://anas.example/api/v1/service/workspaces/main/applications/"+applicationID+"/credentials", strings.NewReader(`{"actor":"owner"}`))
		r.TLS = &tls.ConnectionState{}
		r.Header.Set("Authorization", "Bearer "+strings.TrimSpace(string(token)))
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	for _, password := range []string{"initial-local-password", "changed-local-password"} {
		f.setLocalPassword(password)
		w := request("core-admin")
		var result application.ApplicationCredential
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &result) != nil || result.Password != password || w.Header().Get("Cache-Control") != "no-store" {
			t.Fatal("credential HTTP contract did not return current value")
		}
		if strings.Contains(w.Body.String(), strings.TrimSpace(string(token))) {
			t.Fatal("service token exposed")
		}
	}
	if audits != 2 {
		t.Fatal("credential disclosures were not audited")
	}
	if w := request("other-admin"); w.Code != 403 {
		t.Fatalf("ungranted object status %d", w.Code)
	}
	config.Clients[0].Enabled = false
	// A workspace restore/switch cannot re-enable host authority.
	f.persist()
	if w := request("core-admin"); w.Code != 401 || strings.Contains(w.Body.String(), "changed-local-password") {
		t.Fatal("workspace state restored revoked authority")
	}
	if audits != 2 {
		t.Fatal("rejected requests reached the credential reader")
	}
}
