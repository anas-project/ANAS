package main

import (
	"os"
	"strings"
	"testing"
)

func TestCalcKeycloakUsesRealmDiscoveryEndpoint(t *testing.T) {
	env := map[string]string{
		"KEYCLOAK_DOMAIN_PREFIX": "auth",
		"BASE_DOMAIN":            "nas.test",
		"TRAEFIK_BASE_PORT":      "9000",
		"KEYCLOAK_DB_TYPE":       "postgres",
		"POSTGRES_NETWORK_NAME":  "anas_postgres",
	}
	secrets := &secretStore{values: map[string]string{}}
	if err := calcKeycloak(env, "", secrets); err != nil {
		t.Fatal(err)
	}
	want := "https://auth.nas.test:9000/realms/master/.well-known/openid-configuration"
	if got := env["KEYCLOAK_OIDC_CONFIGURATION_ENDPOINT"]; got != want {
		t.Fatalf("discovery endpoint = %q, want %q", got, want)
	}
	if !strings.Contains(env["KEYCLOAK_OIDC_SERVICE_PRIVATE_KEY"], "BEGIN RSA PRIVATE KEY") {
		t.Fatal("expected generated OIDC private key")
	}
}

func TestComposeUsesExternalHostnamePort(t *testing.T) {
	b, err := os.ReadFile("../docker-compose.yml")
	if err != nil {
		t.Fatal(err)
	}
	compose := string(b)
	if !strings.Contains(compose, "KC_HOSTNAME: ${KEYCLOAK_DOMAIN}") ||
		!strings.Contains(compose, "KC_HOSTNAME_PORT: ${TRAEFIK_BASE_PORT}") {
		t.Fatal("Keycloak hostname must use the configured external hostname and port")
	}
}
