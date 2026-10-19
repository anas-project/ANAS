// TEST_CASES: PORTAL-T-001
package main

import (
	"encoding/json"
	"github.com/anas-project/ANAS/internal/appcatalog"
	"github.com/anas-project/ANAS/modules/user_portal/browserlogout"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestLogoutTicketPurposeExpiryOriginAndRestart(t *testing.T) {
	s := newStore()
	now := time.Now()
	ticket := logoutTicket{"https://photos.example", randomID(), now.Add(time.Minute).Unix()}
	signed := s.signLogout("bridge", ticket)
	if _, ok := s.verifyLogout("bridge", signed, now); !ok {
		t.Fatal("valid ticket rejected")
	}
	for _, tc := range []struct {
		store          *Store
		purpose, value string
		at             time.Time
	}{{s, "flow", signed, now}, {s, "bridge", signed + "x", now}, {s, "bridge", signed, now.Add(2 * time.Minute)}, {newStore(), "bridge", signed, now}} {
		if _, ok := tc.store.verifyLogout(tc.purpose, tc.value, tc.at); ok {
			t.Fatal("invalid ticket accepted")
		}
	}
}
func TestHiddenOldSessionTargetAndAnonymousCompletion(t *testing.T) {
	f := newFixture(t)
	f.server.catalog.Applications = append(f.server.catalog.Applications, appcatalog.Application{Metadata: appcatalog.Metadata{Name: "secret-app", URL: "https://photos.example/private?secret=1", Hidden: true, Summary: appcatalog.Text{"en": "secret"}}, Enabled: true, Access: appcatalog.Access{Mode: "iam", Client: "immich"}, Source: appcatalog.Source{Kind: "module", Module: "immich"}})
	response := f.request("POST", "/api/logout", `{}`)
	var data struct {
		Targets []browserLogoutTarget `json:"targets"`
	}
	if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &data) != nil || len(data.Targets) != 1 {
		t.Fatal(response.Code, response.Body.String())
	}
	for _, private := range []string{"secret-app", "private", "secret=1", "summary", "groups"} {
		if strings.Contains(response.Body.String(), private) {
			t.Fatal("business metadata leaked", private)
		}
	}
	if f.request("GET", "/api/me", "").Code != 401 {
		t.Fatal("portal session survived")
	}

	// An expired authenticated cookie cannot read the business directory. A new
	// anonymous CSRF session can intentionally complete the public logout flow.
	f.session = Session{ID: "anonymous-retry", CSRF: "anonymous-csrf", Anonymous: true, Expires: time.Now().Add(time.Minute)}
	f.server.store.add(f.session)
	if retry := f.request("POST", "/api/logout/complete", `{}`); retry.Code != 200 {
		t.Fatal(retry.Code, retry.Body.String())
	}
	if f.request("GET", "/api/apps", "").Code != 401 {
		t.Fatal("business catalog became public")
	}
	for _, path := range []string{"/", "/signed-out"} {
		r := httptest.NewRequest("GET", "https://portal.example"+path, nil)
		r.RemoteAddr = "127.0.0.1:3000"
		w := httptest.NewRecorder()
		f.server.Handler().ServeHTTP(w, r)
		if strings.Contains(w.Header().Get("Content-Security-Policy"), "https://photos.example") != (path == "/signed-out") {
			t.Fatal("CSP protocol metadata disclosure boundary")
		}
	}
	for _, tc := range []struct {
		host, path string
		want       int
	}{{"photos.example", data.Targets[0].URL, 200}, {"other.example", data.Targets[0].URL, 404}, {"photos.example", "https://photos.example" + browserlogout.Path, 404}, {"photos.example", "https://photos.example/api/me", 403}} {
		r := httptest.NewRequest("GET", tc.path, nil)
		r.Host = tc.host
		r.RemoteAddr = "127.0.0.1:3000"
		w := httptest.NewRecorder()
		f.server.Handler().ServeHTTP(w, r)
		if w.Code != tc.want {
			t.Fatalf("%s: got %d, want %d", tc.host, w.Code, tc.want)
		}
		if w.Code == 200 && (!strings.Contains(w.Header().Get("Content-Security-Policy"), "frame-ancestors https://portal.example") || strings.Contains(w.Body.String(), "backend-access-token")) {
			t.Fatal("unsafe bridge")
		}
	}
}

func TestNativeLogoutCallbackIsPublicMinimalAndRequiresCSRF(t *testing.T) {
	f := newFixture(t)
	f.session.Anonymous = true
	f.server.store.sessions[f.session.ID] = f.session
	// The fixed callback is the only anonymous document with the protocol CSP.
	for _, tc := range []struct{ site, ref, want string }{{"same-site", "https://iam.other/", "manual"}, {"cross-site", "", "manual"}, {"none", "", "manual"}, {"same-site", "", "automatic"}} {
		r := httptest.NewRequest("GET", "https://portal.example/signed-out?native=1", nil)
		r.RemoteAddr = "127.0.0.1:3000"
		r.Header.Set("Sec-Fetch-Site", tc.site)
		r.Header.Set("Sec-Fetch-Dest", "document")
		r.Header.Set("Referer", tc.ref)
		w := httptest.NewRecorder()
		f.server.Handler().ServeHTTP(w, r)
		if !strings.Contains(w.Body.String(), `name="anas-native-logout" content="`+tc.want+`"`) {
			t.Fatal("unsafe callback trigger", tc, w.Body.String())
		}
	}
	if r := f.request("POST", "/api/logout/complete", `{}`); r.Code != 200 {
		t.Fatal(r.Code, r.Body.String())
	}
	r := httptest.NewRequest("POST", "https://portal.example/api/logout/complete", strings.NewReader(`{}`))
	r.RemoteAddr = "127.0.0.1:3000"
	r.Header.Set("Origin", "https://evil.example")
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	f.server.Handler().ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatal("cross-origin callback write accepted")
	}
}

func TestNativeLogoutCanRefreshCSRFAfterBackchannelRevocation(t *testing.T) {
	f := newFixture(t)
	old := f.session
	other := old
	other.ID, other.SID = "independent-session", "independent-sid"
	f.server.store.add(other)
	if f.request("GET", "/api/bootstrap", "").Code != 200 {
		t.Fatal("initial bootstrap failed")
	}
	// The native RP can return before IAM's backchannel reaches this portal.
	if !f.server.store.revoke(old.Subject, old.SID, "native-race", time.Now().Add(time.Minute)) {
		t.Fatal("backchannel did not revoke the original session")
	}
	if r := f.request("POST", "/api/logout/complete", `{}`); r.Code != 403 || !strings.Contains(r.Body.String(), "csrf_rejected") {
		t.Fatal("revoked CSRF was accepted", r.Code)
	}
	bootstrap := f.request("GET", "/api/bootstrap", "")
	var data struct {
		CSRF          string `json:"csrf"`
		Authenticated bool   `json:"authenticated"`
	}
	if bootstrap.Code != 200 || json.Unmarshal(bootstrap.Body.Bytes(), &data) != nil || data.Authenticated || data.CSRF == "" || data.CSRF == old.CSRF {
		t.Fatal("fresh anonymous CSRF unavailable")
	}
	for _, c := range bootstrap.Result().Cookies() {
		if c.Name == sessionCookie {
			f.session = Session{ID: c.Value, CSRF: data.CSRF, Anonymous: true}
		}
	}
	if r := f.request("POST", "/api/logout/complete", `{}`); r.Code != 200 {
		t.Fatal("fresh completion failed", r.Code)
	}
	if f.request("GET", "/api/apps", "").Code != 401 {
		t.Fatal("logout-only recovery exposed the business catalogue")
	}
	if _, ok := f.server.store.get(other.ID); !ok {
		t.Fatal("another browser was revoked")
	}
}
