package main

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"mime"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/anas-project/ANAS/modules/mail/store"
)

type handoffStore interface {
	IssueHandoff(context.Context, string, string, string) (store.Handoff, error)
	RedeemHandoff(context.Context, string, string, string, string) (store.MailboxGrant, error)
	CheckMailboxGrant(context.Context, store.MailboxGrant) (store.MailboxGrant, error)
}

// This is an opt-in development binding, not a rendered module secret yet.
type webmailConfig struct {
	URL, ServiceToken string
}

type webmailBinding struct {
	postURL          string
	beginURL, origin string
	token            string
	key              []byte
	store            handoffStore
}

var (
	webmailIDPattern    = regexp.MustCompile(`^[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}$`)
	webmailStatePattern = regexp.MustCompile(`^[A-Za-z0-9_-]{43}$`)
	webmailKeyPattern   = regexp.MustCompile(`^[a-f0-9]{64}$`)
)

func (s *loginHandler) configureWebmail(cfg webmailConfig, data handoffStore) error {
	u, err := url.Parse(cfg.URL)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || strings.ContainsAny(cfg.URL, "\x00\r\n") || !webmailKeyPattern.MatchString(cfg.ServiceToken) || data == nil || strings.EqualFold(u.Host, s.origin.Host) {
		return errors.New("mail webmail binding invalid")
	}
	key, _ := hex.DecodeString(cfg.ServiceToken)
	u.Path = "/"
	u.RawQuery = "_task=login&_action=plugin.anas_login"
	begin := *u
	begin.RawQuery = "_task=login&_action=plugin.anas_begin"
	s.webmail = &webmailBinding{postURL: u.String(), beginURL: begin.String(), origin: cfg.URL, token: cfg.ServiceToken, key: key, store: data}
	return nil
}

// Roundcube creates a random state in its own browser session and signs these
// exact fields. The signature proves its origin; it grants no personnel login.
// The browser must submit through the authenticated Go origin. A later plugin
// must compare state to its own session before contacting the redeem endpoint.
func (b *webmailBinding) validChallenge(form url.Values, now time.Time) bool {
	box, browser, issued := form.Get("mailbox_id"), form.Get("browser_state"), form.Get("issued_at")
	if !webmailIDPattern.MatchString(box) || !webmailStatePattern.MatchString(browser) || len(issued) != 10 || !webmailKeyPattern.MatchString(form.Get("signature")) {
		return false
	}
	seconds, err := strconv.ParseInt(issued, 10, 64)
	if err != nil || strconv.FormatInt(seconds, 10) != issued || seconds > now.Unix()+5 || seconds < now.Unix()-60 {
		return false
	}
	mac := hmac.New(sha256.New, b.key)
	mac.Write([]byte("anas-mail-webmail-handoff-v1\n" + box + "\n" + browser + "\n" + issued))
	sig, _ := hex.DecodeString(form.Get("signature"))
	return hmac.Equal(mac.Sum(nil), sig)
}

func exactWebmailForm(w http.ResponseWriter, r *http.Request, fields ...string) (url.Values, bool) {
	media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if len(r.Header.Values("Content-Type")) != 1 || err != nil || media != "application/x-www-form-urlencoded" || r.URL.RawQuery != "" || r.URL.ForceQuery {
		loginError(w, 400, "invalid-handoff")
		return nil, false
	}
	r.Body = http.MaxBytesReader(w, r.Body, 2048)
	if r.ParseForm() != nil || len(r.PostForm) != len(fields) {
		loginError(w, 400, "invalid-handoff")
		return nil, false
	}
	for _, key := range fields {
		if len(r.PostForm[key]) != 1 || r.PostForm.Get(key) == "" {
			loginError(w, 400, "invalid-handoff")
			return nil, false
		}
	}
	return r.PostForm, true
}

func handoffError(w http.ResponseWriter, err error) {
	if errors.Is(err, store.ErrHandoffDenied) || errors.Is(err, store.ErrSessionDenied) {
		loginError(w, 401, "handoff-denied")
		return
	}
	loginError(w, 503, "handoff-unavailable")
}

func (s *loginHandler) issueHandoff(w http.ResponseWriter, r *http.Request) {
	if s.webmail == nil {
		loginError(w, 404, "not-found")
		return
	}
	if len(r.Header.Values("Origin")) != 1 || r.Header.Get("Origin") != s.origin.String() || len(r.Header.Values("Authorization")) != 0 {
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
	reply(w, 200, map[string]any{"code": h.Code, "session_id": h.SessionID, "mailbox_id": h.MailboxID, "expires_at": h.Expires, "post_to": s.webmail.postURL})
}

func (s *loginHandler) createHandoff(w http.ResponseWriter, r *http.Request, form url.Values) (store.Handoff, bool) {
	secret, ok := uniqueCookie(r, sessionCookie)
	if !ok {
		loginError(w, 401, "session-required")
		return store.Handoff{}, false
	}
	session, err := s.store.Session(r.Context(), secret)
	if err != nil {
		handoffError(w, err)
		return store.Handoff{}, false
	}
	if session.Issuer != s.issuer {
		loginError(w, 401, "handoff-denied")
		return store.Handoff{}, false
	}
	if s.protocol != nil {
		g, err := s.protocol.store.MailboxForSession(r.Context(), secret, form.Get("mailbox_id"))
		if err == nil {
			_, err = s.freshGrant(r.Context(), g)
		}
		if err != nil {
			protocolError(w, err, false)
			return store.Handoff{}, false
		}
	} else {
		person, err := s.directory.Person(r.Context(), session.OwnerAnchor)
		if !s.admitted(w, r, session.OwnerAnchor, err) {
			return store.Handoff{}, false
		}
		if person.Anchor != session.OwnerAnchor {
			loginError(w, 401, "handoff-denied")
			return store.Handoff{}, false
		}
	}
	// IssueHandoff rechecks local state after the directory read.
	h, err := s.webmail.store.IssueHandoff(r.Context(), secret, form.Get("mailbox_id"), form.Get("browser_state"))
	if err != nil {
		handoffError(w, err)
		return store.Handoff{}, false
	}
	return h, true
}

func (s *loginHandler) redeemHandoff(w http.ResponseWriter, r *http.Request) {
	if s.webmail == nil {
		loginError(w, 404, "not-found")
		return
	}
	// Authenticate before parsing or querying. This endpoint is server-to-server;
	// browser cookies and Origin never substitute for the service credential.
	if len(r.Header.Values("Authorization")) != 1 || subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), []byte("Bearer "+s.webmail.token)) != 1 || len(r.Header.Values("Cookie")) != 0 || len(r.Header.Values("Origin")) != 0 {
		loginError(w, 401, "service-authentication-required")
		return
	}
	form, ok := exactWebmailForm(w, r, "code", "session_id", "mailbox_id", "browser_state")
	if !ok {
		return
	}
	g, err := s.webmail.store.RedeemHandoff(r.Context(), form.Get("code"), form.Get("session_id"), form.Get("mailbox_id"), form.Get("browser_state"))
	if err != nil {
		handoffError(w, err)
		return
	}
	if g.Issuer != s.issuer {
		loginError(w, 401, "handoff-denied")
		return
	}
	person, err := s.directory.Person(r.Context(), g.OwnerAnchor)
	if !s.admitted(w, r, g.OwnerAnchor, err) {
		return
	}
	if person.Anchor != g.OwnerAnchor {
		loginError(w, 401, "handoff-denied")
		return
	}
	// A logout/withholding that arrives during LDAP must prevent the reply too.
	g, err = s.webmail.store.CheckMailboxGrant(r.Context(), g)
	if err != nil {
		handoffError(w, err)
		return
	}
	if s.protocol != nil {
		if !serviceRequest(r, "roundcube", s.webmail.token) {
			loginError(w, 401, "service-authentication-required")
			return
		}
		g, err = s.freshGrant(r.Context(), g)
		if err != nil {
			protocolError(w, err, false)
			return
		}
		credentials, err := s.protocol.store.IssueCredentials(r.Context(), g)
		if err != nil {
			protocolError(w, err, false)
			return
		}
		reply(w, 200, wireCredentials(credentials))
		return
	}
	// Identifiers only. No password, protocol credential or bearer authorization
	// is issued by this development endpoint.
	reply(w, 200, map[string]any{"session_id": g.SessionID, "mailbox_id": g.MailboxID, "protocol_login": g.ProtocolLogin, "roundcube_key": g.RoundcubeKey, "default_address": g.DefaultAddress, "authorization_epoch": g.AuthorizationEpoch})
}
