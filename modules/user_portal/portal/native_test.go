//go:build portal_native

// TEST_CASES: PORTAL-T-005
package main

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-ldap/ldap/v3"
)

// This exercises the shipped directory adapter against a separately prepared
// isolated Samba fixture. Credentials are read only from its private input.
func TestNativeDirectoryBoundary(t *testing.T) {
	input := os.Getenv("PORTAL_NATIVE_FIXTURE")
	if input == "" {
		t.Skip("isolated fixture not supplied")
	}
	root := filepath.Dir(filepath.Dir(input))
	if !strings.HasPrefix(root, "/home/whl/anas-user-portal-e2e-test-") || filepath.Base(input) != "ldap.json" {
		t.Fatal("isolated fixture required")
	}
	var f struct {
		Host, Server, CA, Base, Old, New string
		Services                    map[string]struct{ DN, Password string }
		Anchors                     map[string]string
	}
	data, err := os.ReadFile(input)
	if err != nil {
		t.Fatal("private fixture unavailable")
	}
	var raw map[string]json.RawMessage
	if json.Unmarshal(data, &raw) != nil {
		t.Fatal("invalid fixture")
	}
	json.Unmarshal(raw["host"], &f.Host)
	json.Unmarshal(raw["server_name"], &f.Server)
	json.Unmarshal(raw["ca"], &f.CA)
	json.Unmarshal(raw["base"], &f.Base)
	json.Unmarshal(raw["new"], &f.New)
	json.Unmarshal(raw["old"], &f.Old)
	json.Unmarshal(raw["services"], &f.Services)
	json.Unmarshal(raw["anchors"], &f.Anchors)
	if f.Base != "DC=portal,DC=test" {
		t.Fatal("test directory required")
	}
	roots := x509.NewCertPool()
	ca, err := os.ReadFile(f.CA)
	if err != nil || !roots.AppendCertsFromPEM(ca) {
		t.Fatal("fixture CA unavailable")
	}
	c := Config{LDAPURL: "ldaps://" + f.Host + ":636", LDAPBindDN: f.Services["readonly"].DN, LDAPBindPassword: f.Services["readonly"].Password, PasswordBindDN: f.Services["reset"].DN, PasswordBindPassword: f.Services["reset"].Password, GroupBindDN: f.Services["group"].DN, GroupBindPassword: f.Services["group"].Password, BaseDN: f.Base, UsersDN: "OU=People," + f.Base, GroupsDN: "OU=Groups," + f.Base, AnchorAttribute: "anasIdentityAnchor"}
	d := &LDAPDirectory{config: c, tls: &tls.Config{RootCAs: roots, ServerName: f.Server, MinVersion: tls.VersionTLS12}}
	type result struct {
		Name string `json:"name"`
		Pass bool   `json:"pass"`
		Code uint16 `json:"ldap_code,omitempty"`
	}
	results := []result{}
	record := func(name string, pass bool, err error) {
		code := uint16(0)
		var e *ldap.Error
		if errors.As(err, &e) {
			code = e.ResultCode
		}
		results = append(results, result{name, pass, code})
		if !pass {
			t.Errorf("native boundary check failed: %s (code %d)", name, code)
		}
	}
	record("strict TLS read succeeds", d.Probe() == nil, nil)
	for _, test := range []struct {
		name string
		cfg  *tls.Config
	}{{"wrong CA refused", &tls.Config{RootCAs: x509.NewCertPool(), ServerName: f.Server, MinVersion: tls.VersionTLS12}}, {"wrong server name refused", &tls.Config{RootCAs: roots, ServerName: "invalid.example.invalid", MinVersion: tls.VersionTLS12}}} {
		bad := &LDAPDirectory{config: c, tls: test.cfg}
		conn, err := bad.read()
		if conn != nil {
			conn.Close()
		}
		record(test.name, err != nil, err)
	}
	bad := &LDAPDirectory{config: c, tls: d.tls}
	parsed, _ := url.Parse(c.LDAPURL)
	parsed.Scheme = "ldap"
	bad.config.LDAPURL = parsed.String()
	conn, err := bad.read()
	if conn != nil {
		conn.Close()
	}
	record("plaintext refused", err != nil, err)
	a, err := d.Person(f.Anchors["pscopea"])
	if err != nil {
		t.Fatal("scoped target unavailable")
	}
	b, err := d.Person(f.Anchors["pscopeb"])
	if err != nil {
		t.Fatal("outside target unavailable")
	}
	record("current normal password state is confirmed", b.canLogin(), nil)
	proof, proofErr := d.dial(b.DN, f.Old)
	if proof != nil {
		proof.Close()
	}
	record("original fixture password proves native identity", proofErr == nil, proofErr)
	allowed, err := d.Group(f.Anchors["PortalAllowed"])
	if err != nil {
		t.Fatal("allowed group unavailable")
	}
	other, err := d.Group(f.Anchors["PortalOther"])
	if err != nil {
		t.Fatal("other group unavailable")
	}
	record("reset outside delegated OU denied", d.ResetPassword(b, "Aa1!"+randomID(), false) == "rejected", nil)
	record("group binding outside group denied", d.Member(other, a, true) == "rejected", nil)
	modify := func(service, object, attribute string, values []string) error {
		bind := f.Services[service]
		conn, err := d.dial(bind.DN, bind.Password)
		if err != nil {
			return err
		}
		defer conn.Close()
		m := ldap.NewModifyRequest("<GUID="+object+">", nil)
		m.Replace(attribute, values)
		return conn.Modify(m)
	}
	for _, item := range []struct {
		name, service, object, attribute string
		values                           []string
	}{{"reset binding cannot enable account", "reset", a.GUID, "userAccountControl", []string{"512"}}, {"reset binding cannot change profile", "reset", a.GUID, "mail", []string{"forbidden@portal.test"}}, {"group binding cannot change other attribute", "group", allowed.GUID, "description", []string{"forbidden"}}, {"readonly binding cannot reset password", "readonly", b.GUID, "unicodePwd", []string{passwordValue("Aa1!" + randomID())}}} {
		err := modify(item.service, item.object, item.attribute, item.values)
		var e *ldap.Error
		record(item.name, errors.As(err, &e) && e.ResultCode == ldap.LDAPResultInsufficientAccessRights, err)
	}
	bind := f.Services["shared_reset"]
	conn, err = d.dial(bind.DN, bind.Password)
	if err != nil {
		t.Fatal("shared fixture bind unavailable")
	}
	defer conn.Close()
	m := ldap.NewModifyRequest("<GUID="+allowed.GUID+">", nil)
	m.Add("member", []string{"<GUID=" + b.GUID + ">"})
	err = conn.Modify(m)
	var le *ldap.Error
	record("existing shared password binding has no group write", errors.As(err, &le) && le.ResultCode == ldap.LDAPResultInsufficientAccessRights, err)
	report, _ := json.MarshalIndent(map[string]any{"requirements": []string{"PORTAL-R-016", "PORTAL-R-017"}, "results": results}, "", "  ")
	if os.WriteFile(filepath.Join(root, "evidence/native-directory-boundary.json"), append(report, '\n'), 0600) != nil {
		t.Fatal("cannot preserve native evidence")
	}
}
