package main

import (
	"os"
	"strings"
	"testing"
)

func TestManifestProvidesSambaPasswordPolicyToAuthentik(t *testing.T) {
	manifest, err := os.ReadFile("../module.yml")
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{
		"SAMBA_DC_USER_COMPLEX_PASS", "SAMBA_DC_USER_MIN_PASS_AGE",
		"SAMBA_DC_USER_MIN_PASS_LENGTH", "SAMBA_DC_USER_PASSWORD_HISTORY",
	} {
		if !strings.Contains(string(manifest), "- "+key) {
			t.Fatalf("authentik manifest does not consume %s", key)
		}
	}
}

func TestDirectoryBlueprintUsesEnvironmentContracts(t *testing.T) {
	blueprint := renderDirectoryBlueprint()
	for _, key := range []string{
		"AUTHENTIK_LDAP_SERVER_URI",
		"AUTHENTIK_LDAP_BIND_DN",
		"AUTHENTIK_LDAP_BIND_PASSWORD",
		"AUTHENTIK_LDAP_OBJECT_UNIQUENESS_FIELD",
		"AUTHENTIK_LDAP_PASSWORD_WRITEBACK",
		"AUTHENTIK_PASSWORD_MIN_LENGTH",
		"AUTHENTIK_PASSWORD_POLICY_ERROR",
		"AUTHENTIK_PASSWORD_POLICY_GUIDANCE",
	} {
		if !strings.Contains(blueprint, "!Env "+key) {
			t.Fatalf("directory blueprint does not consume %s through !Env", key)
		}
	}
	for _, literal := range []string{"svc_password", "DC=", "ldaps://"} {
		if strings.Contains(blueprint, literal) {
			t.Fatalf("directory blueprint contains deployment value %q", literal)
		}
	}
	if !strings.Contains(blueprint, "authentik.sources.ldap.auth.LDAPBackend") ||
		!strings.Contains(blueprint, "authentik.core.auth.InbuiltBackend") {
		t.Fatal("password stage must retain LDAP for AD users and inbuilt auth for akadmin")
	}
	if !strings.Contains(blueprint, "default-password-change-password-policy") ||
		!strings.Contains(blueprint, "default-password-change-prompt") ||
		!strings.Contains(blueprint, "type: alert_info") ||
		!strings.Contains(blueprint, "check_zxcvbn: false") {
		t.Fatal("directory blueprint must synchronize and explain the Samba password policy")
	}
	if !strings.Contains(blueprint, `peer_certificate: !Find [authentik_crypto.certificatekeypair, [name, "anas-samba-ad-ca"]]`) {
		t.Fatal("Samba AD source must verify LDAPS with the imported ANAS CA")
	}
	if strings.Contains(blueprint, "sni: true") {
		t.Fatal("explicit SNI must stay disabled because authentik passes the full LDAPS URI as the SNI name")
	}
	if !strings.Contains(blueprint, `"is_superuser": group_name == "Admins"`) ||
		!strings.Contains(blueprint, "- !KeyOf samba-ad-group-role-mapping") {
		t.Fatal("the exact Samba AD Admins group must be the only synchronized superuser role")
	}
	if !strings.Contains(blueprint, `display_name = list_flatten(ldap.get("displayName"))`) ||
		!strings.Contains(blueprint, "- !KeyOf samba-ad-user-display-name-mapping") {
		t.Fatal("Samba displayName must be the Authentik user name used by OIDC profile claims")
	}
}

func TestCalcAuthentikUsesPasswordServiceAccount(t *testing.T) {
	env := map[string]string{
		"AUTHENTIK_DOMAIN_PREFIX":            "auth",
		"BASE_DOMAIN":                        "nas.example",
		"TRAEFIK_BASE_PORT":                  "443",
		"AUTHENTIK_DB_TYPE":                  "postgres",
		"AUTHENTIK_DB_NAME":                  "authentik",
		"POSTGRES_NETWORK_NAME":              "anas_postgres",
		"POSTGRES_HOST":                      "postgres",
		"POSTGRES_PORT":                      "5432",
		"POSTGRES_USERNAME":                  "postgres",
		"POSTGRES_PASSWORD":                  "db-password",
		"AUTHENTIK_LDAP_ENABLED":             "true",
		"SAMBA_DC_LDAPS_SERVER_URL_PORT":     "ldaps://dc.nas.example:636",
		"SAMBA_DC_PASSWORD_BIND_DN":          "CN=svc_password,OU=Service Accounts,DC=nas,DC=example",
		"SAMBA_DC_PASSWORD_BIND_PASSWORD":    "writeback-password",
		"SAMBA_DC_BASE_DN":                   "DC=nas,DC=example",
		"SAMBA_DC_BASE_USERS_DN_PREFIX":      "OU=People",
		"SAMBA_DC_BASE_GROUPS_DN_PREFIX":     "OU=Groups",
		"SAMBA_DC_GROUP_CLASS_FILTER":        "(objectClass=group)",
		"SAMBA_DC_IDENTITY_ANCHOR_ATTRIBUTE": "anasIdentityAnchor",
		"AUTHENTIK_LDAP_PASSWORD_WRITEBACK":  "true",
		"SAMBA_DC_USER_COMPLEX_PASS":         "true",
		"SAMBA_DC_USER_MIN_PASS_AGE":         "1",
		"SAMBA_DC_USER_MIN_PASS_LENGTH":      "12",
		"SAMBA_DC_USER_PASSWORD_HISTORY":     "8",
		"DEFAULT_LANGUAGE":                   "zh-Hans",
		"EMAIL":                              "admin@nas.example",
	}
	secrets := &secretStore{values: map[string]string{}}
	if err := calcAuthentik(env, secrets); err != nil {
		t.Fatal(err)
	}
	if env["AUTHENTIK_LDAP_BIND_DN"] != env["SAMBA_DC_PASSWORD_BIND_DN"] ||
		env["AUTHENTIK_LDAP_BIND_PASSWORD"] != env["SAMBA_DC_PASSWORD_BIND_PASSWORD"] {
		t.Fatal("authentik LDAP writeback did not use samba_dc svc_password credentials")
	}
	if got := env["AUTHENTIK_LDAP_OBJECT_UNIQUENESS_FIELD"]; got != "anasIdentityAnchor" {
		t.Fatalf("authentik uniqueness field = %q", got)
	}
	if !strings.Contains(env["AUTHENTIK_LDAP_USER_OBJECT_FILTER"], "(anasIdentityAnchor=*)") {
		t.Fatalf("authentik user filter does not require the identity anchor: %s", env["AUTHENTIK_LDAP_USER_OBJECT_FILTER"])
	}
	if got := env["AUTHENTIK_PASSWORD_MIN_LENGTH"]; got != "12" {
		t.Fatalf("authentik minimum password length = %q, want Samba value 12", got)
	}
	for _, want := range []string{"至少 12 个字符", "五类中的三类", "最近 8 个密码", "间隔 1 天"} {
		if !strings.Contains(env["AUTHENTIK_PASSWORD_POLICY_GUIDANCE"], want) {
			t.Fatalf("Chinese password guidance %q missing %q", env["AUTHENTIK_PASSWORD_POLICY_GUIDANCE"], want)
		}
	}
	if secrets.values["AUTHENTIK_BREAK_GLASS_PASSWORD"] != "" || env["AUTHENTIK_BOOTSTRAP_PASSWORD"] != "" {
		t.Fatal("legacy break-glass plaintext entered module-generated secrets or deployment env")
	}
}

func TestPasswordPolicyGuidanceOmitsDisabledRules(t *testing.T) {
	got := passwordPolicyGuidance("en", 14, 0, 0, false)
	if !strings.Contains(got, "at least 14 characters") {
		t.Fatalf("English guidance = %q", got)
	}
	for _, unwanted := range []string{"three of", "last 0", "days apart"} {
		if strings.Contains(got, unwanted) {
			t.Fatalf("English guidance %q contains disabled rule %q", got, unwanted)
		}
	}
}

func TestPasswordPolicyRejectsInvalidSambaValues(t *testing.T) {
	env := map[string]string{
		"SAMBA_DC_USER_MIN_PASS_LENGTH":  "twelve",
		"SAMBA_DC_USER_PASSWORD_HISTORY": "8",
		"SAMBA_DC_USER_MIN_PASS_AGE":     "1",
	}
	if err := calcPasswordPolicy(env); err == nil || !strings.Contains(err.Error(), "SAMBA_DC_USER_MIN_PASS_LENGTH") {
		t.Fatalf("invalid Samba policy error = %v", err)
	}
}
