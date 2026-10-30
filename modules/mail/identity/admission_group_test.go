package identity

import (
	"context"
	"errors"
	"testing"

	"github.com/anas-project/ANAS/internal/directorymail"
	"github.com/go-ldap/ldap/v3"
)

type admissionGroupFixture struct {
	results []*ldap.SearchResult
	err     error
	queries []*ldap.SearchRequest
}

func (f *admissionGroupFixture) Close() error { return nil }
func (f *admissionGroupFixture) Search(q *ldap.SearchRequest) (*ldap.SearchResult, error) {
	f.queries = append(f.queries, q)
	if f.err != nil {
		return nil, f.err
	}
	i := len(f.queries) - 1
	if i >= len(f.results) {
		return nil, errors.New("unexpected search")
	}
	return f.results[i], nil
}
func TestAdmissionGroupInitialLookupRequiresUniqueAnchor(t *testing.T) {
	cfg := directoryConfig()
	cfg.GroupsDN = "OU=Groups,DC=example,DC=test"
	dn := cfg.AllowGroups[0].DN
	entry := ldap.NewEntry(dn, map[string][]string{"anasIdentityAnchor": {groupAnchor}})
	for _, name := range []string{"valid", "unanchored", "duplicate", "referral", "wrong-dn", "outage"} {
		t.Run(name, func(t *testing.T) {
			f := &admissionGroupFixture{results: []*ldap.SearchResult{{Entries: []*ldap.Entry{entry}}, {Entries: []*ldap.Entry{entry}}}}
			switch name {
			case "unanchored":
				f.results[0] = &ldap.SearchResult{Entries: []*ldap.Entry{ldap.NewEntry(dn, map[string][]string{})}}
			case "duplicate":
				f.results[1].Entries = append(f.results[1].Entries, ldap.NewEntry("CN=other,"+cfg.UsersDN, map[string][]string{"anasIdentityAnchor": {groupAnchor}}))
			case "referral":
				f.results[1].Referrals = []string{"ldaps://other.test"}
			case "wrong-dn":
				f.results[0].Entries = []*ldap.Entry{ldap.NewEntry("CN=other,"+cfg.GroupsDN, map[string][]string{"anasIdentityAnchor": {groupAnchor}})}
			case "outage":
				f.err = errors.New("offline")
			}
			d, err := NewDirectory(cfg, nil)
			if err != nil {
				t.Fatal(err)
			}
			d.dial = func(context.Context) (searchConnection, error) { return f, nil }
			got, err := d.resolveAdmissionGroup(t.Context(), dn)
			if name != "valid" {
				if err == nil || got != "" {
					t.Fatal("ambiguous initial group accepted")
				}
				return
			}
			if err != nil || got != groupAnchor || len(f.queries) != 2 {
				t.Fatal("initial group", err)
			}
			if f.queries[0].Scope != ldap.ScopeBaseObject || f.queries[1].BaseDN != cfg.BaseDN || len(f.queries[1].Controls) != 1 || f.queries[1].Controls[0].GetControlType() != directorymail.DomainScopeControlOID {
				t.Fatal("unbounded group lookup")
			}
		})
	}
}
