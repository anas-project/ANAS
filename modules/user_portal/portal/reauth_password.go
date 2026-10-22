package main

import (
	"net/http"
	"time"
)

// passwordVerifier proves a person's own domain password without using it
// for any write.
type passwordVerifier interface {
	VerifyActor(Person, string) string
}

// recentSession accepts either a verified OIDC auth_time or a portal-side
// directory password check made by the same person in this session.
func (s *Server) recentSession(v Session) bool {
	at := v.AuthTime
	if v.VerifiedAt.After(at) {
		at = v.VerifiedAt
	}
	return recent(at, s.config.RecentWindow, time.Now())
}

// reauthPassword proves the signed-in person's own domain password with an
// LDAPS bind as that person. The password is used for this request only.
func (s *Server) reauthPassword(w http.ResponseWriter, r *http.Request) {
	v, person, _, ok := s.current(w, r)
	if !ok {
		return
	}
	if !s.csrf(r, v) {
		s.rejected(v.Subject, v.Subject, "session.reauth", "self")
		failure(w, 403, "csrf_rejected")
		return
	}
	var b struct {
		Password string `json:"password"`
	}
	if !decode(r, &b) || b.Password == "" || len(b.Password) > 1024 {
		s.rejected(v.Subject, v.Subject, "session.reauth", "self")
		failure(w, 400, "invalid_request")
		return
	}
	if !s.passwordAttempt(r, person.Anchor) || !s.limiter.allowIdentity(person.Anchor) {
		s.rejected(v.Subject, v.Subject, "session.reauth", "self")
		failure(w, 429, "rate_limited")
		return
	}
	d, ok := s.directory.(passwordVerifier)
	if !ok {
		failure(w, 503, "identity_unavailable")
		return
	}
	fresh, err := s.directory.Person(person.Anchor)
	if err != nil {
		failure(w, 503, "identity_unavailable")
		return
	}
	// Every refusal looks the same: wrong password, disabled, locked or
	// required change must not be distinguishable from each other.
	code := "credentials_rejected"
	if fresh.GUID == person.GUID && fresh.canLogin() {
		code = d.VerifyActor(fresh, b.Password)
	}
	if code == "unavailable" {
		failure(w, 503, "identity_unavailable")
		return
	}
	if code != "" {
		s.rejected(v.Subject, v.Subject, "session.reauth", "self")
		failure(w, 403, "authentication_rejected")
		return
	}
	now := time.Now()
	if s.audit.append(Record{ID: randomID(), Time: now.UTC(), Phase: "result", Actor: person.Anchor, Target: person.Anchor, Action: "session.reauth", Rule: "self", Directory: "saved", ConfigDigest: s.digest}) != nil {
		failure(w, 503, "audit_unavailable")
		return
	}
	if !s.store.verified(v.ID, v.Subject, now) {
		failure(w, 401, "authentication_required")
		return
	}
	respond(w, 200, map[string]any{"recent_authentication": true})
}
