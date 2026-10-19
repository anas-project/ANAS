//go:build portal_native

// TEST_CASES: PORTAL-T-007
package main

import (
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"

	"github.com/go-ldap/ldap/v3"
)

// This independent probe needs only the existing read-only binding. It never
// creates an identity, requests a privileged bind, or sends an LDAP Modify.
func TestNativeAdministrationReadOnly(t *testing.T) {
	path := os.Getenv("PORTAL_ACCOUNT_READONLY_FIXTURE")
	if path == "" {
		t.Skip("same-host read-only fixture not supplied")
	}
	if !strings.HasPrefix(path, "/home/whl/anas-portal-admin-") || filepath.Base(path) != "readonly.private.json" {
		t.Fatal("owned read-only fixture path required")
	}
	var f struct {
		Config     Config `json:"config"`
		ServerName string `json:"server_name"`
	}
	data, err := os.ReadFile(path)
	if err != nil || json.Unmarshal(data, &f) != nil {
		t.Fatal("read-only configuration required")
	}
	c := f.Config
	if c.PasswordBindPassword != "" || c.GroupBindPassword != "" || c.ProfileBindPassword != "" || c.CreateBindPassword != "" || f.ServerName == "" {
		t.Fatal("read-only configuration required")
	}
	roots, err := x509.SystemCertPool()
	if err != nil {
		t.Fatal("system trust store unavailable")
	}
	ca, err := os.ReadFile(c.CAFile)
	if err != nil || !roots.AppendCertsFromPEM(ca) {
		t.Fatal("CA unavailable")
	}
	d := &LDAPDirectory{config: c, tls: &tls.Config{RootCAs: roots, ServerName: f.ServerName, MinVersion: tls.VersionTLS12}}
	if err := d.Probe(); err != nil {
		t.Fatal("strict TLS probe:", err)
	}
	if err := d.resolveAdministration(); err != nil {
		t.Fatal("registered role:", err)
	}
	groups, err := d.Groups()
	if err != nil {
		t.Fatal("group enumeration unavailable")
	}
	role, unix, system := false, false, false
	for _, g := range groups {
		role = role || g.GUID == d.config.AdminGroupGUID && g.AdminRole && g.Managed
		unix = unix || g.Name == "Unix Admins" && g.Managed && g.DomainRisk
		system = system || g.SID == "S-1-5-32-544" && !g.Managed && g.DomainRisk
	}
	if !role || !unix || !system {
		t.Fatal("role, registered infrastructure or system boundary unavailable")
	}
	people, err := d.People()
	if err != nil || len(people) == 0 {
		t.Fatal("human enumeration unavailable")
	}
	for _, p := range people {
		if p.System || underDN(p.DN, c.ServiceAccountsDN) {
			t.Fatal("service subject exposed")
		}
	}
	for _, config := range []*tls.Config{{RootCAs: x509.NewCertPool(), ServerName: f.ServerName, MinVersion: tls.VersionTLS12}, {RootCAs: roots, ServerName: "wrong.example.invalid", MinVersion: tls.VersionTLS12}} {
		bad := &LDAPDirectory{config: c, tls: config}
		conn, err := bad.read()
		if conn != nil {
			conn.Close()
		}
		if err == nil {
			t.Fatal("TLS negative boundary accepted")
		}
	}
	out, _ := json.MarshalIndent(map[string]any{"strict_tls": true, "registered_role": role, "registered_unix_still_protected": unix, "system_group_separate": system, "service_subjects_excluded": true, "wrong_ca_rejected": true, "wrong_server_rejected": true, "human_count": len(people), "writes": 0}, "", "  ")
	if os.WriteFile(filepath.Join(filepath.Dir(filepath.Dir(path)), "evidence/readonly-native.json"), out, 0600) != nil {
		t.Fatal("cannot retain safe read-only evidence")
	}
}

// This test only writes tagged fixture personnel. Credentials stay on the host.
func TestNativeAccountManagement(t *testing.T) {
	path := os.Getenv("PORTAL_ACCOUNT_FIXTURE")
	if path == "" {
		t.Skip("owned account fixture not supplied")
	}
	if !strings.HasPrefix(path, "/home/whl/anas-portal-admin-") || filepath.Base(path) != "native.private.json" {
		t.Fatal("owned fixture path required")
	}
	var f struct {
		Config          Config            `json:"config"`
		ServerName      string            `json:"server_name"`
		Prefix          string            `json:"prefix"`
		Anchors         map[string]string `json:"anchors"`
		Password        string            `json:"password"`
		ManagerPassword string            `json:"manager_password"`
		NewPassword     string            `json:"new_password"`
	}
	data, err := os.ReadFile(path)
	if err != nil || json.Unmarshal(data, &f) != nil {
		t.Fatal("private fixture unavailable")
	}
	if !strings.HasPrefix(f.Prefix, "pua") || len(f.Prefix) != 11 || f.Anchors["outside"] == "" {
		t.Fatal("owned fixture prefix required")
	}
	ca, err := os.ReadFile(f.Config.CAFile)
	if err != nil {
		t.Fatal("CA unavailable")
	}
	roots, err := x509.SystemCertPool()
	if err != nil {
		t.Fatal("system trust store unavailable")
	}
	if !roots.AppendCertsFromPEM(ca) {
		t.Fatal("invalid CA")
	}
	d := &LDAPDirectory{config: f.Config, tls: &tls.Config{RootCAs: roots, ServerName: f.ServerName, MinVersion: tls.VersionTLS12}}
	if err = d.resolveAdministration(); err != nil {
		t.Fatal("registered role unavailable")
	}
	type check struct {
		Name string `json:"name"`
		Pass bool   `json:"pass"`
	}
	checks := []check{}
	record := func(name string, pass bool) {
		checks = append(checks, check{name, pass})
		if !pass {
			t.Error(name)
		}
	}
	person := func(name string) Person {
		p, e := d.Person(f.Anchors[name])
		if e != nil || !strings.HasPrefix(p.Username, f.Prefix) {
			t.Fatal("tagged personnel unavailable", name)
		}
		return p
	}
	manager, domain, target := person("manager"), person("domain"), person("target")
	record("strict TLS and directory boundaries", d.Probe() == nil)
	record("platform role is separate from domain privilege", manager.Administrator && !manager.DomainRisk && domain.Administrator && domain.DomainRisk)
	people, err := d.People()
	record("personnel enumeration succeeds", err == nil)
	serviceSeen := false
	for _, p := range people {
		serviceSeen = serviceSeen || strings.HasPrefix(p.Username, "svc_portal_") || p.System
	}
	record("service and system accounts excluded", !serviceSeen)
	record("personnel in a child OU included", underDN(target.DN, d.config.UsersDN))
	_, outsideError := d.Person(f.Anchors["outside"])
	outsideSeen := false
	for _, p := range people {
		outsideSeen = outsideSeen || p.Anchor == f.Anchors["outside"]
	}
	record("personnel outside configured OU excluded from lookup and enumeration", outsideError != nil && !outsideSeen)
	original := profileOf(target)
	var wg sync.WaitGroup
	results := make([]string, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			name := []string{"Native A", "Native B"}[i]
			results[i] = d.Profile(target, Profile{name, "Test", name + " Test"})
		}(i)
	}
	wg.Wait()
	record("concurrent profile writes have one winner", results[0] == "saved" && results[1] == "conflict" || results[1] == "saved" && results[0] == "conflict")
	target = person("target")
	record("profile writer restores only test profile", d.Profile(target, original) == "saved")
	target = person("target")
	caseOnly := original
	caseOnly.GivenName = strings.ToUpper(original.GivenName)
	if caseOnly.GivenName != original.GivenName {
		record("case-only profile write succeeds", d.Profile(target, caseOnly) == "saved")
		stale := original
		stale.GivenName = "Native Later"
		record("AD old-value comparison also protects case-only concurrent edits", d.Profile(target, stale) == "conflict")
		fresh := person("target")
		record("case comparison fixture restored", d.Profile(fresh, original) == "saved")
	}
	conn, err := d.dial(d.config.ProfileBindDN, d.config.ProfileBindPassword)
	if err != nil {
		t.Fatal("profile writer unavailable")
	}
	r := ldap.NewModifyRequest("<GUID="+target.GUID+">", nil)
	r.Replace("mail", []string{"forbidden@example.invalid"})
	record("profile writer cannot edit email", writeResult(conn.Modify(r)) == "denied")
	conn.Close()
	protected := person("privileged")
	primaryOnly := false
	for _, g := range protected.Groups {
		if strings.HasSuffix(g.SID, "-512") {
			primaryOnly = g.Primary && !g.Direct && g.SID == protected.PrimaryGroupSID && protected.DomainRisk
		}
	}
	record("primary-only domain privilege is recognized", primaryOnly)
	record("ordinary reset writer cannot reset domain account", d.ResetPassword(protected, f.NewPassword, false) == "denied")
	groups, err := d.Groups()
	record("group registration resolves", err == nil)
	var builtin, unix Group
	for _, g := range groups {
		if g.SID == "S-1-5-32-544" {
			builtin = g
		}
		if g.Name == "Unix Admins" {
			unix = g
		}
	}
	record("registered Unix Admins is still domain-risk", unix.Managed && unix.DomainRisk)
	record("ordinary group writer cannot change system group", builtin.GUID != "" && d.Member(builtin, target, true) == "denied")
	op := AdminOperation{Action: "account.unlock"}
	record("platform role does not supply domain ACL rights", d.ExecuteAs(manager, f.ManagerPassword, op, protected, Group{}).Directory == "denied")
	record("another user's password cannot bind operator", d.ExecuteAs(domain, f.ManagerPassword, op, protected, Group{}).Directory == "credentials_rejected")
	staleActor := domain
	staleActor.DN = manager.DN
	record("stale or reused bind DN cannot substitute another principal", d.ExecuteAs(staleActor, f.ManagerPassword, op, protected, Group{}).Directory == "credentials_rejected")
	record("personal domain ACL operation succeeds", d.ExecuteAs(domain, f.Password, op, protected, Group{}).Directory == "saved")
	protected = person("privileged")
	old := profileOf(protected)
	op = AdminOperation{Action: "profile.update", Profile: Profile{"Domain", "Test", "Domain Test"}}
	record("personal domain profile operation succeeds", d.ExecuteAs(domain, f.Password, op, protected, Group{}).Directory == "saved")
	protected = person("privileged")
	record("profile writer can restore tagged privileged name", d.Profile(protected, old) == "saved")
	out := filepath.Join(filepath.Dir(path), "native-account-results.json")
	encoded, _ := json.MarshalIndent(map[string]any{"checks": checks}, "", "  ")
	if os.WriteFile(out, encoded, 0600) != nil {
		t.Fatal("cannot retain safe native results")
	}
}

// Creation runs against the shipped adapter and owned names only. The cleanup
// inventory is written before any Add, so unknown results are reconciled by
// identity/name rather than replaying a create or password operation.
func TestNativeAccountManagementCreation(t *testing.T) {
	path := os.Getenv("PORTAL_ACCOUNT_FIXTURE")
	if path == "" {
		t.Skip("owned account fixture not supplied")
	}
	if !strings.HasPrefix(path, "/home/whl/anas-portal-admin-") || filepath.Base(path) != "native.private.json" {
		t.Fatal("owned fixture path required")
	}
	var f struct {
		Config     Config `json:"config"`
		ServerName string `json:"server_name"`
		Prefix     string `json:"prefix"`
	}
	data, err := os.ReadFile(path)
	if err != nil || json.Unmarshal(data, &f) != nil || !regexp.MustCompile(`^pua[0-9a-f]{8}$`).MatchString(f.Prefix) {
		t.Fatal("owned creation fixture unavailable")
	}
	roots, err := x509.SystemCertPool()
	if err != nil {
		t.Fatal("system trust unavailable")
	}
	ca, err := os.ReadFile(f.Config.CAFile)
	if err != nil || !roots.AppendCertsFromPEM(ca) {
		t.Fatal("fixture CA unavailable")
	}
	d := &LDAPDirectory{config: f.Config, tls: &tls.Config{RootCAs: roots, ServerName: f.ServerName, MinVersion: tls.VersionTLS12}}
	type plannedAccount struct {
		Username string `json:"username"`
		Anchor   string `json:"anchor"`
		Binary   []byte `json:"-"`
	}
	if err := d.resolveAdministration(); err != nil {
		t.Fatal("registered role unavailable")
	}
	planned := []plannedAccount{}
	for _, suffix := range []string{"native0", "native1"} {
		binary := make([]byte, 16)
		if _, err := rand.Read(binary); err != nil {
			t.Fatal("identity unavailable")
		}
		anchor, _ := guidString(binary)
		planned = append(planned, plannedAccount{f.Prefix + suffix, anchor, binary})
	}
	encoded, _ := json.Marshal(map[string]any{"prefix": f.Prefix, "planned_accounts": planned})
	if os.WriteFile(filepath.Join(filepath.Dir(path), "native-created-users.private.json"), encoded, 0600) != nil {
		t.Fatal("cleanup inventory unavailable")
	}
	checks := map[string]bool{}
	for i, plannedAccount := range planned {
		username, binary, anchor := plannedAccount.Username, plannedAccount.Binary, plannedAccount.Anchor
		password := "Aa1!" + randomID()
		creator := &LDAPDirectory{config: d.config, tls: d.tls}
		if i == 1 {
			creator.config.MailDomain = "hlong.wang"
		}
		op := NewAccount{Username: username, Profile: Profile{"Native", "Create", "Native Create"}, ForceChange: i == 1}
		if result := creator.Create(op, anchor, binary, password); result != "saved" {
			t.Fatal("native create failed", result)
		}
		p, err := d.Person(anchor)
		if err != nil {
			t.Fatal("created principal unavailable")
		}
		checks[username+":mail_and_identity"] = p.Anchor == anchor && p.GUID != "" && p.Email == username+"@"+creator.config.MailDomain && equalDN(p.DN, "CN="+username+","+f.Config.UsersDN)
		reader, err := d.read()
		if err != nil {
			t.Fatal("UPN verification unavailable")
		}
		upn, err := reader.Search(ldap.NewSearchRequest("<GUID="+p.GUID+">", ldap.ScopeBaseObject, ldap.NeverDerefAliases, 1, 10, false, "(objectClass=user)", []string{"userPrincipalName"}, nil))
		reader.Close()
		checks[username+":mail_domain_does_not_rename_upn"] = err == nil && len(upn.Entries) == 1 && upn.Entries[0].GetAttributeValue("userPrincipalName") == username+"@"+f.Config.UPNSuffix
		checks[username+":ordinary_person"] = !p.Administrator && !p.DomainRisk && !p.System && strings.HasSuffix(p.PrimaryGroupSID, "-513")
		checks[username+":password_state"] = p.PasswordStateKnown && p.MustChangePassword == op.ForceChange
		op.Profile.DisplayName = "Must not replace existing account"
		checks[username+":conflict_without_reset"] = creator.Create(op, anchor, binary, "Aa1!"+randomID()) == "conflict"
		fresh, err := d.Person(anchor)
		checks[username+":conflict_preserves_identity_profile"] = err == nil && fresh.GUID == p.GUID && fresh.DisplayName == "Native Create"
		if !op.ForceChange {
			proof, err := d.dial(p.DN, password)
			if proof != nil {
				proof.Close()
			}
			checks[username+":initial_password_still_valid"] = err == nil
		}
	}
	reserved := NewAccount{Username: f.Prefix + "reserved", Profile: Profile{DisplayName: "Reserved"}}
	binary := make([]byte, 16)
	if _, err := rand.Read(binary); err != nil {
		t.Fatal("identity unavailable")
	}
	anchor, _ := guidString(binary)
	checks["registered_mail_collision_rejected"] = d.Create(reserved, anchor, binary, "Aa1!"+randomID()) == "conflict"
	result, _ := json.MarshalIndent(map[string]any{"checks": checks}, "", "  ")
	if os.WriteFile(filepath.Join(filepath.Dir(path), "native-creation-results.json"), result, 0600) != nil {
		t.Fatal("safe result unavailable")
	}
	for name, pass := range checks {
		if !pass {
			t.Error(name)
		}
	}
}
