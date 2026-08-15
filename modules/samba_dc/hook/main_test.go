package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode"
)

func TestStructureDoesNotGrantBootstrapAdminApplicationGroups(t *testing.T) {
	script, err := os.ReadFile(filepath.Join("..", "samba_dc", "root", "usr", "local", "bin", "structure.sh"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(script)
	for _, forbidden := range []string{
		`add_to_group "$SAMBA_DC_APP_ALL_NAME" "$SAMBA_DC_ADMIN_NAME"`,
		`add_to_group "APP_$name" "$SAMBA_DC_ADMIN_NAME"`,
	} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("bootstrap admin still receives an application group: %s", forbidden)
		}
	}
}

func TestCalcSambaDCHostIPDefaultsToHostIP(t *testing.T) {
	env := map[string]string{
		"BASE_DOMAIN":      "nas.test",
		"SERVER_NAME":      "fengoffice",
		"HOST_IP":          "192.0.2.10",
		"HOST_SUBNET_MASK": "24",
		"HOST_DNS_SERVER":  "192.0.2.1 1.1.1.1",
	}
	if err := calcSambaDC(env, "", &secretStore{values: map[string]string{}}); err != nil {
		t.Fatal(err)
	}
	if got := env["SAMBA_DC_HOST_IP"]; got != "192.0.2.10" {
		t.Fatalf("SAMBA_DC_HOST_IP = %q", got)
	}
	if got := env["SAMBA_DC_DNS_SERVER"]; got != "192.0.2.10" {
		t.Fatalf("SAMBA_DC_DNS_SERVER = %q", got)
	}
	if got := env["SAMBA_DC_DNS_FORWARDERS"]; got != "192.0.2.1; 1.1.1.1;" {
		t.Fatalf("SAMBA_DC_DNS_FORWARDERS = %q", got)
	}
	// Sibling modules query this resolver from Docker networks whose subnets are
	// allocated at run time, so the private space has to be allowed alongside
	// the LAN or those queries come back REFUSED.
	if got, want := env["SAMBA_DC_DNS_ALLOWED_NETWORKS"],
		"10.0.0.0/8; 172.16.0.0/12; 192.168.0.0/16; 192.0.2.0/24;"; got != want {
		t.Fatalf("SAMBA_DC_DNS_ALLOWED_NETWORKS = %q, want %q", got, want)
	}
}

func TestHostNetworkCIDRNormalizesHostAddress(t *testing.T) {
	if got, want := hostNetworkCIDR("192.168.127.117", "24"), "192.168.127.0/24"; got != want {
		t.Fatalf("hostNetworkCIDR() = %q, want %q", got, want)
	}
}

func TestDNSListAcceptsCommonSeparators(t *testing.T) {
	if got, want := dnsList("1.1.1.1;8.8.8.8, 9.9.9.9"), "1.1.1.1; 8.8.8.8; 9.9.9.9;"; got != want {
		t.Fatalf("dnsList() = %q, want %q", got, want)
	}
}

func TestCalcSambaDCHostIPCanBeOverridden(t *testing.T) {
	env := map[string]string{
		"BASE_DOMAIN":      "nas.test",
		"SERVER_NAME":      "fengoffice",
		"HOST_IP":          "10.254.0.2",
		"SAMBA_DC_HOST_IP": "10.254.0.1",
	}
	if err := calcSambaDC(env, "", &secretStore{values: map[string]string{}}); err != nil {
		t.Fatal(err)
	}
	if got := env["SAMBA_DC_HOST_IP"]; got != "10.254.0.1" {
		t.Fatalf("SAMBA_DC_HOST_IP = %q", got)
	}
}

func TestCalcSambaDCCreatesLeastPrivilegeLDAPBindIdentity(t *testing.T) {
	env := map[string]string{
		"BASE_DOMAIN":             "nas.test",
		"SERVER_NAME":             "fengoffice",
		"HOST_IP":                 "192.0.2.10",
		"SAMBA_DC_LDAP_BIND_NAME": "svc_ldap",
	}
	secrets := &secretStore{values: map[string]string{}}
	if err := calcSambaDC(env, "", secrets); err != nil {
		t.Fatal(err)
	}
	if got, want := env["SAMBA_DC_LDAP_BIND_DN"], "CN=svc_ldap,OU=Service Accounts,DC=nas,DC=test"; got != want {
		t.Fatalf("SAMBA_DC_LDAP_BIND_DN = %q, want %q", got, want)
	}
	if env["SAMBA_DC_LDAP_BIND_PASSWORD"] == "" {
		t.Fatal("SAMBA_DC_LDAP_BIND_PASSWORD was not generated")
	}
	if got := secrets.values["SAMBA_DC_LDAP_BIND_PASSWORD"]; got != env["SAMBA_DC_LDAP_BIND_PASSWORD"] {
		t.Fatal("generated LDAP bind password was not persisted in the secret store")
	}
}

func TestCalcSambaDCGeneratesIndependentAdministratorPasswords(t *testing.T) {
	env := map[string]string{
		"BASE_DOMAIN": "nas.test", "SERVER_NAME": "fengoffice", "HOST_IP": "192.0.2.10",
	}
	secrets := &secretStore{values: map[string]string{}}
	if err := calcSambaDC(env, "", secrets); err != nil {
		t.Fatal(err)
	}
	if env["SAMBA_DC_ADMIN_PASSWORD"] == env["SAMBA_DC_ADMINISTRATOR_PASSWORD"] {
		t.Fatal("routine admin and built-in Administrator must have independent passwords")
	}
	for _, key := range []string{"SAMBA_DC_ADMIN_PASSWORD", "SAMBA_DC_ADMINISTRATOR_PASSWORD", "SAMBA_DC_LDAP_BIND_PASSWORD", "SAMBA_DC_PASSWORD_BIND_PASSWORD"} {
		if !hasPasswordComplexity(env[key]) {
			t.Fatalf("generated %s does not satisfy enabled password complexity", key)
		}
	}
	if secrets.values["SAMBA_DC_ADMIN_PASSWORD"] != env["SAMBA_DC_ADMIN_PASSWORD"] || secrets.values["SAMBA_DC_ADMINISTRATOR_PASSWORD"] != env["SAMBA_DC_ADMINISTRATOR_PASSWORD"] {
		t.Fatal("administrator passwords were not persisted independently")
	}
}

func TestCalcSambaDCCreatesPasswordWriterBindIdentity(t *testing.T) {
	env := map[string]string{
		"BASE_DOMAIN":                 "nas.test",
		"SERVER_NAME":                 "fengoffice",
		"HOST_IP":                     "192.0.2.10",
		"SAMBA_DC_PASSWORD_BIND_NAME": "svc_password",
	}
	secrets := &secretStore{values: map[string]string{}}
	if err := calcSambaDC(env, "", secrets); err != nil {
		t.Fatal(err)
	}
	if got, want := env["SAMBA_DC_PASSWORD_BIND_DN"], "CN=svc_password,OU=Service Accounts,DC=nas,DC=test"; got != want {
		t.Fatalf("SAMBA_DC_PASSWORD_BIND_DN = %q, want %q", got, want)
	}
	if env["SAMBA_DC_PASSWORD_BIND_PASSWORD"] == "" {
		t.Fatal("SAMBA_DC_PASSWORD_BIND_PASSWORD was not generated")
	}
}

func TestCalcSambaDCCreatesIdentityAnchorWriter(t *testing.T) {
	env := map[string]string{
		"BASE_DOMAIN": "nas.test",
		"SERVER_NAME": "fengoffice",
		"HOST_IP":     "192.0.2.10",
	}
	secrets := &secretStore{values: map[string]string{}}
	if err := calcSambaDC(env, "", secrets); err != nil {
		t.Fatal(err)
	}
	if got, want := env["SAMBA_DC_ANCHOR_BIND_DN"], "CN=svc_anchor,OU=Service Accounts,DC=nas,DC=test"; got != want {
		t.Fatalf("SAMBA_DC_ANCHOR_BIND_DN = %q, want %q", got, want)
	}
	if got := env["SAMBA_DC_ANCHOR_BIND_PASSWORD"]; got == "" || secrets.values["SAMBA_DC_ANCHOR_BIND_PASSWORD"] != got {
		t.Fatal("anchor writer password was not generated and persisted")
	}
	if got := env["SAMBA_DC_IDENTITY_ANCHOR_BINARY_ATTRIBUTE"]; got != "mS-DS-ConsistencyGuid" {
		t.Fatalf("binary identity anchor attribute = %q", got)
	}
	if got := env["SAMBA_DC_IDENTITY_ANCHOR_ATTRIBUTE"]; got != "anasIdentityAnchor" {
		t.Fatalf("identity anchor attribute = %q", got)
	}
	if got, want := env["SAMBA_DC_ANCHOR_USER_BASES"], "OU=People,DC=nas,DC=test"; got != want {
		t.Fatalf("anchor user bases = %q, want %q", got, want)
	}
	if got, want := env["SAMBA_DC_GROUP_CLASS_FILTER"], "(&(objectClass=group)(anasIdentityAnchor=*))"; got != want {
		t.Fatalf("group filter = %q, want %q", got, want)
	}
}

func hasPasswordComplexity(password string) bool {
	var upper, lower, digit, symbol bool
	for _, r := range password {
		upper = upper || unicode.IsUpper(r)
		lower = lower || unicode.IsLower(r)
		digit = digit || unicode.IsDigit(r)
		symbol = symbol || unicode.IsPunct(r) || unicode.IsSymbol(r)
	}
	return upper && lower && digit && symbol
}

func TestCalcSambaDCPublishesDirectoryEventJournal(t *testing.T) {
	env := map[string]string{
		"BASE_DOMAIN": "nas.test",
		"SERVER_NAME": "fengoffice",
		"HOST_IP":     "192.0.2.10",
		"DATA_PATH":   "/srv/anas/data",
	}
	if err := calcSambaDC(env, "", &secretStore{values: map[string]string{}}); err != nil {
		t.Fatal(err)
	}
	// The raw Samba log and the normalized journal are separate directories so
	// the anchor worker never needs write access to the DC's own audit trail.
	if got, want := env["SAMBA_DC_AUDIT_PATH"], "/srv/anas/data/samba_dc/audit"; got != want {
		t.Fatalf("SAMBA_DC_AUDIT_PATH = %q, want %q", got, want)
	}
	if got, want := env["SAMBA_DC_EVENTS_PATH"], "/srv/anas/data/samba_dc/events"; got != want {
		t.Fatalf("SAMBA_DC_EVENTS_PATH = %q, want %q", got, want)
	}
	// Subscribers bind to the capability name, never to this module's own.
	if got, want := env["ANAS_DIRECTORY_EVENTS_DIR"], env["SAMBA_DC_EVENTS_PATH"]; got != want {
		t.Fatalf("ANAS_DIRECTORY_EVENTS_DIR = %q, want %q", got, want)
	}
	if got := env["SAMBA_DC_ANCHOR_EVENT_ATTRIBUTES"]; !strings.Contains(got, "member") ||
		!strings.Contains(got, "anasIdentityAnchor") {
		t.Fatalf("published attribute set = %q", got)
	}
	if got := env["SAMBA_DC_ANCHOR_EVENT_ATTRIBUTES"]; strings.Contains(got, "logonCount") {
		t.Fatalf("machine-account churn must not be publishable: %q", got)
	}
}
