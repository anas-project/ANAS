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
		"CASDOOR_SIGNING_MATERIAL":                              "managed-material",
		"CASDOOR_LDAP_HOST":                                     "samba_dc",
		"CASDOOR_LDAP_PORT":                                     "636",
		"CASDOOR_LDAP_BIND_DN":                                  "CN=svc,OU=Service Accounts,DC=example,DC=com",
		"CASDOOR_LDAP_BIND_PASSWORD":                            "ldap-secret",
		"CASDOOR_LDAP_BASE_DN":                                  "OU=Users,DC=example,DC=com",
		"CASDOOR_LDAP_FILTER":                                   "(&(objectClass=user)(anchor=*))",
		"CASDOOR_LDAP_AUTO_SYNC_MINUTES":                        "5",
		"SAMBA_DC_IDENTITY_ANCHOR_ATTRIBUTE":                    "anasIdentityAnchor",
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
		"ANAS_IAM_CLIENT__NEXTCLOUD__ATTRIBUTES":                "name:displayName:1,preferred_username:sAMAccountName:1,email:mail:1,anchor:anasIdentityAnchor:1,groups:groups:0",
		"ANAS_IAM_CLIENT__NEXTCLOUD__ALLOW_GROUPS":              "APP_nextcloud",
		"ANAS_IAM_CLIENT__PAPERLESS__SP_ENTITY_ID":              "https://paper.example/metadata",
		"ANAS_IAM_CLIENT__PAPERLESS__ACS_URL":                   "https://paper.example/acs",
		"ANAS_IAM_CLIENT__PAPERLESS__ATTRIBUTES":                "uid:sAMAccountName:1,email:mail:1,anchor:anasIdentityAnchor:1,groups:groups:0",
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
		Organizations []map[string]any `json:"organizations"`
		Applications  []map[string]any `json:"applications"`
		LDPs          []map[string]any `json:"ldaps"`
		Users         []map[string]any `json:"users"`
		Groups        []map[string]any `json:"groups"`
		Roles         []map[string]any `json:"roles"`
		Permissions   []map[string]any `json:"permissions"`
	}
	if err := json.Unmarshal([]byte(rendered), &doc); err != nil {
		t.Fatal(err)
	}
	if len(doc.Applications) != 4 {
		t.Fatalf("applications = %d, want 4", len(doc.Applications))
	}
	if len(doc.Organizations) != 2 || doc.Organizations[0]["name"] != "built-in" || doc.Organizations[0]["hasPrivilegeConsent"] != true {
		t.Fatalf("built-in organization does not consent to the managed recovery administrator: %#v", doc.Organizations)
	}
	if doc.Organizations[1]["name"] != "anas" || doc.Organizations[1]["defaultApplication"] != "app-anas-directory" {
		t.Fatalf("ANAS organization does not select the managed directory application: %#v", doc.Organizations[1])
	}
	byName := map[string]map[string]any{}
	for _, app := range doc.Applications {
		byName[app["name"].(string)] = app
	}
	if directory := byName["app-anas-directory"]; directory["organization"] != "anas" || directory["enableSignUp"] != false {
		t.Fatalf("managed directory application = %#v", directory)
	}
	oidc := byName["app-anas-nextcloud"]
	if oidc["type"] != "OIDC" || oidc["clientId"] != "nextcloud-id" || oidc["backchannelLogoutUri"] != "https://cloud.example/backchannel" {
		t.Fatalf("OIDC application = %#v", oidc)
	}
	if oidc["expireInHours"] != float64(accessTokenExpireInHours) || oidc["refreshExpireInHours"] != float64(refreshTokenExpireInHours) {
		t.Fatalf("OIDC token lifetime = %#v", oidc)
	}
	if oidc["tokenFormat"] != "JWT-Custom" || oidc["tokenSigningMethod"] != "RS256" {
		t.Fatalf("OIDC token format = %#v", oidc)
	}
	tokenAttributes, ok := oidc["tokenAttributes"].([]any)
	if !ok || len(tokenAttributes) != 5 {
		t.Fatalf("OIDC token attributes = %#v", oidc["tokenAttributes"])
	}
	assertTokenAttribute(t, tokenAttributes, "anchor", "ExternalId", "String")
	assertTokenAttribute(t, tokenAttributes, "groups", "Roles", "Array")
	saml := byName["app-anas-paperless"]
	if saml["type"] != "SAML" || saml["samlReplyUrl"] != "https://paper.example/acs" {
		t.Fatalf("SAML application = %#v", saml)
	}
	assertSAMLAttribute(t, saml["samlAttributes"], "anchor", "$user.externalId")
	assertSAMLAttribute(t, saml["samlAttributes"], "groups", "$user.roles")
	if saml["backchannelLogoutUri"] != "" {
		t.Fatalf("SAML application retained stale back-channel logout URI: %#v", saml)
	}
	if len(doc.LDPs) != 1 || doc.LDPs[0]["host"] != "samba_dc" || doc.LDPs[0]["enableSsl"] != true {
		t.Fatalf("LDAP configuration = %#v", doc.LDPs)
	}
	customAttributes, ok := doc.LDPs[0]["customAttributes"].(map[string]any)
	if !ok || customAttributes["anasIdentityAnchor"] != "anasIdentityAnchor" || customAttributes["distinguishedName"] != "distinguishedName" {
		t.Fatalf("LDAP custom attributes = %#v", doc.LDPs[0]["customAttributes"])
	}
	if len(doc.Users) != 1 || doc.Users[0]["name"] != "admin_casdoor" {
		t.Fatalf("managed recovery user = %#v", doc.Users)
	}
	if len(doc.Groups) != 2 || len(doc.Roles) != 2 || len(doc.Permissions) != 2 {
		t.Fatalf("managed IAM access objects groups=%#v roles=%#v permissions=%#v", doc.Groups, doc.Roles, doc.Permissions)
	}
	permissionGroups, ok := doc.Permissions[0]["groups"].([]any)
	if !ok || len(permissionGroups) != 1 || permissionGroups[0] != "anas/APP_nextcloud" {
		t.Fatalf("OIDC application permission groups = %#v", doc.Permissions[0])
	}
}

func assertTokenAttribute(t *testing.T, attributes []any, name, value, typeName string) {
	t.Helper()
	for _, raw := range attributes {
		attribute := raw.(map[string]any)
		if attribute["name"] == name {
			if attribute["category"] != "Existing Field" || attribute["value"] != value || attribute["type"] != typeName {
				t.Fatalf("OIDC token attribute %s = %#v", name, attribute)
			}
			return
		}
	}
	t.Fatalf("OIDC token attribute %s not found in %#v", name, attributes)
}

func assertSAMLAttribute(t *testing.T, raw any, name, value string) {
	t.Helper()
	attributes, ok := raw.([]any)
	if !ok {
		t.Fatalf("SAML attributes = %#v", raw)
	}
	for _, item := range attributes {
		attribute := item.(map[string]any)
		if attribute["name"] == name {
			if attribute["value"] != value {
				t.Fatalf("SAML attribute %s = %#v", name, attribute)
			}
			return
		}
	}
	t.Fatalf("SAML attribute %s not found in %#v", name, attributes)
}

func TestRemovedOIDCLogoutDeclarationClearsImportedURI(t *testing.T) {
	e := casdoorTestEnv()
	delete(e, "ANAS_IAM_CLIENT__NEXTCLOUD__OIDC_LOGOUT_URI")
	delete(e, "ANAS_IAM_CLIENT__NEXTCLOUD__OIDC_LOGOUT_METHODS")
	application, err := oidcApplication(e, "nextcloud")
	if err != nil {
		t.Fatal(err)
	}
	if got, ok := application["backchannelLogoutUri"]; !ok || got != "" {
		t.Fatalf("backchannelLogoutUri = %#v (present %v), want explicit empty value", got, ok)
	}
}

func TestOIDCManagedGroupsClaimIsAlwaysPublished(t *testing.T) {
	attributes := oidcTokenAttributes(
		"preferred_username:sAMAccountName:1", "anasIdentityAnchor")
	assertTokenAttribute(t, attributes, "groups", "Roles", "Array")
}

func TestSAMLManagedGroupsAttributeIsAlwaysPublished(t *testing.T) {
	attributes := samlAttributes(
		"preferred_username:sAMAccountName:1", "anasIdentityAnchor")
	assertSAMLAttribute(t, attributes, "groups", "$user.roles")
}

func TestSAMLDirectoryAttributesUsePatchedTemplates(t *testing.T) {
	attributes := samlAttributes(
		"name:displayName:1,anchor:anasIdentityAnchor:1", "anasIdentityAnchor")
	assertSAMLAttribute(t, attributes, "name", "$user.displayName")
	assertSAMLAttribute(t, attributes, "anchor", "$user.externalId")
}

func TestOIDCClientCredentialsAreRequired(t *testing.T) {
	e := casdoorTestEnv()
	delete(e, "ANAS_IAM_CLIENT__NEXTCLOUD__CLIENT_SECRET")
	if _, err := renderInitData(e); err == nil || !strings.Contains(err.Error(), "did not publish credentials") {
		t.Fatalf("error = %v", err)
	}
}
