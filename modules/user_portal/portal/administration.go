package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strings"
	"time"
)

type accountDirectory interface {
	Groups() ([]Group, error)
	Profile(Person, Profile) string
	ExecuteAs(Person, string, AdminOperation, Person, Group) string
}

// This is a closed list of personnel actions, never an LDAP/command payload.
type AdminOperation struct {
	Action            string  `json:"action"`
	Target            string  `json:"target"`
	Group             string  `json:"group,omitempty"`
	Reason            string  `json:"reason"`
	ForceChange       bool    `json:"force_change,omitempty"`
	ExpectedDirect    bool    `json:"expected_direct,omitempty"`
	Profile           Profile `json:"profile"`
	ProfileVersion    string  `json:"profile_version,omitempty"`
	Proof             string  `json:"proof,omitempty"`
	ActorPassword     string  `json:"actor_password,omitempty"`
	temporaryPassword string
}

func (op AdminOperation) digest() string {
	op.Proof, op.ActorPassword = "", ""
	data, _ := json.Marshal(op)
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}

type Confirmation struct {
	Session, ActorGUID, TargetGUID, GroupGUID, Digest string
	Expires                                           time.Time
}

func (s *Server) administrator(p Person) bool {
	if !s.config.Administration {
		return false
	}
	for _, g := range p.Groups {
		if g.Anchor == s.config.AdminGroup && g.GUID == s.config.AdminGroupGUID {
			return true
		}
	}
	return false
}

func (s *Server) allGroups() ([]Group, error) {
	d, ok := s.directory.(accountDirectory)
	if !ok {
		return nil, errDenied
	}
	return d.Groups()
}

func (s *Server) adminGroups(w http.ResponseWriter, r *http.Request, actor, target Person) {
	groups, err := s.allGroups()
	if err != nil {
		failure(w, 503, "identity_unavailable")
		return
	}
	result := []map[string]any{}
	for _, g := range groups {
		if !g.Managed && r.URL.Query().Get("advanced") != "true" {
			continue
		}
		direct := false
		for _, member := range g.Members {
			direct = direct || equalDN(member, target.DN)
		}
		blocked := target.System || has(s.config.ProtectedUsers, target.Anchor) || has(s.config.ProtectedGroups, g.Anchor)
		domain := target.DomainRisk || g.DomainRisk
		writable := !blocked && (!domain || s.config.DomainAdministration)
		selfRole := actor.Anchor == target.Anchor && g.AdminRole
		primary := g.SID == target.PrimaryGroupSID
		result = append(result, map[string]any{"group": g, "direct": direct, "inherited": target.inGroup(g.Reference) && !direct, "primary": primary, "requires_domain": domain, "can_add": writable && !direct && !primary, "can_remove": writable && direct && !selfRole && !primary})
	}
	respond(w, 200, map[string]any{"groups": result, "advanced": r.URL.Query().Get("advanced") == "true"})
}

func (s *Server) operationTarget(actor Person, op AdminOperation, self bool) (Person, Group, bool, string) {
	if strings.TrimSpace(op.Reason) == "" || len(op.Reason) > 512 || !anchorPattern.MatchString(op.Target) {
		return Person{}, Group{}, false, "invalid_request"
	}
	if !self && !s.administrator(actor) || self && (op.Action != "profile.update" || op.Target != actor.Anchor) {
		return Person{}, Group{}, false, "access_denied"
	}
	p, err := s.directory.Person(op.Target)
	if err != nil || p.System || has(s.config.ProtectedUsers, p.Anchor) && !self {
		return p, Group{}, false, "access_denied"
	}
	g := Group{}
	domain := p.DomainRisk && !(op.Action == "profile.update" && p.Anchor == actor.Anchor)
	switch op.Action {
	case "profile.update":
		if !op.Profile.valid() || op.ProfileVersion == "" || op.Group != "" || op.ForceChange || op.ExpectedDirect {
			return p, g, domain, "invalid_request"
		}
		if op.ProfileVersion != profileVersion(p) {
			return p, g, domain, "conflict"
		}
	case "password.reset", "account.unlock":
		if op.Group != "" || op.Profile != (Profile{}) || op.ProfileVersion != "" || op.ExpectedDirect || op.Action == "account.unlock" && op.ForceChange {
			return p, g, domain, "invalid_request"
		}
		if p.Anchor == actor.Anchor {
			return p, g, domain, "self_administration_denied"
		}
	case "groups.member.add", "groups.member.remove":
		if op.Profile != (Profile{}) || op.ProfileVersion != "" || op.ForceChange {
			return p, g, domain, "invalid_request"
		}
		groups, err := s.allGroups()
		if err != nil {
			return p, g, domain, "identity_unavailable"
		}
		for _, candidate := range groups {
			if candidate.Reference == op.Group {
				g = candidate
				break
			}
		}
		if g.GUID == "" || has(s.config.ProtectedGroups, g.Anchor) {
			return p, g, domain, "access_denied"
		}
		domain = domain || g.DomainRisk
		direct := false
		for _, m := range g.Members {
			direct = direct || equalDN(m, p.DN)
		}
		add := op.Action == "groups.member.add"
		if direct != op.ExpectedDirect || direct == add {
			return p, g, domain, "conflict"
		}
		if !add {
			if p.PrimaryGroupSID == g.SID || p.Anchor == actor.Anchor && (g.AdminRole || g.DomainRisk) {
				return p, g, domain, "self_administration_denied"
			}
			if (g.AdminRole || g.DomainRisk) && !s.retainsAdministrators(groups, g, p) {
				return p, g, domain, "last_administrator"
			}
		}
	default:
		return p, g, domain, "invalid_request"
	}
	if domain && !s.config.DomainAdministration {
		return p, g, domain, "domain_administration_disabled"
	}
	return p, g, domain, ""
}

func (s *Server) retainsAdministrators(groups []Group, modified Group, target Person) bool {
	people, err := s.directory.People()
	if err != nil {
		return false
	}
	after := append([]Group(nil), groups...)
	for i, g := range after {
		if g.GUID != modified.GUID {
			continue
		}
		after[i].Members = nil
		for _, m := range g.Members {
			if !equalDN(m, target.DN) {
				after[i].Members = append(after[i].Members, m)
			}
		}
	}
	for _, critical := range groups {
		if critical.GUID != s.config.AdminGroupGUID && !strings.HasSuffix(critical.SID, "-512") && critical.SID != "S-1-5-32-544" {
			continue
		}
		touched := critical.GUID == modified.GUID
		for _, parent := range closure(groups, modified.DN, "") {
			touched = touched || parent.GUID == critical.GUID
		}
		if !touched {
			continue
		}
		beforeCount, afterCount := 0, 0
		for _, p := range people {
			if p.System || !p.canLogin() {
				continue
			}
			for _, g := range closure(groups, p.DN, p.PrimaryGroupSID) {
				if g.GUID == critical.GUID {
					beforeCount++
				}
			}
			for _, g := range closure(after, p.DN, p.PrimaryGroupSID) {
				if g.GUID == critical.GUID {
					afterCount++
				}
			}
		}
		if beforeCount == 0 || afterCount == 0 {
			return false
		}
	}
	return true
}

func (s *Server) prepareOperation(w http.ResponseWriter, r *http.Request) {
	s.writes.Lock()
	defer s.writes.Unlock()
	v, actor, _, ok := s.current(w, r)
	if !ok {
		return
	}
	if !s.csrf(r, v) {
		s.rejected(v.Subject, "", "domain.prepare", "")
		failure(w, 403, "csrf_rejected")
		return
	}
	var op AdminOperation
	if !decode(r, &op) || op.Proof != "" || op.ActorPassword != "" {
		s.rejected(v.Subject, "", "domain.prepare", "")
		failure(w, 400, "invalid_request")
		return
	}
	p, g, domain, code := s.operationTarget(actor, op, false)
	if code != "" {
		s.rejected(v.Subject, op.Target, op.Action, "platform_admin")
		failure(w, 403, code)
		return
	}
	if !domain {
		s.rejected(v.Subject, p.Anchor, op.Action, "domain_operator")
		failure(w, 400, "invalid_request")
		return
	}
	if !recent(v.AuthTime, s.config.RecentWindow, time.Now()) {
		s.rejected(v.Subject, p.Anchor, op.Action, "domain_operator")
		failure(w, 403, "recent_authentication_required")
		return
	}
	proof := randomID()
	if !s.store.addConfirmation(proof, Confirmation{v.ID, actor.GUID, p.GUID, g.GUID, op.digest(), time.Now().Add(120 * time.Second)}) {
		failure(w, 503, "capacity_reached")
		return
	}
	respond(w, 200, map[string]any{"proof": proof, "expires_in": 120, "target": p.Username, "group": g.Name, "action": op.Action})
}

func (s *Server) executeOperation(w http.ResponseWriter, r *http.Request) {
	s.accountOperation(w, r, false)
}
func (s *Server) updateProfile(w http.ResponseWriter, r *http.Request) {
	s.accountOperation(w, r, true)
}

func (s *Server) accountOperation(w http.ResponseWriter, r *http.Request, self bool) {
	s.writes.Lock()
	defer s.writes.Unlock()
	v, actor, _, ok := s.current(w, r)
	if !ok {
		return
	}
	if !s.csrf(r, v) {
		s.rejected(v.Subject, "", "account.write", "")
		failure(w, 403, "csrf_rejected")
		return
	}
	var op AdminOperation
	if !decode(r, &op) {
		s.rejected(v.Subject, "", "account.write", "")
		failure(w, 400, "invalid_request")
		return
	}
	if self {
		if op.Action != "" && op.Action != "profile.update" || op.Target != "" && op.Target != actor.Anchor {
			s.rejected(v.Subject, v.Subject, "profile.update", "self")
			failure(w, 400, "invalid_request")
			return
		}
		op.Action, op.Target, op.Reason = "profile.update", actor.Anchor, "self profile"
	}
	p, g, domain, code := s.operationTarget(actor, op, self)
	if code != "" {
		s.rejected(v.Subject, op.Target, op.Action, "platform_admin")
		status := 403
		if code == "invalid_request" {
			status = 400
		}
		if code == "conflict" {
			status = 409
		}
		failure(w, status, code)
		return
	}
	if !recent(v.AuthTime, s.config.RecentWindow, time.Now()) {
		s.rejected(v.Subject, p.Anchor, op.Action, "")
		failure(w, 403, "recent_authentication_required")
		return
	}
	d, ok := s.directory.(accountDirectory)
	if !ok {
		failure(w, 503, "identity_unavailable")
		return
	}
	if domain {
		proof, valid := s.store.consumeConfirmation(op.Proof)
		if !valid || proof.Session != v.ID || proof.ActorGUID != actor.GUID || proof.TargetGUID != p.GUID || proof.GroupGUID != g.GUID || proof.Digest != op.digest() {
			s.rejected(v.Subject, p.Anchor, op.Action, "domain_operator")
			failure(w, 403, "confirmation_required")
			return
		}
		if !s.passwordAttempt(r, actor.Anchor) || !s.limiter.allowIdentity(actor.Anchor) {
			s.rejected(v.Subject, p.Anchor, op.Action, "domain_operator")
			failure(w, 429, "rate_limited")
			return
		}
	} else if op.Proof != "" || op.ActorPassword != "" {
		s.rejected(v.Subject, p.Anchor, op.Action, "platform_admin")
		failure(w, 400, "invalid_request")
		return
	}
	if op.Action == "password.reset" {
		if !s.passwordAttempt(r, p.Anchor) || !s.limiter.allowIdentity(p.Anchor) {
			s.rejected(v.Subject, p.Anchor, op.Action, "platform_admin")
			failure(w, 429, "rate_limited")
			return
		}
		op.temporaryPassword = "Aa1!" + randomID()[:28]
	}
	rule := "platform_admin"
	if self {
		rule = "self"
	}
	if domain {
		rule = "domain_operator"
	}
	record := Record{Actor: actor.Anchor, Target: p.Anchor, Action: op.Action, Rule: rule, Group: op.Group, Reason: op.Reason}
	if op.Action == "password.reset" {
		record.ForceChange = &op.ForceChange
	}
	if strings.HasPrefix(op.Action, "groups.member.") {
		record.ExpectedDirect = &op.ExpectedDirect
	}
	if op.Action == "profile.update" {
		old := profileOf(p)
		for i, name := range []string{"given_name", "surname", "display_name"} {
			if []string{old.GivenName, old.Surname, old.DisplayName}[i] != []string{op.Profile.GivenName, op.Profile.Surname, op.Profile.DisplayName}[i] {
				record.ChangedFields = append(record.ChangedFields, name)
			}
		}
	}
	s.write(w, r, record, func() string {
		if !s.validWriteSession(r.Context(), v.ID, actor.Anchor, true) {
			return "denied"
		}
		freshActor, err := s.directory.Person(actor.Anchor)
		if err != nil || freshActor.GUID != actor.GUID || !freshActor.canLogin() {
			return "denied"
		}
		fresh, freshGroup, needsDomain, errCode := s.operationTarget(freshActor, op, self)
		if errCode != "" || fresh.GUID != p.GUID || freshGroup.GUID != g.GUID || needsDomain != domain {
			return "conflict"
		}
		if domain {
			return d.ExecuteAs(freshActor, op.ActorPassword, op, fresh, freshGroup)
		}
		switch op.Action {
		case "profile.update":
			return d.Profile(fresh, op.Profile)
		case "password.reset":
			return s.directory.ResetPassword(fresh, op.temporaryPassword, op.ForceChange)
		case "account.unlock":
			return s.directory.Unlock(fresh)
		case "groups.member.add":
			return s.directory.Member(freshGroup, fresh, true)
		case "groups.member.remove":
			return s.directory.Member(freshGroup, fresh, false)
		}
		return "denied"
	}, op.temporaryPassword, false)
}
