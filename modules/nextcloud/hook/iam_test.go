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
