package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"net/url"
	"regexp"
	"time"

	"github.com/anas-project/ANAS/internal/directorymail"
	"github.com/anas-project/ANAS/modules/mail/identity"
	"github.com/anas-project/ANAS/modules/mail/store"
)

var addressOwner = regexp.MustCompile(`^[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}$`)

const addressReturnCookie = "__Host-anas-mail-address-return"

type addressManager interface {
	Person(context.Context, string) (identity.Person, error)
	People(context.Context, string) ([]identity.Person, error)
	View(context.Context, string, string) (directorymail.ManagementView, error)
	Apply(context.Context, string, string, directorymail.MailRequest, func() error) (directorymail.ManagementResult, error)
}
type addressAuditStore interface {
	BeginAddressAudit(context.Context, string, string, directorymail.MailRequest) (int64, error)
	FinishAddressAudit(context.Context, int64, string) error
}

func recentMailAuthentication(at time.Time) bool {
	now := time.Now()
	return !at.IsZero() && !at.Before(now.Add(-5*time.Minute)) && !at.After(now.Add(30*time.Second))
}
func (s *loginHandler) addressPage(w http.ResponseWriter, r *http.Request) {
	if s.addresses == nil {
		loginError(w, 503, "address-management-unavailable")
		return
	}
	q, err := url.ParseQuery(r.URL.RawQuery)
	owner := q.Get("owner")
	if err != nil || len(q["owner"]) > 1 || len(q["lang"]) > 1 || owner != "" && !addressOwner.MatchString(owner) {
		loginError(w, 400, "invalid-owner")
		return
	}
	for key := range q {
		if key != "owner" && key != "lang" {
			loginError(w, 400, "invalid-request")
			return
		}
	}
	secret, signedIn := uniqueCookie(r, sessionCookie)
	if signedIn {
		if _, err := s.store.Session(r.Context(), secret); err != nil {
			if !errors.Is(err, store.ErrSessionDenied) {
				loginError(w, 503, "session-unavailable")
				return
			}
			setLoginCookie(w, sessionCookie, "", -1)
			signedIn = false
		}
	}
	if !signedIn {
		if owner == "" {
			owner = "self"
		}
		setLoginCookie(w, addressReturnCookie, owner, 300)
		http.Redirect(w, r, "/login", 303)
		return
	}
	if _, ok := s.currentPerson(w, r); !ok {
		return
	}
	raw, _ := mailUI.ReadFile("ui/addresses.html")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; script-src 'self'; style-src 'self'; connect-src 'self'; form-action 'none'; frame-ancestors 'none'; base-uri 'none'")
	w.Write(raw)
}
func (s *loginHandler) addressAPI(w http.ResponseWriter, r *http.Request) {
	if s.addresses == nil {
		loginError(w, 503, "address-management-unavailable")
		return
	}
	if r.Method == "POST" && (len(r.Header.Values("Origin")) != 1 || r.Header.Get("Origin") != s.origin.String()) {
		loginError(w, 403, "invalid-origin")
		return
	}
	q, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil || len(q) > 1 || len(q["owner"]) > 1 || len(q) == 1 && !q.Has("owner") {
		loginError(w, 400, "invalid-request")
		return
	}
	actor, ok := s.currentPerson(w, r)
	if !ok {
		return
	}
	owner := q.Get("owner")
	if owner == "" {
		owner = actor.Anchor
	}
	if !addressOwner.MatchString(owner) {
		loginError(w, 400, "invalid-owner")
		return
	}
	_, adminErr := s.administrator(r.Context(), actor)
	if owner != actor.Anchor && adminErr != nil {
		loginError(w, 403, "access-denied")
		return
	}
	if r.Method == "GET" {
		target, e := s.addresses.Person(r.Context(), owner)
		if e != nil || target.Anchor != owner {
			loginError(w, 403, "access-denied")
			return
		}
		view, e := s.addresses.View(r.Context(), actor.Anchor, owner)
		if e != nil {
			loginError(w, 503, "address-management-unavailable")
			return
		}
		// A response cannot outlive revocation while the directory was read.
		fresh, ok := s.currentPerson(w, r)
		if !ok {
			return
		}
		_, adminErr = s.administrator(r.Context(), fresh)
		if owner != actor.Anchor && adminErr != nil {
			loginError(w, 403, "access-denied")
			return
		}
		if adminErr != nil {
			view.Policy.Protected = nil
		}
		reply(w, 200, map[string]any{"view": view, "language": s.language, "owner": owner, "display_name": target.DisplayName, "username": target.Username, "can_manage_others": adminErr == nil})
		return
	}
	media, _, e := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if e != nil || media != "application/json" || len(r.Header.Values("Content-Type")) != 1 {
		loginError(w, 400, "invalid-request")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 8192)
	request, valid := decodeAddressRequest(r.Body)
	if !valid {
		loginError(w, 400, "invalid-request")
		return
	}
	secret, _ := uniqueCookie(r, sessionCookie)
	check := func() error {
		session, e := s.store.Session(r.Context(), secret)
		if e != nil || session.OwnerAnchor != actor.Anchor || session.Issuer != s.issuer || !recentMailAuthentication(session.AuthenticatedAt) {
			return directorymail.ErrDenied
		}
		fresh, e := s.directory.Person(r.Context(), actor.Anchor)
		if e != nil || fresh.Anchor != actor.Anchor {
			return directorymail.ErrDenied
		}
		if owner != actor.Anchor {
			if _, e = s.administrator(r.Context(), fresh); e != nil {
				return directorymail.ErrDenied
			}
		}
		_, e = s.store.Session(r.Context(), secret)
		return e
	}
	session, e := s.store.Session(r.Context(), secret)
	if e != nil {
		loginError(w, 401, "session-unavailable")
		return
	}
	if !recentMailAuthentication(session.AuthenticatedAt) {
		loginError(w, 403, "recent-authentication-required")
		return
	}
	if check() != nil {
		loginError(w, 403, "access-denied")
		return
	}
	audit, ok := s.store.(addressAuditStore)
	if !ok {
		loginError(w, 503, "audit-unavailable")
		return
	}
	id, e := audit.BeginAddressAudit(r.Context(), actor.Anchor, owner, request)
	if e != nil {
		loginError(w, 503, "audit-unavailable")
		return
	}
	result, e := s.addresses.Apply(r.Context(), actor.Anchor, owner, request, check)
	outcome := "saved"
	switch {
	case e == nil:
	case errors.Is(e, directorymail.ErrDenied):
		outcome = "denied"
	case errors.Is(e, directorymail.ErrQuota):
		outcome = "quota"
	case errors.Is(e, directorymail.ErrProtected):
		outcome = "protected"
	case errors.Is(e, directorymail.ErrConflict):
		outcome = "conflict"
	default:
		outcome = "unknown"
	}
	auditStatus := "saved"
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 5*time.Second)
	defer cancel()
	if audit.FinishAddressAudit(ctx, id, outcome) != nil {
		auditStatus = "unavailable"
	}
	reply(w, 200, map[string]any{"directory": outcome, "audit": auditStatus, "mail": result})
}

func decodeAddressRequest(body io.Reader) (directorymail.MailRequest, bool) {
	d := json.NewDecoder(body)
	start, err := d.Token()
	if err != nil || start != json.Delim('{') {
		return directorymail.MailRequest{}, false
	}
	allowed := map[string]bool{"operation_id": true, "version": true, "action": true, "mailbox_id": true, "address": true}
	fields := make(map[string]string, len(allowed))
	for d.More() {
		token, err := d.Token()
		key, ok := token.(string)
		if err != nil || !ok || !allowed[key] {
			return directorymail.MailRequest{}, false
		}
		if _, duplicate := fields[key]; duplicate {
			return directorymail.MailRequest{}, false
		}
		var raw json.RawMessage
		var value string
		if d.Decode(&raw) != nil || len(raw) == 0 || raw[0] != '"' || json.Unmarshal(raw, &value) != nil {
			return directorymail.MailRequest{}, false
		}
		fields[key] = value
	}
	end, err := d.Token()
	if err != nil || end != json.Delim('}') {
		return directorymail.MailRequest{}, false
	}
	if _, err = d.Token(); err != io.EOF {
		return directorymail.MailRequest{}, false
	}
	for _, key := range []string{"operation_id", "version", "action", "address"} {
		if _, ok := fields[key]; !ok {
			return directorymail.MailRequest{}, false
		}
	}
	request := directorymail.MailRequest{ID: fields["operation_id"], Version: fields["version"], Action: fields["action"], MailboxID: fields["mailbox_id"], Address: fields["address"]}
	return request, request.Valid()
}

func (s *loginHandler) addressPeople(w http.ResponseWriter, r *http.Request) {
	if s.addresses == nil {
		loginError(w, 503, "address-management-unavailable")
		return
	}
	if _, _, ok := s.setupAccess(w, r); !ok {
		return
	}
	q, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil || len(q) != 1 || len(q["q"]) != 1 {
		loginError(w, 400, "invalid-request")
		return
	}
	people, err := s.addresses.People(r.Context(), q.Get("q"))
	if err != nil {
		loginError(w, 503, "address-management-unavailable")
		return
	}
	if _, _, ok := s.setupAccess(w, r); !ok {
		return
	}
	type item struct {
		Anchor      string `json:"anchor"`
		Username    string `json:"username"`
		DisplayName string `json:"display_name"`
	}
	list := []item{}
	for _, p := range people {
		list = append(list, item{p.Anchor, p.Username, p.DisplayName})
	}
	reply(w, 200, map[string]any{"people": list})
}
