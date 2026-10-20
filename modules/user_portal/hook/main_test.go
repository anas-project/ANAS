// TEST_CASES: PORTAL-T-001
package main

import (
	"encoding/json"
	"fmt"
	"github.com/anas-project/ANAS/internal/appcatalog"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

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
	for _, tc := range []struct{ selected, inherited, want string }{{"", "zh-CN", "zh-CN"}, {"ru", "zh-CN", "en"}, {"fr-CA", "zh-CN", "fr"}, {"", "zh-Hant-TW", "zh-TW"}, {"zh-CN", "en", "zh-CN"}, {"", "", "en"}} {
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

func TestRenderLogoutRuleUsesFrozenCatalogIncludingHiddenApplications(t *testing.T) {
	icons := map[string]appcatalog.Icon{}
	for _, size := range appcatalog.IconSizes {
		key := fmt.Sprint(size)
		icons[key] = appcatalog.Icon{Path: "assets/" + strings.Repeat("a", 64) + ".png", SHA256: strings.Repeat("a", 64), Width: size, Height: size, MediaType: "image/png"}
	}
	catalog := appcatalog.Catalog{Schema: appcatalog.Schema, Applications: []appcatalog.Application{{Metadata: appcatalog.Metadata{Name: "hidden", URL: "https://photos.example.test:9000/private", Hidden: true}, Enabled: true, Access: appcatalog.Access{Mode: "iam", Client: "immich"}, Source: appcatalog.Source{Kind: "module", Module: "immich"}, Icons: icons}}}
	data, _ := json.Marshal(catalog)
	path := filepath.Join(t.TempDir(), "catalog.json")
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	r := request{Phase: "render_env", Env: map[string]string{"ANAS_IAM_BINDING__USER_PORTAL__OIDC_SELF_ACCOUNT_API_SCHEMA": "casdoor-account/v1", "USER_PORTAL_DOMAIN_PREFIX": "portal", "BASE_DOMAIN": "example.test", "TRAEFIK_BASE_PORT": "9000", "ANAS_IAM_BINDING__USER_PORTAL__INTERFACE": "oidc", "ANAS_IAM_BINDING__USER_PORTAL__OIDC_ISSUER_URL": "https://iam.example.test:9000", "ANAS_APP_CATALOG_FILE": path}}
	out, err := handle(r)
	if err != nil {
		t.Fatal(err)
	}
	if got := out.Env["USER_PORTAL_LOGOUT_HOST_RULE"]; got != "(Host(`photos.example.test`)) && Path(`/.well-known/anas/browser-logout`)" {
		t.Fatal(got)
	}
}
