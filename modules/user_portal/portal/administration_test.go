// TEST_CASES: PORTAL-T-006
package main

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/anas-project/ANAS/internal/groupcatalog"
	"github.com/go-ldap/ldap/v3"
)

type accountFake struct {
	*fakeDirectory
	asCalls     int
	verifyCalls int
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
func (d *accountFake) ExecuteAs(_ Person, password string, _ AdminOperation, _ Person, _ Group) OperationResult {
	if password != "operator-proof" {
		return OperationResult{Directory: "credentials_rejected"}
	}
	d.asCalls++
	return OperationResult{Directory: d.do()}
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
		t.Fatalf("prepare returned %d %s", response.Code, response.Body.String())
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
	op := AdminOperation{Action: "password.reset", Target: "target-anchor", ForceChange: true}
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
	op := AdminOperation{Action: "password.reset", Target: p.Anchor}
	if r := operationRequest(f, "/api/administration/execute", op); r.Code != 200 || d.writes != 1 {
		t.Fatal("platform role treated as domain privilege")
	}
	op.Target = f.person.Anchor
	if r := operationRequest(f, "/api/administration/execute", op); r.Code != 403 || d.writes != 1 {
		t.Fatal("administrator reset own password")
	}
}

func TestDomainConfirmationBindsParametersSessionAndIdentity(t *testing.T) {
	for _, change := range []string{"direct", "parameters", "target", "expiry", "role", "session", "password", "replay", "workflow"} {
		t.Run(change, func(t *testing.T) {
			f, d := adminFixture(t)
			p := d.people["target-anchor"]
			p.DomainRisk = true
			d.people[p.Anchor] = p
			op := AdminOperation{Action: "password.reset", Target: p.Anchor, ForceChange: true}
			if change == "workflow" {
				op.Action, op.Group, op.ForceChange = "groups.member.add", "editable-group", false
			}
			if change != "direct" {
				op.Proof = proofFor(t, f, op)
			}
			op.ActorPassword = "operator-proof"
			switch change {
			case "workflow":
				g := d.groups["editable-group"]
				g.DomainRisk = true
				d.groups["editable-group"] = g
			case "parameters":
				op.ForceChange = false
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
		op := AdminOperation{Action: "account.unlock", Target: "target-anchor"}
		if r := operationRequest(f, "/api/administration/execute", op); r.Code != 403 || d.writes != 0 {
			t.Fatal("stale authentication accepted")
		}
	})
	t.Run("csrf", func(t *testing.T) {
		f, d := adminFixture(t)
		f.session.CSRF = "forged"
		op := AdminOperation{Action: "account.unlock", Target: "target-anchor"}
		if r := operationRequest(f, "/api/administration/execute", op); r.Code != 403 || d.writes != 0 {
			t.Fatal("forged CSRF accepted")
		}
	})
	t.Run("audit", func(t *testing.T) {
		f, d := adminFixture(t)
		f.server.audit.broken = true
		op := AdminOperation{Action: "account.unlock", Target: "target-anchor"}
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
	op := AdminOperation{Action: "groups.member.remove", Target: f.person.Anchor, Group: "operator-group", ExpectedDirect: true}
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

// TEST_CASES: PORTAL-T-009
func TestAdministrationWithoutReasonsKeepsAuthorizationAndAudit(t *testing.T) {
	for _, action := range []string{"account.unlock", "password.reset", "groups.member.add", "profile.update"} {
		t.Run(action, func(t *testing.T) {
			f, d := adminFixture(t)
			op := AdminOperation{Action: action, Target: "target-anchor"}
			if action == "groups.member.add" {
				op.Group = "editable-group"
			}
			if action == "profile.update" {
				op.Profile = Profile{"Bob", "Wang", "Bob Wang"}
				op.ProfileVersion = profileVersion(d.people[op.Target])
			}
			r := operationRequest(f, "/api/administration/execute", op)
			if r.Code != 200 || d.writes != 1 {
				t.Fatal("authorized write without reason failed", r.Code)
			}
			records, _ := json.Marshal(f.server.audit.list())
			if strings.Contains(string(records), `"reason"`) || !strings.Contains(string(records), `"phase":"result"`) {
				t.Fatal("audit must preserve the result without a reason", string(records))
			}
		})
	}
	f, d, op := creationFixture(t)
	if r := creationRequest(f, op); r.Code != 200 || d.writes != 1 {
		t.Fatal("creation without a reason rejected", r.Code)
	}
	for _, endpoint := range []string{"/api/administration/prepare", "/api/administration/execute", "/api/me/profile"} {
		f, d := adminFixture(t)
		r := f.request("POST", endpoint, `{"action":"account.unlock","target":"target-anchor","reason":"removed field"}`)
		if r.Code != 400 || d.writes != 0 {
			t.Fatal("removed reason field accepted", endpoint, r.Code, d.writes)
		}
	}
	f, d, op = creationFixture(t)
	body, _ := json.Marshal(map[string]any{"username": op.Username, "profile": op.Profile, "reason": "removed field"})
	if r := f.request("POST", "/api/users", string(body)); r.Code != 400 || d.writes != 0 {
		t.Fatal("creation reason accepted", r.Code, d.writes)
	}
}

// TEST_CASES: GROUPCAT-T-001
func TestGroupDescriptionUsesDirectoryLocatorAndNativeRisk(t *testing.T) {
	d := &LDAPDirectory{config: Config{AdminGroupDN: "CN=Admins,OU=Role,OU=Groups,DC=example", GroupCatalog: groupcatalog.Catalog{Schema: groupcatalog.Schema, Groups: []groupcatalog.Group{
		{Name: "Admins", DN: "CN=Admins,OU=Role,OU=Groups,DC=example", Managed: true, DisplayName: groupcatalog.Text{"en": "ANAS administrators", "zh-CN": "ANAS 管理员"}, Description: groupcatalog.Text{"en": "Platform administration", "zh-CN": "平台管理"}},
		{Name: "Domain Admins", DomainRID: 512, DisplayName: groupcatalog.Text{"en": "Domain Admins"}, Description: groupcatalog.Text{"en": "Domain-wide privileges"}},
	}}}}
	groups := []Group{{Name: "Admins", DN: d.config.AdminGroupDN, SID: "S-1-5-21-1-2-3-1000"}, {Name: "APP_fake", DN: "CN=APP_fake,OU=Other,DC=example", SID: "S-1-5-21-1-2-3-1001", Description: "Directory supplied"}, {Name: "Renamed", SID: "S-1-5-21-1-2-3-512"}, {Name: "Domain Admins", SID: "S-1-5-21-9-9-9-512"}}
	d.describeGroups(groups)
	if localizeGroup(groups[0], "fr").Description != "Platform administration" || localizeGroup(groups[0], "zh-CN").DisplayName != "ANAS 管理员" {
		t.Fatal("language fallback wrong")
	}
	if groups[1].Metadata != nil || localizeGroup(groups[1], "en").Description != "Directory supplied" {
		t.Fatal("prefix was used as catalog ownership")
	}
	if groups[2].Metadata == nil || !domainGroup(groups[2]) || groups[3].Metadata != nil {
		t.Fatal("system locator must use this domain SID, native risk remains independent")
	}
}

// TEST_CASES: PORTAL-T-009
func TestDomainPreviewVersionDetectsReplacedTarget(t *testing.T) {
	f, d := adminFixture(t)
	p := d.people["target-anchor"]
	p.DomainRisk = true
	d.people[p.Anchor] = p
	op := AdminOperation{Action: "password.reset", Target: p.Anchor}
	first := operationRequest(f, "/api/administration/prepare", op)
	var a map[string]any
	if first.Code != 200 || json.Unmarshal(first.Body.Bytes(), &a) != nil || a["identity_version"] == "" {
		t.Fatal("preview has no directory binding")
	}
	p.GUID = "replacement-guid"
	d.people[p.Anchor] = p
	second := operationRequest(f, "/api/administration/prepare", op)
	var b map[string]any
	if second.Code != 200 || json.Unmarshal(second.Body.Bytes(), &b) != nil || a["identity_version"] == b["identity_version"] || a["target"] != b["target"] {
		t.Fatal("same-name replacement lost its identity change")
	}
}

// TEST_CASES: PORTAL-T-006
func TestAdminRoleMembershipNeedsConfirmationAndRecentAuthentication(t *testing.T) {
	f, d := adminFixture(t)
	// The role is not a domain group: the check must hold without domain
	// administration and must still use the portal's scoped writer.
	f.server.config.DomainAdministration = false
	r := f.request("GET", "/api/groups?target=target-anchor", "")
	var listed struct {
		Groups []struct {
			Group            Group `json:"group"`
			RequiresDomain   bool  `json:"requires_domain"`
			RequiresPassword bool  `json:"requires_password"`
		} `json:"groups"`
	}
	if r.Code != 200 || json.Unmarshal(r.Body.Bytes(), &listed) != nil {
		t.Fatal("groups unavailable", r.Code)
	}
	for _, item := range listed.Groups {
		if item.RequiresDomain || item.RequiresPassword {
			t.Fatal("wrong confirmation tier", item.Group.Reference, item.RequiresPassword)
		}
	}
	op := AdminOperation{Action: "groups.member.add", Target: "target-anchor", Group: "operator-group"}
	if r = operationRequest(f, "/api/administration/execute", op); r.Code != 403 || !strings.Contains(r.Body.String(), "confirmation_required") || d.writes != 0 {
		t.Fatal("administrator role granted without confirmation", r.Code)
	}
	op.Proof = proofFor(t, f, op)
	op.ActorPassword = "unnecessary-password"
	if r = operationRequest(f, "/api/administration/execute", op); r.Code != 400 || d.writes != 0 {
		t.Fatal("unexpected password accepted", r.Code)
	}
	op.ActorPassword = ""
	if r = operationRequest(f, "/api/administration/execute", op); r.Code != 200 || d.writes != 1 || d.asCalls != 0 {
		t.Fatal("recent confirmed role change failed or used personal AD rights", r.Code)
	}
	if r = operationRequest(f, "/api/administration/execute", op); r.Code != 403 || d.writes != 1 {
		t.Fatal("consumed confirmation was reused", r.Code)
	}
	records, _ := json.Marshal(f.server.audit.list())
	if !strings.Contains(string(records), "platform_admin_confirmed") || strings.Contains(string(records), "operator-proof") {
		t.Fatal("audit must record the confirmed rule without the password", string(records))
	}
	ordinary := AdminOperation{Action: "groups.member.add", Target: "target-anchor", Group: "editable-group"}
	if r = operationRequest(f, "/api/administration/execute", ordinary); r.Code != 200 || d.writes != 2 {
		t.Fatal("ordinary managed group no longer adds directly", r.Code)
	}
}

func TestOrdinaryOperationsReuseAuthenticationForFiveMinutes(t *testing.T) {
	for _, age := range []time.Duration{299 * time.Second, 301 * time.Second} {
		t.Run(age.String(), func(t *testing.T) {
			f, d := adminFixture(t)
			v := f.session
			v.AuthTime = time.Now().Add(-age)
			f.server.store.add(v)
			op := AdminOperation{Action: "account.unlock", Target: "target-anchor"}
			for i := 0; i < 2; i++ {
				r := operationRequest(f, "/api/administration/execute", op)
				if age < 5*time.Minute {
					if r.Code != 200 || d.writes != i+1 || d.asCalls != 0 {
						t.Fatal("recent authentication was not reused", r.Code)
					}
				} else if r.Code != 403 || !strings.Contains(r.Body.String(), "recent_authentication_required") || d.writes != 0 {
					t.Fatal("expired authentication wrote", r.Code)
				}
			}
			role := AdminOperation{Action: "groups.member.add", Target: "target-anchor", Group: "operator-group"}
			r := operationRequest(f, "/api/administration/prepare", role)
			if age < 5*time.Minute && r.Code != 200 || age > 5*time.Minute && (r.Code != 403 || !strings.Contains(r.Body.String(), "recent_authentication_required")) {
				t.Fatal("role confirmation has wrong authentication window", r.Code)
			}
		})
	}
}

func TestConfigRejectsAuthenticationWindowsLongerThanFiveMinutes(t *testing.T) {
	values := map[string]string{"URL": "https://portal.example.test", "ISSUER": "https://auth.example.test", "LDAP_URL": "ldaps://dc.example.test", "CLIENT_ID": "portal", "REAUTH_CLIENT_ID": "reauth", "LDAP_BIND_DN": "CN=reader,DC=example", "LDAP_BIND_PASSWORD": "fixture-only", "BASE_DN": "DC=example", "USERS_DN": "OU=People,DC=example", "CA_FILE": "/fixture/ca.pem", "ANCHOR_ATTRIBUTE": "anasIdentityAnchor", "GROUPS_DN": "OU=Groups,DC=example"}
	for key, value := range values {
		t.Setenv("USER_PORTAL_"+key, value)
	}
	for _, seconds := range []string{"300", "301", "900"} {
		t.Setenv("USER_PORTAL_RECENT_WINDOW_SECONDS", seconds)
		c, err := loadConfig()
		if seconds == "300" {
			if err != nil || c.RecentWindow != 5*time.Minute {
				t.Fatal("five minute configuration rejected", err)
			}
		} else if err == nil {
			t.Fatal("window longer than five minutes accepted", seconds)
		}
	}
}
