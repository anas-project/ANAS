package main

import (
	"context"
	"errors"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/anas-project/ANAS/modules/mail/identity"
	"github.com/anas-project/ANAS/modules/mail/store"
)

type logoutTestStore struct {
	*httpStore
	used bool
}

func (s *logoutTestStore) BeginLogout(context.Context) (identity.Flow, string, error) {
	return s.flow, "logout-browser", s.flowErr
}
func (s *logoutTestStore) ConsumeLogout(_ context.Context, state, browser string) (identity.Flow, error) {
	if s.used || state != s.flow.State || browser != "logout-browser" {
		return identity.Flow{}, store.ErrLoginFlowDenied
	}
	s.used = true
	return s.flow, nil
}
func (s *logoutTestStore) LogoutSessionID(_ context.Context, cookie, issuer string) (string, error) {
	if cookie != "session-secret" || issuer != fixtureIssuer {
		return "", store.ErrSessionDenied
	}
	return webmailTestBox, nil
}

type logoutTestIAM struct {
	*httpIAM
	data        *httpStore
	unavailable bool
}

func (i logoutTestIAM) LogoutURL(state, callback string) (string, error) {
	if i.data.revoked != 1 {
		return "", errors.New("local withdrawal did not commit first")
	}
	if i.unavailable {
		return "", identity.ErrAuthentication
	}
	return fixtureIssuer + "/logout?state=" + url.QueryEscape(state) + "&post_logout_redirect_uri=" + url.QueryEscape(callback), nil
}
func TestBrowserLogoutCommitsLocalWithoutDirectoryOrIAM(t *testing.T) {
	for _, mode := range []string{"success", "iam-unavailable", "foreign-origin", "no-cookie"} {
		t.Run(mode, func(t *testing.T) {
			h, data, iam, dir := loginFixture(t)
			s := &logoutTestStore{httpStore: data}
			h.store = s
			h.iam = logoutTestIAM{httpIAM: iam, data: data, unavailable: mode == "iam-unavailable"}
			dir.err = identity.ErrDirectoryUnavailable
			origin := "https://mail.example.test"
			cookie := sessionCookie + "=session-secret"
			if mode == "foreign-origin" {
				origin = "https://foreign.example.test"
			}
			if mode == "no-cookie" {
				cookie = ""
			}
			w := request(t, h, "POST", "/api/session/logout", cookie, "", origin)
			if mode == "foreign-origin" || mode == "no-cookie" {
				if w.Code < 400 || data.revoked != 0 {
					t.Fatal("unbound logout accepted", w.Code)
				}
				return
			}
			if w.Code != 200 || data.revoked != 1 || dir.reads != 0 || !strings.Contains(w.Body.String(), `"local":"revoked"`) {
				t.Fatal("local logout dependency", w.Code, w.Body.String())
			}
			if mode == "iam-unavailable" {
				if !strings.Contains(w.Body.String(), `"iam":"unavailable"`) {
					t.Fatal("central failure falsely reported success")
				}
				return
			}
			if !strings.Contains(w.Body.String(), "iam_redirect") {
				t.Fatal("central logout not started")
			}
			path := "/oidc/logout-callback?state=" + s.flow.State
			if w := request(t, h, "GET", path, logoutCookie+"=wrong", "", ""); w.Code != 400 || s.used {
				t.Fatal("wrong browser consumed callback")
			}
			if w := request(t, h, "GET", path, logoutCookie+"=logout-browser", "", ""); w.Code != 200 || !s.used {
				t.Fatal("bound callback rejected", w.Code)
			}
			if w := request(t, h, "GET", path, logoutCookie+"=logout-browser", "", ""); w.Code != 400 {
				t.Fatal("callback replay accepted")
			}
		})
	}
}
func TestRoundcubeLogoutRequiresSignedSameGoSession(t *testing.T) {
	for _, mode := range []string{"success", "other-session", "forged", "expired"} {
		t.Run(mode, func(t *testing.T) {
			h, _, _, data := protocolFixture(t)
			h.store = &logoutTestStore{httpStore: data}
			h.iam = logoutTestIAM{data: data}
			nonce := strings.Repeat("a", 43)
			issued := strconv.FormatInt(time.Now().Unix(), 10)
			if mode == "expired" {
				issued = strconv.FormatInt(time.Now().Add(-2*time.Minute).Unix(), 10)
			}
			id := webmailTestBox
			if mode == "other-session" {
				id = "22222222-2222-4222-8222-222222222222"
			}
			signature := h.webmail.logoutSignature(id, nonce, issued)
			if mode == "forged" {
				signature = strings.Repeat("0", 64)
			}
			form := url.Values{"session_id": {id}, "nonce": {nonce}, "issued_at": {issued}, "signature": {signature}}
			w := request(t, h, "POST", "/webmail/logout-continue", sessionCookie+"=session-secret", form.Encode(), h.webmail.origin)
			if mode == "success" {
				if w.Code != 200 || data.revoked != 1 {
					t.Fatal("signed logout rejected", w.Code)
				}
				if w.Header().Get("Location") != "" || !strings.Contains(w.Body.String(), `id="iam-logout"`) || strings.Contains(w.Body.String(), "<form") || !strings.Contains(w.Body.String(), "location.replace(") {
					t.Fatal("logout must finish the form navigation before starting the IdP GET")
				}
				if !strings.Contains(w.Header().Get("Content-Security-Policy"), "script-src 'nonce-") || !strings.Contains(w.Header().Get("Content-Security-Policy"), "form-action 'none'") || w.Header().Get("Referrer-Policy") != "no-referrer" || w.Header().Get("Cache-Control") != "no-store" {
					t.Fatal("logout navigation policy missing")
				}
			} else if w.Code != 401 || data.revoked != 0 {
				t.Fatal("unbound Roundcube logout accepted", w.Code)
			}
		})
	}
}
