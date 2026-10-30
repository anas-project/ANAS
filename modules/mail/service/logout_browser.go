package main

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"html/template"
	"net/http"
	"strconv"
	"time"

	"github.com/anas-project/ANAS/modules/mail/identity"
	"github.com/anas-project/ANAS/modules/mail/store"
)

const logoutCookie = "__Host-anas-mail-logout"

type browserLogoutStore interface {
	BeginLogout(context.Context) (identity.Flow, string, error)
	ConsumeLogout(context.Context, string, string) (identity.Flow, error)
	LogoutSessionID(context.Context, string, string) (string, error)
}
type browserLogoutIAM interface {
	LogoutURL(string, string) (string, error)
}

func (b *webmailBinding) logoutSignature(session, nonce, issued string) string {
	mac := hmac.New(sha256.New, b.key)
	mac.Write([]byte("anas-mail-webmail-logout-v1\n" + session + "\n" + nonce + "\n" + issued))
	return hex.EncodeToString(mac.Sum(nil))
}
func (s *loginHandler) webmailLogout(w http.ResponseWriter, r *http.Request) {
	if !serviceRequest(r, "roundcube", s.webmail.token) {
		loginError(w, 401, "service-authentication-required")
		return
	}
	form, ok := exactWebmailForm(w, r, "refresh_token")
	if !ok {
		return
	}
	id, err := s.protocol.store.RevokeCredentialSession(r.Context(), form.Get("refresh_token"))
	if err != nil {
		protocolError(w, err, false)
		return
	}
	var random [32]byte
	if _, err = rand.Read(random[:]); err != nil {
		loginError(w, 503, "logout-unavailable")
		return
	}
	nonce := base64.RawURLEncoding.EncodeToString(random[:])
	issued := strconv.FormatInt(time.Now().Unix(), 10)
	reply(w, 200, map[string]any{"post_to": s.origin.String() + "/webmail/logout-continue", "fields": map[string]string{"session_id": id, "nonce": nonce, "issued_at": issued, "signature": s.webmail.logoutSignature(id, nonce, issued)}})
}
func (s *loginHandler) logout(w http.ResponseWriter, r *http.Request, fromWebmail bool) {
	data, ok := s.store.(browserLogoutStore)
	if !ok {
		loginError(w, 503, "logout-unavailable")
		return
	}
	if len(r.Header.Values("Origin")) != 1 || len(r.Header.Values("Authorization")) != 0 || r.URL.RawQuery != "" || r.URL.ForceQuery {
		loginError(w, 403, "invalid-origin")
		return
	}
	secret, ok := uniqueCookie(r, sessionCookie)
	if !ok {
		loginError(w, 401, "session-required")
		return
	}
	id, err := data.LogoutSessionID(r.Context(), secret, s.issuer)
	if err != nil {
		protocolError(w, err, false)
		return
	}
	if fromWebmail {
		if s.webmail == nil || r.Header.Get("Origin") != s.webmail.origin {
			loginError(w, 403, "invalid-origin")
			return
		}
		form, ok := exactWebmailForm(w, r, "session_id", "nonce", "issued_at", "signature")
		if !ok {
			return
		}
		seconds, err := strconv.ParseInt(form.Get("issued_at"), 10, 64)
		if err != nil || len(form.Get("issued_at")) != 10 || seconds < time.Now().Unix()-60 || seconds > time.Now().Unix()+5 || !webmailStatePattern.MatchString(form.Get("nonce")) || form.Get("session_id") != id || !hmac.Equal([]byte(form.Get("signature")), []byte(s.webmail.logoutSignature(id, form.Get("nonce"), form.Get("issued_at")))) {
			loginError(w, 401, "logout-denied")
			return
		}
	} else if r.Header.Get("Origin") != s.origin.String() {
		loginError(w, 403, "invalid-origin")
		return
	}
	// Local terminal withdrawal commits before any central logout operation.
	if err = s.store.RevokeSession(r.Context(), secret); err != nil {
		loginError(w, 503, "revocation-unavailable")
		return
	}
	setLoginCookie(w, sessionCookie, "", -1)
	iam, ok := s.iam.(browserLogoutIAM)
	if !ok {
		s.logoutUnavailable(w, fromWebmail)
		return
	}
	flow, browser, err := data.BeginLogout(r.Context())
	if err != nil {
		s.logoutUnavailable(w, fromWebmail)
		return
	}
	to, err := iam.LogoutURL(flow.State, s.origin.String()+"/oidc/logout-callback")
	if err != nil {
		s.logoutUnavailable(w, fromWebmail)
		return
	}
	setLoginCookie(w, logoutCookie, browser, 300)
	if fromWebmail {
		// Finish Roundcube's form navigation at its sole trusted Go origin.
		// A 303 to the IdP remains part of that submission and Chrome blocks
		// it under Roundcube's form-action policy. The Go document starts a
		// separate GET navigation to the already verified IdP endpoint.
		s.navigateLogout(w, to)
	} else {
		reply(w, 200, map[string]string{"local": "revoked", "iam_redirect": to})
	}
}

var logoutNavigationPage = template.Must(template.New("logout-navigation").Parse(`<!doctype html><html><head><meta charset="utf-8"><meta name="referrer" content="no-referrer"><title>ANAS Mail</title></head><body><a id="iam-logout" href="{{.URL}}">{{.Continue}}</a><script nonce="{{.Nonce}}">location.replace(document.getElementById('iam-logout').href);</script></body></html>`))

// Only logout() calls this with the URL returned by the verified IAM binding.
// There is no browser-selected destination and no form forwarding to the IdP.
func (s *loginHandler) navigateLogout(w http.ResponseWriter, to string) {
	var random [32]byte
	if _, err := rand.Read(random[:]); err != nil {
		s.signedOut(w, 503, false)
		return
	}
	nonce := base64.RawStdEncoding.EncodeToString(random[:])
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; script-src 'nonce-"+nonce+"'; form-action 'none'; frame-ancestors 'none'; base-uri 'none'")
	_ = logoutNavigationPage.Execute(w, map[string]string{"URL": to, "Nonce": nonce, "Continue": s.words()[0]})
}
func (s *loginHandler) logoutUnavailable(w http.ResponseWriter, browser bool) {
	if browser {
		s.signedOut(w, 503, false)
	} else {
		reply(w, 200, map[string]string{"local": "revoked", "iam": "unavailable"})
	}
}
func (s *loginHandler) logoutCallback(w http.ResponseWriter, r *http.Request) {
	data, ok := s.store.(browserLogoutStore)
	if !ok {
		loginError(w, 404, "not-found")
		return
	}
	q := r.URL.Query()
	browser, hasCookie := uniqueCookie(r, logoutCookie)
	setLoginCookie(w, logoutCookie, "", -1)
	if !hasCookie || len(q) != 1 || len(q["state"]) != 1 {
		loginError(w, 400, "logout-unconfirmed")
		return
	}
	if _, err := data.ConsumeLogout(r.Context(), q.Get("state"), browser); err != nil {
		status := 503
		if errors.Is(err, store.ErrLoginFlowDenied) {
			status = 400
		}
		loginError(w, status, "logout-unconfirmed")
		return
	}
	// This callback confirms the fixed IdP round trip, never creates a session.
	s.signedOut(w, 200, true)
}

var signedOutPage = template.Must(template.New("signed-out").Parse(`<!doctype html><html lang="{{.Language}}"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>ANAS Mail</title><link rel="stylesheet" href="/assets/mail.css"></head><body><main><h1>{{.Title}}</h1><p>{{.Message}}</p><a href="/login">{{.Login}}</a></main></body></html>`))

func (s *loginHandler) signedOut(w http.ResponseWriter, status int, confirmed bool) {
	words := s.words()
	message := words[3]
	if confirmed {
		message = words[2]
	}
	language := s.language
	if language == "" {
		language = "en"
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'self'; frame-ancestors 'none'; base-uri 'none'")
	w.WriteHeader(status)
	_ = signedOutPage.Execute(w, map[string]string{"Language": language, "Title": words[1], "Message": message, "Login": words[4]})
}
