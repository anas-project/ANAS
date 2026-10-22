package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/anas-project/ANAS/internal/appcatalog"
)

func credentialFixture(t *testing.T, service func(http.ResponseWriter, *http.Request)) *fixture {
	t.Helper()
	f := newFixture(t)
	f.person.Groups[0].GUID = "admin-group-guid"
	f.directory.people[f.person.Anchor] = f.person
	f.server.config.Administration = true
	f.server.config.AdminGroup = "operator-group"
	f.server.config.AdminGroupGUID = "admin-group-guid"
	app := &f.server.catalog.Applications[0]
	app.Kind, app.Access.Mode = "admin", "local"
	app.Source = appcatalog.Source{Kind: "module", Module: "test_module"}
	app.CredentialsAvailable = true
	upstream := httptest.NewTLSServer(http.HandlerFunc(service))
	t.Cleanup(upstream.Close)
	f.server.config.ServiceAPIURL = upstream.URL
	f.server.config.ServiceAPIWorkspace = "main"
	f.server.config.ServiceAPITokenFile = filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(f.server.config.ServiceAPITokenFile, []byte("portal.first-token"), 0600); err != nil {
		t.Fatal(err)
	}
	return f
}

func serveCredential(w http.ResponseWriter, r *http.Request) {
	respond(w, 200, applicationCredential{Application: "tool", Username: "local-admin", Password: "fixture-password-only", URL: "https://tool.example"})
}

func TestCredentialsCurrentAuthorityAndRecentAuthentication(t *testing.T) {
	for _, tc := range []struct {
		name, code string
		change     func(*fixture)
	}{
		{"non-administrator", "access_denied", func(f *fixture) { f.server.config.Administration = false }},
		{"role-removed", "access_denied", func(f *fixture) { p := f.person; p.Groups = p.Groups[1:]; f.directory.people[p.Anchor] = p }},
		{"disabled-account", "authentication_required", func(f *fixture) { p := f.person; p.Disabled = true; f.directory.people[p.Anchor] = p }},
		{"IAM-policy-removed", "access_denied", func(f *fixture) { f.permission.IsEnabled = false }},
		{"IAM-application", "access_denied", func(f *fixture) { f.server.catalog.Applications[0].Access.Mode = "iam" }},
		{"user-application", "access_denied", func(f *fixture) { f.server.catalog.Applications[0].Kind = "user" }},
		{"external-application", "access_denied", func(f *fixture) { f.server.catalog.Applications[0].Source.Kind = "external" }},
		{"undeclared-credential", "access_denied", func(f *fixture) { f.server.catalog.Applications[0].CredentialsAvailable = false }},
		{"hidden-application", "access_denied", func(f *fixture) { f.server.catalog.Applications[0].Hidden = true }},
		{"expired-verification", "recent_authentication_required", func(f *fixture) {
			f.server.store.delete(f.session.ID)
			v := f.session
			v.AuthTime = time.Now().Add(-6 * time.Minute)
			f.server.store.add(v)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			f := credentialFixture(t, func(w http.ResponseWriter, r *http.Request) { calls++; serveCredential(w, r) })
			tc.change(f)
			w := f.request("POST", "/api/apps/tool/credentials", `{}`)
			if w.Code < 400 || !strings.Contains(w.Body.String(), tc.code) || calls != 0 {
				t.Fatalf("guard bypassed: status=%d body=%s calls=%d", w.Code, w.Body.String(), calls)
			}
		})
	}
}

func TestCredentialsUseFiveMinuteProofAndDoNotExposeServiceToken(t *testing.T) {
	calls := 0
	f := credentialFixture(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		var body struct{ Actor, RequestID string }
		var raw map[string]string
		if json.NewDecoder(r.Body).Decode(&raw) != nil {
			t.Error("bad service request")
		}
		body.Actor, body.RequestID = raw["actor"], raw["request_id"]
		if r.URL.Path != "/api/v1/service/workspaces/main/applications/tool/credentials" || r.Method != "POST" || r.Header.Get("Authorization") != "Bearer portal.first-token" || body.Actor != "actor-anchor" || body.RequestID == "" || len(raw) != 2 {
			t.Error("incorrect service authorization or attribution")
		}
		serveCredential(w, r)
	})
	f.server.store.delete(f.session.ID)
	v := f.session
	v.AuthTime, v.VerifiedAt = time.Now().Add(-time.Hour), time.Now().Add(-4*time.Minute)
	f.server.store.add(v)
	for i := 0; i < 2; i++ {
		w := f.request("POST", "/api/apps/tool/credentials", `{}`)
		if w.Code != 200 || !strings.Contains(w.Body.String(), "fixture-password-only") || w.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("recent verification rejected: %d %s", w.Code, w.Body.String())
		}
		if strings.Contains(w.Body.String(), "portal.first-token") {
			t.Fatal("service token reached browser")
		}
	}
	if calls != 2 {
		t.Fatal("credential was cached instead of reading the service")
	}
	records, _ := json.Marshal(f.server.audit.list())
	if strings.Contains(string(records), "fixture-password-only") || strings.Contains(string(records), "portal.first-token") || !strings.Contains(string(records), `"service":"read"`) {
		t.Fatal("audit must preserve outcomes without credentials")
	}
	if w := f.request("GET", "/api/audit", ""); w.Code != 200 || !strings.Contains(w.Body.String(), `"action":"application.credentials.read"`) {
		t.Fatal("credential audit is not visible to its operator")
	}
	view := f.request("GET", "/api/apps", "").Body.String()
	if !strings.Contains(view, `"credentials_available":true`) || strings.Contains(view, "fixture-password-only") {
		t.Fatal("catalog availability or secrecy is incorrect")
	}
	f.server.config.Administration = false
	if strings.Contains(f.request("GET", "/api/apps", "").Body.String(), `"credentials_available":true`) {
		t.Fatal("credential button advertised to a non-administrator")
	}
}

func TestCredentialsCSRFAndBodyRejected(t *testing.T) {
	calls := 0
	f := credentialFixture(t, func(w http.ResponseWriter, r *http.Request) { calls++; serveCredential(w, r) })
	for _, origin := range []string{"", "https://other.example"} {
		r := httptest.NewRequest("POST", "https://portal.example/api/apps/tool/credentials", strings.NewReader(`{}`))
		r.RemoteAddr = "127.0.0.1:1000"
		r.AddCookie(&http.Cookie{Name: sessionCookie, Value: f.session.ID})
		r.Header.Set("Origin", origin)
		r.Header.Set("X-CSRF-Token", f.session.CSRF)
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		f.server.Handler().ServeHTTP(w, r)
		if w.Code != 403 || !strings.Contains(w.Body.String(), "csrf_rejected") {
			t.Fatal("cross-origin credentials accepted")
		}
	}
	if w := f.request("POST", "/api/apps/tool/credentials", `{"actor":"another-person"}`); w.Code != 400 {
		t.Fatal("browser can override the actor")
	}
	if calls != 0 {
		t.Fatal("rejected browser requests reached the service")
	}
}

func TestCredentialsFailClosedWhenAuthorityChangesInFlight(t *testing.T) {
	for _, change := range []string{"logout", "role", "audit"} {
		t.Run(change, func(t *testing.T) {
			var f *fixture
			f = credentialFixture(t, func(w http.ResponseWriter, r *http.Request) {
				switch change {
				case "logout":
					f.server.store.delete(f.session.ID)
				case "role":
					p := f.person
					p.Groups = p.Groups[1:]
					f.directory.people[p.Anchor] = p
				case "audit":
					f.server.audit.broken = true
				}
				serveCredential(w, r)
			})
			w := f.request("POST", "/api/apps/tool/credentials", `{}`)
			if w.Code < 400 || strings.Contains(w.Body.String(), "fixture-password-only") {
				t.Fatal("credential disclosed after authority or audit changed")
			}
		})
	}
}

func TestCredentialsTokenPublicationRecoveryAndRotation(t *testing.T) {
	calls := 0
	var f *fixture
	f = credentialFixture(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Header.Get("Authorization") == "Bearer portal.first-token" {
			tmp := f.server.config.ServiceAPITokenFile + ".new"
			if err := os.WriteFile(tmp, []byte("portal.next-token"), 0600); err != nil {
				t.Error(err)
			}
			if err := os.Rename(tmp, f.server.config.ServiceAPITokenFile); err != nil {
				t.Error(err)
			}
			failure(w, 401, "expired")
			return
		}
		serveCredential(w, r)
	})
	if err := os.Remove(f.server.config.ServiceAPITokenFile); err != nil {
		t.Fatal(err)
	}
	if w := f.request("POST", "/api/apps/tool/credentials", `{}`); w.Code != 503 || calls != 0 {
		t.Fatal("missing startup token did not fail locally")
	}
	if err := os.WriteFile(f.server.config.ServiceAPITokenFile, []byte("portal.first-token"), 0600); err != nil {
		t.Fatal(err)
	}
	if w := f.request("POST", "/api/apps/tool/credentials", `{}`); w.Code != 200 || calls != 2 {
		t.Fatalf("newly published token did not recover: status=%d calls=%d", w.Code, calls)
	}
	if w := f.request("POST", "/api/apps/tool/credentials", `{}`); w.Code != 200 || calls != 3 {
		t.Fatal("rotated token not read on subsequent request")
	}
}

func TestCredentialsServiceFailureNeverRetriesOrLeaksResponses(t *testing.T) {
	for _, status := range []int{401, 403, 307, 500, 200} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			calls := 0
			f := credentialFixture(t, func(w http.ResponseWriter, r *http.Request) {
				calls++
				w.Header().Set("Location", "/redirect-target")
				w.WriteHeader(status)
				w.Write([]byte("upstream-sensitive-error"))
			})
			w := f.request("POST", "/api/apps/tool/credentials", `{}`)
			if w.Code != 503 || calls != 1 || strings.Contains(w.Body.String(), "upstream-sensitive-error") {
				t.Fatalf("unsafe retry or error disclosure: status=%d calls=%d", w.Code, calls)
			}
		})
	}
}

func TestCredentialAPIConfiguration(t *testing.T) {
	if err := validateServiceAPIConfig(Config{}); err != nil {
		t.Fatal("disabled feature broke an existing deployment")
	}
	for _, origin := range []string{"http://anasd.example", "https://user:password@anasd.example", "https://anasd.example/path", "https://anasd.example?secret=x"} {
		if validateServiceAPIConfig(Config{ServiceAPIURL: origin, ServiceAPIWorkspace: "main", ServiceAPITokenFile: "/run/anas/service-client/token"}) == nil {
			t.Fatal("unsafe service origin accepted")
		}
	}
}
