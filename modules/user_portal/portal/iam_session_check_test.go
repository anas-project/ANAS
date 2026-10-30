package main

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func silentRequest(f *fixture, path string, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	r := httptest.NewRequest("GET", "https://portal.example"+path, nil)
	r.RemoteAddr = "127.0.0.1:1000"
	for _, c := range cookies {
		r.AddCookie(c)
	}
	w := httptest.NewRecorder()
	f.server.Handler().ServeHTTP(w, r)
	return w
}
func TestIAMCheckAuthorizationAndBoundErrorFallback(t *testing.T) {
	for _, code := range []string{"login_required", "interaction_required", "consent_required", "account_selection_required", "server_error", ""} {
		t.Run(code, func(t *testing.T) {
			f := newFixture(t)
			w := silentRequest(f, "/login?check=1")
			u, _ := url.Parse(w.Header().Get("Location"))
			q := u.Query()
			if w.Code != 303 || q.Get("prompt") != "none" || q.Get("nonce") == "" || q.Get("code_challenge_method") != "S256" || q.Get("max_age") != "" {
				t.Fatal("invalid silent authorization", w.Code)
			}
			var browser *http.Cookie
			for _, c := range w.Result().Cookies() {
				if c.Name == flowCookie {
					browser = c
				}
			}
			path := "/oidc/callback?state=" + q.Get("state") + "&error=" + code
			w = silentRequest(f, path, browser)
			want := "/?iam_check=required"
			if code == "server_error" || code == "" {
				want = "/?iam_check=failed"
			}
			if w.Code != 303 || w.Header().Get("Location") != want {
				t.Fatal("missing bounded fallback", w.Code, w.Header())
			}
			if w = silentRequest(f, path, browser); w.Code != 401 {
				t.Fatal("replayed error accepted")
			}
		})
	}
}
func TestIAMCheckRejectsUnboundAndAmbiguousCallbacks(t *testing.T) {
	for _, scenario := range []string{"cookie", "state", "duplicate", "code-and-error"} {
		t.Run(scenario, func(t *testing.T) {
			f := newFixture(t)
			flow := Flow{State: "state", Cookie: "browser", Silent: true, Expires: time.Now().Add(time.Minute)}
			f.server.store.addFlow(flow)
			cookie := &http.Cookie{Name: flowCookie, Value: "browser"}
			path := "/oidc/callback?state=state&error=login_required"
			switch scenario {
			case "cookie":
				cookie.Value = "other"
			case "state":
				path = strings.Replace(path, "state=state", "state=other", 1)
			case "duplicate":
				path += "&error=server_error"
			case "code-and-error":
				path += "&code=code"
			}
			w := silentRequest(f, path, cookie)
			if scenario == "code-and-error" {
				if w.Header().Get("Location") != "/?iam_check=failed" {
					t.Fatal("ambiguous response hid failure")
				}
			} else if w.Code != 401 {
				t.Fatal("unbound callback accepted", w.Code)
			}
		})
	}
}
func TestIAMCheckCreatesSessionWithoutFresheningAuthentication(t *testing.T) {
	f := newFixture(t)
	at := time.Now().Add(-20 * time.Minute).Unix()
	flow := Flow{State: "state", Cookie: "browser", Nonce: "nonce", Verifier: "verifier", Silent: true, Expires: time.Now().Add(time.Minute)}
	f.server.store.addFlow(flow)
	f.claims = map[string]any{"iss": f.iam.URL, "sub": f.person.Anchor, "aud": "portal", "nonce": "nonce", "iat": time.Now().Unix(), "exp": time.Now().Add(time.Hour).Unix(), "auth_time": at}
	w := silentRequest(f, "/oidc/callback?state=state&code=code", &http.Cookie{Name: flowCookie, Value: "browser"})
	if w.Code != 303 || w.Header().Get("Location") != "/" {
		t.Fatal("silent login failed", w.Code, w.Body.String())
	}
	for _, c := range w.Result().Cookies() {
		if c.Name == sessionCookie {
			r := httptest.NewRequest("GET", "https://portal.example/", nil)
			r.AddCookie(c)
			v, ok := f.server.session(r)
			if !ok || v.AuthTime.Unix() != at || f.server.recentSession(v) {
				t.Fatal("silent login renewed authentication time")
			}
			return
		}
	}
	t.Fatal("missing authenticated cookie")
}
func TestIAMCheckDoesNotRestartAuthenticatedSession(t *testing.T) {
	f := newFixture(t)
	w := f.request("GET", "/login?check=1", "")
	if w.Code != 303 || w.Header().Get("Location") != "/" {
		t.Fatal("active session restarted")
	}
}
