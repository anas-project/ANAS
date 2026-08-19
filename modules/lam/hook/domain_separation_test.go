package main

import (
	"strings"
	"testing"
)

func TestLAMSeparatedDomainContract(t *testing.T) {
	directory := map[string]string{
		"SAMBA_DC_DOMAIN":             "ad.identity.test",
		"SAMBA_DC_LDAPS_SERVER_URL":   "ldaps://apps.example.test",
		"SAMBA_DC_BASE_DN":            "DC=ad,DC=identity,DC=test",
		"SAMBA_DC_BASE_USERS_DN":      "OU=People,DC=ad,DC=identity,DC=test",
		"SAMBA_DC_BASE_GROUPS_DN":     "OU=Groups,DC=ad,DC=identity,DC=test",
		"SAMBA_DC_BASE_COMPUTERS_DN":  "OU=Computers,DC=ad,DC=identity,DC=test",
		"SAMBA_DC_ADMIN_GROUP_DN":     "CN=Admins,OU=Groups,DC=ad,DC=identity,DC=test",
		"SAMBA_DC_LDAP_BIND_DN":       "CN=svc_ldap,OU=Service Accounts,DC=ad,DC=identity,DC=test",
		"SAMBA_DC_LDAP_BIND_PASSWORD": "directory-secret",
	}
	env := map[string]string{
		"BASE_DOMAIN":        "apps.example.test",
		"LAM_DOMAIN_PREFIX":  "accounts",
		"DEFAULT_LANGUAGE":   "en-US",
		"LAM_ADMIN_PASSWORD": "Lam-Test-1!",
	}
	for key, value := range directory {
		env[key] = value
	}

	if _, err := calcLAM(env, "", &secretStore{values: map[string]string{}}); err != nil {
		t.Fatal(err)
	}
	if got, want := env["LAM_DOMAIN"], "accounts.apps.example.test"; got != want {
		t.Fatalf("LAM web domain = %q, want %q", got, want)
	}
	for key, want := range directory {
		if got := env[key]; got != want {
			t.Errorf("%s = %q, want Samba export %q", key, got, want)
		}
	}

	configuration := readLAMFile(t, "../lam/configure.php")
	for _, want := range []string{
		"getenv('SAMBA_DC_LDAPS_SERVER_URL')",
		"getenv('SAMBA_DC_DOMAIN')",
		"getenv('SAMBA_DC_BASE_DN')",
		"getenv('SAMBA_DC_BASE_USERS_DN')",
		"getenv('SAMBA_DC_BASE_GROUPS_DN')",
		"getenv('SAMBA_DC_LDAP_BIND_DN')",
	} {
		if !strings.Contains(configuration, want) {
			t.Errorf("LAM directory render contract is missing %q", want)
		}
	}
	if strings.Contains(configuration, "getenv('BASE_DOMAIN')") {
		t.Fatal("LAM directory configuration must not derive LDAP settings from BASE_DOMAIN")
	}
}
