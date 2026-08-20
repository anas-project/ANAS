package main

import (
	"encoding/json"
	"strings"
	"testing"
)

func casdoorTestEnv() map[string]string {
	return map[string]string{
		"CASDOOR_DOMAIN_FULL":                                   "https://auth.example:443",
		"CASDOOR_PORTAL_CLIENT_ID":                              "portal-id",
		"CASDOOR_PORTAL_CLIENT_SECRET":                          "portal-secret",
		"CASDOOR_LOCAL_ADMIN__BREAK_GLASS_USERNAME":             "admin_casdoor",
		"CASDOOR_SIGNING_CERT":                                  "certificate",
		"CASDOOR_SIGNING_KEY":                                   "private-key",
		"CASDOOR_LDAP_HOST":                                     "samba_dc",
		"CASDOOR_LDAP_PORT":                                     "636",
		"CASDOOR_LDAP_BIND_DN":                                  "CN=svc,OU=Service Accounts,DC=example,DC=com",
		"CASDOOR_LDAP_BIND_PASSWORD":                            "ldap-secret",
		"CASDOOR_LDAP_BASE_DN":                                  "OU=Users,DC=example,DC=com",
		"CASDOOR_LDAP_FILTER":                                   "(&(objectClass=user)(anchor=*))",
		"CASDOOR_LDAP_AUTO_SYNC_MINUTES":                        "5",
		"ANAS_IDENTITY_OIDC_CLIENTS":                            "nextcloud",
		"ANAS_IDENTITY_SAML_CLIENTS":                            "paperless",
		"ANAS_IAM_BINDING__NEXTCLOUD__INTERFACE":                "oidc",
		"ANAS_IAM_BINDING__PAPERLESS__INTERFACE":                "saml",
		"ANAS_IAM_CLIENT__NEXTCLOUD__CLIENT_ID":                 "nextcloud-id",
		"ANAS_IAM_CLIENT__NEXTCLOUD__CLIENT_SECRET":             "nextcloud-secret",
		"ANAS_IAM_CLIENT__NEXTCLOUD__REDIRECT_URIS":             "https://cloud.example/callback",
		"ANAS_IAM_CLIENT__NEXTCLOUD__POST_LOGOUT_REDIRECT_URIS": "https://cloud.example",
		"ANAS_IAM_CLIENT__NEXTCLOUD__OIDC_LOGOUT_URI":           "https://cloud.example/backchannel",
		"ANAS_IAM_CLIENT__NEXTCLOUD__OIDC_LOGOUT_METHODS":       "backchannel",
		"ANAS_IAM_CLIENT__NEXTCLOUD__ALLOW_GROUPS":              "APP_nextcloud",
		"ANAS_IAM_CLIENT__PAPERLESS__SP_ENTITY_ID":              "https://paper.example/metadata",
		"ANAS_IAM_CLIENT__PAPERLESS__ACS_URL":                   "https://paper.example/acs",
		"ANAS_IAM_CLIENT__PAPERLESS__ATTRIBUTES":                "uid:sAMAccountName:1,email:mail:1,anchor:anasIdentityAnchor:1",
		"ANAS_IAM_CLIENT__PAPERLESS__ALLOW_GROUPS":              "APP_paperless",
		"APPS_LIST__NEXTCLOUD__NAME":                            "Nextcloud",
		"APPS_LIST__NEXTCLOUD__URI":                             "https://cloud.example",
		"APPS_LIST__PAPERLESS__NAME":                            "Paperless",
		"APPS_LIST__PAPERLESS__URI":                             "https://paper.example",
	}
}

func TestPublishIAMEndpointsDoesNotInventSAMLLogout(t *testing.T) {
	e := casdoorTestEnv()
	if err := publishIAMEndpoints(e); err != nil {
		t.Fatal(err)
	}
	if got, want := e["ANAS_IAM_BINDING__NEXTCLOUD__OIDC_DISCOVERY_URL"], "https://auth.example:443/.well-known/openid-configuration"; got != want {
		t.Fatalf("OIDC discovery = %q, want %q", got, want)
	}
	if got, want := e["ANAS_IAM_BINDING__PAPERLESS__SAML_METADATA_URL"], "https://auth.example:443/api/saml/metadata?application=admin/app-anas-paperless"; got != want {
		t.Fatalf("SAML metadata = %q, want %q", got, want)
	}
	if got := e["ANAS_IAM_BINDING__PAPERLESS__SAML_SLO_URL"]; got != "" {
		t.Fatalf("unexpected SAML SLO endpoint %q", got)
	}
}

func TestRenderInitDataRegistersDirectoryAndClients(t *testing.T) {
	rendered, err := renderInitData(casdoorTestEnv())
	if err != nil {
		t.Fatal(err)
	}
	if count := strings.Count(rendered, passwordPlaceholder); count != 1 {
		t.Fatalf("password placeholder count = %d", count)
	}
	var doc struct {
		Applications []map[string]any `json:"applications"`
		LDPs         []map[string]any `json:"ldaps"`
		Users        []map[string]any `json:"users"`
	}
	if err := json.Unmarshal([]byte(rendered), &doc); err != nil {
		t.Fatal(err)
	}
	if len(doc.Applications) != 3 {
		t.Fatalf("applications = %d, want 3", len(doc.Applications))
	}
	byName := map[string]map[string]any{}
	for _, app := range doc.Applications {
		byName[app["name"].(string)] = app
	}
	oidc := byName["app-anas-nextcloud"]
	if oidc["type"] != "OIDC" || oidc["clientId"] != "nextcloud-id" || oidc["backchannelLogoutUri"] != "https://cloud.example/backchannel" {
		t.Fatalf("OIDC application = %#v", oidc)
	}
	saml := byName["app-anas-paperless"]
	if saml["type"] != "SAML" || saml["samlReplyUrl"] != "https://paper.example/acs" {
		t.Fatalf("SAML application = %#v", saml)
	}
	if len(doc.LDPs) != 1 || doc.LDPs[0]["host"] != "samba_dc" || doc.LDPs[0]["enableSsl"] != true {
		t.Fatalf("LDAP configuration = %#v", doc.LDPs)
	}
	if len(doc.Users) != 1 || doc.Users[0]["name"] != "admin_casdoor" {
		t.Fatalf("managed recovery user = %#v", doc.Users)
	}
	// Casdoor has no verified equivalent for the generic ALLOW_GROUPS contract.
	// Keep this visible as a lifecycle limitation instead of silently claiming
	// that application access policy has been enforced.
	if _, ok := oidc["tags"]; ok {
		t.Fatalf("unsupported group policy unexpectedly rendered: %#v", oidc["tags"])
	}
}

func TestOIDCClientCredentialsAreRequired(t *testing.T) {
	e := casdoorTestEnv()
	delete(e, "ANAS_IAM_CLIENT__NEXTCLOUD__CLIENT_SECRET")
	if _, err := renderInitData(e); err == nil || !strings.Contains(err.Error(), "did not publish credentials") {
		t.Fatalf("error = %v", err)
	}
}
