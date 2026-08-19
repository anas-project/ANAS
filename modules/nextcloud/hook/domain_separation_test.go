package main

import (
	"os"
	"strings"
	"testing"
)

func TestNextcloudSeparatedDomainContract(t *testing.T) {
	directory := map[string]string{
		"SAMBA_DC_LDAPS_SERVER_URL":          "ldaps://apps.example.test",
		"SAMBA_DC_LDAPS_PORT":                "636",
		"SAMBA_DC_PASSWORD_BIND_DN":          "CN=svc_password,OU=Service Accounts,DC=ad,DC=identity,DC=test",
		"SAMBA_DC_PASSWORD_BIND_PASSWORD":    "directory-secret",
		"SAMBA_DC_BASE_DN":                   "DC=ad,DC=identity,DC=test",
		"SAMBA_DC_BASE_USERS_DN":             "OU=People,DC=ad,DC=identity,DC=test",
		"SAMBA_DC_BASE_GROUPS_ROLE_DN":       "OU=Role,OU=Groups,DC=ad,DC=identity,DC=test",
		"SAMBA_DC_USER_CLASS_FILTER":         "(objectClass=user)",
		"SAMBA_DC_USER_ENABLED_FILTER":       "(!(userAccountControl:1.2.840.113556.1.4.803:=2))",
		"SAMBA_DC_USER_LOGIN_ATTRS":          "sAMAccountName,userPrincipalName,mail",
		"SAMBA_DC_IDENTITY_ANCHOR_ATTRIBUTE": "anasIdentityAnchor",
	}
	env := map[string]string{
		"BASE_DOMAIN":             "apps.example.test",
		"NEXTCLOUD_DOMAIN_PREFIX": "cloud",
		"TRAEFIK_BASE_PORT":       "443",
		"NEXTCLOUD_DB_TYPE":       "postgres",
		"DEFAULT_LANGUAGE":        "en",
		"DEFAULT_LOCALE":          "en-US",
	}
	for key, value := range directory {
		env[key] = value
	}

	if _, err := calcNextcloud(env, "", &secretStore{values: map[string]string{}}); err != nil {
		t.Fatal(err)
	}
	if got, want := env["NEXTCLOUD_DOMAIN_FULL"], "https://cloud.apps.example.test:443"; got != want {
		t.Fatalf("Nextcloud web URL = %q, want %q", got, want)
	}
	if got := env["ANAS_IAM_CLIENT__NEXTCLOUD__REDIRECT_URIS"]; got != "https://cloud.apps.example.test:443/apps/user_oidc/code" {
		t.Fatalf("Nextcloud OIDC redirect URI = %q", got)
	}
	for key, want := range directory {
		if got := env[key]; got != want {
			t.Errorf("%s = %q, want Samba export %q", key, got, want)
		}
	}
	if got := env["NEXTCLOUD_USER_LOGIN_FILTER"]; !strings.Contains(got, "(userPrincipalName=%uid)") {
		t.Fatalf("Nextcloud login filter does not preserve the Samba UPN attribute: %s", got)
	}

	task, err := os.ReadFile("../nextcloud/root/usr/local/bin/task.sh")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`set_ldap_config ldapHost "$SAMBA_DC_LDAPS_SERVER_URL"`,
		`set_ldap_config ldapAgentName "$SAMBA_DC_PASSWORD_BIND_DN"`,
		`set_ldap_config ldapBase "$SAMBA_DC_BASE_DN"`,
		`set_ldap_config ldapBaseUsers "$SAMBA_DC_BASE_USERS_DN"`,
		`set_ldap_config ldapBaseGroups "$SAMBA_DC_BASE_GROUPS_ROLE_DN"`,
	} {
		if !strings.Contains(string(task), want) {
			t.Errorf("Nextcloud LDAP render contract is missing %q", want)
		}
	}
}
