// TEST_CASES: MAILMOD-T-005 MAILMOD-T-017 MAILMOD-T-020
package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/anas-project/ANAS/modules/mail/identity"
	"github.com/anas-project/ANAS/modules/mail/store"
)

type setupHTTPStore struct {
	*httpStore
	state      store.SetupState
	saves      int
	err        error
	afterDraft func()
}

func (s *setupHTTPStore) AdmissionGroup(context.Context, string) (string, error) {
	return "private-group", nil
}
func (s *setupHTTPStore) PinAdmissionGroup(context.Context, string, string) error { return nil }
func (s *setupHTTPStore) SetupDraft(context.Context, string, string, string) (store.SetupState, error) {
	if s.afterDraft != nil {
		s.afterDraft()
	}
	return s.state, s.err
}
func (s *setupHTTPStore) SaveSetupDraft(_ context.Context, _, _, _ string, revision int64, d store.SetupDraft) (store.SetupState, error) {
	if s.err != nil {
		return store.SetupState{}, s.err
	}
	if s.state.Revision != revision {
		return store.SetupState{}, store.ErrSetupConflict
	}
	s.saves++
	s.state.Revision++
	s.state.Draft = d
	if s.afterDraft != nil {
		s.afterDraft()
	}
	return s.state, nil
}

type setupHTTPDirectory struct {
	*httpDirectory
	admin bool
}

func (d *setupHTTPDirectory) Person(ctx context.Context, anchor string) (identity.Person, error) {
	p, err := d.httpDirectory.Person(ctx, anchor)
	if !d.admin {
		p.Groups = nil
	}
	return p, err
}
func setupRequest(t *testing.T, h http.Handler, method, body, origin string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(method, "https://mail.example.test/api/setup", strings.NewReader(body))
	r.AddCookie(&http.Cookie{Name: sessionCookie, Value: "session-secret"})
	r.Header.Set("Content-Type", "application/json")
	if origin != "" {
		r.Header.Set("Origin", origin)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}
func TestSetupRequiresFreshPinnedAdministratorAndRejectsSecrets(t *testing.T) {
	for _, mode := range []string{"normal", "not-admin", "role-removed-during-save", "directory-down", "revoked-during-save", "invalid-origin", "secret-field", "unknown-status-field", "stale-revision"} {
		t.Run(mode, func(t *testing.T) {
			h, login, _, dir := loginFixture(t)
			data := &setupHTTPStore{httpStore: login}
			d := &setupHTTPDirectory{httpDirectory: dir, admin: true}
			h.store = data
			h.directory = d
			body := `{"revision":0,"draft":{"inbound":"worker-r2","domain":"example.test"}}`
			origin := "https://mail.example.test"
			switch mode {
			case "not-admin":
				d.admin = false
			case "role-removed-during-save":
				dir.afterRead = func() {
					if dir.reads >= 2 {
						d.admin = false
					}
				}
			case "directory-down":
				dir.err = identity.ErrDirectoryUnavailable
			case "revoked-during-save":
				dir.afterRead = func() {
					if dir.reads >= 2 {
						login.sessionErr = store.ErrSessionDenied
					}
				}
			case "invalid-origin":
				origin = "https://evil.test"
			case "secret-field":
				body = `{"revision":0,"draft":{"password":"never-echo-this"}}`
			case "unknown-status-field":
				body = `{"revision":0,"draft":{},"enabled":true}`
			case "stale-revision":
				data.state.Revision = 1
			}
			w := setupRequest(t, h, "PUT", body, origin)
			status := map[string]int{"normal": 200, "not-admin": 403, "role-removed-during-save": 403, "directory-down": 503, "revoked-during-save": 401, "invalid-origin": 403, "secret-field": 400, "unknown-status-field": 400, "stale-revision": 409}[mode]
			if w.Code != status || strings.Contains(w.Body.String(), "never-echo-this") {
				t.Fatal("draft guard", w.Code, w.Body.String())
			}
			if mode != "normal" && data.saves != 0 {
				t.Fatal("rejected write persisted")
			}
			if mode == "normal" {
				var result struct {
					Enabled bool              `json:"enabled"`
					Checks  map[string]string `json:"checks"`
				}
				if json.Unmarshal(w.Body.Bytes(), &result) != nil || result.Enabled || result.Checks["cloud_storage"] != "unknown" || result.Checks["local_delivery"] != "unknown" {
					t.Fatal("save pretended to verify mail")
				}
			}
		})
	}
}

func TestDirectSetupIsPendingUntilVerified(t *testing.T) {
	checks := setupChecks(store.SetupDraft{Outbound: store.SetupOutbound{Provider: "direct"}})
	if checks["outbound"] != "pending" {
		t.Fatal("direct draft incorrectly reported ready or missing", checks)
	}
}
func TestDirectReceivingDraftNeverClaimsReady(t *testing.T) {
	d := store.SetupDraft{Inbound: "direct", Domain: "example.test", LimitsConfirmed: true, Limits: store.SetupLimits{MailboxBytes: 1024, MessagesPerMinute: 1}}
	checks := setupChecks(d)
	if checks["source"] != "pending" || checks["limits"] != "pending" || checks["local_delivery"] != "unknown" || checks["cloud_storage"] != "not-applicable" || checks["notification"] != "not-applicable" || checks["account_quota"] != "not-applicable" {
		t.Fatal("direct receiving draft or limits misreported", checks)
	}
}

func TestSetupRejectsAmbiguousDraftJSON(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		status     int
	}{
		{"valid", `{"revision":0,"draft":{"inbound":"direct","domain":"example.test","limits":{"aliases":2}}}`, 200},
		{"missing revision", `{"draft":{}}`, 400},
		{"missing draft", `{"revision":0}`, 400},
		{"duplicate revision", `{"revision":1,"revision":0,"draft":{}}`, 400},
		{"case variant", `{"revision":0,"draft":{"Domain":"example.test"}}`, 400},
		{"duplicate draft key", `{"revision":0,"draft":{"domain":"bad.test","domain":"example.test"}}`, 400},
		{"escaped duplicate", `{"revision":0,"draft":{"domain":"bad.test","\u0064omain":"example.test"}}`, 400},
		{"duplicate nested", `{"revision":0,"draft":{"limits":{"aliases":1,"aliases":2}}}`, 400},
		{"nested case", `{"revision":0,"draft":{"outbound":{"Provider":"direct"}}}`, 400},
		{"null revision", `{"revision":null,"draft":{}}`, 400},
		{"null field", `{"revision":0,"draft":{"limits_confirmed":null}}`, 400},
		{"null nested field", `{"revision":0,"draft":{"limits":{"aliases":null}}}`, 400},
		{"null object", `{"revision":0,"draft":{"outbound":null}}`, 400},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, login, _, dir := loginFixture(t)
			data := &setupHTTPStore{httpStore: login}
			h.store = data
			h.directory = &setupHTTPDirectory{httpDirectory: dir, admin: true}
			w := setupRequest(t, h, "PUT", tc.body, "https://mail.example.test")
			if w.Code != tc.status || (tc.status != 200 && data.saves != 0) {
				t.Fatalf("status=%d saves=%d", w.Code, data.saves)
			}
		})
	}
}

type switchedSetupStore struct {
	*setupHTTPStore
	group string
}

func (s *switchedSetupStore) AdmissionGroup(context.Context, string) (string, error) {
	return s.group, nil
}

type dualGroupSetupDirectory struct{ *httpDirectory }

func (d *dualGroupSetupDirectory) Person(ctx context.Context, anchor string) (identity.Person, error) {
	p, err := d.httpDirectory.Person(ctx, anchor)
	p.Groups = append(p.Groups, identity.Group{Anchor: "replacement-group"})
	return p, err
}

func TestSetupRechecksSameAdministratorGroupBeforeReply(t *testing.T) {
	for _, method := range []string{"GET", "PUT"} {
		t.Run(method, func(t *testing.T) {
			h, login, _, dir := loginFixture(t)
			data := &switchedSetupStore{setupHTTPStore: &setupHTTPStore{httpStore: login}, group: "private-group"}
			data.afterDraft = func() { data.group = "replacement-group" }
			h.store = data
			h.directory = &dualGroupSetupDirectory{httpDirectory: dir}
			w := setupRequest(t, h, method, `{"revision":0,"draft":{}}`, "https://mail.example.test")
			if w.Code != 403 || strings.Contains(w.Body.String(), `"state"`) {
				t.Fatalf("changed role accepted: %d %s", w.Code, w.Body.String())
			}
			if (method == "PUT" && data.saves != 1) || (method == "GET" && data.saves != 0) {
				t.Fatal("unexpected draft writes", data.saves)
			}
		})
	}
}
