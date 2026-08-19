package main

import (
	"os"
	"strings"
	"testing"
)

func TestLLNGSeparatedDomainContract(t *testing.T) {
	env := map[string]string{
		"BASE_DOMAIN":                            "apps.example.test",
		"LLNG_DOMAIN_PREFIX":                     "login",
		"LLNG_MANAGER_DOMAIN_PREFIX":             "login-manager",
		"TRAEFIK_BASE_PORT":                      "443",
		"LLNG_DB_TYPE":                           "postgres",
		"ANAS_IDENTITY_OIDC_CLIENTS":             "nextcloud",
		"ANAS_IAM_BINDING__NEXTCLOUD__INTERFACE": "oidc",
		"SAMBA_DC_LDAPS_SERVER_URL":              "ldaps://apps.example.test",
		"SAMBA_DC_LDAPS_PORT":                    "636",
		"SAMBA_DC_PASSWORD_BIND_DN":              "CN=svc_password,OU=Service Accounts,DC=ad,DC=identity,DC=test",
		"SAMBA_DC_PASSWORD_BIND_PASSWORD":        "directory-secret",
		"SAMBA_DC_BASE_USERS_DN":                 "OU=People,DC=ad,DC=identity,DC=test",
		"SAMBA_DC_BASE_GROUPS_DN":                "OU=Groups,DC=ad,DC=identity,DC=test",
		"SAMBA_DC_USER_CLASS_FILTER":             "(objectClass=user)",
		"SAMBA_DC_USER_ENABLED_FILTER":           "(!(userAccountControl:1.2.840.113556.1.4.803:=2))",
		"SAMBA_DC_USER_NAME":                     "sAMAccountName",
		"SAMBA_DC_USER_EMAIL":                    "mail",
	}

	if err := identityCalc("LLNG")(env, &secretStore{values: map[string]string{}}); err != nil {
		t.Fatal(err)
	}
	if got, want := env["LLNG_DOMAIN_FULL"], "https://login.apps.example.test:443"; got != want {
		t.Fatalf("LLNG portal URL = %q, want %q", got, want)
	}
	if got, want := env["LLNG_MANAGER_DOMAIN_FULL"], "https://login-manager.apps.example.test:443"; got != want {
		t.Fatalf("LLNG manager URL = %q, want %q", got, want)
	}
	if got, want := env["ANAS_IAM_BINDING__NEXTCLOUD__OIDC_ISSUER_URL"], "https://login.apps.example.test:443"; got != want {
		t.Fatalf("LLNG OIDC issuer = %q, want %q", got, want)
	}
	for key, want := range map[string]string{
		"SAMBA_DC_LDAPS_SERVER_URL": "ldaps://apps.example.test",
		"SAMBA_DC_PASSWORD_BIND_DN": "CN=svc_password,OU=Service Accounts,DC=ad,DC=identity,DC=test",
		"SAMBA_DC_BASE_USERS_DN":    "OU=People,DC=ad,DC=identity,DC=test",
		"SAMBA_DC_BASE_GROUPS_DN":   "OU=Groups,DC=ad,DC=identity,DC=test",
	} {
		if got := env[key]; got != want {
			t.Errorf("%s = %q, want Samba export %q", key, got, want)
		}
	}

	template, err := os.ReadFile("../llng/root/root/lmConf.json")
	if err != nil {
		t.Fatal(err)
	}
	contents := string(template)
	for _, want := range []string{
		`"domain": "{{BASE_DOMAIN}}"`,
		`"portal": "{{LLNG_DOMAIN_FULL}}/"`,
		`"ldapServer": "{{SAMBA_DC_LDAPS_SERVER_URL}}"`,
		`"managerDn": "{{SAMBA_DC_PASSWORD_BIND_DN}}"`,
		`"ldapBase": "{{SAMBA_DC_BASE_USERS_DN}}"`,
		`"ldapGroupBase": "{{SAMBA_DC_BASE_GROUPS_DN}}"`,
		`"userPrincipalName": "userPrincipalName"`,
	} {
		if !strings.Contains(contents, want) {
			t.Errorf("LLNG render contract is missing %q", want)
		}
	}
	if strings.Contains(contents, `"ldapBase": "{{BASE_DOMAIN}}`) ||
		strings.Contains(contents, `"managerDn": "{{BASE_DOMAIN}}`) {
		t.Fatal("LLNG LDAP configuration must not derive directory identity from BASE_DOMAIN")
	}
}
