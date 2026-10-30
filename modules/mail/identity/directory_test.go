package identity

import (
	"context"
	"crypto/tls"
	"encoding/binary"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/anas-project/ANAS/internal/directorymail"
	"github.com/go-ldap/ldap/v3"
)

const groupAnchor = "66666666-7777-8888-9999-aaaaaaaaaaaa"

func directoryConfig() DirectoryConfig {
	return DirectoryConfig{URL: "ldaps://dc.example.test:636", BindDN: "CN=reader,OU=Services,DC=example,DC=test", BindPassword: "synthetic-service-secret", BaseDN: "DC=example,DC=test", UsersDN: "OU=People,DC=example,DC=test", MailDomain: "example.test", AllowGroups: []Group{{DN: "CN=APP_mail,OU=Groups,DC=example,DC=test", Anchor: groupAnchor}}}
}

func personEntry() *ldap.Entry {
	sid := []byte{1, 5, 0, 0, 0, 0, 0, 5}
	for _, n := range []uint32{21, 1, 2, 3, 1100} {
		sid = binary.LittleEndian.AppendUint32(sid, n)
	}
	return ldap.NewEntry("CN=Alice*(test),OU=People,DC=example,DC=test", map[string][]string{
		"anasIdentityAnchor": {testAnchor}, "sAMAccountName": {"alice"}, "displayName": {"Alice"},
		"mail": {"Alice@EXAMPLE.TEST"}, "proxyAddresses": {"SMTP:alice@example.test", "smtp:support+team@example.test", "smtp:separate-box@example.test", "X500:unmanaged-value"},
		"objectSid": {string(sid)}, "userAccountControl": {"512"}, "accountExpires": {"0"}, "pwdLastSet": {"133000000000000000"}, "primaryGroupID": {"513"}, "msDS-User-Account-Control-Computed": {"0"}, "lockoutTime": {"0"},
	})
}

type readFixture struct {
	person  *ldap.SearchResult
	group   *ldap.SearchResult
	err     error
	queries []*ldap.SearchRequest
}

func (f *readFixture) Close() error { return nil }
func (f *readFixture) Search(r *ldap.SearchRequest) (*ldap.SearchResult, error) {
	f.queries = append(f.queries, r)
	if _, err := ldap.CompileFilter(r.Filter); err != nil {
		return nil, err
	}
	if f.err != nil {
		return nil, f.err
	}
	if r.Scope == ldap.ScopeWholeSubtree && !strings.Contains(r.Filter, "(objectClass=group)") {
		return f.person, nil
	}
	return f.group, nil
}

func directoryFixture(t *testing.T) (*Directory, *readFixture) {
	t.Helper()
	cfg := directoryConfig()
	d, err := NewDirectory(cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	f := &readFixture{person: &ldap.SearchResult{Entries: []*ldap.Entry{personEntry()}}, group: &ldap.SearchResult{Entries: []*ldap.Entry{ldap.NewEntry(cfg.AllowGroups[0].DN, map[string][]string{"anasIdentityAnchor": {groupAnchor}})}}}
	d.dial = func(context.Context) (searchConnection, error) { return f, nil }
	return d, f
}

func TestReadOnlyDirectoryAdmissionAndAddressFacts(t *testing.T) {
	d, f := directoryFixture(t)
	p, err := d.Person(t.Context(), testAnchor)
	if err != nil || p.Anchor != testAnchor || p.Username != "alice" || p.Mail != "alice@example.test" || len(p.Groups) != 1 || p.Groups[0].Anchor != groupAnchor {
		t.Fatal("directory admission failed", err)
	}
	if strings.Join(p.SMTPAddresses, ",") != "alice@example.test,separate-box@example.test,support+team@example.test" {
		t.Fatal("directory SMTP facts lost or incorrectly assigned", p.SMTPAddresses)
	}
	if len(f.queries) != 3 || f.queries[0].BaseDN != d.config.UsersDN || f.queries[0].SizeLimit != 2 || !strings.Contains(f.queries[0].Filter, "(anasIdentityAnchor="+testAnchor+")") || strings.Contains(f.queries[0].Filter, "(mail=") {
		t.Fatal("directory identity lookup changed")
	}
	locator := f.queries[1]
	if locator.BaseDN != d.config.BaseDN || locator.Scope != ldap.ScopeWholeSubtree || len(locator.Controls) != 1 || locator.Controls[0].GetControlType() != directorymail.DomainScopeControlOID || strings.Contains(locator.Filter, "member:") {
		t.Fatal("group identity not resolved independently")
	}
	group := f.queries[2]
	if group.BaseDN != d.config.AllowGroups[0].DN || group.Scope != ldap.ScopeBaseObject || !strings.Contains(group.Filter, "member:1.2.840.113556.1.4.1941:=") || !strings.Contains(group.Filter, `Alice\2a\28test\29`) || !strings.Contains(group.Filter, "(objectSid=") {
		t.Fatal("nested/primary group query or DN escaping missing", group.Filter)
	}
	for _, label := range []string{"alice", "alice@example.test", testAnchor + ")(objectClass=*)"} {
		before := len(f.queries)
		if _, err := d.Person(t.Context(), label); err != ErrAdmission || len(f.queries) != before {
			t.Fatal("editable label reached identity lookup")
		}
	}
}

func changeAttribute(e *ldap.Entry, name string, values []string) {
	for _, a := range e.Attributes {
		if strings.EqualFold(a.Name, name) {
			a.Values = values
			a.ByteValues = nil
			for _, v := range values {
				a.ByteValues = append(a.ByteValues, []byte(v))
			}
			return
		}
	}
	e.Attributes = append(e.Attributes, ldap.NewEntry("", map[string][]string{name: values}).Attributes...)
}

func TestDirectoryRejectsInvalidOrUnavailablePersonnel(t *testing.T) {
	changes := []struct {
		name   string
		modify func(*ldap.Entry)
	}{
		{"disabled", func(e *ldap.Entry) { changeAttribute(e, "userAccountControl", []string{"514"}) }},
		{"computer", func(e *ldap.Entry) { changeAttribute(e, "userAccountControl", []string{"4608"}) }},
		{"locked", func(e *ldap.Entry) { changeAttribute(e, "msDS-User-Account-Control-Computed", []string{"16"}) }},
		{"expired password", func(e *ldap.Entry) { changeAttribute(e, "msDS-User-Account-Control-Computed", []string{"8388608"}) }},
		{"must change password", func(e *ldap.Entry) { changeAttribute(e, "pwdLastSet", []string{"0"}) }},
		{"expired account", func(e *ldap.Entry) { changeAttribute(e, "accountExpires", []string{"1"}) }},
		{"invalid expiry", func(e *ldap.Entry) { changeAttribute(e, "accountExpires", []string{"18446744073709551615"}) }},
		{"missing account state", func(e *ldap.Entry) { changeAttribute(e, "userAccountControl", nil) }},
		{"wrong anchor", func(e *ldap.Entry) { changeAttribute(e, "anasIdentityAnchor", []string{groupAnchor}) }},
		{"multiple anchors", func(e *ldap.Entry) { changeAttribute(e, "anasIdentityAnchor", []string{testAnchor, groupAnchor}) }},
		{"ambiguous mail", func(e *ldap.Entry) { changeAttribute(e, "mail", []string{"alice@example.test", "bob@example.test"}) }},
		{"mail outside domain", func(e *ldap.Entry) { changeAttribute(e, "mail", []string{"alice@other.test"}) }},
		{"malformed alias", func(e *ldap.Entry) { changeAttribute(e, "proxyAddresses", []string{"smtp:Alice <alice@example.test>"}) }},
		{"wrong people boundary", func(e *ldap.Entry) { e.DN = "CN=Alice,OU=Services,DC=example,DC=test" }},
		{"invalid SID", func(e *ldap.Entry) { changeAttribute(e, "objectSid", []string{"not-a-sid"}) }},
		{"builtin personnel", func(e *ldap.Entry) {
			sid := e.GetRawAttributeValue("objectSid")
			binary.LittleEndian.PutUint32(sid[24:28], 500)
			changeAttribute(e, "objectSid", []string{string(sid)})
		}},
		{"fallback lockout", func(e *ldap.Entry) {
			changeAttribute(e, "msDS-User-Account-Control-Computed", nil)
			changeAttribute(e, "lockoutTime", []string{"1"})
		}},
		{"unknown lockout", func(e *ldap.Entry) {
			changeAttribute(e, "msDS-User-Account-Control-Computed", nil)
			changeAttribute(e, "lockoutTime", nil)
		}},
		{"unknown password expiry", func(e *ldap.Entry) { changeAttribute(e, "msDS-User-Account-Control-Computed", nil) }},
		{"computed password expired", func(e *ldap.Entry) { changeAttribute(e, "msDS-UserPasswordExpiryTimeComputed", []string{"1"}) }},
		{"ambiguous password expiry", func(e *ldap.Entry) { changeAttribute(e, "msDS-UserPasswordExpiryTimeComputed", []string{"1", "2"}) }},
	}
	for _, tt := range changes {
		t.Run(tt.name, func(t *testing.T) {
			d, f := directoryFixture(t)
			tt.modify(f.person.Entries[0])
			if p, err := d.Person(t.Context(), testAnchor); err != ErrAdmission || p.Anchor != "" {
				t.Fatal("invalid personnel admitted", err)
			}
		})
	}
	for _, kind := range []string{"missing", "duplicate", "retired group", "recreated group", "referral", "search failure", "bind failure", "canceled"} {
		t.Run(kind, func(t *testing.T) {
			d, f := directoryFixture(t)
			ctx := t.Context()
			switch kind {
			case "missing":
				f.person.Entries = nil
			case "duplicate":
				f.person.Entries = append(f.person.Entries, personEntry())
			case "retired group":
				f.group.Entries = nil
			case "recreated group":
				changeAttribute(f.group.Entries[0], "anasIdentityAnchor", []string{testAnchor})
			case "referral":
				f.person.Referrals = []string{"ldaps://unexpected.example.test"}
			case "search failure":
				f.err = errors.New("private-service-secret")
			case "bind failure":
				d.dial = func(context.Context) (searchConnection, error) { return nil, errors.New("private-service-secret") }
			case "canceled":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			p, err := d.Person(ctx, testAnchor)
			if err == nil || p.Anchor != "" || strings.Contains(err.Error(), "private-service-secret") {
				t.Fatal("ambiguous/unavailable directory granted access", err)
			}
			if kind == "search failure" || kind == "bind failure" || kind == "referral" || kind == "canceled" {
				if err != ErrDirectoryUnavailable {
					t.Fatal("outage mislabeled as absent person", err)
				}
			}
		})
	}
}

func TestDirectoryConfigurationAndExpiry(t *testing.T) {
	for _, endpoint := range []string{"ldap://dc.example.test", "ldaps://user:pass@dc.example.test", "ldaps://dc.example.test/path", "ldaps://dc.example.test/?", "ldaps://dc.example.test/#x"} {
		cfg := directoryConfig()
		cfg.URL = endpoint
		if _, err := NewDirectory(cfg, nil); err == nil {
			t.Fatal("unsafe directory endpoint accepted")
		}
	}
	if _, err := NewDirectory(directoryConfig(), &tls.Config{InsecureSkipVerify: true}); err == nil {
		t.Fatal("TLS verification bypass accepted")
	}
	cfg := directoryConfig()
	cfg.AllowGroups[0].Anchor = "APP_mail"
	if _, err := NewDirectory(cfg, nil); err == nil {
		t.Fatal("mutable group name used as identity")
	}
	cfg = directoryConfig()
	cfg.UsersDN = "OU=People,DC=other,DC=test"
	if _, err := NewDirectory(cfg, nil); err == nil {
		t.Fatal("directory boundary outside domain accepted")
	}
	d, f := directoryFixture(t)
	changeAttribute(f.person.Entries[0], "accountExpires", []string{fmt.Sprint((time.Now().Add(time.Hour).Unix() + 11644473600) * 10000000)})
	changeAttribute(f.person.Entries[0], "msDS-User-Account-Control-Computed", nil)
	changeAttribute(f.person.Entries[0], "msDS-UserPasswordExpiryTimeComputed", []string{"9223372036854775807"})
	if _, err := d.Person(t.Context(), testAnchor); err != nil {
		t.Fatal("future expiry/known unlocked fallback rejected", err)
	}
}

func TestAdmissionGroupFollowsUniqueAnchorAcrossRename(t *testing.T) {
	d, f := directoryFixture(t)
	currentDN := "CN=Mail renamed,OU=Other Groups,DC=example,DC=test"
	f.group.Entries[0].DN = currentDN
	p, err := d.Person(t.Context(), testAnchor)
	if err != nil || len(p.Groups) != 1 || p.Groups[0].DN != currentDN || f.queries[2].BaseDN != currentDN {
		t.Fatal("group rename broke stable group identity", err)
	}
	for _, invalid := range []string{"duplicate-anchor", "foreign-domain"} {
		t.Run(invalid, func(t *testing.T) {
			d, f := directoryFixture(t)
			if invalid == "duplicate-anchor" {
				f.group.Entries = append(f.group.Entries, ldap.NewEntry(currentDN, map[string][]string{"anasIdentityAnchor": {groupAnchor}}))
			} else {
				f.group.Entries[0].DN = "CN=Mail,DC=foreign,DC=test"
			}
			if _, err := d.Person(t.Context(), testAnchor); !errors.Is(err, ErrAdmission) {
				t.Fatal("ambiguous or foreign admission group accepted", err)
			}
			if len(f.queries) != 2 {
				t.Fatal("membership query hid ambiguous group identity")
			}
		})
	}
}

func TestEndpointValidationDoesNotAuthorizeUnresolvedAdmissionGroups(t *testing.T) {
	cfg := directoryConfig()
	cfg.AllowGroups = nil
	if err := ValidateDirectoryEndpoint(cfg, nil); err != nil {
		t.Fatal("local endpoint validation unexpectedly needs LDAP", err)
	}
	if _, err := NewDirectory(cfg, nil); err == nil {
		t.Fatal("unresolved admission configuration became a login directory")
	}
	if err := ValidateDirectoryEndpoint(cfg, &tls.Config{InsecureSkipVerify: true}); err == nil {
		t.Fatal("bootstrap accepted unverified LDAP TLS")
	}
}
