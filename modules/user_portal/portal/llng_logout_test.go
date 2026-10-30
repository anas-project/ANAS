// TEST_CASES: LLNG-READY-T-001
package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestLLNGLogoutDestroysLocalSessionWhenProviderIsUnavailable(t *testing.T) {
	f := newFixture(t)
	f.server.config.AccountSchema = "llng-account/v1"
	f.session.IDToken = "verified-original-id-token"
	f.server.store.sessions[f.session.ID] = f.session
	f.iam.Close()
	r := f.request("POST", "/api/logout", `{}`)
	var data struct {
		Redirect string `json:"iam_redirect"`
		Local    string `json:"local"`
	}
	if r.Code != 200 || json.Unmarshal(r.Body.Bytes(), &data) != nil || data.Local != "logged_out" {
		t.Fatal("local logout depended on the unavailable provider")
	}
	if _, ok := f.server.store.get(f.session.ID); ok {
		t.Fatal("old local session survived")
	}
	u, err := url.Parse(data.Redirect)
	if err != nil || u.Scheme+"://"+u.Host+u.Path != f.server.oidc.logout || u.Query().Get("client_id") != "portal" || u.Query().Get("post_logout_redirect_uri") != "https://portal.example/oidc/logout-callback" || u.Query().Get("id_token_hint") != f.session.IDToken || len(u.Query().Get("state")) != 43 {
		t.Fatal("invalid standard OIDC logout request")
	}
	var binding *http.Cookie
	for _, c := range r.Result().Cookies() {
		if c.Name == logoutCookie {
			binding = c
		}
	}
	if binding == nil || !binding.Secure || !binding.HttpOnly || binding.SameSite != http.SameSiteLaxMode {
		t.Fatal("logout browser binding missing")
	}
	callback := func() *httptest.ResponseRecorder {
		req := httptest.NewRequest("GET", "https://portal.example/oidc/logout-callback?state="+u.Query().Get("state"), nil)
		req.RemoteAddr = "127.0.0.1:3000"
		req.AddCookie(binding)
		w := httptest.NewRecorder()
		f.server.Handler().ServeHTTP(w, req)
		return w
	}
	first := callback()
	if first.Code != 200 || !strings.Contains(first.Body.String(), `name="anas-oidc-logout" content="confirmed"`) || strings.Contains(first.Body.String(), f.session.IDToken) {
		t.Fatal("valid callback was not confirmed or leaked its token")
	}
	if callback().Code != 400 {
		t.Fatal("logout callback replay accepted")
	}
}

func TestLLNGLogoutCallbackRejectsUnboundExpiredAndLoginFlows(t *testing.T) {
	for _, kind := range []string{"cookie", "state", "expired", "login", "provider-error"} {
		t.Run(kind, func(t *testing.T) {
			f := newFixture(t)
			flow := Flow{State: randomID(), Cookie: randomID(), Logout: true, Expires: time.Now().Add(time.Minute)}
			if kind == "expired" {
				flow.Expires = time.Now().Add(-time.Second)
			}
			if kind == "login" {
				flow.Logout = false
			}
			f.server.store.addFlow(flow)
			state, binding := flow.State, flow.Cookie
			if kind == "cookie" {
				binding = randomID()
			}
			if kind == "state" {
				state = randomID()
			}
			path := "https://portal.example/oidc/logout-callback?state=" + state
			if kind == "provider-error" {
				path += "&error=access_denied"
			}
			req := httptest.NewRequest("GET", path, nil)
			req.RemoteAddr = "127.0.0.1:3000"
			req.AddCookie(&http.Cookie{Name: logoutCookie, Value: binding})
			w := httptest.NewRecorder()
			f.server.Handler().ServeHTTP(w, req)
			if w.Code != 400 || strings.Contains(w.Body.String(), `content="confirmed"`) {
				t.Fatal("invalid logout callback accepted")
			}
		})
	}
}
