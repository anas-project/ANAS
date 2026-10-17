// TEST_CASES: PORTAL-T-006
package main

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-ldap/ldap/v3"
)

type accountFake struct {
	*fakeDirectory
	asCalls int
}

func (d *accountFake) Groups() ([]Group, error) {
	groups := []Group{}
	for _, g := range d.groups {
		groups = append(groups, g)
	}
	return groups, nil
}
func (d *accountFake) Profile(p Person, next Profile) string {
	result := d.do()
	if result == "saved" {
		p.GivenName, p.Surname, p.DisplayName = next.GivenName, next.Surname, next.DisplayName
		d.people[p.Anchor] = p
	}
	return result
}
func (d *accountFake) ExecuteAs(_ Person, password string, _ AdminOperation, _ Person, _ Group) string {
	if password != "operator-proof" {
		return "credentials_rejected"
	}
	d.asCalls++
	return d.do()
}
func adminFixture(t *testing.T) (*fixture, *accountFake) {
	f := newFixture(t)
	f.server.config.Administration = true
	f.server.config.DomainAdministration = true
	f.server.config.AdminGroup = "operator-group"
	f.server.config.AdminGroupGUID = "operator-guid"
	f.server.config.Rules = nil
	f.server.config.ProfileBindDN = "profile-writer"
	f.server.config.ProfileBindPassword = "service-proof"
	p := f.directory.people[f.person.Anchor]
	p.Groups[0].GUID = "operator-guid"
	p.Groups[0].Reference = "operator-group"
	f.directory.people[p.Anchor] = p
	f.person = p
	g := Group{Anchor: "operator-group", Reference: "operator-group", GUID: "operator-guid", SID: "S-1-5-21-1-2-3-1001", DN: "CN=Operators,OU=Groups,DC=example", Name: "Admins", Managed: true, AdminRole: true, Category: "roles", Members: []string{p.DN}}
	f.directory.groups[g.Reference] = g
	g = f.directory.groups["editable-group"]
	g.Reference = g.Anchor
	g.Managed = true
	g.Category = "applications"
	g.DN = "CN=Editable,OU=Groups,DC=example"
	f.directory.groups[g.Anchor] = g
	p = f.directory.people["target-anchor"]
	p.PasswordStateKnown = true
	f.directory.people[p.Anchor] = p
	d := &accountFake{fakeDirectory: f.directory}
	f.server.directory = d
	return f, d
}
func operationRequest(f *fixture, path string, op AdminOperation) *httptest.ResponseRecorder {
	data, _ := json.Marshal(op)
	return f.request("POST", path, string(data))
}
func proofFor(t *testing.T, f *fixture, op AdminOperation) string {
	t.Helper()
	response := operationRequest(f, "/api/administration/prepare", op)
	if response.Code != 200 {
		t.Fatalf("prepare returned %d", response.Code)
	}
	var data struct {
		Proof string `json:"proof"`
	}
	json.Unmarshal(response.Body.Bytes(), &data)
	return data.Proof
}

func TestPersonalProfileWhiteListVersionAndAudit(t *testing.T) {
	f, d := adminFixture(t)
	op := AdminOperation{Profile: Profile{"Alice", "Wang", "Alice Wang"}, ProfileVersion: profileVersion(f.person)}
	r := operationRequest(f, "/api/me/profile", op)
	if r.Code != 200 || d.people[f.person.Anchor].DisplayName != "Alice Wang" || d.writes != 1 {
		t.Fatal("self profile was not saved")
	}
	if r = operationRequest(f, "/api/me/profile", op); r.Code != 409 || d.writes != 1 {
		t.Fatal("stale version accepted")
	}
	for _, field := range []string{"mail", "sAMAccountName", "anchor", "email", "dn"} {
		if r = f.request("POST", "/api/me/profile", `{"`+field+`":"forbidden"}`); r.Code != 400 || d.writes != 1 {
			t.Fatal("unlisted profile field accepted", field)
		}
	}
	data, _ := json.Marshal(f.server.audit.list())
	if !strings.Contains(string(data), "changed_fields") || strings.Contains(string(data), "Alice Wang") {
		t.Fatal("audit must record field names, not profile payload")
	}
}

func TestAdministrationUsesCurrentStableANASRole(t *testing.T) {
	f, d := adminFixture(t)
	r := f.request("GET", "/api/users", "")
	if r.Code != 200 || !strings.Contains(r.Body.String(), "bob") {
		t.Fatal("administrator cannot list personnel")
	}
	op := AdminOperation{Action: "password.reset", Target: "target-anchor", Reason: "test reset", ForceChange: true}
	if r = operationRequest(f, "/api/administration/execute", op); r.Code != 200 || d.writes != 1 || d.asCalls != 0 {
		t.Fatal("normal reset did not use scoped writer")
	}
	p := d.people[f.person.Anchor]
	p.Groups[0].GUID = "recreated-admin-group"
	d.people[p.Anchor] = p
	if r = operationRequest(f, "/api/administration/execute", op); r.Code != 403 || d.writes != 1 {
		t.Fatal("recreated role reused authorization")
	}
	r = f.request("GET", "/api/users", "")
	if strings.Contains(r.Body.String(), "bob") {
		t.Fatal("revoked role can enumerate personnel")
	}
}

func TestOtherANASAdministratorIsManageableButSelfResetIsNot(t *testing.T) {
	f, d := adminFixture(t)
	p := d.people["target-anchor"]
	p.Protected = true
	p.Groups = append(p.Groups, f.person.Groups[0])
	d.people[p.Anchor] = p
	op := AdminOperation{Action: "password.reset", Target: p.Anchor, Reason: "other ANAS administrator"}
	if r := operationRequest(f, "/api/administration/execute", op); r.Code != 200 || d.writes != 1 {
		t.Fatal("platform role treated as domain privilege")
	}
	op.Target = f.person.Anchor
	if r := operationRequest(f, "/api/administration/execute", op); r.Code != 403 || d.writes != 1 {
		t.Fatal("administrator reset own password")
	}
}

func TestDomainConfirmationBindsParametersSessionAndIdentity(t *testing.T) {
	for _, change := range []string{"direct", "reason", "target", "expiry", "role", "session", "password", "replay"} {
		t.Run(change, func(t *testing.T) {
			f, d := adminFixture(t)
			p := d.people["target-anchor"]
			p.DomainRisk = true
			d.people[p.Anchor] = p
			op := AdminOperation{Action: "password.reset", Target: p.Anchor, Reason: "domain operation", ForceChange: true}
			if change != "direct" {
				op.Proof = proofFor(t, f, op)
			}
			op.ActorPassword = "operator-proof"
			switch change {
			case "reason":
				op.Reason = "changed parameters"
			case "target":
				op.Target = f.person.Anchor
			case "expiry":
				c := f.server.store.confirmations[op.Proof]
				c.Expires = time.Now().Add(-time.Second)
				f.server.store.confirmations[op.Proof] = c
			case "role":
				p := d.people[f.person.Anchor]
				p.Groups = nil
				d.people[p.Anchor] = p
			case "session":
				c := f.server.store.confirmations[op.Proof]
				c.Session = "another session"
				f.server.store.confirmations[op.Proof] = c
			case "password":
				op.ActorPassword = "another-person-proof"
			case "replay":
				r := operationRequest(f, "/api/administration/execute", op)
				if r.Code != 200 || d.asCalls != 1 {
					t.Fatal("valid domain operation failed")
				}
			}
			r := operationRequest(f, "/api/administration/execute", op)
			if r.Code == 200 {
				t.Fatal("invalid domain operation succeeded")
			}
			if change != "replay" && (d.writes != 0 || d.asCalls != 0) {
				t.Fatal("invalid domain request reached write")
			}
			if change == "replay" && (d.writes != 1 || d.asCalls != 1) {
				t.Fatal("confirmation was replayed")
			}
			data, _ := json.Marshal(f.server.audit.list())
			if strings.Contains(string(data), "operator-proof") || strings.Contains(string(data), "another-person-proof") {
				t.Fatal("personal domain password entered audit")
			}
		})
	}
}

func TestAdministrationRecentCSRFAndAuditFailure(t *testing.T) {
	t.Run("recent", func(t *testing.T) {
		f, d := adminFixture(t)
		v := f.session
		v.AuthTime = time.Now().Add(-time.Hour)
		f.server.store.add(v)
		op := AdminOperation{Action: "account.unlock", Target: "target-anchor", Reason: "test"}
		if r := operationRequest(f, "/api/administration/execute", op); r.Code != 403 || d.writes != 0 {
			t.Fatal("stale authentication accepted")
		}
	})
	t.Run("csrf", func(t *testing.T) {
		f, d := adminFixture(t)
		f.session.CSRF = "forged"
		op := AdminOperation{Action: "account.unlock", Target: "target-anchor", Reason: "test"}
		if r := operationRequest(f, "/api/administration/execute", op); r.Code != 403 || d.writes != 0 {
			t.Fatal("forged CSRF accepted")
		}
	})
	t.Run("audit", func(t *testing.T) {
		f, d := adminFixture(t)
		f.server.audit.broken = true
		op := AdminOperation{Action: "account.unlock", Target: "target-anchor", Reason: "test"}
		if r := operationRequest(f, "/api/administration/execute", op); r.Code != 503 || d.writes != 0 {
			t.Fatal("audit failure did not prevent write")
		}
	})
}

func TestGroupCategoriesAdvancedAndSelfProtection(t *testing.T) {
	f, d := adminFixture(t)
	d.groups["sid:S-1-5-32-544"] = Group{Reference: "sid:S-1-5-32-544", GUID: "builtin-guid", Name: "Administrators", SID: "S-1-5-32-544", Category: "system", DomainRisk: true}
	r := f.request("GET", "/api/groups?target="+f.person.Anchor, "")
	if strings.Contains(r.Body.String(), "Administrators") {
		t.Fatal("system groups in default view")
	}
	r = f.request("GET", "/api/groups?target="+f.person.Anchor+"&advanced=true", "")
	if !strings.Contains(r.Body.String(), "Administrators") {
		t.Fatal("advanced groups missing")
	}
	op := AdminOperation{Action: "groups.member.remove", Target: f.person.Anchor, Group: "operator-group", ExpectedDirect: true, Reason: "self removal"}
	if r = operationRequest(f, "/api/administration/execute", op); r.Code != 403 || d.writes != 0 {
		t.Fatal("self administrator removal accepted")
	}
	// The operator only holds platform rights. Removing the sole domain admin
	// must fail even if that operator has a delegated AD member-write right.
	g := d.groups["sid:S-1-5-32-544"]
	g.Members = []string{d.people["target-anchor"].DN}
	d.groups[g.Reference] = g
	op.Target = "target-anchor"
	op.Group = g.Reference
	if r = operationRequest(f, "/api/administration/prepare", op); r.Code != 403 || !strings.Contains(r.Body.String(), "last_administrator") {
		t.Fatal("last domain administrator removal accepted")
	}
}

func TestProfileValidationAndAtomicSingleObjectChange(t *testing.T) {
	for _, p := range []Profile{{"", "", ""}, {"Alice\n", "", "Alice"}, {"", "", strings.Repeat("界", 65)}, {"Alice", "", "Alice\u202E"}} {
		if p.valid() {
			t.Fatal("invalid profile accepted")
		}
	}
	if !(Profile{"小明", "王", "王小明"}).valid() {
		t.Fatal("valid Chinese profile rejected")
	}
	r := ldap.NewModifyRequest("<GUID=test>", nil)
	if !profileChanges(r, Profile{"Alice", "Wang", "Alice Wang"}, Profile{"Alicia", "Wang", "Alicia Wang"}) || len(r.Changes) != 4 {
		t.Fatal("profile change not atomic delete/add")
	}
	for _, change := range r.Changes {
		if change.Operation == ldap.ReplaceAttribute {
			t.Fatal("profile overwrites concurrent values")
		}
	}
}
