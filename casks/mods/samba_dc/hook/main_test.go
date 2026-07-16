package main

import (
	"testing"
	"unicode"
)

func TestCalcSambaDCHostIPDefaultsToHostIP(t *testing.T) {
	env := map[string]string{
		"BASE_DOMAIN": "nas.test",
		"SERVER_NAME": "fengoffice",
		"HOST_IP":     "192.0.2.10",
	}
	if err := calcSambaDC(env, "", &secretStore{values: map[string]string{}}); err != nil {
		t.Fatal(err)
	}
	if got := env["SAMBA_DC_HOST_IP"]; got != "192.0.2.10" {
		t.Fatalf("SAMBA_DC_HOST_IP = %q", got)
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

func TestCalcSambaDCUsesDefaultServicePasswordForHumanAdministrators(t *testing.T) {
	env := map[string]string{
		"BASE_DOMAIN":                   "nas.test",
		"SERVER_NAME":                   "fengoffice",
		"HOST_IP":                       "192.0.2.10",
		"DEFAULT_SERVICE_ROOT_PASSWORD": "HumanAdmin1!",
	}
	secrets := &secretStore{values: map[string]string{}}
	if err := calcSambaDC(env, "", secrets); err != nil {
		t.Fatal(err)
	}
	if got := env["SAMBA_DC_ADMIN_PASSWORD"]; got != "HumanAdmin1!" {
		t.Fatalf("custom admin password = %q", got)
	}
	if got := env["SAMBA_DC_ADMINISTRATOR_PASSWORD"]; got != "HumanAdmin1!" {
		t.Fatalf("built-in Administrator password = %q", got)
	}
	for _, key := range []string{"SAMBA_DC_LDAP_BIND_PASSWORD", "SAMBA_DC_PASSWORD_BIND_PASSWORD"} {
		if !hasPasswordComplexity(env[key]) {
			t.Fatalf("generated %s does not satisfy enabled password complexity", key)
		}
	}
	if secrets.values["SAMBA_DC_ADMIN_PASSWORD"] != "" || secrets.values["SAMBA_DC_ADMINISTRATOR_PASSWORD"] != "" {
		t.Fatal("human administrator passwords must not be generated independently")
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
