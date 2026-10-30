// TEST_CASES: PORTAL-T-008
package main

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-ldap/ldap/v3"
)

type ownerFake struct {
	d                       *LDAPDirectory
	p                       Person
	owner                   []byte
	searches, writes        int
	ownerError, memberError error
	readbackFailure         bool
}

func (f *ownerFake) Search(r *ldap.SearchRequest) (*ldap.SearchResult, error) {
	f.searches++
	if r.BaseDN == f.d.config.BaseDN {
		sid, _ := domainAdminSID(f.p.SID)
		return &ldap.SearchResult{Entries: []*ldap.Entry{ldap.NewEntry("CN=Domain Admins,"+f.d.config.BaseDN, map[string][]string{"objectSid": {string(sid)}})}}, nil
	}
	if f.readbackFailure && f.writes > 0 {
		return nil, errors.New("connection lost")
	}
	return &ldap.SearchResult{Entries: []*ldap.Entry{ldap.NewEntry(f.p.DN, map[string][]string{"objectGUID": {string(make([]byte, 16))}, "objectSid": {string(testUserSID())}, f.d.config.AnchorAttribute: {f.p.Anchor}, "nTSecurityDescriptor": {string(ownerDescriptor(f.owner))}})}}, nil
}
func (f *ownerFake) Modify(r *ldap.ModifyRequest) error {
	f.writes++
	if len(r.Changes) != 1 {
		panic("unexpected change count")
	}
	if r.Changes[0].Modification.Type == "nTSecurityDescriptor" {
		if len(r.Controls) != 1 || r.Controls[0].GetControlType() != "1.2.840.113556.1.4.801" {
			panic("owner-only control missing")
		}
		if f.ownerError != nil {
			return f.ownerError
		}
		f.owner, _ = descriptorOwner([]byte(r.Changes[0].Modification.Vals[0]))
		return nil
	}
	if r.Changes[0].Modification.Type != "member" {
		panic("unexpected attribute")
	}
	if f.ownerError != nil || f.readbackFailure {
		panic("unsafe membership write")
	}
	return f.memberError
}
func testUserSID() []byte {
	sid, _ := domainAdminSID("S-1-5-21-1-2-3-1001")
	binary.LittleEndian.PutUint32(sid[24:], 1001)
	return sid
}
func TestDomainOwnershipSteps(t *testing.T) {
	for _, tc := range []struct {
		name                          string
		ownerError, memberError       error
		readbackFailure, alreadyOwned bool
		ownership, membership         string
		writes                        int
	}{
		{"success", nil, nil, false, false, "saved", "saved", 2},
		{"owner denied", &ldap.Error{ResultCode: ldap.LDAPResultInsufficientAccessRights}, nil, false, false, "denied", "not_attempted", 1},
		{"owner unknown", errors.New("network"), nil, false, false, "unknown", "not_attempted", 1},
		{"readback unknown", nil, nil, true, false, "unknown", "not_attempted", 1},
		{"member denied", nil, &ldap.Error{ResultCode: ldap.LDAPResultInsufficientAccessRights}, false, false, "saved", "denied", 2},
		{"member unknown", nil, errors.New("network"), false, false, "saved", "unknown", 2},
		{"already owned", nil, nil, false, true, "saved", "saved", 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := &LDAPDirectory{config: Config{BaseDN: "DC=example", UsersDN: "OU=People,DC=example", AnchorAttribute: "anchor"}}
			p := Person{Anchor: "target-anchor", GUID: "00000000-0000-0000-0000-000000000000", SID: sidString(testUserSID()), DN: "CN=person,OU=People,DC=example"}
			f := &ownerFake{d: d, p: p, owner: testUserSID(), ownerError: tc.ownerError, memberError: tc.memberError, readbackFailure: tc.readbackFailure}
			if tc.alreadyOwned {
				f.owner, _ = domainAdminSID(p.SID)
			}
			r := d.addDomainMember(f, p, Group{GUID: "group-guid"})
			if r.Ownership != tc.ownership || r.Membership != tc.membership || f.writes != tc.writes {
				t.Fatalf("unexpected steps: %+v, writes %d", r, f.writes)
			}
			if tc.ownership == "saved" {
				want, _ := domainAdminSID(p.SID)
				if !bytes.Equal(f.owner, want) {
					t.Fatal("unsafe ownership rollback")
				}
			}
		})
	}
}
func TestOwnerDescriptorBounds(t *testing.T) {
	valid := ownerDescriptor(testUserSID())
	for i := 0; i < len(valid); i++ {
		if _, err := descriptorOwner(valid[:i]); err == nil {
			t.Fatal("truncated descriptor accepted", i)
		}
	}
	for _, offset := range []uint32{0, 4, 19, 0xffffffff} {
		sd := bytes.Clone(valid)
		binary.LittleEndian.PutUint32(sd[4:], offset)
		if _, err := descriptorOwner(sd); err == nil {
			t.Fatal("bad owner offset accepted")
		}
	}
	for _, sid := range []string{"S-1-5-32-544", "S-1-5-21-1-2-3-4-5", "S-1-5-21-1-2-3--1", "S-1-5-21-1-2-3-4294967296"} {
		if _, ok := domainAdminSID(sid); ok {
			t.Fatal("invalid domain SID accepted")
		}
	}
}
func TestOwnershipPartialOutcomeAndAudit(t *testing.T) {
	f, _ := adminFixture(t)
	// Exercise the production result writer with the two independently known outcomes.
	recorder := httptest.NewRecorder()
	f.server.writeResult(recorder, httptest.NewRequest("POST", "/api/administration/execute", nil), Record{Actor: f.person.Anchor, Target: "target-anchor", Action: "groups.member.add", Ownership: "pending", Membership: "pending"}, func() OperationResult {
		return OperationResult{Directory: "denied", Ownership: "saved", Membership: "denied"}
	}, "", false)
	if recorder.Code != 409 || !strings.Contains(recorder.Body.String(), `"ownership":"saved"`) || !strings.Contains(recorder.Body.String(), `"membership":"denied"`) {
		t.Fatal("partial result lost")
	}
	data, _ := json.Marshal(f.server.audit.list())
	if !bytes.Contains(data, []byte(`"ownership":"saved"`)) {
		t.Fatal("partial audit lost")
	}
}
