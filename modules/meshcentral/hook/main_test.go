package main

import (
	"strings"
	"testing"
)

func TestMeshcentralRequiresIdentityAnchorInLDAPFilter(t *testing.T) {
	env := map[string]string{
		"MESHCENTRAL_DOMAIN_PREFIX":          "mesh",
		"MESHCENTRAL_DB_TYPE":                "postgres",
		"MESHCENTRAL_DB_HOST":                "anas_postgres",
		"MESHCENTRAL_DB_PORT":                "5432",
		"MESHCENTRAL_DB_NAME":                "meshcentral",
		"MESHCENTRAL_DB_USERNAME":            "meshcentral",
		"MESHCENTRAL_DB_PASSWORD":            "dedicated-secret",
		"MESHCENTRAL_NETWORK_DB":             "anas_postgres",
		"BASE_DOMAIN":                        "nas.test",
		"SERVER_NAME":                        "nas",
		"POSTGRES_HOST":                      "anas_postgres",
		"POSTGRES_PORT":                      "5432",
		"POSTGRES_USERNAME":                  "postgres",
		"POSTGRES_PASSWORD":                  "secret",
		"POSTGRES_NETWORK_NAME":              "anas_postgres",
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
	if got := env["MESHCENTRAL_DB_HOST"]; got != "anas_postgres" {
		t.Fatalf("database host = %q, want anas_postgres", got)
	}
	if got := env["MESHCENTRAL_NETWORK_DB"]; got != "anas_postgres" {
		t.Fatalf("database network = %q, want anas_postgres", got)
	}
}

func TestMeshcentralMapsMariaDBBinding(t *testing.T) {
	env := map[string]string{
		"MESHCENTRAL_DOMAIN_PREFIX":          "mesh",
		"MESHCENTRAL_DB_TYPE":                "mariadb",
		"MESHCENTRAL_DB_HOST":                "anas_mariadb",
		"MESHCENTRAL_DB_PORT":                "3306",
		"MESHCENTRAL_DB_NAME":                "meshcentral",
		"MESHCENTRAL_DB_USERNAME":            "meshcentral",
		"MESHCENTRAL_DB_PASSWORD":            "dedicated-secret",
		"MESHCENTRAL_NETWORK_DB":             "anas_mariadb",
		"BASE_DOMAIN":                        "nas.test",
		"SERVER_NAME":                        "nas",
		"MARIADB_HOST":                       "anas_mariadb",
		"MARIADB_PORT":                       "3306",
		"MARIADB_USERNAME":                   "root",
		"MARIADB_PASSWORD":                   "secret",
		"MARIADB_NETWORK_NAME":               "anas_mariadb",
		"SAMBA_DC_USER_CLASS_FILTER":         "(objectClass=user)",
		"SAMBA_DC_IDENTITY_ANCHOR_ATTRIBUTE": "anasIdentityAnchor",
		"SAMBA_DC_USER_LOGIN_ATTRS":          "sAMAccountName",
		"SAMBA_DC_APP_FILTER":                "false",
	}
	if err := calcMeshcentral(env, "", &secretStore{values: map[string]string{}}); err != nil {
		t.Fatal(err)
	}
	if got := env["MESHCENTRAL_DB_HOST"]; got != "anas_mariadb" {
		t.Fatalf("database host = %q, want anas_mariadb", got)
	}
	if got := env["MESHCENTRAL_NETWORK_DB"]; got != "anas_mariadb" {
		t.Fatalf("database network = %q, want anas_mariadb", got)
	}
	if got := env["MESHCENTRAL_DB_USERNAME"]; got != "meshcentral" {
		t.Fatalf("database username = %q, want dedicated meshcentral user", got)
	}
}

func TestMeshcentralPublishesOIDCRegistration(t *testing.T) {
	secrets := &secretStore{values: map[string]string{}}
	env := map[string]string{
		"MESHCENTRAL_DOMAIN_PREFIX":          "meshcentral",
		"MESHCENTRAL_DB_TYPE":                "postgres",
		"BASE_DOMAIN":                        "nas.test",
		"TRAEFIK_BASE_PORT":                  "9000",
		"SAMBA_DC_IDENTITY_ANCHOR_ATTRIBUTE": "anasIdentityAnchor",
		"SAMBA_DC_USER_CLASS_FILTER":         "(objectClass=user)",
		"SAMBA_DC_USER_LOGIN_ATTRS":          "sAMAccountName",
		"SAMBA_DC_APP_FILTER":                "true",
		"SAMBA_DC_ADMIN_GROUP_NAME":          "Admins",
		"SAMBA_DC_BASE_APP_DN":               "OU=Apps,DC=nas,DC=test",
		"SAMBA_DC_APP_ALL_DN":                "CN=APP_all,OU=Apps,DC=nas,DC=test",
		"SAMBA_DC_ADMIN_GROUP_DN":            "CN=Admins,OU=Roles,DC=nas,DC=test",
	}
	if err := calcMeshcentral(env, "", secrets); err != nil {
		t.Fatal(err)
	}
	if env[meshcentralIAMClientPrefix+"POST_LOGOUT_REDIRECT_URIS"] == "" {
		t.Fatal("MeshCentral must register its fixed-version RP post-logout URI")
	}
	for _, suffix := range []string{"OIDC_LOGOUT_URI", "OIDC_LOGOUT_METHODS"} {
		if got := env[meshcentralIAMClientPrefix+suffix]; got != "" {
			t.Fatalf("MeshCentral must not invent an IAM-to-Module receiver: %s=%q", suffix, got)
		}
	}
	checks := map[string]string{
		"ANAS_IAM_CLIENT__MESHCENTRAL__INTERFACE":     "oidc",
		"ANAS_IAM_CLIENT__MESHCENTRAL__CLIENT_ID":     "meshcentral",
		"ANAS_IAM_CLIENT__MESHCENTRAL__REDIRECT_URIS": "https://meshcentral.nas.test:9000/auth-oidc-callback",
		"APPS_LIST__MESHCENTRAL__URI":                 "https://meshcentral.nas.test:9000",
	}
	for key, want := range checks {
		if got := env[key]; got != want {
			t.Errorf("%s = %q, want %q", key, got, want)
		}
	}
	if got := env["ANAS_IAM_CLIENT__MESHCENTRAL__ALLOW_GROUPS"]; got != "APP_meshcentral,APP_all,Admins" {
		t.Fatalf("allow groups = %q", got)
	}
	if env["MESHCENTRAL_OIDC_CLIENT_SECRET"] == "" || secrets.values["MESHCENTRAL_OIDC_CLIENT_SECRET"] != env["MESHCENTRAL_OIDC_CLIENT_SECRET"] {
		t.Fatal("OIDC client secret was not persisted")
	}
}

func TestMeshcentralApplicationGroupsUseRecursiveMembership(t *testing.T) {
	env := map[string]string{
		"MESHCENTRAL_DB_TYPE":                "postgres",
		"SAMBA_DC_APP_FILTER":                "true",
		"SAMBA_DC_USER_CLASS_FILTER":         "(objectClass=user)",
		"SAMBA_DC_IDENTITY_ANCHOR_ATTRIBUTE": "anasIdentityAnchor",
		"SAMBA_DC_BASE_APP_DN":               "OU=Apps,OU=Groups,DC=nas,DC=test",
		"SAMBA_DC_APP_ALL_DN":                "CN=APP_all,OU=Apps,OU=Groups,DC=nas,DC=test",
		"SAMBA_DC_ADMIN_GROUP_DN":            "CN=Admins,OU=Role,OU=Groups,DC=nas,DC=test",
	}
	if err := calcMeshcentral(env, "", &secretStore{values: map[string]string{}}); err != nil {
		t.Fatal(err)
	}
	if got := env["MESHCENTRAL_USER_FILTER"]; strings.Count(got, "memberOf:1.2.840.113556.1.4.1941:=") != 3 {
		t.Fatalf("application group filter is not recursive for all alternatives: %s", got)
	}
}

func TestMeshcentralAppliesOIDCBinding(t *testing.T) {
	env := map[string]string{
		"ANAS_IAM_BINDING__MESHCENTRAL__INTERFACE":          "oidc",
		"ANAS_IAM_BINDING__MESHCENTRAL__OIDC_ISSUER_URL":    "https://auth.example/application/o/meshcentral/",
		"ANAS_IAM_BINDING__MESHCENTRAL__OIDC_DISCOVERY_URL": "https://auth.example/application/o/meshcentral/.well-known/openid-configuration",
		"ANAS_IAM_PORTAL_URL":                               "https://auth.example:9000",
		"MESHCENTRAL_OIDC_CLIENT_ID":                        "meshcentral",
		"MESHCENTRAL_OIDC_CLIENT_SECRET":                    "secret",
	}
	if err := moduleMeshcentral(env); err != nil {
		t.Fatal(err)
	}
	if got := env["MESHCENTRAL_IAM_HOST"]; got != "auth.example" {
		t.Fatalf("IAM host = %q", got)
	}
}
