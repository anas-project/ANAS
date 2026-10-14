// TEST_CASES: PORTAL-T-001
package main

import "testing"

func TestPublicPKCERegistrationAndDefaultPortOrigin(t *testing.T) {
	r := request{ABI: "anas.module-hook/v1", Phase: "calculate", Env: map[string]string{"ANAS_IAM_BINDING__USER_PORTAL__OIDC_SELF_ACCOUNT_API_SCHEMA": "casdoor-account/v1", "USER_PORTAL_DOMAIN_PREFIX": "portal", "BASE_DOMAIN": "example.net", "TRAEFIK_BASE_PORT": "443", "ANAS_IAM_BINDING__USER_PORTAL__INTERFACE": "oidc", "ANAS_IAM_BINDING__USER_PORTAL__OIDC_ISSUER_URL": "https://iam.example.net"}}
	out, err := handle(r)
	if err != nil {
		t.Fatal(err)
	}
	const p = "ANAS_IAM_CLIENT__USER_PORTAL__"
	if out.Env["USER_PORTAL_URL"] != "https://portal.example.net" || out.Env[p+"PUBLIC_CLIENT"] != "true" || out.Env[p+"REAUTHENTICATION_CLIENT_ID"] != "user-portal-reauth" {
		t.Fatal("public registration incorrect")
	}
	for _, key := range []string{p + "CLIENT_SECRET", "USER_PORTAL_CLIENT_SECRET"} {
		if out.Env[key] != "" {
			t.Fatal("application secret delivered to portal")
		}
	}
	if out.Env[p+"OIDC_LOGOUT_URI"] != "https://portal.example.net/oidc/backchannel-logout" {
		t.Fatal("receiver missing")
	}
}

func TestLanguageInheritanceAndUnsupportedFallback(t *testing.T) {
	for _, tc := range []struct{ selected, inherited, want string }{{"", "zh-CN", "zh-CN"}, {"fr", "zh-CN", "en"}, {"zh-CN", "en", "zh-CN"}, {"", "", "en"}} {
		r := request{Phase: "calculate", Env: map[string]string{"ANAS_IAM_BINDING__USER_PORTAL__OIDC_SELF_ACCOUNT_API_SCHEMA": "casdoor-account/v1", "USER_PORTAL_LANGUAGE": tc.selected, "DEFAULT_LANGUAGE": tc.inherited, "USER_PORTAL_DOMAIN_PREFIX": "portal", "BASE_DOMAIN": "example.net", "TRAEFIK_BASE_PORT": "443", "ANAS_IAM_BINDING__USER_PORTAL__INTERFACE": "oidc", "ANAS_IAM_BINDING__USER_PORTAL__OIDC_ISSUER_URL": "https://iam.example.net"}}
		out, err := handle(r)
		if err != nil || out.Env["USER_PORTAL_LANGUAGE"] != tc.want {
			t.Fatal(tc, out.Env["USER_PORTAL_LANGUAGE"], err)
		}
	}
}

func TestMissingAccountAPIRejectedBeforePublication(t *testing.T) {
	for _, profile := range []string{"", "unknown/v1"} {
		r := request{Phase: "calculate", Env: map[string]string{"ANAS_IAM_BINDING__USER_PORTAL__INTERFACE": "oidc", "ANAS_IAM_BINDING__USER_PORTAL__OIDC_SELF_ACCOUNT_API_SCHEMA": profile}}
		if out, err := handle(r); err == nil || len(out.Env) != 0 {
			t.Fatal("missing or unsupported account API schema accepted")
		}
	}
}
