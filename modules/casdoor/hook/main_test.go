package main

import (
	"os"
	"strings"
	"testing"
)

func TestManifestSubscribesToDirectoryEventJournal(t *testing.T) {
	manifest, err := os.ReadFile("../module.yml")
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"ANAS_DIRECTORY_EVENTS_DIR", "ANAS_DIRECTORY_EVENTS_FILE_NAME"} {
		if !strings.Contains(string(manifest), "- "+key) {
			t.Fatalf("Casdoor manifest does not consume %s", key)
		}
	}
}

func TestCalculatePublishesCasdoorAndLDAPConfiguration(t *testing.T) {
	e := map[string]string{
		"CASDOOR_DOMAIN_PREFIX": "auth", "BASE_DOMAIN": "example.test", "TRAEFIK_BASE_PORT": "443",
		"CASDOOR_DB_TYPE": "postgres", "CASDOOR_DB_HOST": "postgres", "CASDOOR_DB_PORT": "5432",
		"CASDOOR_DB_USERNAME": "casdoor", "CASDOOR_DB_PASSWORD": "db-secret", "CASDOOR_DB_NAME": "casdoor",
		"CASDOOR_LDAP_AUTO_SYNC_MINUTES": "5", "DEFAULT_LANGUAGE": "zh-CN",
		"SAMBA_DC_LDAPS_SERVER_URL": "ldaps://samba_dc/", "SAMBA_DC_LDAPS_PORT": "636",
		"SAMBA_DC_LDAP_BIND_DN": "CN=svc,DC=example,DC=test", "SAMBA_DC_LDAP_BIND_PASSWORD": "ldap-secret",
		"SAMBA_DC_BASE_USERS_DN": "OU=Users,DC=example,DC=test", "SAMBA_DC_IDENTITY_ANCHOR_ATTRIBUTE": "anasIdentityAnchor",
		"SAMBA_DC_USER_CLASS_FILTER": "(objectClass=user)", "SAMBA_DC_USER_ENABLED_FILTER": "(!(userAccountControl:1.2.840.113556.1.4.803:=2))",
		"ANAS_DIRECTORY_EVENTS_DIR": "/srv/anas/data/samba_dc/events", "ANAS_DIRECTORY_EVENTS_FILE_NAME": "events.jsonl",
		"DATA_PATH": "/srv/anas/data",
	}
	secrets := &secretStore{values: map[string]string{}}
	if err := calcCasdoor(e, secrets); err != nil {
		t.Fatal(err)
	}
	if got, want := e["CASDOOR_DOMAIN_FULL"], "https://auth.example.test:443"; got != want {
		t.Fatalf("domain = %q, want %q", got, want)
	}
	if got := e["CASDOOR_LDAP_HOST"]; got != "samba_dc" {
		t.Fatalf("LDAP host = %q", got)
	}
	if got := e["CASDOOR_DEFAULT_LANGUAGE"]; got != "zh" {
		t.Fatalf("language = %q", got)
	}
	if got, want := e["CASDOOR_DIRWATCH_EVENTS_DIR"], "/srv/anas/data/samba_dc/events"; got != want {
		t.Fatalf("directory event subscription = %q, want %q", got, want)
	}
	if got, want := e["CASDOOR_DIRWATCH_EVENT_FILE"], "/var/lib/anas-directory-events/events.jsonl"; got != want {
		t.Fatalf("directory event file = %q, want %q", got, want)
	}
	if e["CASDOOR_DIRWATCH_CLIENT_ID"] == "" || e["CASDOOR_DIRWATCH_CLIENT_SECRET"] == "" ||
		e["CASDOOR_DIRWATCH_DEBOUNCE_SECONDS"] == "" || e["CASDOOR_DIRWATCH_MIN_INTERVAL_SECONDS"] == "" {
		t.Fatal("directory watcher API credentials or debounce settings are missing")
	}
	for _, key := range []string{"CASDOOR_PORTAL_CLIENT_ID", "CASDOOR_PORTAL_CLIENT_SECRET", "CASDOOR_SIGNING_KEY", "CASDOOR_SIGNING_CERT"} {
		if secrets.values[key] == "" {
			t.Fatalf("secret %s was not generated", key)
		}
	}
}

func TestRenderAppConfIncludesPostgresDatabaseNameInDSN(t *testing.T) {
	e := casdoorTestEnv()
	e["CASDOOR_DB_HOST"] = "postgres"
	e["CASDOOR_DB_PORT"] = "5432"
	e["CASDOOR_DB_USERNAME"] = "casdoor"
	e["CASDOOR_DB_PASSWORD"] = "db-secret"
	e["CASDOOR_DB_NAME"] = "casdoor"

	rendered, err := renderAppConf(e)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(rendered, "dbname=casdoor") {
		t.Fatalf("PostgreSQL DSN is missing the database name:\n%s", rendered)
	}
}
