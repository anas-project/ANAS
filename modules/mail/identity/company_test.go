package identity

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/go-ldap/ldap/v3"
)

const companyParent = "77777777-7777-4777-8777-777777777777"
const companyChild = "88888888-8888-4888-8888-888888888888"

type companyConnection struct {
	users, groups *ldap.SearchResult
	queries       []*ldap.SearchRequest
	err           error
}

func (c *companyConnection) Close() error { return nil }
func (c *companyConnection) Search(request *ldap.SearchRequest) (*ldap.SearchResult, error) {
	c.queries = append(c.queries, request)
	if c.err != nil {
		return nil, c.err
	}
	if strings.HasPrefix(request.BaseDN, "OU=People,") {
		return c.users, nil
	}
	return c.groups, nil
}
func companyFixture(t *testing.T) (*Directory, *companyConnection) {
	t.Helper()
	cfg := directoryConfig()
	cfg.GroupsDN = "OU=Groups," + cfg.BaseDN
	d, err := NewDirectory(cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	alice := personEntry()
	changeAttribute(alice, "primaryGroupID", []string{"9999"})
	changeAttribute(alice, "telephoneNumber", []string{"12345"})
	changeAttribute(alice, "department", []string{"Finance"})
	group := func(id, name string, rid uint32, members ...string) *ldap.Entry {
		sid := append([]byte(nil), alice.GetRawAttributeValue("objectSid")...)
		binary.LittleEndian.PutUint32(sid[24:28], rid)
		return ldap.NewEntry("CN="+name+","+cfg.GroupsDN, map[string][]string{"anasIdentityAnchor": {id}, "cn": {name}, "objectSid": {string(sid)}, "member": members})
	}
	child := group(companyChild, "Team", 2001, alice.DN)
	parent := group(companyParent, "Finance", 2002, child.DN)
	control := func() *ldap.ControlPaging { return ldap.NewControlPaging(250) }
	c := &companyConnection{users: &ldap.SearchResult{Entries: []*ldap.Entry{alice}, Controls: []ldap.Control{control()}}, groups: &ldap.SearchResult{Entries: []*ldap.Entry{parent, child}, Controls: []ldap.Control{control()}}}
	d.dial = func(context.Context) (searchConnection, error) { return c, nil }
	return d, c
}
func TestCompanySnapshotPublicFieldsAndNestedGroups(t *testing.T) {
	d, c := companyFixture(t)
	snapshot, err := d.CompanySnapshot(t.Context())
	if err != nil || len(snapshot.People) != 1 || len(snapshot.Groups) != 2 {
		t.Fatal("company snapshot", err)
	}
	person := snapshot.People[0]
	if person.Email != "alice@example.test" || person.Phone != "12345" || len(person.Groups) != 2 || len(person.DirectGroups) != 1 || person.DirectGroups[0] != companyChild {
		t.Fatal("public profile/nested membership lost")
	}
	raw, _ := json.Marshal(snapshot)
	for _, private := range []string{"proxyAddresses", "separate-box", "support+team", "OU=People", "OU=Groups", "objectSid", "sAMAccountName"} {
		if strings.Contains(string(raw), private) {
			t.Fatal("private or internal directory fact leaked", private)
		}
	}
	for _, request := range c.queries {
		if request.SizeLimit != 5001 || ldap.FindControl(request.Controls, ldap.ControlTypePaging) == nil {
			t.Fatal("unbounded directory read")
		}
		for _, attribute := range request.Attributes {
			if strings.EqualFold(attribute, "proxyAddresses") {
				t.Fatal("public company search requested aliases")
			}
		}
	}
	// Cycles never cause recursion storms or duplicate memberships.
	changeAttribute(c.groups.Entries[1], "member", []string{c.users.Entries[0].DN, c.groups.Entries[0].DN})
	snapshot, err = d.CompanySnapshot(t.Context())
	if err != nil || len(snapshot.People[0].Groups) != 2 {
		t.Fatal("nested cycle handling", err)
	}
	changeAttribute(c.users.Entries[0], "userAccountControl", []string{"514"})
	snapshot, err = d.CompanySnapshot(t.Context())
	if err != nil || len(snapshot.People) != 0 {
		t.Fatal("disabled personnel exposed", err)
	}
}

func TestCompanyPrimaryGroupIsNotLostWhenMemberOmitsPersonnel(t *testing.T) {
	d, c := companyFixture(t)
	changeAttribute(c.users.Entries[0], "primaryGroupID", []string{"2002"})
	changeAttribute(c.groups.Entries[1], "member", nil)
	snapshot, err := d.CompanySnapshot(t.Context())
	if err != nil || len(snapshot.People[0].Groups) != 1 || snapshot.People[0].Groups[0] != companyParent || len(snapshot.People[0].DirectGroups) != 1 {
		t.Fatal("primary group omitted", err)
	}
}
func TestCompanySnapshotRejectsAmbiguityAndPartialSources(t *testing.T) {
	for _, mode := range []string{"duplicate-anchor", "duplicate-email", "referral", "paging-missing", "range-members", "search-failure", "foreign-person", "invalid-member-dn"} {
		t.Run(mode, func(t *testing.T) {
			d, c := companyFixture(t)
			switch mode {
			case "duplicate-anchor":
				c.users.Entries = append(c.users.Entries, personEntry())
			case "duplicate-email":
				other := personEntry()
				other.DN = "CN=Other," + d.config.UsersDN
				changeAttribute(other, "anasIdentityAnchor", []string{groupAnchor})
				c.users.Entries = append(c.users.Entries, other)
			case "referral":
				c.groups.Referrals = []string{"ldaps://foreign.example.test"}
			case "paging-missing":
				c.users.Controls = nil
			case "range-members":
				changeAttribute(c.groups.Entries[0], "member;range=0-1499", []string{"CN=partial"})
			case "search-failure":
				c.err = errors.New("private-service-secret")
			case "foreign-person":
				c.users.Entries[0].DN = "CN=Alice,DC=foreign,DC=test"
			case "invalid-member-dn":
				changeAttribute(c.groups.Entries[0], "member", []string{"not-a-dn"})
			}
			if snapshot, err := d.CompanySnapshot(t.Context()); !errors.Is(err, ErrDirectoryUnavailable) || len(snapshot.People) != 0 || strings.Contains(err.Error(), "private-service-secret") {
				t.Fatal("partial or unsafe company snapshot published", err)
			}
		})
	}
}
