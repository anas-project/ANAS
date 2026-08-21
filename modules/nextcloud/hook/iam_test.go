package main

import "testing"

func TestApplyNextcloudOIDCBinding(t *testing.T) {
	e := map[string]string{
		"ANAS_IAM_BINDING__NEXTCLOUD__INTERFACE":          "oidc",
		"ANAS_IAM_BINDING__NEXTCLOUD__OIDC_ISSUER_URL":    "https://auth.example/application/o/nextcloud/",
		"ANAS_IAM_BINDING__NEXTCLOUD__OIDC_DISCOVERY_URL": "https://auth.example/application/o/nextcloud/.well-known/openid-configuration",
		"ANAS_IAM_PORTAL_URL":                             "https://auth.example",
		"NEXTCLOUD_OIDC_CLIENT_ID":                        "nextcloud",
		"NEXTCLOUD_OIDC_CLIENT_SECRET":                    "secret",
	}
	if err := applyIAMBinding(e); err != nil {
		t.Fatal(err)
	}
	if got := e["NEXTCLOUD_IAM_PROTOCOL"]; got != "oidc" {
		t.Fatalf("protocol = %q", got)
	}
	if got := e["NEXTCLOUD_OIDC_DISCOVERY_URL"]; got != e["ANAS_IAM_BINDING__NEXTCLOUD__OIDC_DISCOVERY_URL"] {
		t.Fatalf("discovery = %q", got)
	}
	if got := e["NEXTCLOUD_IAM_HOST"]; got != "auth.example" {
		t.Fatalf("IAM host = %q", got)
	}
}

func TestNextcloudPublishesSAMLSingleLogoutService(t *testing.T) {
	e := map[string]string{
		"ANAS_IAM_BINDING__NEXTCLOUD__INTERFACE": "saml",
		"NEXTCLOUD_DOMAIN_FULL":                  "https://nc.example",
		"SAMBA_DC_IDENTITY_ANCHOR_ATTRIBUTE":     "anasIdentityAnchor",
	}
	if err := publishClientRegistration(e, "APP_nextcloud", &secretStore{values: map[string]string{}}); err != nil {
		t.Fatal(err)
	}
	if got, want := e["ANAS_IAM_CLIENT__NEXTCLOUD__SAML_SLS_URL"], "https://nc.example/index.php/apps/user_saml/saml/sls"; got != want {
		t.Fatalf("SAML SLS URL = %q, want %q", got, want)
	}
	if got := e["ANAS_IAM_CLIENT__NEXTCLOUD__SAML_SLS_BINDINGS"]; got != "redirect" {
		t.Fatalf("SAML SLS bindings = %q", got)
	}
}

func TestApplyNextcloudSAMLBindingStillSupported(t *testing.T) {
	e := map[string]string{
		"ANAS_IAM_BINDING__NEXTCLOUD__INTERFACE":         "saml",
		"ANAS_IAM_BINDING__NEXTCLOUD__SAML_ENTITY_ID":    "https://auth.example/metadata",
		"ANAS_IAM_BINDING__NEXTCLOUD__SAML_SSO_URL":      "https://auth.example/sso",
		"ANAS_IAM_BINDING__NEXTCLOUD__SAML_SLO_URL":      "https://auth.example/slo",
		"ANAS_IAM_BINDING__NEXTCLOUD__SAML_SIGNING_CERT": "cert",
		"ANAS_IAM_PORTAL_URL":                            "https://auth.example",
	}
	if err := applyIAMBinding(e); err != nil {
		t.Fatal(err)
	}
	if got := e["NEXTCLOUD_IAM_PROTOCOL"]; got != "saml" {
		t.Fatalf("protocol = %q", got)
	}
	if got := e["NEXTCLOUD_SAML_IDP_SLO_RESPONSE"]; got != "https://auth.example/slo" {
		t.Fatalf("SLO response = %q", got)
	}
}

func TestApplyNextcloudSAMLBindingWithoutOptionalSLO(t *testing.T) {
	e := map[string]string{
		"ANAS_IAM_BINDING__NEXTCLOUD__INTERFACE":         "saml",
		"ANAS_IAM_BINDING__NEXTCLOUD__SAML_ENTITY_ID":    "https://auth.example/metadata",
		"ANAS_IAM_BINDING__NEXTCLOUD__SAML_SSO_URL":      "https://auth.example/sso",
		"ANAS_IAM_BINDING__NEXTCLOUD__SAML_SIGNING_CERT": "cert",
		"ANAS_IAM_PORTAL_URL":                            "https://auth.example",
		"NEXTCLOUD_SAML_IDP_SLO":                         "https://stale.example/slo",
		"NEXTCLOUD_SAML_IDP_SLO_RESPONSE":                "https://stale.example/response",
	}
	if err := applyIAMBinding(e); err != nil {
		t.Fatal(err)
	}
	if _, ok := e["NEXTCLOUD_SAML_IDP_SLO"]; ok {
		t.Fatal("stale SAML SLO endpoint was retained")
	}
	if _, ok := e["NEXTCLOUD_SAML_IDP_SLO_RESPONSE"]; ok {
		t.Fatal("stale SAML SLO response endpoint was retained")
	}
}

func TestNextcloudProtocolSwitchRebuildsRegistration(t *testing.T) {
	e := map[string]string{
		"ANAS_IAM_BINDING__NEXTCLOUD__INTERFACE": "oidc",
		"NEXTCLOUD_DOMAIN_FULL":                  "https://old.example",
		"SAMBA_DC_IDENTITY_ANCHOR_ATTRIBUTE":     "anasIdentityAnchor",
	}
	secrets := &secretStore{values: map[string]string{}}
	if err := publishClientRegistration(e, "APP_nextcloud", secrets); err != nil {
		t.Fatal(err)
	}
	e["ANAS_IAM_BINDING__NEXTCLOUD__INTERFACE"] = "saml"
	e["NEXTCLOUD_DOMAIN_FULL"] = "https://new.example"
	if err := publishClientRegistration(e, "APP_nextcloud", secrets); err != nil {
		t.Fatal(err)
	}
	for _, suffix := range []string{"POST_LOGOUT_REDIRECT_URIS", "OIDC_LOGOUT_URI", "OIDC_LOGOUT_METHODS", "OIDC_LOGOUT_SESSION_REQUIRED"} {
		if got := e[iamClientPrefix+suffix]; got != "" {
			t.Errorf("stale OIDC %s = %q after SAML switch", suffix, got)
		}
	}
	if got := e[iamClientPrefix+"SAML_SLS_URL"]; got != "https://new.example/index.php/apps/user_saml/saml/sls" {
		t.Fatalf("SAML SLS URL = %q after domain/protocol switch", got)
	}

	e["ANAS_IAM_BINDING__NEXTCLOUD__INTERFACE"] = "oidc"
	if err := publishClientRegistration(e, "APP_nextcloud", secrets); err != nil {
		t.Fatal(err)
	}
	for _, suffix := range []string{"SAML_SLS_URL", "SAML_SLS_BINDINGS", "SP_METADATA_URL", "SP_ENTITY_ID", "ACS_URL"} {
		if got := e[iamClientPrefix+suffix]; got != "" {
			t.Errorf("stale SAML %s = %q after OIDC switch", suffix, got)
		}
	}
}
