package main

import (
	"crypto/tls"
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf16"

	"github.com/go-ldap/ldap/v3"
)

type Directory interface {
	Person(string) (Person, error)
	Login(string) (Person, error)
	People() ([]Person, error)
	Group(string) (Group, error)
	ChangePassword(Person, string, string) string
	ResetPassword(Person, string, bool) string
	Unlock(Person) string
	Member(Group, Person, bool) string
}

type LDAPDirectory struct {
	config        Config
	tls           *tls.Config
	profileWrites sync.Mutex
}

func (d *LDAPDirectory) Probe() error {
	conn, err := d.read()
	if err != nil {
		return err
	}
	defer conn.Close()
	for _, base := range []string{d.config.UsersDN, d.config.GroupsDN} {
		result, err := conn.Search(ldap.NewSearchRequest(base, ldap.ScopeBaseObject, ldap.NeverDerefAliases, 1, 10, false, "(objectClass=*)", []string{"1.1"}, nil))
		if err != nil || len(result.Entries) != 1 {
			return errors.New("directory boundary unavailable")
		}
	}
	return nil
}

func (d *LDAPDirectory) dial(dn, password string) (*ldap.Conn, error) {
	u, err := url.Parse(d.config.LDAPURL)
	if err != nil || u.Scheme != "ldaps" {
		return nil, errDenied
	}
	conn, err := ldap.DialURL(d.config.LDAPURL, ldap.DialWithTLSConfig(d.tls), ldap.DialWithDialer(&net.Dialer{Timeout: 10 * time.Second}))
	if err != nil {
		return nil, errors.New("directory unavailable")
	}
	conn.SetTimeout(10 * time.Second)
	if err := conn.Bind(dn, password); err != nil {
		conn.Close()
		var e *ldap.Error
		if errors.As(err, &e) && e.ResultCode == ldap.LDAPResultInvalidCredentials {
			return nil, errDenied
		}
		return nil, errors.New("directory bind failed")
	}
	return conn, nil
}

func (d *LDAPDirectory) read() (*ldap.Conn, error) {
	return d.dial(d.config.LDAPBindDN, d.config.LDAPBindPassword)
}

func search(conn *ldap.Conn, base, filter string, limit int, attributes []string) ([]*ldap.Entry, error) {
	r, err := conn.Search(ldap.NewSearchRequest(base, ldap.ScopeWholeSubtree, ldap.NeverDerefAliases, limit, 10, false, filter, attributes, nil))
	if err != nil {
		return nil, errors.New("directory query incomplete")
	}
	return r.Entries, nil
}

func (d *LDAPDirectory) users(filter string) string {
	return "(&(objectClass=user)(!(objectClass=computer))(" + d.config.AnchorAttribute + "=*)" + filter + ")"
}

func (d *LDAPDirectory) attributes() []string {
	return []string{d.config.AnchorAttribute, "objectGUID", "objectSid", "sAMAccountName", "givenName", "sn", "displayName", "mail", "userAccountControl", "accountExpires", "pwdLastSet", "msDS-UserPasswordExpiryTimeComputed", "msDS-User-Account-Control-Computed", "lockoutTime", "primaryGroupID"}
}

func (d *LDAPDirectory) peopleBase() string {
	if d.config.Administration {
		return d.config.BaseDN
	}
	return d.config.UsersDN
}

func (d *LDAPDirectory) Person(anchor string) (Person, error) {
	if !anchorPattern.MatchString(anchor) {
		return Person{}, errDenied
	}
	return d.find("("+d.config.AnchorAttribute+"="+ldap.EscapeFilter(anchor)+")", anchor)
}

func (d *LDAPDirectory) Login(label string) (Person, error) {
	if len(label) < 1 || len(label) > 256 {
		return Person{}, errDenied
	}
	value := ldap.EscapeFilter(label)
	return d.find("(|(sAMAccountName="+value+")(userPrincipalName="+value+"))", "")
}

func (d *LDAPDirectory) find(filter, expected string) (Person, error) {
	conn, err := d.read()
	if err != nil {
		return Person{}, err
	}
	defer conn.Close()
	entries, err := search(conn, d.peopleBase(), d.users(filter), 2, d.attributes())
	if err != nil || len(entries) != 1 {
		return Person{}, errDenied
	}
	groups, err := d.groups(conn)
	if err != nil {
		return Person{}, err
	}
	p, err := d.person(entries[0], groups)
	if p.System {
		return Person{}, errDenied
	}
	if expected != "" && p.Anchor != expected {
		return Person{}, errDenied
	}
	return p, err
}

func (d *LDAPDirectory) People() ([]Person, error) {
	conn, err := d.read()
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	entries, err := search(conn, d.peopleBase(), d.users(""), 5000, d.attributes())
	if err != nil {
		return nil, err
	}
	groups, err := d.groups(conn)
	if err != nil {
		return nil, err
	}
	people := []Person{}
	seen := map[string]bool{}
	for _, e := range entries {
		p, err := d.person(e, groups)
		if err != nil || seen[p.Anchor] {
			return nil, errDenied
		}
		if p.System {
			continue
		}
		seen[p.Anchor] = true
		people = append(people, p)
	}
	sort.Slice(people, func(i, j int) bool { return people[i].Username < people[j].Username })
	return people, nil
}

func (d *LDAPDirectory) groups(conn *ldap.Conn) ([]Group, error) {
	entries, err := search(conn, d.config.BaseDN, "(objectClass=group)", 5000, []string{d.config.AnchorAttribute, "objectGUID", "objectSid", "cn", "member"})
	if err != nil {
		return nil, err
	}
	groups := []Group{}
	seenAnchors := map[string]bool{}
	for _, e := range entries {
		for _, a := range e.Attributes {
			if strings.HasPrefix(strings.ToLower(a.Name), "member;range=") {
				return nil, errors.New("directory group data incomplete")
			}
		}
		guid, err := guidString(e.GetRawAttributeValue("objectGUID"))
		if err != nil {
			return nil, errDenied
		}
		anchor := e.GetAttributeValue(d.config.AnchorAttribute)
		sid := sidString(e.GetRawAttributeValue("objectSid"))
		if sid == "" {
			return nil, errDenied
		}
		if anchor != "" {
			if !anchorPattern.MatchString(anchor) || seenAnchors[anchor] {
				return nil, errDenied
			}
			seenAnchors[anchor] = true
		}
		groups = append(groups, Group{Anchor: e.GetAttributeValue(d.config.AnchorAttribute), Name: e.GetAttributeValue("cn"), DN: e.DN, GUID: guid, SID: sid, Members: e.GetAttributeValues("member")})
	}
	return groups, nil
}

func equalDN(a, b string) bool {
	x, err := ldap.ParseDN(a)
	if err != nil {
		return false
	}
	y, err := ldap.ParseDN(b)
	return err == nil && x.EqualFold(y)
}

func underDN(dn, base string) bool {
	x, err := ldap.ParseDN(dn)
	if err != nil {
		return false
	}
	y, err := ldap.ParseDN(base)
	return err == nil && (y.EqualFold(x) || y.AncestorOfFold(x))
}

func closure(groups []Group, dn, primarySID string) []Group {
	groups = append([]Group(nil), groups...)
	for i := range groups {
		groups[i].Reference = groups[i].Anchor
		if groups[i].Reference == "" {
			groups[i].Reference = "sid:" + groups[i].SID
		}
	}
	result := []Group{}
	seen := map[string]bool{}
	for changed := true; changed; {
		changed = false
		for _, g := range groups {
			if seen[g.GUID] {
				continue
			}
			match := primarySID != "" && g.SID == primarySID
			g.Primary = match
			g.Direct = false
			g.Sources = []string{}
			for _, m := range g.Members {
				if equalDN(m, dn) {
					match = true
					g.Direct = true
				}
				for _, child := range result {
					if equalDN(m, child.DN) {
						match = true
						if child.Reference != "" {
							g.Sources = append(g.Sources, child.Reference)
						}
					}
				}
			}
			if match {
				seen[g.GUID] = true
				result = append(result, g)
				changed = true
			}
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Name < result[j].Name })
	for i := range result {
		result[i].Sources = []string{}
		for _, member := range result[i].Members {
			for _, child := range result {
				if child.Reference != "" && child.GUID != result[i].GUID && equalDN(member, child.DN) {
					result[i].Sources = append(result[i].Sources, child.Reference)
				}
			}
		}
	}
	return result
}

func (d *LDAPDirectory) privilegedGroup(g Group) bool {
	if has(d.config.ProtectedGroups, g.Anchor) || has(d.config.ProtectedGroupNames, g.Name) && underDN(g.DN, d.config.GroupsDN) {
		return true
	}
	for _, r := range d.config.Rules {
		if has(r.OperatorGroups, g.Anchor) {
			return true
		}
	}
	for _, rid := range []string{"512", "516", "518", "519", "520", "544", "548", "549", "550", "551", "552"} {
		if strings.HasSuffix(g.SID, "-"+rid) {
			return true
		}
	}
	return false
}

func (d *LDAPDirectory) protectedGroup(g Group) bool {
	if d.privilegedGroup(g) {
		return true
	}
	for _, r := range d.config.Rules {
		if has(r.TargetUserGroups, g.Anchor) {
			return true
		}
	}
	return false
}

func (d *LDAPDirectory) person(e *ldap.Entry, groups []Group) (Person, error) {
	anchor := e.GetAttributeValue(d.config.AnchorAttribute)
	guid, err := guidString(e.GetRawAttributeValue("objectGUID"))
	if err != nil || !anchorPattern.MatchString(anchor) {
		return Person{}, errDenied
	}
	p := Person{Anchor: anchor, Username: e.GetAttributeValue("sAMAccountName"), DisplayName: e.GetAttributeValue("displayName"), GivenName: e.GetAttributeValue("givenName"), Surname: e.GetAttributeValue("sn"), Email: e.GetAttributeValue("mail"), DN: e.DN, GUID: guid, SID: sidString(e.GetRawAttributeValue("objectSid")), Groups: []Group{}, KnownGroups: map[string]string{}}
	uac, err := strconv.ParseUint(e.GetAttributeValue("userAccountControl"), 10, 64)
	if err != nil || p.SID == "" {
		return Person{}, errDenied
	}
	if _, err := strconv.ParseUint(e.GetAttributeValue("accountExpires"), 10, 64); err != nil {
		return Person{}, errDenied
	}
	for _, name := range []string{"pwdLastSet", "primaryGroupID"} {
		if _, err := strconv.ParseUint(e.GetAttributeValue(name), 10, 64); err != nil {
			return Person{}, errDenied
		}
	}
	computed := uint64(0)
	if value := e.GetAttributeValue("msDS-User-Account-Control-Computed"); value != "" {
		computed, err = strconv.ParseUint(value, 10, 64)
		if err != nil {
			return Person{}, errDenied
		}
	}
	p.Disabled = uac&2 != 0
	p.Locked = computed&16 != 0
	if e.GetAttributeValue("msDS-User-Account-Control-Computed") == "" {
		p.Locked = e.GetAttributeValue("lockoutTime") != "" && e.GetAttributeValue("lockoutTime") != "0"
	}
	p.AccountExpired = expiredFiletime(e.GetAttributeValue("accountExpires"), time.Now())
	p.MustChangePassword = e.GetAttributeValue("pwdLastSet") == "0"
	p.PasswordStateKnown = p.MustChangePassword || uac&65536 != 0 || e.GetAttributeValue("msDS-User-Account-Control-Computed") != "" || e.GetAttributeValue("msDS-UserPasswordExpiryTimeComputed") != ""
	p.PasswordExpiresAt = filetime(e.GetAttributeValue("msDS-UserPasswordExpiryTimeComputed"))
	p.PasswordExpired = computed&0x800000 != 0 || (p.PasswordExpiresAt != nil && p.PasswordExpiresAt.Before(time.Now()))
	primarySID := ""
	if i := strings.LastIndex(p.SID, "-"); i > 0 {
		primarySID = p.SID[:i+1] + e.GetAttributeValue("primaryGroupID")
	}
	p.Groups = closure(groups, p.DN, primarySID)
	p.PrimaryGroupSID = primarySID
	p.System = d.config.ServiceAccountsDN != "" && underDN(p.DN, d.config.ServiceAccountsDN) || uac&0x3800 != 0 || strings.HasSuffix(p.SID, "-502") || strings.HasSuffix(p.SID, "-501")
	p.DomainRisk = strings.HasSuffix(p.SID, "-500")
	p.Protected = has(d.config.ProtectedUsers, p.Anchor) || strings.HasSuffix(p.SID, "-500")
	for _, g := range p.Groups {
		p.Administrator = p.Administrator || g.Anchor == d.config.AdminGroup && g.GUID == d.config.AdminGroupGUID && d.config.AdminGroupGUID != ""
		if domainGroup(g) {
			p.DomainRisk = true
		}
		if d.privilegedGroup(g) {
			p.Protected = true
		}
	}
	for _, g := range groups {
		if !underDN(g.DN, d.config.GroupsDN) {
			continue
		}
		if _, ok := p.KnownGroups[g.Name]; ok {
			p.KnownGroups[g.Name] = ""
		} else {
			p.KnownGroups[g.Name] = g.Anchor
		}
	}
	return p, nil
}

func (d *LDAPDirectory) Group(anchor string) (Group, error) {
	if !anchorPattern.MatchString(anchor) {
		return Group{}, errDenied
	}
	conn, err := d.read()
	if err != nil {
		return Group{}, err
	}
	defer conn.Close()
	groups, err := d.groups(conn)
	if err != nil {
		return Group{}, err
	}
	var found *Group
	for i := range groups {
		g := &groups[i]
		if (g.Anchor != "" && g.Anchor == anchor && underDN(g.DN, d.config.GroupsDN)) || (d.config.Administration && "sid:"+g.SID == anchor) {
			if found != nil {
				return Group{}, errDenied
			}
			found = g
		}
	}
	if found == nil {
		return Group{}, errDenied
	}
	result := *found
	result.Reference = result.Anchor
	if result.Reference == "" {
		result.Reference = "sid:" + result.SID
	}
	result.Protected = d.protectedGroup(result)
	for _, parent := range closure(groups, result.DN, "") {
		if d.protectedGroup(parent) {
			result.Protected = true
		}
	}
	return result, nil
}

func guidString(b []byte) (string, error) {
	if len(b) != 16 {
		return "", errDenied
	}
	return fmt.Sprintf("%08x-%04x-%04x-%x-%x", binary.LittleEndian.Uint32(b[0:4]), binary.LittleEndian.Uint16(b[4:6]), binary.LittleEndian.Uint16(b[6:8]), b[8:10], b[10:16]), nil
}

func sidString(b []byte) string {
	if len(b) < 8 || len(b) != 8+int(b[1])*4 {
		return ""
	}
	authority := uint64(0)
	for _, v := range b[2:8] {
		authority = authority<<8 | uint64(v)
	}
	s := fmt.Sprintf("S-%d-%d", b[0], authority)
	for i := 0; i < int(b[1]); i++ {
		s += fmt.Sprintf("-%d", binary.LittleEndian.Uint32(b[8+4*i:12+4*i]))
	}
	return s
}

func filetime(s string) *time.Time {
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil || n <= 0 || n == 9223372036854775807 {
		return nil
	}
	t := time.Unix(n/10000000-11644473600, (n%10000000)*100).UTC()
	return &t
}
func expiredFiletime(s string, now time.Time) bool {
	t := filetime(s)
	return t != nil && t.Before(now)
}

func passwordValue(password string) string {
	units := utf16.Encode([]rune("\"" + password + "\""))
	b := make([]byte, len(units)*2)
	for i, v := range units {
		binary.LittleEndian.PutUint16(b[i*2:], v)
	}
	return string(b)
}

func writeResult(err error) string {
	if err == nil {
		return "saved"
	}
	var e *ldap.Error
	if errors.As(err, &e) && e.ResultCode == ldap.LDAPResultInsufficientAccessRights {
		return "denied"
	}
	if errors.As(err, &e) && e.ResultCode > 0 && e.ResultCode < ldap.ErrorNetwork {
		return "rejected"
	}
	return "unknown"
}

func (d *LDAPDirectory) modify(dn, password, guid string, changes func(*ldap.ModifyRequest)) string {
	if dn == "" || password == "" {
		return "unavailable"
	}
	conn, err := d.dial(dn, password)
	if err != nil {
		return "unavailable"
	}
	defer conn.Close()
	req := ldap.NewModifyRequest("<GUID="+guid+">", nil)
	changes(req)
	return modificationResult(conn.Modify(req), req)
}

func modificationResult(err error, req *ldap.ModifyRequest) string {
	for _, change := range req.Changes {
		if change.Modification.Type != "unicodePwd" {
			continue
		}
		var e *ldap.Error
		if errors.As(err, &e) {
			if e.ResultCode == ldap.LDAPResultConstraintViolation {
				return "password_policy_rejected"
			}
			if e.ResultCode == ldap.LDAPResultInvalidCredentials {
				return "credentials_rejected"
			}
		}
	}
	return writeResult(err)
}

func (d *LDAPDirectory) ChangePassword(p Person, oldPassword, newPassword string) string {
	return d.modify(d.config.LDAPBindDN, d.config.LDAPBindPassword, p.GUID, func(r *ldap.ModifyRequest) {
		r.Delete("unicodePwd", []string{passwordValue(oldPassword)})
		r.Add("unicodePwd", []string{passwordValue(newPassword)})
	})
}
func (d *LDAPDirectory) ResetPassword(p Person, password string, force bool) string {
	return d.modify(d.config.PasswordBindDN, d.config.PasswordBindPassword, p.GUID, func(r *ldap.ModifyRequest) {
		r.Replace("unicodePwd", []string{passwordValue(password)})
		if force {
			r.Replace("pwdLastSet", []string{"0"})
		}
	})
}
func (d *LDAPDirectory) Unlock(p Person) string {
	return d.modify(d.config.PasswordBindDN, d.config.PasswordBindPassword, p.GUID, func(r *ldap.ModifyRequest) { r.Replace("lockoutTime", []string{"0"}) })
}
func (d *LDAPDirectory) Member(g Group, p Person, add bool) string {
	return d.modify(d.config.GroupBindDN, d.config.GroupBindPassword, g.GUID, func(r *ldap.ModifyRequest) {
		member := []string{"<GUID=" + p.GUID + ">"}
		if add {
			r.Add("member", member)
		} else {
			r.Delete("member", member)
		}
	})
}
