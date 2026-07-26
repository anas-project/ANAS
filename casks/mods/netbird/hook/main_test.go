package main

import "testing"

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
