package main

import (
	"crypto/rand"
	"errors"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/anas-project/ANAS/internal/directorymail"
	"github.com/go-ldap/ldap/v3"
)

type NewAccount struct {
	Username string  `json:"username"`
	Profile  Profile `json:"profile"`

	ForceChange bool `json:"force_change"`
}

type accountCreator interface {
	Create(NewAccount, string, []byte, string) string
}

var newUsername = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9._-]{0,19}$`)

func validUserBase(base, users string) bool {
	parsed, err := ldap.ParseDN(users)
	return base != "" && err == nil && len(parsed.RDNs) > 0 && len(parsed.RDNs[0].Attributes) == 1 && strings.EqualFold(parsed.RDNs[0].Attributes[0].Type, "OU") && underDN(users, base) && !equalDN(users, base)
}

func (s *Server) managedPerson(p Person) bool {
	return !p.System && underDN(p.DN, s.config.UsersDN)
}

func (s *Server) creationEnabled() bool {
	_, supported := s.directory.(accountCreator)
	return s.config.Administration && supported && s.config.CreateBindDN != "" && s.config.CreateBindPassword != "" && s.config.MailDomain != "" && s.config.UPNSuffix != ""
}

func (s *Server) createUser(w http.ResponseWriter, r *http.Request) {
	s.writes.Lock()
	defer s.writes.Unlock()
	v, actor, _, ok := s.current(w, r)
	if !ok {
		return
	}
	if !s.csrf(r, v) {
		s.rejected(actor.Anchor, "", "user.create", "")
		failure(w, 403, "csrf_rejected")
		return
	}
	if !s.administrator(actor) || !s.creationEnabled() {
		s.rejected(actor.Anchor, "", "user.create", "")
		failure(w, 403, "access_denied")
		return
	}
	op := NewAccount{ForceChange: true}
	if !decode(r, &op) || !op.valid(s.config.MailDomain) {
		s.rejected(actor.Anchor, "", "user.create", "platform_admin")
		failure(w, 400, "invalid_request")
		return
	}
	if !recent(v.AuthTime, s.config.RecentWindow, time.Now()) {
		s.rejected(actor.Anchor, "", "user.create", "platform_admin")
		failure(w, 403, "recent_authentication_required")
		return
	}
	if !s.passwordAttempt(r, actor.Anchor) || !s.limiter.allowIdentity(actor.Anchor) {
		s.rejected(actor.Anchor, "", "user.create", "platform_admin")
		failure(w, 429, "rate_limited")
		return
	}
	binary := make([]byte, 16)
	if _, err := rand.Read(binary); err != nil {
		failure(w, 503, "identity_unavailable")
		return
	}
	anchor, _ := guidString(binary)
	password := "Aa1!" + randomID()[:28]
	s.write(w, r, Record{Actor: actor.Anchor, Target: anchor, Action: "user.create", Rule: "platform_admin", ForceChange: &op.ForceChange}, func() string {
		if !s.validWriteSession(r.Context(), v.ID, actor.Anchor, true) {
			return "denied"
		}
		fresh, err := s.directory.Person(actor.Anchor)
		if err != nil || fresh.GUID != actor.GUID || !fresh.canLogin() || !s.administrator(fresh) {
			return "denied"
		}
		return s.directory.(accountCreator).Create(op, anchor, binary, password)
	}, password, false)
}

func (op NewAccount) valid(domain string) bool {
	_, err := directorymail.Address(op.Username, domain)
	return err == nil && newUsername.MatchString(op.Username) && op.Profile.valid()
}

func (d *LDAPDirectory) Create(op NewAccount, anchor string, binary []byte, password string) string {
	if !op.valid(d.config.MailDomain) || len(binary) != 16 || d.config.CreateBindDN == "" || d.config.CreateBindPassword == "" || !validUserBase(d.config.BaseDN, d.config.UsersDN) {
		return "denied"
	}
	wantAnchor, _ := guidString(binary)
	if anchor != wantAnchor || password == "" {
		return "denied"
	}
	address, _ := directorymail.Address(op.Username, d.config.MailDomain)
	upn := op.Username + "@" + d.config.UPNSuffix
	reader, err := d.read()
	if err != nil {
		return "unavailable"
	}
	// This bounded preflight covers unanchored entries too. AD itself enforces
	// account-name/DN uniqueness on Add. External directory writers do not share
	// the portal's mutex or a global mail uniqueness constraint.
	filter := "(|(sAMAccountName=" + ldap.EscapeFilter(op.Username) + ")(userPrincipalName=" + ldap.EscapeFilter(upn) + ")(mail=" + ldap.EscapeFilter(address) + ")(proxyAddresses=" + ldap.EscapeFilter("smtp:"+address) + "))"
	result, err := reader.Search(ldap.NewSearchRequest(d.config.BaseDN, ldap.ScopeWholeSubtree, ldap.NeverDerefAliases, 1, 10, false, filter, []string{"1.1"}, nil))
	reader.Close()
	var collision *ldap.Error
	if errors.As(err, &collision) && collision.ResultCode == ldap.LDAPResultSizeLimitExceeded {
		return "conflict"
	}
	if err != nil {
		return "unavailable"
	}
	if len(result.Entries) != 0 {
		return "conflict"
	}
	conn, err := d.dial(d.config.CreateBindDN, d.config.CreateBindPassword)
	if err != nil {
		return "unavailable"
	}
	defer conn.Close()
	req := ldap.NewAddRequest("CN="+ldap.EscapeDN(op.Username)+","+d.config.UsersDN, nil)
	for name, values := range map[string][]string{
		"objectClass": {"top", "person", "organizationalPerson", "user"},
		"cn":          {op.Username}, "sAMAccountName": {op.Username}, "userPrincipalName": {upn},
		"displayName": {op.Profile.DisplayName}, "mail": {address}, "userAccountControl": {"512"},
		"unicodePwd": {passwordValue(password)}, "mS-DS-ConsistencyGuid": {string(binary)}, d.config.AnchorAttribute: {anchor},
	} {
		req.Attribute(name, values)
	}
	if op.Profile.GivenName != "" {
		req.Attribute("givenName", []string{op.Profile.GivenName})
	}
	if op.Profile.Surname != "" {
		req.Attribute("sn", []string{op.Profile.Surname})
	}
	if op.ForceChange {
		req.Attribute("pwdLastSet", []string{"0"})
	}
	err = conn.Add(req)
	var ldapError *ldap.Error
	if errors.As(err, &ldapError) && ldapError.ResultCode == ldap.LDAPResultEntryAlreadyExists {
		return "conflict"
	}
	return writeResult(err)
}
