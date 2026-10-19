// TEST_CASES: PORTAL-T-006
package main

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func (d *accountFake) Create(op NewAccount, anchor string, _ []byte, password string) string {
	result := d.do()
	if result == "saved" {
		d.people[anchor] = Person{Anchor: anchor, GUID: "created-guid", Username: op.Username, DisplayName: op.Profile.DisplayName, GivenName: op.Profile.GivenName, Surname: op.Profile.Surname, DN: "CN=" + op.Username + ",OU=People,DC=example", Email: op.Username + "@example.com", PasswordStateKnown: true, MustChangePassword: op.ForceChange}
	}
	return result
}

func creationFixture(t *testing.T) (*fixture, *accountFake, NewAccount) {
	f, d := adminFixture(t)
	f.server.config.CreateBindDN, f.server.config.CreateBindPassword = "create-writer", "writer-fixture"
	f.server.config.MailDomain, f.server.config.UPNSuffix = "example.com", "ad.example.com"
	return f, d, NewAccount{Username: "new-person", Profile: Profile{"New", "Person", "New Person"}, ForceChange: true}
}

func creationRequest(f *fixture, op NewAccount) *httptest.ResponseRecorder {
	data, _ := json.Marshal(op)
	return f.request("POST", "/api/users", string(data))
}

func TestCreationUsesStableTargetAndShowsTemporaryPasswordOnce(t *testing.T) {
	f, d, op := creationFixture(t)
	r := creationRequest(f, op)
	var response struct{ Directory, Target, TemporaryPassword string }
	var data map[string]any
	if json.Unmarshal(r.Body.Bytes(), &data) != nil || r.Code != 200 || d.writes != 1 {
		t.Fatal("creation failed", r.Code)
	}
	response.Directory, _ = data["directory"].(string)
	response.Target, _ = data["target"].(string)
	response.TemporaryPassword, _ = data["temporary_password"].(string)
	if response.Directory != "saved" || response.TemporaryPassword == "" || d.people[response.Target].Username != op.Username || !d.people[response.Target].MustChangePassword {
		t.Fatal("creation result lost generated identity or force-change state")
	}
	audit, _ := json.Marshal(f.server.audit.list())
	if strings.Contains(string(audit), response.TemporaryPassword) || strings.Contains(string(audit), "writer-fixture") || strings.Contains(string(audit), op.Profile.DisplayName) {
		t.Fatal("creation leaked a password or full profile into audit")
	}
}

func TestCreationRequiresPasswordChangeUnlessExplicitlyDisabled(t *testing.T) {
	for _, explicit := range []bool{false, true} {
		f, d, op := creationFixture(t)
		body := map[string]any{"username": op.Username, "profile": op.Profile}
		if explicit {
			body["force_change"] = false
		}
		data, _ := json.Marshal(body)
		r := f.request("POST", "/api/users", string(data))
		var result struct {
			Target string `json:"target"`
		}
		if r.Code != 200 || json.Unmarshal(r.Body.Bytes(), &result) != nil || d.people[result.Target].MustChangePassword == explicit {
			t.Fatal("creation lost its safe default or explicit option")
		}
	}
}

func TestCreationRejectsAuthorityAndPayloadBypasses(t *testing.T) {
	for _, scenario := range []string{"ordinary", "stale", "csrf", "audit", "disabled"} {
		t.Run(scenario, func(t *testing.T) {
			f, d, op := creationFixture(t)
			switch scenario {
			case "ordinary":
				p := d.people[f.person.Anchor]
				p.Groups = nil
				d.people[p.Anchor] = p
			case "stale":
				v := f.session
				v.AuthTime = time.Now().Add(-time.Hour)
				f.server.store.add(v)
			case "csrf":
				f.session.CSRF = "forged"
			case "audit":
				f.server.audit.broken = true
			case "disabled":
				f.server.config.CreateBindPassword = ""
			}
			if r := creationRequest(f, op); r.Code == 200 || d.writes != 0 {
				t.Fatal("unauthorized creation reached directory")
			}
		})
	}
	for _, field := range []string{"mail", "email", "dn", "groups", "userAccountControl", "password", "anchor"} {
		f, d, op := creationFixture(t)
		b, _ := json.Marshal(op)
		body := strings.TrimSuffix(string(b), "}") + `,"` + field + `":"forbidden"}`
		if r := f.request("POST", "/api/users", body); r.Code != 400 || d.writes != 0 {
			t.Fatal("unlisted creation field accepted", field)
		}
	}
}

func TestCreationUnknownAndConflictingResultsNeverReturnPassword(t *testing.T) {
	for _, outcome := range []string{"unknown", "conflict", "denied", "rejected"} {
		f, d, op := creationFixture(t)
		d.result = outcome
		r := creationRequest(f, op)
		if r.Code != 409 || d.writes != 1 || strings.Contains(r.Body.String(), "temporary_password") {
			t.Fatal("failed or unknown creation exposed password or replayed", outcome)
		}
		audit := f.request("GET", "/api/audit", "")
		if audit.Code != 200 || !strings.Contains(audit.Body.String(), `"action":"user.create"`) {
			t.Fatal("operator cannot see their own creation outcome", outcome)
		}
	}
}

func TestManagementRejectsStableReferencesOutsideConfiguredOU(t *testing.T) {
	f, d := adminFixture(t)
	outside := Person{Anchor: "outside-anchor", Username: "outside", DN: "CN=Outside,OU=Admins,DC=example", GUID: "outside-guid", PasswordStateKnown: true}
	d.people[outside.Anchor] = outside
	if r := f.request("GET", "/api/users", ""); r.Code != 200 || strings.Contains(r.Body.String(), outside.Anchor) {
		t.Fatal("outside user appeared in enumeration")
	}
	for _, path := range []string{"/api/users/" + outside.Anchor, "/api/groups?target=" + outside.Anchor} {
		if r := f.request("GET", path, ""); r.Code != 403 {
			t.Fatal("outside user reference was readable", path)
		}
	}
	op := AdminOperation{Action: "account.unlock", Target: outside.Anchor}
	if r := operationRequest(f, "/api/administration/execute", op); r.Code != 403 || d.writes != 0 {
		t.Fatal("outside user reference was writable")
	}
	if !f.server.managedPerson(Person{DN: "CN=Child,OU=Team,OU=People,DC=example"}) || f.server.managedPerson(Person{DN: "CN=Alias,OU=NotPeople,DC=example"}) {
		t.Fatal("OU boundary does not respect DN components")
	}
}
