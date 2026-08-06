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

	if err := calcNextcloud(env, "", secrets); err != nil {
		t.Fatal(err)
	}
	if got := env["NEXTCLOUD_IMAGINARY_HOSTNAME"]; got != "anas_test_nextcloud_imaginary" {
		t.Fatalf("imaginary hostname = %q", got)
	}
	if got := env["NEXTCLOUD_PUSH_HOSTNAME"]; got != "anas_test_nextcloud_push" {
		t.Fatalf("push hostname = %q", got)
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
	if err := calcNextcloud(first, "", secrets); err != nil {
		t.Fatal(err)
	}
	second := cloneMap(base)
	if err := calcNextcloud(second, "", secrets); err != nil {
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
	if err := calcNextcloud(env, "", &secretStore{values: map[string]string{}}); err != nil {
		t.Fatal(err)
	}
	if got, want := env["NEXTCLOUD_USER_FILTER"], "(&(objectClass=user)(anasIdentityAnchor=*))"; got != want {
		t.Fatalf("user filter = %q, want %q", got, want)
	}
	if got := env["NEXTCLOUD_USER_LOGIN_FILTER"]; strings.Contains(got, "objectGUID") {
		t.Fatalf("login filter must not accept the old identity attribute: %s", got)
	}
	if got := env["ANAS_IAM_CLIENT__NEXTCLOUD__ATTRIBUTES"]; !strings.Contains(got, "anasIdentityAnchor:anasIdentityAnchor:1") {
		t.Fatalf("SAML registration does not publish the printable identity anchor: %s", got)
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
