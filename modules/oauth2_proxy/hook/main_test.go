package main

import (
	"os"
	"strings"
	"testing"
)

func TestOAuth2ProxyPublishesLocalOnlyLogoutContract(t *testing.T) {
	env := map[string]string{
		"OAUTH2_PROXY_REDIRECT_URL": "https://gate.example/oauth2/callback",
		"OAUTH2_PROXY_DOMAIN_FULL":  "https://gate.example",
		"OAUTH2_PROXY_DOMAIN":       "gate.example",
		"OAUTH2_PROXY_ALLOW_GROUPS": "Admins",
		"SAMBA_DC_ADMIN_GROUP_NAME": "Admins",
	}
	if err := registerIAMClient(env, &secretStore{values: map[string]string{}}); err != nil {
		t.Fatal(err)
	}
	if got := env[iamClientPrefix+"POST_LOGOUT_REDIRECT_URIS"]; got != "https://gate.example" {
		t.Fatalf("post-logout redirect = %q", got)
	}
	for _, suffix := range []string{"OIDC_LOGOUT_URI", "OIDC_LOGOUT_METHODS", "OIDC_LOGOUT_SESSION_REQUIRED"} {
		if got := env[iamClientPrefix+suffix]; got != "" {
			t.Fatalf("oauth2-proxy must not publish reliable-global-logout field %s=%q", suffix, got)
		}
	}
	compose, err := os.ReadFile("../docker-compose.yml")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(compose), "backend-logout-url") {
		t.Fatal("backend-logout-url runs before cookie clearing and must remain disabled")
	}
}
