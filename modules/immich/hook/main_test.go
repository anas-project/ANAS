package main

import (
	"encoding/json"
	"strings"
	"testing"
)

func fixture() map[string]string {
	return map[string]string{
		"IMMICH_DB_TYPE": "postgres", "IMMICH_IAM_PROTOCOL": "oidc", "IMMICH_MACHINE_LEARNING": "false", "IMMICH_JOB_CONCURRENCY": "2", "IMMICH_VIDEO_CONCURRENCY": "1",
		"IMMICH_DOMAIN_PREFIX": "photos", "BASE_DOMAIN": "nas.test", "TRAEFIK_BASE_PORT": "443",
		"SAMBA_DC_IDENTITY_ANCHOR_ATTRIBUTE": "anasIdentityAnchor", "SAMBA_DC_APP_FILTER": "true", "SAMBA_DC_ADMIN_GROUP_NAME": "Admins",
		bindingPrefix + "INTERFACE": "oidc", bindingPrefix + "OIDC_ISSUER_URL": "https://auth.nas.test/", bindingPrefix + "OIDC_DISCOVERY_URL": "https://auth.nas.test/.well-known/openid-configuration",
		"IMMICH_DB_HOST": "anas_postgres", "IMMICH_DB_NAME": "immich", "IMMICH_DB_USERNAME": "immich", "IMMICH_DB_PASSWORD": "resource-password", "IMMICH_NETWORK_DB": "anas_db",
	}
}

func TestOIDCRegistrationAndStableSecret(t *testing.T) {
	e, secrets := fixture(), map[string]string{}
	if err := calculate(e, secrets); err != nil {
		t.Fatal(err)
	}
	first := secrets["IMMICH_OIDC_CLIENT_SECRET"]
	if len(first) != 64 {
		t.Fatal("OIDC secret was not generated")
	}
	if err := calculate(e, secrets); err != nil {
		t.Fatal(err)
	}
	if secrets["IMMICH_OIDC_CLIENT_SECRET"] != first {
		t.Fatal("secret changed on repeated calculate")
	}
	for key, want := range map[string]string{"ATTRIBUTES": "sub:anasIdentityAnchor:1,name:displayName:1,email:mail:1,anas_role:anasRole:1", "ALLOW_GROUPS": "APP_immich,APP_all,Admins", "OIDC_LOGOUT_METHODS": "backchannel", "OIDC_LOGOUT_URI": "https://photos.nas.test:443/api/oauth/backchannel-logout"} {
		if got := e[clientPrefix+key]; got != want {
			t.Fatalf("%s = %q, want %q", key, got, want)
		}
	}
	if strings.Contains(e[clientPrefix+"REDIRECT_URIS"], "/auth/login,https://photos.nas.test:443/user-settings,app.immich:///oauth-callback") == false {
		t.Fatal("missing web/mobile redirects")
	}
	if e["APPS_LIST"] != "immich" {
		t.Fatal("launcher registration is not idempotent")
	}
}

func TestManagedConfigMapsDatabaseAndOnlyOIDC(t *testing.T) {
	e := fixture()
	if err := calculate(e, map[string]string{}); err != nil {
		t.Fatal(err)
	}
	body, err := render(e)
	if err != nil {
		t.Fatal(err)
	}
	var config map[string]any
	if err := json.Unmarshal([]byte(body), &config); err != nil {
		t.Fatal(err)
	}
	oauth := config["oauth"].(map[string]any)
	if oauth["roleClaim"] != "anas_role" || oauth["autoRegister"] != true || oauth["storageLabelClaim"] != "" {
		t.Fatalf("OIDC configuration = %#v", oauth)
	}
	if config["passwordLogin"].(map[string]any)["enabled"] != false || config["machineLearning"].(map[string]any)["enabled"] != false {
		t.Fatal("managed switches ignored")
	}
	if config["backup"].(map[string]any)["database"].(map[string]any)["enabled"] != false {
		t.Fatal("app backup scheduler enabled")
	}
	if e["DB_USERNAME"] != "immich" || e["DB_PASSWORD"] != "resource-password" || e["DB_VECTOR_EXTENSION"] != "pgvector" || e["IMMICH_ALLOW_SETUP"] != "false" {
		t.Fatal("database/setup projection incorrect")
	}
	for _, key := range []string{"POSTGRES_PASSWORD", "POSTGRES_USER", "SAMBA_DC_LDAP_BIND_PASSWORD"} {
		if e[key] != "" {
			t.Fatalf("unexpected provider credential %s", key)
		}
	}
}

func TestMLDisabledAndConfigurationFailsClosed(t *testing.T) {
	e := fixture()
	resp, err := handle(hookRequest{Module: "immich", Phase: "services", Env: e})
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.DisableServices) != 1 || resp.DisableServices[0] != "anas_immich_machine_learning" {
		t.Fatalf("disabled = %#v", resp.DisableServices)
	}
	for key, value := range map[string]string{"IMMICH_DB_TYPE": "mariadb", "IMMICH_IAM_PROTOCOL": "saml", "IMMICH_JOB_CONCURRENCY": "0", "IMMICH_VIDEO_CONCURRENCY": "9", "IMMICH_MACHINE_LEARNING": "auto"} {
		t.Run(key, func(t *testing.T) {
			bad := fixture()
			bad[key] = value
			if err := validate(bad); err == nil {
				t.Fatal("invalid config accepted")
			}
		})
	}
	e = fixture()
	e["IMMICH_OIDC_CLIENT_SECRET"] = "secret"
	delete(e, bindingPrefix+"OIDC_ISSUER_URL")
	if _, err := render(e); err == nil {
		t.Fatal("missing binding accepted")
	}
}
