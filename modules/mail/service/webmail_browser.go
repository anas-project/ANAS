package main

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"html/template"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

var postPage = template.Must(template.New("post").Parse(`<!doctype html><html><head><meta charset="utf-8"><meta name="referrer" content="origin"><title>Mail</title></head><body><form id="mail-handoff" method="post" action="{{.URL}}">{{range $key,$value:=.Fields}}<input type="hidden" name="{{$key}}" value="{{$value}}">{{end}}<button type="submit">{{.Continue}}</button></form><script nonce="{{.Nonce}}">document.getElementById('mail-handoff').submit();</script></body></html>`))

func (s *loginHandler) autoPost(w http.ResponseWriter, to string, fields map[string]string) {
	var random [32]byte
	if _, err := rand.Read(random[:]); err != nil {
		loginError(w, 503, "handoff-unavailable")
		return
	}
	nonce := base64.RawStdEncoding.EncodeToString(random[:])
	origin, _ := url.Parse(to)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "origin")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; script-src 'nonce-"+nonce+"'; form-action "+origin.Scheme+"://"+origin.Host+"; frame-ancestors 'none'; base-uri 'none'")
	_ = postPage.Execute(w, map[string]any{"URL": to, "Fields": fields, "Nonce": nonce, "Continue": s.words()[0]})
}

// This signed start protects Roundcube's context switch from unsolicited
// cross-origin POSTs. It grants no login; the later code is still single-use,
// personnel/session/box-bound and tied to Roundcube's own browser state.
func (s *loginHandler) startWebmail(w http.ResponseWriter, r *http.Request) {
	if s.protocol == nil || s.webmail == nil {
		loginError(w, 404, "not-found")
		return
	}
	if len(r.Header.Values("Origin")) != 1 || r.Header.Get("Origin") != s.origin.String() || len(r.Header.Values("Authorization")) != 0 {
		loginError(w, 403, "invalid-origin")
		return
	}
	form, ok := exactWebmailForm(w, r, "mailbox_id")
	if !ok {
		return
	}
	secret, ok := uniqueCookie(r, sessionCookie)
	if !ok {
		loginError(w, 401, "session-required")
		return
	}
	g, err := s.protocol.store.MailboxForSession(r.Context(), secret, form.Get("mailbox_id"))
	if err == nil {
		_, err = s.freshGrant(r.Context(), g)
	}
	if err != nil {
		protocolError(w, err, false)
		return
	}
	var raw [32]byte
	if _, err = rand.Read(raw[:]); err != nil {
		loginError(w, 503, "handoff-unavailable")
		return
	}
	nonce := base64.RawURLEncoding.EncodeToString(raw[:])
	issued := strconv.FormatInt(time.Now().Unix(), 10)
	mac := hmac.New(sha256.New, s.webmail.key)
	mac.Write([]byte("anas-mail-webmail-start-v1\n" + g.MailboxID + "\n" + nonce + "\n" + issued))
	reply(w, 200, map[string]any{"post_to": s.webmail.beginURL, "fields": map[string]string{"mailbox_id": g.MailboxID, "start_nonce": nonce, "issued_at": issued, "signature": hex.EncodeToString(mac.Sum(nil))}})
}

func (s *loginHandler) continueWebmail(w http.ResponseWriter, r *http.Request) {
	if s.protocol == nil || s.webmail == nil {
		loginError(w, 404, "not-found")
		return
	}
	if len(r.Header.Values("Origin")) != 1 || r.Header.Get("Origin") != s.webmail.origin || len(r.Header.Values("Authorization")) != 0 {
		loginError(w, 403, "invalid-origin")
		return
	}
	form, ok := exactWebmailForm(w, r, "mailbox_id", "browser_state", "issued_at", "signature")
	if !ok {
		return
	}
	if !s.webmail.validChallenge(form, time.Now()) {
		loginError(w, 401, "handoff-denied")
		return
	}
	h, ok := s.createHandoff(w, r, form)
	if !ok {
		return
	}
	s.autoPost(w, s.webmail.postURL, map[string]string{"code": h.Code, "session_id": h.SessionID, "mailbox_id": h.MailboxID, "browser_state": form.Get("browser_state")})
}
