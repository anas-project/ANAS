package main

import (
	"context"
	"encoding/json"
	"errors"
	"mime"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/anas-project/ANAS/modules/mail/dms"
	"github.com/anas-project/ANAS/modules/mail/identity"
	"github.com/anas-project/ANAS/modules/mail/store"
)

const (
	flowCookie    = "__Host-anas-mail-flow"
	sessionCookie = "__Host-anas-mail-session"
)

type loginStore interface {
	BeginLogin(context.Context) (identity.Flow, string, error)
	ConsumeLogin(context.Context, string, string) (identity.Flow, error)
	AdmitSession(context.Context, store.VerifiedLogin) (store.Session, string, error)
	Session(context.Context, string) (store.Session, error)
	RevokeSession(context.Context, string) error
	DenyPerson(context.Context, string) error
	ApplyLogout(context.Context, identity.Logout) error
}
type authenticator interface {
	AuthorizationURL(identity.Flow) (string, error)
	Exchange(context.Context, identity.Flow, string) (identity.Authentication, error)
	VerifyLogout(context.Context, string) (identity.Logout, error)
}
type directoryReader interface {
	Person(context.Context, string) (identity.Person, error)
}
type loginHandler struct {
	origin              *url.URL
	issuer              string
	store               loginStore
	iam                 authenticator
	reauthIAM           authenticator
	addresses           addressManager
	directory           directoryReader
	webmail             *webmailBinding
	protocol            *protocolBinding
	dms                 *dms.Client
	coordinator         *mailCoordinator
	language, portalURL string
	directoryEvents     bool
	companyEnabled      bool
	sources             ingressInspector
	lookupMX            func(context.Context, string) ([]*net.MX, error)
	lookupIP            func(context.Context, string, string) ([]net.IP, error)
	lookupTXT           func(context.Context, string) ([]string, error)
	probeDial           func(context.Context, string, string) (net.Conn, error)
	queueProbe          func(context.Context) (string, error)
	install             workerInstaller
}

func loginOrigin(origin string) (*url.URL, error) {
	u, err := url.Parse(origin)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || strings.ContainsAny(origin, "\x00\r\n") {
		return nil, errors.New("mail login binding is invalid")
	}
	return u, nil
}

func newLoginHandler(origin, issuer string, data loginStore, iam authenticator, directory directoryReader) (*loginHandler, error) {
	u, err := loginOrigin(origin)
	if err != nil || data == nil || iam == nil || directory == nil || issuer == "" {
		return nil, errors.New("mail login binding is invalid")
	}
	return &loginHandler{origin: u, issuer: issuer, store: data, iam: iam, directory: directory}, nil
}

func reply(w http.ResponseWriter, status int, value any) {
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(value)
}
func loginError(w http.ResponseWriter, status int, code string) {
	reply(w, status, map[string]string{"error": code})
}
func setLoginCookie(w http.ResponseWriter, name, value string, age int) {
	http.SetCookie(w, &http.Cookie{Name: name, Value: value, Path: "/", Secure: true, HttpOnly: true, SameSite: http.SameSiteLaxMode, MaxAge: age})
}
func uniqueCookie(r *http.Request, name string) (string, bool) {
	cookies := r.CookiesNamed(name)
	if len(cookies) != 1 || cookies[0].Value == "" {
		return "", false
	}
	return cookies[0].Value, true
}

func (s *loginHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'")
	// Never derive the public callback or trust boundary from forwarded headers.
	if !strings.EqualFold(r.Host, s.origin.Host) {
		loginError(w, 400, "invalid-host")
		return
	}
	timeout := 20 * time.Second
	if r.Method == "POST" && (r.URL.Path == "/api/setup/worker-install" || r.URL.Path == "/api/setup/relay-install" || r.URL.Path == "/api/setup/vps-install") {
		timeout = 3*time.Minute + 30*time.Second
		_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(4 * time.Minute))
	}
	ctx, cancel := context.WithTimeout(r.Context(), timeout)
	defer cancel()
	r = r.WithContext(ctx)
	switch r.Method + " " + r.URL.Path {
	case "GET /api/addresses/people":
		s.addressPeople(w, r)
	case "GET /addresses":
		s.addressPage(w, r)
	case "GET /api/addresses", "POST /api/addresses":
		s.addressAPI(w, r)
	case "GET /":
		s.home(w, r)
	case "GET /assets/mail.js", "GET /assets/mail.css", "GET /assets/setup.js", "GET /assets/addresses.js", "GET /assets/address-words.js":
		s.asset(w, r)
	case "GET /setup":
		s.setupPage(w, r)
	case "GET /api/setup", "PUT /api/setup":
		s.setup(w, r)
	case "POST /api/setup/dns":
		s.setupDNS(w, r)
	case "POST /api/setup/vps-smtp-probe":
		s.setupVPSSMTPProbe(w, r)
	case "POST /api/setup/outbound-smtp-probe":
		s.setupOutboundSMTPProbe(w, r)
	case "POST /api/setup/outbound-dns":
		s.setupOutboundDNS(w, r)
	case "POST /api/setup/outbound-queue":
		s.setupOutboundQueue(w, r)
	case "POST /api/setup/outbound-apply":
		s.setupOutboundApply(w, r)
	case "POST /api/setup/worker-install":
		s.setupWorkerInstall(w, r)
	case "POST /api/setup/relay-install":
		s.setupRelayInstall(w, r)
	case "POST /api/setup/vps-install":
		s.setupVPSInstall(w, r)
	case "GET /api/ingress":
		s.ingressStatus(w, r)
	case "GET /api/mailboxes":
		s.mailboxes(w, r)
	case "GET /reauth", "GET /oidc/reauth-callback":
		if s.reauthIAM == nil {
			loginError(w, 503, "authentication-unavailable")
			return
		}
		if _, ok := s.currentPerson(w, r); !ok {
			return
		}
		copy := *s
		copy.iam = s.reauthIAM
		if r.URL.Path == "/reauth" {
			owner := r.URL.Query().Get("owner")
			if owner != "" && !addressOwner.MatchString(owner) {
				loginError(w, 400, "invalid-owner")
				return
			}
			if owner == "" {
				owner = "self"
			}
			setLoginCookie(w, addressReturnCookie, owner, 300)
			copy.begin(w, r)
		} else {
			copy.callback(w, r)
		}
	case "GET /login":
		s.begin(w, r)
	case "GET /oidc/callback":
		s.callback(w, r)
	case "GET /api/me":
		s.me(w, r)
	case "POST /api/session/revoke":
		s.revoke(w, r)
	case "POST /api/session/logout":
		s.logout(w, r, false)
	case "POST /webmail/logout-continue":
		s.logout(w, r, true)
	case "GET /oidc/logout-callback":
		s.logoutCallback(w, r)
	case "POST /oidc/backchannel-logout":
		s.backchannel(w, r)
	case "POST /api/webmail/handoff":
		s.issueHandoff(w, r)
	case "POST /api/webmail/start":
		s.startWebmail(w, r)
	case "POST /webmail/continue":
		s.continueWebmail(w, r)
	case "POST /internal/webmail/redeem":
		s.redeemHandoff(w, r)
	default:
		loginError(w, 404, "not-found")
	}
}

func (s *loginHandler) begin(w http.ResponseWriter, r *http.Request) {
	f, browser, err := s.store.BeginLogin(r.Context())
	if err != nil {
		loginError(w, 503, "login-unavailable")
		return
	}
	location, err := s.iam.AuthorizationURL(f)
	if err != nil {
		loginError(w, 503, "login-unavailable")
		return
	}
	setLoginCookie(w, flowCookie, browser, 300)
	http.Redirect(w, r, location, http.StatusSeeOther)
}

func (s *loginHandler) callback(w http.ResponseWriter, r *http.Request) {
	q, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil || len(r.URL.RawQuery) > 16384 || len(q["state"]) != 1 || q.Get("state") == "" {
		loginError(w, 400, "invalid-login")
		return
	}
	for _, values := range q {
		if len(values) != 1 {
			loginError(w, 400, "invalid-login")
			return
		}
	}
	browser, ok := uniqueCookie(r, flowCookie)
	if !ok {
		loginError(w, 400, "invalid-login")
		return
	}
	f, err := s.store.ConsumeLogin(r.Context(), q.Get("state"), browser)
	if err != nil {
		status := 503
		if errors.Is(err, store.ErrLoginFlowDenied) {
			status = 400
		}
		loginError(w, status, "invalid-login")
		return
	}
	setLoginCookie(w, flowCookie, "", -1)
	if q.Get("error") != "" || q.Get("code") == "" || (q.Has("iss") && q.Get("iss") != s.issuer) {
		loginError(w, 400, "invalid-login")
		return
	}
	auth, err := s.iam.Exchange(r.Context(), f, q.Get("code"))
	if err != nil || auth.Issuer != s.issuer {
		loginError(w, 401, "authentication-denied")
		return
	}
	if r.URL.Path == "/oidc/reauth-callback" {
		secret, ok := uniqueCookie(r, sessionCookie)
		old, e := s.store.Session(r.Context(), secret)
		if !ok || e != nil || old.OwnerAnchor != auth.Anchor || !recentMailAuthentication(auth.AuthenticatedAt) {
			loginError(w, 401, "authentication-denied")
			return
		}
	}
	person, err := s.directory.Person(r.Context(), auth.Anchor)
	if !s.admitted(w, r, auth.Anchor, err) {
		return
	}
	if person.Anchor != auth.Anchor {
		loginError(w, 401, "authentication-denied")
		return
	}
	expires := minTime(auth.Expires, time.Now().Add(2*time.Hour))
	_, secret, err := s.store.AdmitSession(r.Context(), store.VerifiedLogin{OwnerAnchor: auth.Anchor, Issuer: auth.Issuer, SID: auth.SID, AuthenticatedAt: auth.AuthenticatedAt, Expires: expires})
	if err != nil {
		status := 503
		if errors.Is(err, store.ErrSessionDenied) {
			status = 401
		}
		loginError(w, status, "session-unavailable")
		return
	}
	setLoginCookie(w, sessionCookie, secret, max(1, int(time.Until(expires).Seconds())))
	if target, ok := uniqueCookie(r, addressReturnCookie); ok && (target == "self" || addressOwner.MatchString(target)) {
		setLoginCookie(w, addressReturnCookie, "", -1)
		path := "/addresses"
		if target != "self" {
			path += "?owner=" + target
		}
		http.Redirect(w, r, path, http.StatusSeeOther)
	} else if r.URL.Path == "/oidc/reauth-callback" {
		http.Redirect(w, r, "/addresses", http.StatusSeeOther)
	} else if s.protocol != nil {
		http.Redirect(w, r, "/", http.StatusSeeOther)
	} else {
		http.Redirect(w, r, "/api/me", http.StatusSeeOther)
	}
}

func minTime(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}
	return b
}

func (s *loginHandler) admitted(w http.ResponseWriter, r *http.Request, anchor string, err error) bool {
	if err == nil {
		return true
	}
	if !errors.Is(err, identity.ErrAdmission) {
		loginError(w, 503, "directory-unavailable")
		return false
	}
	if err := s.store.DenyPerson(r.Context(), anchor); err != nil {
		loginError(w, 503, "revocation-unavailable")
		return false
	}
	setLoginCookie(w, sessionCookie, "", -1)
	loginError(w, 401, "admission-denied")
	return false
}

func (s *loginHandler) me(w http.ResponseWriter, r *http.Request) {
	person, ok := s.currentPerson(w, r)
	if !ok {
		return
	}
	reply(w, 200, map[string]string{"anchor": person.Anchor, "username": person.Username, "display_name": person.DisplayName, "mail": person.Mail})
}

func (s *loginHandler) currentPerson(w http.ResponseWriter, r *http.Request) (identity.Person, bool) {
	secret, ok := uniqueCookie(r, sessionCookie)
	if !ok {
		loginError(w, 401, "session-required")
		return identity.Person{}, false
	}
	session, err := s.store.Session(r.Context(), secret)
	if err != nil {
		status := 503
		if errors.Is(err, store.ErrSessionDenied) {
			status = 401
			setLoginCookie(w, sessionCookie, "", -1)
		}
		loginError(w, status, "session-unavailable")
		return identity.Person{}, false
	}
	if session.Issuer != s.issuer {
		loginError(w, 401, "session-unavailable")
		return identity.Person{}, false
	}
	person, err := s.directory.Person(r.Context(), session.OwnerAnchor)
	if !s.admitted(w, r, session.OwnerAnchor, err) {
		return identity.Person{}, false
	}
	if person.Anchor != session.OwnerAnchor {
		loginError(w, 401, "admission-denied")
		return identity.Person{}, false
	}
	// A revocation received while LDAP was being read takes effect before reply.
	if _, err := s.store.Session(r.Context(), secret); err != nil {
		status := 503
		if errors.Is(err, store.ErrSessionDenied) {
			status = 401
		}
		loginError(w, status, "session-unavailable")
		return identity.Person{}, false
	}
	return person, true
}

func (s *loginHandler) revoke(w http.ResponseWriter, r *http.Request) {
	// This endpoint intentionally reports local scope, not RP-initiated IAM or
	// downstream logout. It remains private with the rest of this foundation.
	if len(r.Header.Values("Origin")) != 1 || r.Header.Get("Origin") != s.origin.String() || r.URL.RawQuery != "" {
		loginError(w, 403, "invalid-origin")
		return
	}
	secret, ok := uniqueCookie(r, sessionCookie)
	if !ok {
		loginError(w, 401, "session-required")
		return
	}
	if err := s.store.RevokeSession(r.Context(), secret); err != nil {
		status := 503
		if errors.Is(err, store.ErrSessionDenied) {
			status = 401
		}
		loginError(w, status, "revocation-unavailable")
		return
	}
	setLoginCookie(w, sessionCookie, "", -1)
	reply(w, 200, map[string]string{"status": "local-session-revoked"})
}

func (s *loginHandler) backchannel(w http.ResponseWriter, r *http.Request) {
	media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || media != "application/x-www-form-urlencoded" || r.URL.RawQuery != "" {
		loginError(w, 400, "invalid-logout")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 17000)
	if r.ParseForm() != nil || len(r.PostForm) != 1 || len(r.PostForm["logout_token"]) != 1 {
		loginError(w, 400, "invalid-logout")
		return
	}
	l, err := s.iam.VerifyLogout(r.Context(), r.PostForm.Get("logout_token"))
	if err != nil || l.Issuer != s.issuer {
		loginError(w, 400, "invalid-logout")
		return
	}
	if err := s.store.ApplyLogout(r.Context(), l); err != nil {
		loginError(w, 503, "revocation-unavailable")
		return
	}
	w.WriteHeader(200)
}
