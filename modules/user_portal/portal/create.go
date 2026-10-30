package main

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"strings"

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
	if !s.recentSession(v) {
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
		if err != nil || fresh.GUID != actor.GUID || !fresh.canAuthenticate(v.Authentication) || !s.administrator(fresh) {
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
	conn, err := d.dial(d.config.CreateBindDN, d.config.CreateBindPassword)
	if err != nil {
		return "unavailable"
	}
	defer conn.Close()
	return d.createWithMail(conn, op, anchor, binary, password, address, upn)
}

func creationIntent(op NewAccount, peopleDN, upn string) directorymail.AccountIntent {
	profile, _ := json.Marshal(op.Profile)
	digest := sha256.Sum256(profile)
	return directorymail.AccountIntent{Username: op.Username, UPN: upn, PersonDN: "CN=" + ldap.EscapeDN(op.Username) + "," + peopleDN, ProfileHash: hex.EncodeToString(digest[:]), ForceChange: op.ForceChange}
}

// Includes unanchored/legacy addresses. Only an exact same-person result may be
// reconciled, and only after the mail operation proves creation was started.
// The address DN reservation supplies controlled-writer concurrency safety;
// this preflight also detects direct/out-of-band AD writes, without claiming a
// cross-object transaction against arbitrary domain administrators.
func (d *LDAPDirectory) creationPerson(conn directorymail.ResourceLDAP, op NewAccount, anchor string, binary []byte, address, upn string) (*ldap.Entry, string) {
	filter := "(|(sAMAccountName=" + ldap.EscapeFilter(op.Username) + ")(userPrincipalName=" + ldap.EscapeFilter(upn) + ")(mail=" + ldap.EscapeFilter(address) + ")(proxyAddresses=" + ldap.EscapeFilter("smtp:"+address) + "))"
	attrs := []string{"objectClass", "objectGUID", "mS-DS-ConsistencyGuid", d.config.AnchorAttribute, "sAMAccountName", "userPrincipalName", "mail", "userAccountControl", "givenName", "sn", "displayName"}
	result, err := conn.Search(ldap.NewSearchRequest(d.config.BaseDN, ldap.ScopeWholeSubtree, ldap.NeverDerefAliases, 2, 10, false, filter, attrs, directorymail.DomainSearchControls()))
	if ldap.IsErrorWithCode(err, ldap.LDAPResultSizeLimitExceeded) {
		return nil, "conflict"
	}
	if err != nil || result == nil || len(result.Referrals) != 0 {
		return nil, "unavailable"
	}
	if len(result.Entries) == 0 {
		return nil, ""
	}
	if len(result.Entries) != 1 || !d.createdPersonMatches(result.Entries[0], op, anchor, binary, address, upn) {
		return nil, "conflict"
	}
	return result.Entries[0], ""
}

func (d *LDAPDirectory) createdPersonMatches(e *ldap.Entry, op NewAccount, anchor string, binary []byte, address, upn string) bool {
	person, computer := false, false
	for _, class := range e.GetAttributeValues("objectClass") {
		person = person || strings.EqualFold(class, "user")
		computer = computer || strings.EqualFold(class, "computer")
	}
	guid, err := guidString(e.GetRawAttributeValue("objectGUID"))
	if err != nil || guid == "" || !person || computer || !equalDN(e.DN, creationIntent(op, d.config.UsersDN, upn).PersonDN) {
		return false
	}
	for name, expected := range map[string]string{d.config.AnchorAttribute: anchor, "mS-DS-ConsistencyGuid": string(binary), "sAMAccountName": op.Username, "userPrincipalName": upn, "mail": address, "userAccountControl": "512", "displayName": op.Profile.DisplayName, "givenName": op.Profile.GivenName, "sn": op.Profile.Surname} {
		values := e.GetRawAttributeValues(name)
		if expected == "" && len(values) == 0 {
			continue
		}
		if len(values) != 1 || string(values[0]) != expected {
			return false
		}
	}
	return true
}

// No password is persisted in AD resource intent, and a reconciled/unknown User
// Add never returns a newly generated password as if it were the original one.
func (d *LDAPDirectory) createWithMail(conn directorymail.ResourceLDAP, op NewAccount, anchor string, binary []byte, password, address, upn string) string {
	person, result := d.creationPerson(conn, op, anchor, binary, address, upn)
	if result != "" {
		return result
	}
	resources, err := directorymail.NewPrimaryDirectory(conn, d.config.BaseDN)
	if err != nil {
		return "unavailable"
	}
	registration, err := resources.Reserve(anchor, address, creationIntent(op, d.config.UsersDN, upn))
	if err != nil {
		if errors.Is(err, directorymail.ErrConflict) {
			return "conflict"
		}
		return "unavailable"
	}
	if person != nil {
		if registration.Stage == "reserved" {
			return "conflict"
		}
		return finishPrimaryCreation(resources, registration, person, false)
	}
	if registration.Stage != "reserved" {
		// An interrupted User Add with no current person is ambiguous. Do not
		// recreate a deleted person with the old stable identity or release it.
		return "unknown"
	}
	// Repeat the legacy preflight after reservation, before electing a creator.
	if person, result = d.creationPerson(conn, op, anchor, binary, address, upn); result != "" || person != nil {
		if result == "" {
			result = "conflict"
		}
		return result
	}
	registration, err = resources.StartCreation(registration)
	if err != nil {
		return "unknown"
	}
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
	addErr := conn.Add(req)
	person, result = d.creationPerson(conn, op, anchor, binary, address, upn)
	if result != "" || person == nil {
		// Even a timeout or a definitive rejection leaves durable pending intent.
		// Nothing is rolled back by deleting an address or another user's data.
		return "unknown"
	}
	return finishPrimaryCreation(resources, registration, person, addErr == nil)
}

func finishPrimaryCreation(resources *directorymail.PrimaryDirectory, registration directorymail.PrimaryRegistration, person *ldap.Entry, acknowledged bool) string {
	guid, err := guidString(person.GetRawAttributeValue("objectGUID"))
	if err == nil {
		registration, err = resources.ConfirmAccount(registration, guid)
	}
	if err == nil {
		_, err = resources.Complete(registration)
	}
	if err != nil || !acknowledged {
		return "unknown"
	}
	return "saved"
}
