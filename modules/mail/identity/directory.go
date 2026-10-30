package identity

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/binary"
	"errors"
	"net"
	"net/mail"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/anas-project/ANAS/internal/directorymail"
	"github.com/go-ldap/ldap/v3"
)

var (
	ErrDirectoryUnavailable = errors.New("mail directory unavailable")
	ErrAdmission            = errors.New("mail directory admission denied")
)

type Group struct{ DN, Anchor string }
type DirectoryConfig struct {
	URL, BindDN, BindPassword, BaseDN, UsersDN, MailDomain string
	GroupsDN                                               string
	AllowGroups                                            []Group
}

// SMTPAddresses are directory facts only. proxyAddresses does not describe
// which independent mailbox owns each address; callers must not infer that all
// addresses belong to the primary mailbox or expose them as a public directory.
type Person struct {
	Anchor, Username, DisplayName, Mail string
	SMTPAddresses                       []string
	Groups                              []Group
}

type searchConnection interface {
	Search(*ldap.SearchRequest) (*ldap.SearchResult, error)
	Close() error
}

type Directory struct {
	config DirectoryConfig
	dial   func(context.Context) (searchConnection, error)
}

func withinDN(value, base string) bool {
	d, err := ldap.ParseDN(value)
	if err != nil {
		return false
	}
	b, err := ldap.ParseDN(base)
	return err == nil && (b.EqualFold(d) || b.AncestorOfFold(d))
}

func NewDirectory(cfg DirectoryConfig, trust *tls.Config) (*Directory, error) {
	return newDirectory(cfg, trust, true)
}

// ValidateDirectoryEndpoint checks local transport and directory bounds before
// startup needs to resolve the durable admission-group anchors over LDAP.
func ValidateDirectoryEndpoint(cfg DirectoryConfig, trust *tls.Config) error {
	_, err := newDirectory(cfg, trust, false)
	return err
}

func newDirectory(cfg DirectoryConfig, trust *tls.Config, requireGroups bool) (*Directory, error) {
	u, err := url.Parse(cfg.URL)
	if err != nil || u.Scheme != "ldaps" || u.Hostname() == "" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || cfg.BindPassword == "" || strings.ContainsAny(cfg.BindPassword, "\x00\r\n") {
		return nil, ErrDirectoryUnavailable
	}
	for _, dn := range []string{cfg.BaseDN, cfg.UsersDN, cfg.BindDN} {
		parsed, e := ldap.ParseDN(dn)
		if e != nil || len(parsed.RDNs) == 0 || !withinDN(dn, cfg.BaseDN) {
			return nil, ErrDirectoryUnavailable
		}
	}
	if sameDN(cfg.UsersDN, cfg.BaseDN) || requireGroups && len(cfg.AllowGroups) == 0 || len(cfg.AllowGroups) > 16 {
		return nil, ErrDirectoryUnavailable
	}
	if cfg.GroupsDN != "" && (!withinDN(cfg.GroupsDN, cfg.BaseDN) || sameDN(cfg.GroupsDN, cfg.BaseDN)) {
		return nil, ErrDirectoryUnavailable
	}
	seen := map[string]bool{}
	for _, g := range cfg.AllowGroups {
		if !anchorPattern.MatchString(g.Anchor) || !withinDN(g.DN, cfg.BaseDN) || seen[g.Anchor] {
			return nil, ErrDirectoryUnavailable
		}
		seen[g.Anchor] = true
	}
	domain, e := directorymail.Domain(cfg.MailDomain, "")
	if e != nil {
		return nil, ErrDirectoryUnavailable
	}
	cfg.MailDomain = domain
	cfg.AllowGroups = append([]Group(nil), cfg.AllowGroups...)
	if trust == nil {
		trust = &tls.Config{}
	}
	trust = trust.Clone()
	if trust.InsecureSkipVerify || trust.VerifyPeerCertificate != nil || trust.VerifyConnection != nil {
		return nil, ErrDirectoryUnavailable
	}
	if trust.ServerName == "" {
		trust.ServerName = u.Hostname()
	}
	if trust.MinVersion < tls.VersionTLS12 {
		trust.MinVersion = tls.VersionTLS12
	}
	address := net.JoinHostPort(u.Hostname(), "636")
	if u.Port() != "" {
		address = u.Host
	}
	d := &Directory{config: cfg}
	d.dial = func(ctx context.Context) (searchConnection, error) {
		dialer := tls.Dialer{NetDialer: &net.Dialer{Timeout: 5 * time.Second}, Config: trust}
		raw, e := dialer.DialContext(ctx, "tcp", address)
		if e != nil {
			return nil, ErrDirectoryUnavailable
		}
		conn := ldap.NewConn(raw, true)
		conn.SetTimeout(5 * time.Second)
		conn.Start()
		stop := context.AfterFunc(ctx, func() { conn.Close() })
		e = conn.Bind(cfg.BindDN, cfg.BindPassword)
		stop()
		if e != nil || ctx.Err() != nil {
			conn.Close()
			return nil, ErrDirectoryUnavailable
		}
		return conn, nil
	}
	return d, nil
}

var personAttributes = []string{"anasIdentityAnchor", "sAMAccountName", "displayName", "mail", "proxyAddresses", "objectSid", "userAccountControl", "accountExpires", "pwdLastSet", "primaryGroupID", "msDS-User-Account-Control-Computed", "msDS-UserPasswordExpiryTimeComputed", "lockoutTime"}

// Person performs a fresh read using only the service bind. There are no AD
// add/modify/delete, personnel-password bind, username or email lookup methods.
func (d *Directory) Person(ctx context.Context, anchor string) (Person, error) {
	return d.lookupPerson(ctx, anchor, true)
}

// Configuration facts do not authorize login. They let a disabled person's
// explicitly owned mailbox keep receiving without weakening Person admission.
func (d *Directory) lookupPerson(ctx context.Context, anchor string, admit bool) (Person, error) {
	if !anchorPattern.MatchString(anchor) {
		return Person{}, ErrAdmission
	}
	conn, err := d.dial(ctx)
	if err != nil {
		return Person{}, ErrDirectoryUnavailable
	}
	defer conn.Close()
	stop := context.AfterFunc(ctx, func() { conn.Close() })
	defer stop()
	filter := "(&(objectClass=user)(!(objectClass=computer))(anasIdentityAnchor=" + ldap.EscapeFilter(anchor) + "))"
	result, err := conn.Search(ldap.NewSearchRequest(d.config.UsersDN, ldap.ScopeWholeSubtree, ldap.NeverDerefAliases, 2, 5, false, filter, personAttributes, nil))
	if err != nil || result == nil || len(result.Referrals) != 0 {
		return Person{}, ErrDirectoryUnavailable
	}
	if len(result.Entries) != 1 {
		return Person{}, ErrAdmission
	}
	entry := result.Entries[0]
	p, primarySID, err := d.personRecord(entry, anchor, time.Now(), admit)
	if err != nil {
		return Person{}, err
	}
	if !admit {
		if ctx.Err() != nil {
			return Person{}, ErrDirectoryUnavailable
		}
		return p, nil
	}
	for _, g := range d.config.AllowGroups {
		// Resolve the immutable group anchor independently of membership. A
		// renamed group stays the same role, while duplicate anchors cannot
		// disappear behind a membership filter and authorize the wrong group.
		identityFilter := "(&(objectClass=group)(anasIdentityAnchor=" + ldap.EscapeFilter(g.Anchor) + "))"
		groups, lookupErr := conn.Search(ldap.NewSearchRequest(d.config.BaseDN, ldap.ScopeWholeSubtree, ldap.NeverDerefAliases, 2, 5, false, identityFilter, []string{"anasIdentityAnchor"}, directorymail.DomainSearchControls()))
		if lookupErr != nil || groups == nil || len(groups.Referrals) != 0 {
			return Person{}, ErrDirectoryUnavailable
		}
		if len(groups.Entries) > 1 {
			return Person{}, ErrAdmission
		}
		if len(groups.Entries) == 0 {
			continue
		}
		actual, lookupErr := single(groups.Entries[0], "anasIdentityAnchor", true)
		if lookupErr != nil || actual != g.Anchor || !withinDN(groups.Entries[0].DN, d.config.BaseDN) {
			return Person{}, ErrAdmission
		}
		currentDN := groups.Entries[0].DN
		// AD's matching-rule-in-chain covers nested membership. AD primary
		// groups are checked separately because member omits primary members.
		filter = "(&(objectClass=group)(anasIdentityAnchor=" + ldap.EscapeFilter(g.Anchor) + ")(|(member:1.2.840.113556.1.4.1941:=" + ldap.EscapeFilter(entry.DN) + ")(objectSid=" + ldap.EscapeFilter(string(primarySID)) + ")))"
		r, e := conn.Search(ldap.NewSearchRequest(currentDN, ldap.ScopeBaseObject, ldap.NeverDerefAliases, 2, 5, false, filter, []string{"anasIdentityAnchor"}, nil))
		if e != nil || r == nil || len(r.Referrals) != 0 {
			return Person{}, ErrDirectoryUnavailable
		}
		if len(r.Entries) > 1 {
			return Person{}, ErrAdmission
		}
		if len(r.Entries) == 1 {
			actual, e := single(r.Entries[0], "anasIdentityAnchor", true)
			if e != nil || actual != g.Anchor || !sameDN(r.Entries[0].DN, currentDN) {
				return Person{}, ErrAdmission
			}
			p.Groups = append(p.Groups, Group{DN: r.Entries[0].DN, Anchor: g.Anchor})
		}
	}
	if len(p.Groups) == 0 {
		return Person{}, ErrAdmission
	}
	if ctx.Err() != nil {
		return Person{}, ErrDirectoryUnavailable
	}
	return p, nil
}

func sameDN(a, b string) bool { return withinDN(a, b) && withinDN(b, a) }

func single(e *ldap.Entry, name string, required bool) (string, error) {
	values := e.GetAttributeValues(name)
	if len(values) == 0 && !required {
		return "", nil
	}
	if len(values) != 1 || values[0] == "" || strings.ContainsAny(values[0], "\x00\r\n") {
		return "", ErrAdmission
	}
	return values[0], nil
}

func number(e *ldap.Entry, name string, required bool) (uint64, error) {
	value, err := single(e, name, required)
	if err != nil {
		return 0, err
	}
	if value == "" && !required {
		return 0, nil
	}
	n, err := strconv.ParseUint(value, 10, 64)
	if err != nil {
		return 0, ErrAdmission
	}
	return n, nil
}

func smtpAddress(raw string) (string, error) {
	if len(raw) > 254 || strings.ContainsAny(raw, "\x00\r\n") {
		return "", ErrAdmission
	}
	for _, r := range raw {
		if r > 127 {
			return "", ErrAdmission
		}
	}
	a, err := mail.ParseAddress(raw)
	parts := strings.Split(raw, "@")
	if err != nil || a.Name != "" || a.Address != raw || len(parts) != 2 || strings.Contains(parts[0], "\"") {
		return "", ErrAdmission
	}
	domain, err := directorymail.Domain(parts[1], "")
	if err != nil {
		return "", ErrAdmission
	}
	return strings.ToLower(parts[0]) + "@" + domain, nil
}

func (d *Directory) person(e *ldap.Entry, expected string, now time.Time) (Person, []byte, error) {
	return d.personRecord(e, expected, now, true)
}
func (d *Directory) personRecord(e *ldap.Entry, expected string, now time.Time, admit bool) (Person, []byte, error) {
	p := Person{}
	if e == nil || !withinDN(e.DN, d.config.UsersDN) {
		return p, nil, ErrAdmission
	}
	var err error
	if p.Anchor, err = single(e, "anasIdentityAnchor", true); err != nil || p.Anchor != expected {
		return Person{}, nil, ErrAdmission
	}
	if p.Username, err = single(e, "sAMAccountName", true); err != nil || strings.HasSuffix(p.Username, "$") {
		return Person{}, nil, ErrAdmission
	}
	if p.DisplayName, err = single(e, "displayName", false); err != nil {
		return Person{}, nil, ErrAdmission
	}
	if p.DisplayName == "" {
		p.DisplayName = p.Username
	}
	value, err := single(e, "mail", true)
	if err != nil {
		return Person{}, nil, ErrAdmission
	}
	if p.Mail, err = smtpAddress(value); err != nil || !strings.HasSuffix(p.Mail, "@"+d.config.MailDomain) {
		return Person{}, nil, ErrAdmission
	}
	uac, err := number(e, "userAccountControl", true)
	if err != nil || uac&512 == 0 || uac&0x3800 != 0 || admit && uac&2 != 0 {
		return Person{}, nil, ErrAdmission
	}
	if admit {
		expires, err := number(e, "accountExpires", true)
		if err != nil || (expires != 0 && expires != 0x7fffffffffffffff && expires <= uint64(now.Unix()+11644473600)*10000000) || expires > 0x7fffffffffffffff {
			return Person{}, nil, ErrAdmission
		}
		pwd, err := number(e, "pwdLastSet", true)
		if err != nil || pwd == 0 {
			return Person{}, nil, ErrAdmission
		}
		computed, err := number(e, "msDS-User-Account-Control-Computed", false)
		if err != nil || computed&(16|0x800000) != 0 {
			return Person{}, nil, ErrAdmission
		}
		if len(e.GetAttributeValues("msDS-User-Account-Control-Computed")) == 0 {
			locked, lockErr := number(e, "lockoutTime", true)
			if lockErr != nil || locked != 0 {
				return Person{}, nil, ErrAdmission
			}
			// A known unlocked state alone says nothing about password expiry.
			if uac&65536 == 0 && len(e.GetAttributeValues("msDS-UserPasswordExpiryTimeComputed")) == 0 {
				return Person{}, nil, ErrAdmission
			}
		}
		if len(e.GetAttributeValues("msDS-UserPasswordExpiryTimeComputed")) != 0 {
			expiry, e := number(e, "msDS-UserPasswordExpiryTimeComputed", true)
			if e != nil || expiry > 0x7fffffffffffffff || (expiry != 0x7fffffffffffffff && expiry <= uint64(now.Unix()+11644473600)*10000000) {
				return Person{}, nil, ErrAdmission
			}
		}
	}
	sids := e.GetRawAttributeValues("objectSid")
	// Only domain personnel SIDs (S-1-5-21-a-b-c-rid) qualify. Built-in,
	// service/trust/computer accounts cannot become a personnel mail session.
	if len(sids) != 1 || len(sids[0]) != 28 || sids[0][0] != 1 || sids[0][1] != 5 || !bytes.Equal(sids[0][2:8], []byte{0, 0, 0, 0, 0, 5}) || binary.LittleEndian.Uint32(sids[0][8:12]) != 21 {
		return Person{}, nil, ErrAdmission
	}
	rid := binary.LittleEndian.Uint32(sids[0][24:28])
	if rid <= 502 {
		return Person{}, nil, ErrAdmission
	}
	primary, err := number(e, "primaryGroupID", true)
	if err != nil || primary == 0 || primary > 0xffffffff {
		return Person{}, nil, ErrAdmission
	}
	primarySID := append([]byte(nil), sids[0]...)
	binary.LittleEndian.PutUint32(primarySID[24:28], uint32(primary))
	addresses := map[string]bool{p.Mail: true}
	for _, value := range e.GetAttributeValues("proxyAddresses") {
		if len(value) < 5 || !strings.EqualFold(value[:5], "smtp:") {
			continue
		}
		address, err := smtpAddress(value[5:])
		if err != nil {
			return Person{}, nil, ErrAdmission
		}
		addresses[address] = true
	}
	for value := range addresses {
		p.SMTPAddresses = append(p.SMTPAddresses, value)
	}
	sort.Strings(p.SMTPAddresses)
	return p, primarySID, nil
}
