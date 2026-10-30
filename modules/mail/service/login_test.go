package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/anas-project/ANAS/modules/mail/identity"
	"github.com/anas-project/ANAS/modules/mail/store"
)

const fixtureAnchor = "11111111-2222-3333-4444-555555555555"
const fixtureIssuer = "https://iam.example.test"

type httpStore struct {
	flow                                                identity.Flow
	flowErr, sessionErr, admitErr, revokeErr, logoutErr error
	consumed, admitted, denied, revoked, loggedOut      int
	reads                                               int
	session                                             store.Session
	verified                                            store.VerifiedLogin
	afterRead                                           func()
}

func (s *httpStore) BeginLogin(context.Context) (identity.Flow, string, error) {
	return s.flow, "browser-secret", s.flowErr
}
func (s *httpStore) ConsumeLogin(_ context.Context, state, browser string) (identity.Flow, error) {
	if s.flowErr != nil {
		return identity.Flow{}, s.flowErr
	}
	if state != s.flow.State || browser != "browser-secret" || s.consumed > 0 {
		return identity.Flow{}, store.ErrLoginFlowDenied
	}
	s.consumed++
	return s.flow, nil
}
func (s *httpStore) AdmitSession(_ context.Context, l store.VerifiedLogin) (store.Session, string, error) {
	s.admitted++
	s.verified = l
	return s.session, "session-secret", s.admitErr
}
func (s *httpStore) Session(context.Context, string) (store.Session, error) {
	s.reads++
	if s.afterRead != nil {
		s.afterRead()
	}
	return s.session, s.sessionErr
}
func (s *httpStore) RevokeSession(context.Context, string) error { s.revoked++; return s.revokeErr }
func (s *httpStore) DenyPerson(context.Context, string) error    { s.denied++; return s.revokeErr }
func (s *httpStore) ApplyLogout(context.Context, identity.Logout) error {
	s.loggedOut++
	return s.logoutErr
}

type httpIAM struct {
	auth      identity.Authentication
	err       error
	exchanged int
}

func (a *httpIAM) AuthorizationURL(identity.Flow) (string, error) {
	return fixtureIssuer + "/authorize", a.err
}
func (a *httpIAM) Exchange(context.Context, identity.Flow, string) (identity.Authentication, error) {
	a.exchanged++
	return a.auth, a.err
}
func (a *httpIAM) VerifyLogout(context.Context, string) (identity.Logout, error) {
	return identity.Logout{Issuer: fixtureIssuer, ID: "logout", SID: "sid", IssuedAt: time.Now()}, a.err
}

type httpDirectory struct {
	err       error
	reads     int
	afterRead func()
}

func (d *httpDirectory) Person(context.Context, string) (identity.Person, error) {
	d.reads++
	if d.afterRead != nil {
		d.afterRead()
	}
	return identity.Person{Anchor: fixtureAnchor, Username: "alice", DisplayName: "Alice", Mail: "alice@example.test", SMTPAddresses: []string{"private-alias@example.test"}, Groups: []identity.Group{{DN: "private-dn", Anchor: "private-group"}}}, d.err
}
func loginFixture(t *testing.T) (*loginHandler, *httpStore, *httpIAM, *httpDirectory) {
	t.Helper()
	flow, err := identity.NewFlow()
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	data := &httpStore{flow: flow, session: store.Session{OwnerAnchor: fixtureAnchor, Issuer: fixtureIssuer, Expires: now.Add(time.Hour)}}
	iam := &httpIAM{auth: identity.Authentication{Anchor: fixtureAnchor, Issuer: fixtureIssuer, SID: "sid", AuthenticatedAt: now, Expires: now.Add(4 * time.Hour)}}
	dir := &httpDirectory{}
	h, err := newLoginHandler("https://mail.example.test", fixtureIssuer, data, iam, dir)
	if err != nil {
		t.Fatal(err)
	}
	return h, data, iam, dir
}
func request(t *testing.T, h http.Handler, method, path, cookie, body, origin string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(method, "https://mail.example.test"+path, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if cookie != "" {
		r.Header.Set("Cookie", cookie)
	}
	if origin != "" {
		r.Header.Set("Origin", origin)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}
func TestLoginHTTPAndProfile(t *testing.T) {
	h, s, iam, d := loginFixture(t)
	w := request(t, h, "GET", "/login", "", "", "")
	if w.Code != 303 || w.Header().Get("Location") != fixtureIssuer+"/authorize" {
		t.Fatal(w.Code, w.Body.String())
	}
	c := w.Result().Cookies()
	if len(c) != 1 || c[0].Name != flowCookie || !c[0].Secure || !c[0].HttpOnly || c[0].SameSite != http.SameSiteLaxMode || c[0].Domain != "" || c[0].Path != "/" {
		t.Fatal("insecure flow cookie")
	}
	path := "/oidc/callback?state=" + s.flow.State + "&code=one-use-code"
	w = request(t, h, "GET", path, flowCookie+"=browser-secret", "", "")
	if w.Code != 303 || w.Header().Get("Location") != "/api/me" || s.admitted != 1 || iam.exchanged != 1 || d.reads != 1 || s.verified.OwnerAnchor != fixtureAnchor || time.Until(s.verified.Expires) > 2*time.Hour {
		t.Fatal("verified callback not connected", w.Code, w.Body.String())
	}
	var session *http.Cookie
	for _, c := range w.Result().Cookies() {
		if c.Name == sessionCookie {
			session = c
		}
	}
	if session == nil || !session.Secure || !session.HttpOnly || session.MaxAge > 7200 {
		t.Fatal("insecure session cookie")
	}
	w = request(t, h, "GET", path, flowCookie+"=browser-secret", "", "")
	if w.Code != 400 || iam.exchanged != 1 || s.admitted != 1 {
		t.Fatal("callback replay")
	}
	w = request(t, h, "GET", "/api/me", sessionCookie+"=session-secret", "", "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), "alice@example.test") || strings.Contains(w.Body.String(), "private-") || s.reads != 2 || d.reads != 2 {
		t.Fatal("profile authorization/whitelist", w.Code, w.Body.String())
	}
}

func TestCallbackRejectsUnboundAndAmbiguousRequests(t *testing.T) {
	for _, kind := range []string{"no cookie", "duplicate cookie", "wrong browser", "wrong state", "duplicate state", "duplicate code", "malformed query", "issuer mixup", "iam rejection", "directory rejection", "directory outage", "database failure", "logged out"} {
		t.Run(kind, func(t *testing.T) {
			h, s, iam, d := loginFixture(t)
			path := "/oidc/callback?state=" + s.flow.State + "&code=code"
			cookie := flowCookie + "=browser-secret"
			want := 400
			switch kind {
			case "no cookie":
				cookie = ""
			case "duplicate cookie":
				cookie += "; " + cookie
			case "wrong browser":
				cookie = flowCookie + "=other"
			case "wrong state":
				path = "/oidc/callback?state=other&code=code"
			case "duplicate state":
				path += "&state=other"
			case "duplicate code":
				path += "&code=other"
			case "malformed query":
				path += "&bad=%zz"
			case "issuer mixup":
				path += "&iss=https://other.test"
			case "iam rejection":
				iam.err = identity.ErrAuthentication
				want = 401
			case "directory rejection":
				d.err = identity.ErrAdmission
				want = 401
			case "directory outage":
				d.err = identity.ErrDirectoryUnavailable
				want = 503
			case "database failure":
				s.admitErr = errors.New("private-password")
				want = 503
			case "logged out":
				s.admitErr = store.ErrSessionDenied
				want = 401
			}
			w := request(t, h, "GET", path, cookie, "", "")
			if w.Code != want || strings.Contains(w.Body.String(), "private-password") {
				t.Fatal(w.Code, w.Body.String())
			}
			if kind == "directory rejection" && s.denied != 1 {
				t.Fatal("admission rejection not persisted")
			}
			if kind == "directory outage" && s.denied != 0 {
				t.Fatal("directory outage treated as deletion")
			}
			for _, c := range w.Result().Cookies() {
				if c.Name == sessionCookie && c.MaxAge > 0 {
					t.Fatal("failed login set session")
				}
			}
		})
	}
}

func TestSessionFreshnessAndLocalRevocation(t *testing.T) {
	for _, kind := range []string{"missing", "duplicate", "expired", "database", "directory outage", "directory denied", "concurrent logout"} {
		t.Run(kind, func(t *testing.T) {
			h, s, _, d := loginFixture(t)
			cookie := sessionCookie + "=session-secret"
			want := 401
			switch kind {
			case "missing":
				cookie = ""
			case "duplicate":
				cookie += "; " + cookie
			case "expired":
				s.sessionErr = store.ErrSessionDenied
			case "database":
				s.sessionErr = errors.New("private-password")
				want = 503
			case "directory outage":
				d.err = identity.ErrDirectoryUnavailable
				want = 503
			case "directory denied":
				d.err = identity.ErrAdmission
			case "concurrent logout":
				d.afterRead = func() { s.sessionErr = store.ErrSessionDenied }
			}
			w := request(t, h, "GET", "/api/me", cookie, "", "")
			if w.Code != want {
				t.Fatal(w.Code, w.Body.String())
			}
			if kind == "directory outage" && s.denied != 0 {
				t.Fatal("outage revoked person")
			}
		})
	}
	h, s, _, _ := loginFixture(t)
	for _, origin := range []string{"", "https://evil.test"} {
		if w := request(t, h, "POST", "/api/session/revoke", sessionCookie+"=session-secret", "", origin); w.Code != 403 {
			t.Fatal("cross-origin logout")
		}
	}
	w := request(t, h, "POST", "/api/session/revoke", sessionCookie+"=session-secret", "", "https://mail.example.test")
	if w.Code != 200 || s.revoked != 1 || !strings.Contains(w.Body.String(), "local-session-revoked") {
		t.Fatal("local logout", w.Code)
	}
	s.revokeErr = errors.New("private-password")
	if w := request(t, h, "POST", "/api/session/revoke", sessionCookie+"=session-secret", "", "https://mail.example.test"); w.Code != 503 {
		t.Fatal("false successful logout")
	}
}

func TestBackchannelIndependentOfDirectory(t *testing.T) {
	h, s, iam, d := loginFixture(t)
	d.err = identity.ErrDirectoryUnavailable
	w := request(t, h, "POST", "/oidc/backchannel-logout", "", "logout_token=signed-token", "")
	if w.Code != 200 || s.loggedOut != 1 || d.reads != 0 {
		t.Fatal("logout depends on LDAP")
	}
	iam.err = identity.ErrAuthentication
	if w := request(t, h, "POST", "/oidc/backchannel-logout", "", "logout_token=bad", ""); w.Code != 400 || s.loggedOut != 1 {
		t.Fatal("unsigned logout persisted")
	}
	iam.err = nil
	s.logoutErr = errors.New("private-password")
	if w := request(t, h, "POST", "/oidc/backchannel-logout", "", "logout_token=signed", ""); w.Code != 503 {
		t.Fatal("failed persistence acknowledged")
	}
	for _, body := range []string{"", "logout_token=a&logout_token=b", "logout_token=a&owner=other", "logout_token=" + strings.Repeat("x", 18000)} {
		if w := request(t, h, "POST", "/oidc/backchannel-logout", "", body, ""); w.Code != 400 {
			t.Fatal("malformed body", w.Code)
		}
	}
}

func TestLoginHostAndUnpublishedReadiness(t *testing.T) {
	h, _, _, _ := loginFixture(t)
	r := httptest.NewRequest("GET", "https://evil.test/login", nil)
	r.Header.Set("X-Forwarded-Host", "mail.example.test")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 400 {
		t.Fatal("forwarded host trusted")
	}
	if w := request(t, handler(nil, h), "GET", "/readyz", "", "", ""); w.Code != 503 {
		t.Fatal("partial module ready")
	}
	if w := request(t, h, "GET", "/mailboxes", "", "", ""); w.Code != 404 {
		t.Fatal("pending mailboxes published")
	}
}
