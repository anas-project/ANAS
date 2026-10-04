package main

import "testing"

func TestAnchorSubjectRejectsNetbirdProjection(t *testing.T) {
	for _, protocol := range []string{"OIDC", "SAML"} {
		e := map[string]string{"ANAS_IDENTITY_" + protocol + "_CLIENTS": "nextcloud,netbird"}
		if err := publishIAMEndpoints(e); err == nil {
			t.Fatal("published an incompatible consumer")
		}
		if _, err := renderInitData(e); err == nil {
			t.Fatal("rendered an incompatible consumer")
		}
	}
	if err := validateSubjectConsumers(map[string]string{"ANAS_IDENTITY_OIDC_CLIENTS": "nextcloud,forgejo,meshcentral,vikunja,oauth2_proxy"}); err != nil {
		t.Fatal(err)
	}
}
