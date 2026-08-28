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

// The gate's admission policy is derived, not configured. These pin the two
// halves of that: it follows the directory's real group name, and it can never
// resolve to an empty allow list, which would be an open gate rather than a
// permissive one.
func TestGateFollowsTheDirectoryAdministratorGroup(t *testing.T) {
	env := map[string]string{
		"OAUTH2_PROXY_REDIRECT_URL": "https://gate.example/oauth2/callback",
		"OAUTH2_PROXY_DOMAIN_FULL":  "https://gate.example",
		"OAUTH2_PROXY_DOMAIN":       "gate.example",
		"SAMBA_DC_ADMIN_GROUP_NAME": "NAS Admins",
	}
	if err := registerIAMClient(env, &secretStore{values: map[string]string{}}); err != nil {
		t.Fatal(err)
	}
	if got := env[iamClientPrefix+"ALLOW_GROUPS"]; got != "NAS Admins" {
		t.Fatalf("allow groups = %q, want the directory's administrator group", got)
	}
}

func TestGateFallsBackToTheRoleGroupNameWithoutADirectory(t *testing.T) {
	env := map[string]string{
		"OAUTH2_PROXY_REDIRECT_URL": "https://gate.example/oauth2/callback",
		"OAUTH2_PROXY_DOMAIN_FULL":  "https://gate.example",
		"OAUTH2_PROXY_DOMAIN":       "gate.example",
	}
	if err := registerIAMClient(env, &secretStore{values: map[string]string{}}); err != nil {
		t.Fatal(err)
	}
	if got := env[iamClientPrefix+"ALLOW_GROUPS"]; got != "Admins" {
		t.Fatalf("allow groups = %q, want the role's contract group name", got)
	}
}

// Whitespace must not read as "a group named nothing". An empty allow list is
// not a wide gate, it is no gate, in front of administrative interfaces.
func TestGateNeverResolvesToAnEmptyAllowList(t *testing.T) {
	for _, value := range []string{"", "   ", "\t"} {
		group, err := platformAdminGroup(map[string]string{"SAMBA_DC_ADMIN_GROUP_NAME": value})
		if err != nil {
			t.Fatalf("SAMBA_DC_ADMIN_GROUP_NAME=%q: %v", value, err)
		}
		if strings.TrimSpace(group) == "" {
			t.Fatalf("SAMBA_DC_ADMIN_GROUP_NAME=%q resolved to an empty allow list", value)
		}
	}
}

// A user-set value must have no effect: the parameter is gone, and a leftover
// environment entry from an older deployment must not resurrect it.
func TestGateIgnoresAnyLeftoverAllowGroupsValue(t *testing.T) {
	env := map[string]string{
		"OAUTH2_PROXY_REDIRECT_URL": "https://gate.example/oauth2/callback",
		"OAUTH2_PROXY_DOMAIN_FULL":  "https://gate.example",
		"OAUTH2_PROXY_DOMAIN":       "gate.example",
		"OAUTH2_PROXY_ALLOW_GROUPS": "Domain Users",
		"SAMBA_DC_ADMIN_GROUP_NAME": "Admins",
	}
	if err := registerIAMClient(env, &secretStore{values: map[string]string{}}); err != nil {
		t.Fatal(err)
	}
	if got := env[iamClientPrefix+"ALLOW_GROUPS"]; got != "Admins" {
		t.Fatalf("allow groups = %q, want the derived administrator group", got)
	}
}
