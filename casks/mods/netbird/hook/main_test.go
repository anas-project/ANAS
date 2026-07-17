package main

import "testing"

func TestModuleNetbirdUsesResolvedSSOProvider(t *testing.T) {
	env := map[string]string{
		"NETBIRD_SSO_PROVIDER":                 "llng",
		"LLNG_OIDC_CONFIGURATION_ENDPOINT":     "https://auth.example/.well-known/openid-configuration",
		"KEYCLOAK_OIDC_CONFIGURATION_ENDPOINT": "https://keycloak.example/realms/master/.well-known/openid-configuration",
	}
	if err := moduleNetbird(env, ""); err != nil {
		t.Fatal(err)
	}
	if got := env["NETBIRD_AUTH_OIDC_CONFIGURATION_ENDPOINT"]; got != env["LLNG_OIDC_CONFIGURATION_ENDPOINT"] {
		t.Fatalf("OIDC endpoint = %q, want selected LLNG endpoint", got)
	}
}
