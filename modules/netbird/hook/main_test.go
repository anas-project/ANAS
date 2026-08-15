package main

import (
	"encoding/base64"
	"strings"
	"testing"
)

func TestCalcNetbirdUses32ByteEncryptionKey(t *testing.T) {
	env := map[string]string{
		"NETBIRD_DATASTORE_ENC_KEY": strings.Repeat("x", 48),
	}
	secrets := &secretStore{values: map[string]string{
		"NETBIRD_DATASTORE_ENC_KEY": env["NETBIRD_DATASTORE_ENC_KEY"],
	}}

	if err := calcNetbird(env, "", secrets); err != nil {
		t.Fatal(err)
	}
	decoded, err := base64.StdEncoding.DecodeString(env["NETBIRD_DATASTORE_ENC_KEY"])
	if err != nil {
		t.Fatalf("decode encryption key: %v", err)
	}
	if got := len(decoded); got != 32 {
		t.Fatalf("decoded encryption key length = %d, want 32", got)
	}
	if got := secrets.values["NETBIRD_DATASTORE_ENC_KEY"]; got != env["NETBIRD_DATASTORE_ENC_KEY"] {
		t.Fatalf("persisted encryption key = %q, want %q", got, env["NETBIRD_DATASTORE_ENC_KEY"])
	}
}

func TestCalcNetbirdPreservesValidEncryptionKey(t *testing.T) {
	key := base64.StdEncoding.EncodeToString([]byte(strings.Repeat("x", 32)))
	env := map[string]string{"NETBIRD_DATASTORE_ENC_KEY": key}
	secrets := &secretStore{values: map[string]string{"NETBIRD_DATASTORE_ENC_KEY": key}}

	if err := calcNetbird(env, "", secrets); err != nil {
		t.Fatal(err)
	}
	if got := env["NETBIRD_DATASTORE_ENC_KEY"]; got != key {
		t.Fatalf("encryption key = %q, want existing key", got)
	}
}

func TestModuleNetbirdReadsItsOwnBinding(t *testing.T) {
	env := map[string]string{
		"ANAS_IAM_BINDING__NETBIRD__INTERFACE":          "oidc",
		"ANAS_IAM_BINDING__NETBIRD__OIDC_DISCOVERY_URL": "https://auth.example/.well-known/openid-configuration",
		"ANAS_IAM_BINDING__NEXTCLOUD__OIDC_ISSUER_URL":  "https://auth.example/other",
		"ANAS_IAM_CLIENT__NETBIRD__CLIENT_ID":           "netbird",
		"ANAS_IAM_CLIENT__NETBIRD__CLIENT_SECRET":       "s3cret",
	}
	if err := moduleNetbird(env, ""); err != nil {
		t.Fatal(err)
	}
	want := env["ANAS_IAM_BINDING__NETBIRD__OIDC_DISCOVERY_URL"]
	if got := env["NETBIRD_AUTH_OIDC_CONFIGURATION_ENDPOINT"]; got != want {
		t.Fatalf("OIDC endpoint = %q, want %q", got, want)
	}
	if got := env["AUTH_CLIENT_SECRET"]; got != "s3cret" {
		t.Fatalf("AUTH_CLIENT_SECRET = %q, want the generic client secret", got)
	}
}

func TestModuleNetbirdRejectsNonOIDCBinding(t *testing.T) {
	env := map[string]string{
		"ANAS_IAM_BINDING__NETBIRD__INTERFACE": "saml",
	}
	if err := moduleNetbird(env, ""); err == nil {
		t.Fatal("expected netbird to reject a saml binding")
	}
}

func TestModuleNetbirdRequiresPublishedEndpoint(t *testing.T) {
	env := map[string]string{
		"ANAS_IAM_BINDING__NETBIRD__INTERFACE": "oidc",
	}
	if err := moduleNetbird(env, ""); err == nil {
		t.Fatal("expected netbird to fail when the provider published no discovery URL")
	}
}

func TestNetbirdPublishesTheCommonApplicationGroupContract(t *testing.T) {
	env := map[string]string{
		"SAMBA_DC_APP_FILTER":       "true",
		"SAMBA_DC_ADMIN_GROUP_NAME": "Directory Admins",
	}
	if err := calcNetbird(env, "", &secretStore{values: map[string]string{}}); err != nil {
		t.Fatal(err)
	}
	want := "APP_netbird,APP_all,Directory Admins"
	if got := env["ANAS_IAM_CLIENT__NETBIRD__ALLOW_GROUPS"]; got != want {
		t.Fatalf("allow groups = %q, want %q", got, want)
	}
	if got := env["APPS_LIST__NETBIRD__ALLOW_GROUPS"]; got != want {
		t.Fatalf("launcher allow groups = %q, want %q", got, want)
	}
}
