package main

import (
	"strings"
	"testing"
)

func TestMeshcentralRequiresIdentityAnchorInLDAPFilter(t *testing.T) {
	env := map[string]string{
		"MESHCENTRAL_DOMAIN_PREFIX":          "mesh",
		"BASE_DOMAIN":                        "nas.test",
		"SERVER_NAME":                        "nas",
		"SAMBA_DC_USER_CLASS_FILTER":         "(objectClass=user)",
		"SAMBA_DC_IDENTITY_ANCHOR_ATTRIBUTE": "anasIdentityAnchor",
		"SAMBA_DC_USER_LOGIN_ATTRS":          "sAMAccountName,userPrincipalName,mail",
		"SAMBA_DC_USER_ENABLED_FILTER":       "(!(userAccountControl:1.2.840.113556.1.4.803:=2))",
		"SAMBA_DC_APP_FILTER":                "false",
	}
	if err := calcMeshcentral(env, "", &secretStore{values: map[string]string{}}); err != nil {
		t.Fatal(err)
	}
	if got, want := env["MESHCENTRAL_USER_FILTER"], "(&(objectClass=user)(anasIdentityAnchor=*))"; got != want {
		t.Fatalf("user filter = %q, want %q", got, want)
	}
	if !strings.Contains(env["MESHCENTRAL_USER_LOGIN_FILTER"], "(anasIdentityAnchor=*)") {
		t.Fatalf("login filter does not require identity anchor: %s", env["MESHCENTRAL_USER_LOGIN_FILTER"])
	}
}
