package main

import (
	"os"
	"strings"
	"testing"
)

func TestCalcNextcloudUsesServiceContainerNames(t *testing.T) {
	env := map[string]string{
		"CONTAINER_PREFIX":  "anas_test_",
		"NEXTCLOUD_DB_TYPE": "postgres",
	}
	secrets := &secretStore{values: map[string]string{}}

	if _, err := calcNextcloud(env, "", secrets); err != nil {
		t.Fatal(err)
	}
	if got := env["NEXTCLOUD_IMAGINARY_HOSTNAME"]; got != "anas_test_nextcloud_imaginary" {
		t.Fatalf("imaginary hostname = %q", got)
	}
	if got := env["NEXTCLOUD_PUSH_HOSTNAME"]; got != "anas_test_nextcloud_push" {
		t.Fatalf("push hostname = %q", got)
	}
}

func TestNextcloudLocalizationUsesUpstreamCodes(t *testing.T) {
	env := map[string]string{
		"CONTAINER_PREFIX": "anas_", "NEXTCLOUD_DB_TYPE": "postgres",
		"DEFAULT_LANGUAGE": "zh-Hant-HK", "DEFAULT_LOCALE": "en-SG",
	}
	if _, err := calcNextcloud(env, "", &secretStore{values: map[string]string{}}); err != nil {
		t.Fatal(err)
	}
	if got := env["NEXTCLOUD_LANGUAGE"]; got != "zh_HK" && got != "zh_TW" {
		t.Fatalf("language = %q, want a traditional-Chinese upstream code", got)
	}
	if got := env["NEXTCLOUD_LOCALE"]; got != "en_SG" {
		t.Fatalf("locale = %q, want en_SG", got)
	}
}

func TestNextcloudWarnsAndFallsBackForExplicitUnsupportedLanguage(t *testing.T) {
	env := map[string]string{
		"NEXTCLOUD_DB_TYPE": "postgres", "NEXTCLOUD_LANGUAGE": "cy-GB", "DEFAULT_LOCALE": "en-US",
	}
	warnings, err := calcNextcloud(env, "", &secretStore{values: map[string]string{}})
	if err != nil {
		t.Fatalf("explicit unsupported language blocked processing: %v", err)
	}
	if len(warnings) != 1 || env["NEXTCLOUD_LANGUAGE"] == "cy-GB" {
		t.Fatalf("warnings = %v, fallback = %q", warnings, env["NEXTCLOUD_LANGUAGE"])
	}
}

func TestExplicitManagedAdminPasswordIsRejected(t *testing.T) {
	env := map[string]string{"NEXTCLOUD_DB_TYPE": "postgres", "NEXTCLOUD_ADMIN_PASSWORD": "legacy-cleartext"}
	_, err := calcNextcloud(env, "", &secretStore{values: map[string]string{}})
	if err == nil || !strings.Contains(err.Error(), "not accepted as configuration") {
		t.Fatalf("error = %v", err)
	}
}

func TestManagedBreakGlassPlaintextIsNotPublishedToDeploymentEnv(t *testing.T) {
	req := hookRequest{Module: "nextcloud", Phase: "calculate", Env: map[string]string{
		"NEXTCLOUD_DB_TYPE": "postgres", "NEXTCLOUD_LOCAL_ADMIN__BREAK_GLASS_USERNAME": "admin_nc",
		"NEXTCLOUD_LOCAL_ADMIN__BREAK_GLASS_PASSWORD": "must-stay-in-hook-input",
	}, Secrets: map[string]string{}}
	resp, err := handle(req)
	if err != nil {
		t.Fatal(err)
	}
	for key, value := range resp.Env {
		if strings.Contains(key, "LOCAL_ADMIN") && strings.Contains(key, "PASSWORD") || value == "must-stay-in-hook-input" {
			t.Fatalf("plaintext published as %s", key)
		}
	}
	if _, ok := resp.Env["NEXTCLOUD_ADMIN_PASSWORD"]; ok {
		t.Fatal("legacy admin password was published")
	}
}

func TestNextcloudPublishesDirectBreakGlassEntry(t *testing.T) {
	env := map[string]string{"NEXTCLOUD_DB_TYPE": "postgres", "NEXTCLOUD_DOMAIN_PREFIX": "nc", "BASE_DOMAIN": "example.test", "TRAEFIK_BASE_PORT": "443"}
	if _, err := calcNextcloud(env, "", &secretStore{values: map[string]string{}}); err != nil {
		t.Fatal(err)
	}
	if got := env["NEXTCLOUD_BREAK_GLASS_URL"]; got != "https://nc.example.test:443/login?direct=1" {
		t.Fatalf("URL = %q", got)
	}
}

func TestServiceKeysAreStableGeneratedSecrets(t *testing.T) {
	secrets := &secretStore{values: map[string]string{}}
	base := map[string]string{
		"DATA_PATH": "./data", "CONTAINER_PREFIX": "anas_", "NETWORK_PREFIX": "anas_",
		"NEXTCLOUD_DOMAIN_PREFIX": "nc", "BASE_DOMAIN": "nas.test", "TRAEFIK_BASE_PORT": "9000",
		"NEXTCLOUD_DB_TYPE": "postgres", "POSTGRES_NETWORK_NAME": "anas_postgres",
	}
	first := cloneMap(base)
	if _, err := calcNextcloud(first, "", secrets); err != nil {
		t.Fatal(err)
	}
	second := cloneMap(base)
	if _, err := calcNextcloud(second, "", secrets); err != nil {
		t.Fatal(err)
	}
	keys := []string{"NEXTCLOUD_TALK_INTERNAL_SECRET", "TALK_SIGNALING_SECRET", "NEXTCLOUD_IMAGINARY_SECRET"}
	for _, key := range keys {
		if first[key] == "" || first[key] != second[key] {
			t.Fatalf("%s is not stable", key)
		}
		if secrets.values[key] != first[key] {
			t.Fatalf("%s was not persisted", key)
		}
	}
	if first[keys[0]] == first[keys[1]] || first[keys[1]] == first[keys[2]] || first[keys[0]] == first[keys[2]] {
		t.Fatal("service secrets must be distinct")
	}
}

func TestLDAPIdentityUsesPrintableAnchorFromFirstInstall(t *testing.T) {
	env := map[string]string{
		"DATA_PATH": "./data", "CONTAINER_PREFIX": "anas_", "NETWORK_PREFIX": "anas_",
		"NEXTCLOUD_DOMAIN_PREFIX": "nc", "BASE_DOMAIN": "nas.test", "TRAEFIK_BASE_PORT": "9000",
		"NEXTCLOUD_DB_TYPE": "postgres", "POSTGRES_NETWORK_NAME": "anas_postgres",
		"SAMBA_DC_USER_CLASS_FILTER":         "(objectClass=user)",
		"SAMBA_DC_IDENTITY_ANCHOR_ATTRIBUTE": "anasIdentityAnchor",
		"SAMBA_DC_USER_LOGIN_ATTRS":          "sAMAccountName,userPrincipalName,mail",
		"SAMBA_DC_USER_ENABLED_FILTER":       "(!(userAccountControl:1.2.840.113556.1.4.803:=2))",
	}
	if _, err := calcNextcloud(env, "", &secretStore{values: map[string]string{}}); err != nil {
		t.Fatal(err)
	}
	if got, want := env["NEXTCLOUD_USER_FILTER"], "(&(objectClass=user)(anasIdentityAnchor=*))"; got != want {
		t.Fatalf("user filter = %q, want %q", got, want)
	}
	if got := env["NEXTCLOUD_USER_LOGIN_FILTER"]; strings.Contains(got, "objectGUID") {
		t.Fatalf("login filter must not accept the old identity attribute: %s", got)
	}
	if got := env["ANAS_IAM_CLIENT__NEXTCLOUD__ATTRIBUTES"]; !strings.Contains(got, "anasIdentityAnchor:anasIdentityAnchor:1") {
		t.Fatalf("IAM registration does not publish the printable identity anchor: %s", got)
	}
}

func TestNextcloudApplicationGroupsUseRecursiveMembership(t *testing.T) {
	env := map[string]string{
		"NEXTCLOUD_DB_TYPE":                  "postgres",
		"SAMBA_DC_APP_FILTER":                "true",
		"SAMBA_DC_USER_CLASS_FILTER":         "(objectClass=user)",
		"SAMBA_DC_IDENTITY_ANCHOR_ATTRIBUTE": "anasIdentityAnchor",
		"SAMBA_DC_BASE_APP_DN":               "OU=Apps,OU=Groups,DC=nas,DC=test",
		"SAMBA_DC_APP_ALL_DN":                "CN=APP_all,OU=Apps,OU=Groups,DC=nas,DC=test",
		"SAMBA_DC_ADMIN_GROUP_DN":            "CN=Admins,OU=Role,OU=Groups,DC=nas,DC=test",
	}
	if _, err := calcNextcloud(env, "", &secretStore{values: map[string]string{}}); err != nil {
		t.Fatal(err)
	}
	if got := env["NEXTCLOUD_USER_FILTER"]; strings.Count(got, "memberOf:1.2.840.113556.1.4.1941:=") != 3 {
		t.Fatalf("application group filter is not recursive for all alternatives: %s", got)
	}
}

func TestNextcloudOIDCIsDefaultAndPreservesLDAPProvisioning(t *testing.T) {
	secrets := &secretStore{values: map[string]string{}}
	env := map[string]string{
		"NEXTCLOUD_DB_TYPE":                  "postgres",
		"NEXTCLOUD_DOMAIN_PREFIX":            "nc",
		"BASE_DOMAIN":                        "example.test",
		"TRAEFIK_BASE_PORT":                  "9000",
		"SAMBA_DC_IDENTITY_ANCHOR_ATTRIBUTE": "anasIdentityAnchor",
		"SAMBA_DC_APP_FILTER":                "true",
		"SAMBA_DC_ADMIN_GROUP_NAME":          "Admins",
	}
	if _, err := calcNextcloud(env, "", secrets); err != nil {
		t.Fatal(err)
	}
	if got := env["ANAS_IAM_CLIENT__NEXTCLOUD__INTERFACE"]; got != "oidc" {
		t.Fatalf("interface = %q, want oidc", got)
	}
	if got := env["ANAS_IAM_CLIENT__NEXTCLOUD__REDIRECT_URIS"]; got != "https://nc.example.test:9000/apps/user_oidc/code" {
		t.Fatalf("redirect URI = %q", got)
	}
	if got := env["ANAS_IAM_CLIENT__NEXTCLOUD__ALLOW_GROUPS"]; got != "APP_nextcloud,APP_all,Admins" {
		t.Fatalf("allow groups = %q, want direct, all-app, or administrator access", got)
	}
	if got := env["APPS_LIST__NEXTCLOUD__ALLOW_GROUPS"]; got != "APP_nextcloud,APP_all,Admins" {
		t.Fatalf("launcher allow groups = %q, want the IAM policy group set", got)
	}
	if env["NEXTCLOUD_OIDC_CLIENT_SECRET"] == "" || secrets.values["NEXTCLOUD_OIDC_CLIENT_SECRET"] != env["NEXTCLOUD_OIDC_CLIENT_SECRET"] {
		t.Fatal("OIDC client secret was not generated and persisted")
	}
	if env["NEXTCLOUD_SAML_SP_PRIVATE_KEY"] != "" {
		t.Fatal("OIDC mode generated unused SAML private material")
	}
	task, err := os.ReadFile("../nextcloud/root/usr/local/bin/task.sh")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(task), "--mapping-uid=preferred_username") {
		t.Fatal("OIDC user ID must match the sAMAccountName-backed LDAP internal username")
	}
	if !strings.Contains(string(task), "--send-id-token-hint=1") {
		t.Fatal("OIDC logout must identify the session to the provider")
	}
	if !strings.Contains(string(task), `--postlogouturi="$NEXTCLOUD_DOMAIN_FULL"`) {
		t.Fatal("OIDC logout must return only to the registered Nextcloud URI")
	}
}

func TestNextcloudRequestsAuthentikSupportedWindowsNameID(t *testing.T) {
	task, err := os.ReadFile("../nextcloud/root/usr/local/bin/task.sh")
	if err != nil {
		t.Fatal(err)
	}
	got := string(task)
	want := "urn:oasis:names:tc:SAML:2.0:nameid-format:WindowsDomainQualifiedName"
	if !strings.Contains(got, want) {
		t.Fatalf("Nextcloud task does not request the supported NameID format %q", want)
	}
	if strings.Contains(got, "urn:oasis:names:tc:SAML:1.1:nameid-format:WindowsDomainQualifiedName") {
		t.Fatal("Nextcloud task still contains the unsupported SAML 1.1 Windows NameID format")
	}
	if !strings.Contains(got, `--saml-attribute-mapping-user_id_ldap_mapping="$SAMBA_DC_IDENTITY_ANCHOR_ATTRIBUTE"`) {
		t.Fatal("SAML login must resolve the anchor claim to the existing LDAP user")
	}
}

func TestSambaAdminsArePromotedAsTheDynamicNextcloudAdminGroup(t *testing.T) {
	task, err := os.ReadFile("../nextcloud/root/usr/local/bin/task.sh")
	if err != nil {
		t.Fatal(err)
	}
	got := string(task)
	if !strings.Contains(got, `occ ldap:promote-group --yes "$SAMBA_DC_ADMIN_GROUP_NAME"`) {
		t.Fatal("Samba Admins is not configured as Nextcloud's LDAP administrative group")
	}
	if strings.Contains(got, "occ group:adduser admin") || strings.Contains(got, "waiting_admin") {
		t.Fatal("bootstrap-only local admin membership must not replace the directory role mapping")
	}
}
