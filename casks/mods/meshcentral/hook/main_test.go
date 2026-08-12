package main

import (
	"strings"
	"testing"
)

func TestMeshcentralRequiresIdentityAnchorInLDAPFilter(t *testing.T) {
	env := map[string]string{
		"MESHCENTRAL_DOMAIN_PREFIX":          "mesh",
		"MESHCENTRAL_DB_TYPE":                "postgres",
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
}
