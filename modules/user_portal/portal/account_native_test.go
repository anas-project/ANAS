//go:build portal_native

// TEST_CASES: PORTAL-T-007
package main

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"os"
	"path/filepath"
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
	if c.PasswordBindPassword != "" || c.GroupBindPassword != "" || c.ProfileBindPassword != "" || f.ServerName == "" {
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
	if !strings.HasPrefix(f.Prefix, "pua") || len(f.Prefix) != 11 {
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
	record("cross-OU personnel included", !underDN(target.DN, d.config.UsersDN))
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
	record("platform role does not supply domain ACL rights", d.ExecuteAs(manager, f.ManagerPassword, op, protected, Group{}) == "denied")
	record("another user's password cannot bind operator", d.ExecuteAs(domain, f.ManagerPassword, op, protected, Group{}) == "credentials_rejected")
	staleActor := domain
	staleActor.DN = manager.DN
	record("stale or reused bind DN cannot substitute another principal", d.ExecuteAs(staleActor, f.ManagerPassword, op, protected, Group{}) == "credentials_rejected")
	record("personal domain ACL operation succeeds", d.ExecuteAs(domain, f.Password, op, protected, Group{}) == "saved")
	protected = person("privileged")
	old := profileOf(protected)
	op = AdminOperation{Action: "profile.update", Profile: Profile{"Domain", "Test", "Domain Test"}}
	record("personal domain profile operation succeeds", d.ExecuteAs(domain, f.Password, op, protected, Group{}) == "saved")
	protected = person("privileged")
	record("profile writer can restore tagged privileged name", d.Profile(protected, old) == "saved")
	out := filepath.Join(filepath.Dir(path), "native-account-results.json")
	encoded, _ := json.MarshalIndent(map[string]any{"checks": checks}, "", "  ")
	if os.WriteFile(out, encoded, 0600) != nil {
		t.Fatal("cannot retain safe native results")
	}
}
