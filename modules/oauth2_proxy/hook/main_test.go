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
	if got := env["ANAS_PROXY_PLATFORM_ADMIN_GROUP"]; got != "NAS Admins" {
		t.Fatalf("proxy assertion group = %q, want the resolved administrator group", got)
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

func TestEnabledConsoleProxyPublishesForwardAuthAndMutualTLSTraefikRoute(t *testing.T) {
	env := map[string]string{
		"OAUTH2_PROXY_CONSOLE_PROXY_ENABLED": "true", "OAUTH2_PROXY_CONSOLE_PROXY_PORT": "8443",
		"BASE_DOMAIN": "example.test", "TRAEFIK_BASE_PORT": "9000",
		"ANAS_TLS_TRUST_BUNDLE_NAME":   "anas-trust-bundle.crt",
		"ANAS_FORWARD_AUTH_MIDDLEWARE": "anas-forward-auth@docker",
	}
	if err := publishConsoleRoute(env); err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"ANAS_TRAEFIK_ROUTE__ANAS_CONSOLE__RULE":                         "Host(`anas.example.test`)",
		"ANAS_TRAEFIK_ROUTE__ANAS_CONSOLE__URL":                          "https://host.docker.internal:8443",
		"ANAS_TRAEFIK_ROUTE__ANAS_CONSOLE__MIDDLEWARES":                  "anas-forward-auth@docker",
		"ANAS_TRAEFIK_ROUTE__ANAS_CONSOLE__SERVERS_TRANSPORT":            "ANAS_CONSOLE_MTLS",
		"ANAS_TRAEFIK_SERVERS_TRANSPORT__ANAS_CONSOLE_MTLS__SERVER_NAME": "anas.example.test",
		"ANAS_TRAEFIK_SERVERS_TRANSPORT__ANAS_CONSOLE_MTLS__ROOT_CAS":    "/certs/anas-trust-bundle.crt",
		"ANAS_CONSOLE_PROXY_PUBLIC_URL":                                  "https://anas.example.test:9000",
	}
	for name, value := range want {
		if got := env[name]; got != value {
			t.Errorf("%s = %q, want %q", name, got, value)
		}
	}
	compose, err := os.ReadFile("../docker-compose.yml")
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{"--http-address=127.0.0.1:4181", "--set-authorization-header=true", ":4182/", "X-Anas-Identity-Issuer", "X-Anas-Identity-Assertion"} {
		if !strings.Contains(string(compose), value) {
			t.Errorf("oauth2_proxy compose is missing %q", value)
		}
	}
}
