package main

import (
	"strings"
	"testing"
)

func TestAuthentikSeparatedDomainContract(t *testing.T) {
	directory := map[string]string{
		"SAMBA_DC_LDAPS_SERVER_URL_PORT":     "ldaps://apps.example.test:636",
		"SAMBA_DC_PASSWORD_BIND_DN":          "CN=svc_password,OU=Service Accounts,DC=ad,DC=identity,DC=test",
		"SAMBA_DC_PASSWORD_BIND_PASSWORD":    "directory-secret",
		"SAMBA_DC_BASE_DN":                   "DC=ad,DC=identity,DC=test",
		"SAMBA_DC_BASE_USERS_DN_PREFIX":      "OU=People",
		"SAMBA_DC_BASE_GROUPS_DN_PREFIX":     "OU=Groups",
		"SAMBA_DC_GROUP_CLASS_FILTER":        "(objectClass=group)",
		"SAMBA_DC_IDENTITY_ANCHOR_ATTRIBUTE": "anasIdentityAnchor",
		"SAMBA_DC_USER_COMPLEX_PASS":         "true",
		"SAMBA_DC_USER_MIN_PASS_AGE":         "1",
		"SAMBA_DC_USER_MIN_PASS_LENGTH":      "12",
		"SAMBA_DC_USER_PASSWORD_HISTORY":     "8",
	}
	env := map[string]string{
		"BASE_DOMAIN":                            "apps.example.test",
		"AUTHENTIK_DOMAIN_PREFIX":                "login",
		"TRAEFIK_BASE_PORT":                      "443",
		"AUTHENTIK_DB_TYPE":                      "postgres",
		"AUTHENTIK_LDAP_ENABLED":                 "true",
		"DEFAULT_LANGUAGE":                       "en",
		"ANAS_IDENTITY_OIDC_CLIENTS":             "nextcloud",
		"ANAS_IAM_BINDING__NEXTCLOUD__INTERFACE": "oidc",
	}
	for key, value := range directory {
		env[key] = value
	}

	if err := calcAuthentik(env, &secretStore{values: map[string]string{}}); err != nil {
		t.Fatal(err)
	}
	if got, want := env["AUTHENTIK_DOMAIN_FULL"], "https://login.apps.example.test:443"; got != want {
		t.Fatalf("Authentik web URL = %q, want %q", got, want)
	}
	if got, want := env["ANAS_IAM_BINDING__NEXTCLOUD__OIDC_ISSUER_URL"], "https://login.apps.example.test:443/application/o/nextcloud/"; got != want {
		t.Fatalf("Authentik OIDC issuer = %q, want %q", got, want)
	}
	checks := map[string]string{
		"AUTHENTIK_LDAP_SERVER_URI":          directory["SAMBA_DC_LDAPS_SERVER_URL_PORT"],
		"AUTHENTIK_LDAP_BIND_DN":             directory["SAMBA_DC_PASSWORD_BIND_DN"],
		"AUTHENTIK_LDAP_BIND_PASSWORD":       directory["SAMBA_DC_PASSWORD_BIND_PASSWORD"],
		"AUTHENTIK_LDAP_BASE_DN":             directory["SAMBA_DC_BASE_DN"],
		"AUTHENTIK_LDAP_ADDITIONAL_USER_DN":  directory["SAMBA_DC_BASE_USERS_DN_PREFIX"],
		"AUTHENTIK_LDAP_ADDITIONAL_GROUP_DN": directory["SAMBA_DC_BASE_GROUPS_DN_PREFIX"],
	}
	for key, want := range checks {
		if got := env[key]; got != want {
			t.Errorf("%s = %q, want Samba export %q", key, got, want)
		}
	}

	blueprint := renderDirectoryBlueprint()
	for _, want := range []string{
		"server_uri: !Env AUTHENTIK_LDAP_SERVER_URI",
		"bind_cn: !Env AUTHENTIK_LDAP_BIND_DN",
		"base_dn: !Env AUTHENTIK_LDAP_BASE_DN",
		"additional_user_dn: !Env AUTHENTIK_LDAP_ADDITIONAL_USER_DN",
		"authentik default Active Directory Mapping: userPrincipalName",
	} {
		if !strings.Contains(blueprint, want) {
			t.Errorf("Authentik directory blueprint is missing %q", want)
		}
	}
	if strings.Contains(blueprint, "BASE_DOMAIN") {
		t.Fatal("Authentik directory blueprint must not derive LDAP settings from BASE_DOMAIN")
	}
}
