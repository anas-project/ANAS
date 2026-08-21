package main

import (
	"strings"
	"testing"
)

func boundEnv() map[string]string {
	return map[string]string{
		"AUTHENTIK_DOMAIN_FULL":                  "https://auth.example:443",
		"AUTHENTIK_SIGNING_CERT":                 "-----BEGIN CERTIFICATE-----\nMIIB\n-----END CERTIFICATE-----\n",
		"AUTHENTIK_SIGNING_KEY":                  "-----BEGIN RSA PRIVATE KEY-----\nMIIE\n-----END RSA PRIVATE KEY-----\n",
		"SAMBA_DC_IDENTITY_ANCHOR_ATTRIBUTE":     "anasIdentityAnchor",
		"ANAS_IDENTITY_OIDC_CLIENTS":             "netbird",
		"ANAS_IDENTITY_SAML_CLIENTS":             "nextcloud",
		"ANAS_IAM_BINDING__NETBIRD__INTERFACE":   "oidc",
		"ANAS_IAM_BINDING__NEXTCLOUD__INTERFACE": "saml",
	}
}

// The point of this module in the design: its endpoints genuinely differ per
// application, so a consumer that read a deployment-level singleton would get
// the wrong issuer.
func TestEndpointsDifferPerApplication(t *testing.T) {
	e := boundEnv()
	e["ANAS_IDENTITY_OIDC_CLIENTS"] = "netbird,collabora"
	e["ANAS_IAM_BINDING__COLLABORA__INTERFACE"] = "oidc"
	delete(e, "ANAS_IDENTITY_SAML_CLIENTS")
	delete(e, "ANAS_IAM_BINDING__NEXTCLOUD__INTERFACE")
	if err := publishIAMEndpoints(e); err != nil {
		t.Fatal(err)
	}
	netbird := e["ANAS_IAM_BINDING__NETBIRD__OIDC_ISSUER_URL"]
	collabora := e["ANAS_IAM_BINDING__COLLABORA__OIDC_ISSUER_URL"]
	if netbird == collabora {
		t.Fatalf("both consumers got issuer %q, want per-application issuers", netbird)
	}
	if netbird != "https://auth.example:443/application/o/netbird/" {
		t.Fatalf("netbird issuer = %q", netbird)
	}
	if want := netbird + ".well-known/openid-configuration"; e["ANAS_IAM_BINDING__NETBIRD__OIDC_DISCOVERY_URL"] != want {
		t.Fatalf("discovery = %q, want %q", e["ANAS_IAM_BINDING__NETBIRD__OIDC_DISCOVERY_URL"], want)
	}
}

func TestPublishesRequiredEndpointsForEachProtocol(t *testing.T) {
	e := boundEnv()
	if err := publishIAMEndpoints(e); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{
		"ANAS_IAM_BINDING__NETBIRD__OIDC_ISSUER_URL",
		"ANAS_IAM_BINDING__NETBIRD__OIDC_DISCOVERY_URL",
		"ANAS_IAM_BINDING__NEXTCLOUD__SAML_METADATA_URL",
		"ANAS_IAM_BINDING__NEXTCLOUD__SAML_ENTITY_ID",
		"ANAS_IAM_BINDING__NEXTCLOUD__SAML_SSO_URL",
		"ANAS_IAM_BINDING__NEXTCLOUD__SAML_SIGNING_CERT",
	} {
		if e[key] == "" {
			t.Fatalf("%s is empty; the runner requires it for this binding", key)
		}
	}
	// A consumer bound to OIDC must not receive SAML endpoints and vice versa.
	if e["ANAS_IAM_BINDING__NETBIRD__SAML_SSO_URL"] != "" {
		t.Fatal("netbird is bound to oidc but received a SAML endpoint")
	}
	if got, want := e["ANAS_IAM_BINDING__NEXTCLOUD__SAML_SSO_URL"], "https://auth.example:443/application/saml/nextcloud/"; got != want {
		t.Fatalf("Nextcloud SSO URL = %q, want canonical endpoint %q", got, want)
	}
	if got, want := e["ANAS_IAM_BINDING__NEXTCLOUD__SAML_ENTITY_ID"], "https://auth.example:443/application/saml/nextcloud/metadata/"; got != want {
		t.Fatalf("Nextcloud IdP entity ID = %q, want metadata entity ID %q", got, want)
	}
}

func TestSAMLProviderRespondsUsingPostBinding(t *testing.T) {
	e := boundEnv()
	e["ANAS_IDENTITY_OIDC_CLIENTS"] = ""
	e["ANAS_IAM_CLIENT__NEXTCLOUD__SP_METADATA_URL"] = "https://nc.example/metadata"
	e["ANAS_IAM_CLIENT__NEXTCLOUD__SP_ENTITY_ID"] = "https://nc.example/metadata"
	e["ANAS_IAM_CLIENT__NEXTCLOUD__ACS_URL"] = "https://nc.example/acs"
	blueprint, err := renderClientBlueprint(e)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(blueprint, "      sp_binding: post\n") {
		t.Fatal("SAML provider must return assertions through the SP's HTTP-POST ACS binding")
	}
	if !strings.Contains(blueprint, "      sign_assertion: true\n") || !strings.Contains(blueprint, "      sign_response: true\n") {
		t.Fatal("SAML provider must sign both the assertion and response required by Nextcloud")
	}
}

func TestIdentityAnchorMappingUsesAuthentikLDAPUniquenessValue(t *testing.T) {
	got := samlAttributeExpression("anasIdentityAnchor", "anasIdentityAnchor")
	if want := `return request.user.attributes.get("ldap_uniq")`; got != want {
		t.Fatalf("identity anchor expression = %q, want %q", got, want)
	}
}

func TestPublishIAMEndpointsRejectsUnknownInterface(t *testing.T) {
	e := boundEnv()
	e["ANAS_IAM_BINDING__NETBIRD__INTERFACE"] = "ldap"
	if err := publishIAMEndpoints(e); err == nil {
		t.Fatal("expected an unsupported interface to be rejected")
	}
}

func TestNoConsumersPublishesNothing(t *testing.T) {
	e := map[string]string{"AUTHENTIK_DOMAIN_FULL": "https://auth.example"}
	if err := publishIAMEndpoints(e); err != nil {
		t.Fatal(err)
	}
	if e["ANAS_IAM_PORTAL_URL"] != "" {
		t.Fatal("published a portal URL without any consumer")
	}
	blueprint, err := renderClientBlueprint(e)
	if err != nil {
		t.Fatal(err)
	}
	if blueprint != "" {
		t.Fatalf("blueprint = %q, want none without clients", blueprint)
	}
}

func TestBlueprintTranslatesGenericRegistrations(t *testing.T) {
	e := boundEnv()
	e["ANAS_IAM_CLIENT__NETBIRD__CLIENT_ID"] = "netbird"
	e["ANAS_IAM_CLIENT__NETBIRD__CLIENT_SECRET"] = "s3cret"
	e["ANAS_IAM_CLIENT__NETBIRD__REDIRECT_URIS"] = "https://netbird.example/auth,https://netbird.example/silent-auth"
	e["ANAS_IAM_CLIENT__NETBIRD__POST_LOGOUT_REDIRECT_URIS"] = "https://netbird.example"
	e["ANAS_IAM_CLIENT__NETBIRD__OIDC_LOGOUT_URI"] = "https://netbird.example/backchannel-logout"
	e["ANAS_IAM_CLIENT__NETBIRD__OIDC_LOGOUT_METHODS"] = "backchannel,frontchannel"
	e["ANAS_IAM_CLIENT__NETBIRD__ATTRIBUTES"] = "cn:cn:1,sAMAccountName:sAMAccountName:1,anasIdentityAnchor:anasIdentityAnchor:1"
	e["ANAS_IAM_CLIENT__NEXTCLOUD__SP_METADATA_URL"] = "https://nc.example/apps/user_saml/saml/metadata?idp=1"
	e["ANAS_IAM_CLIENT__NEXTCLOUD__SP_ENTITY_ID"] = "https://nc.example/apps/user_saml/saml/metadata"
	e["ANAS_IAM_CLIENT__NEXTCLOUD__ACS_URL"] = "https://nc.example/apps/user_saml/saml/acs"
	e["ANAS_IAM_CLIENT__NEXTCLOUD__NAME_ID_FORMAT"] = "windows"
	e["ANAS_IAM_CLIENT__NEXTCLOUD__ATTRIBUTES"] = "cn:cn:1,sAMAccountName:sAMAccountName:1"
	e["ANAS_IAM_CLIENT__NEXTCLOUD__SAML_SLS_URL"] = "https://nc.example/index.php/apps/user_saml/saml/sls"
	e["ANAS_IAM_CLIENT__NEXTCLOUD__SAML_SLS_BINDINGS"] = "redirect"
	e["APPS_LIST__NEXTCLOUD__NAME"] = "Nextcloud"

	blueprint, err := renderClientBlueprint(e)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"authentik_flows.flowstagebinding",
		"default-provider-invalidation-flow",
		"authentik_stages_user_logout.userlogoutstage",
		"default-invalidation-logout",
		"authentik_providers_oauth2.oauth2provider",
		"authentik_providers_oauth2.scopemapping",
		"authentik_providers_saml.samlprovider",
		`client_id: "netbird"`,
		"grant_types:\n        - authorization_code\n        - refresh_token",
		"sub_mode: user_uuid",
		`url: "https://netbird.example/silent-auth"`,
		"redirect_uri_type: authorization",
		"redirect_uri_type: logout",
		`logout_uri: "https://netbird.example/backchannel-logout"`,
		"logout_method: backchannel",
		"scope_name: profile",
		`claims["anasIdentityAnchor"] = request.user.attributes.get("ldap_uniq")`,
		"- !KeyOf oidc-mapping-netbird-profile",
		`acs_url: "https://nc.example/apps/user_saml/saml/acs"`,
		"authentik_providers_saml.samlpropertymapping",
		`saml_name: "sAMAccountName"`,
		"return request.user.username",
		"- !KeyOf saml-mapping-nextcloud-samaccountname",
		"slug: nextcloud",
		`name: "Nextcloud"`,
		"provider: !KeyOf provider-nextcloud",
	} {
		if !strings.Contains(blueprint, want) {
			t.Fatalf("blueprint missing %q:\n%s", want, blueprint)
		}
	}
	for _, want := range []string{
		`sls_url: "https://nc.example/index.php/apps/user_saml/saml/sls"`,
		"sls_binding: redirect",
		"logout_method: frontchannel_native",
		"sign_logout_request: true",
		"sign_logout_response: true",
	} {
		if !strings.Contains(blueprint, want) {
			t.Fatalf("SAML logout blueprint missing %q:\n%s", want, blueprint)
		}
	}
	// PEM material must survive as a literal block, not a mangled scalar.
	if !strings.Contains(blueprint, "certificate_data: |") ||
		!strings.Contains(blueprint, "        -----BEGIN CERTIFICATE-----") {
		t.Fatalf("signing certificate is not a YAML block scalar:\n%s", blueprint)
	}
}

func TestSAMLPostLogoutRemainsBrowserMediated(t *testing.T) {
	binding, method, err := selectAuthentikSAMLLogout("post")
	if err != nil {
		t.Fatal(err)
	}
	if binding != "post" || method != "frontchannel_native" {
		t.Fatalf("binding/method = %q/%q, want post/frontchannel_native", binding, method)
	}
}

func TestBlueprintRequiresRegistrationFields(t *testing.T) {
	e := boundEnv()
	// netbird is listed as an OIDC client but published no client id.
	if _, err := renderClientBlueprint(e); err == nil {
		t.Fatal("expected a missing CLIENT_ID to be rejected")
	}
}

func TestAuthentikTreatsPreferredSAMLPostAsBrowserBinding(t *testing.T) {
	binding, method, err := selectAuthentikSAMLLogout("redirect,post")
	if err != nil {
		t.Fatal(err)
	}
	if binding != "post" || method != "frontchannel_native" {
		t.Fatalf("SAML logout selection = %s/%s, want post/frontchannel_native", binding, method)
	}
}
