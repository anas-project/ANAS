package main

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"os"
	"strings"
	"testing"
	"time"
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
		"SAMBA_DC_BASE_USERS_DN": "OU=Users,DC=example,DC=test", "SAMBA_DC_BASE_GROUPS_DN": "OU=Groups,DC=example,DC=test",
		"SAMBA_DC_IDENTITY_ANCHOR_ATTRIBUTE": "anasIdentityAnchor", "SAMBA_DC_GROUP_CLASS_FILTER": "(objectClass=group)",
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
	if got, want := e["CASDOOR_DIRWATCH_IDENTITY_ANCHOR_ATTRIBUTE"], "anasIdentityAnchor"; got != want {
		t.Fatalf("directory watcher identity anchor = %q, want %q", got, want)
	}
	for _, key := range []string{"CASDOOR_PORTAL_CLIENT_ID", "CASDOOR_PORTAL_CLIENT_SECRET", "CASDOOR_SIGNING_MATERIAL"} {
		if secrets.values[key] == "" {
			t.Fatalf("secret %s was not generated", key)
		}
	}
	if e["CASDOOR_SIGNING_CERT"] == "" || e["CASDOOR_SIGNING_KEY"] != "" {
		t.Fatal("signing certificate was not published or the private key escaped its managed material")
	}
	firstMaterial, firstCertificate := secrets.values["CASDOOR_SIGNING_MATERIAL"], e["CASDOOR_SIGNING_CERT"]
	secondEnv := cloneMap(e)
	if err := calcCasdoor(secondEnv, secrets); err != nil {
		t.Fatal(err)
	}
	if secrets.values["CASDOOR_SIGNING_MATERIAL"] != firstMaterial || secondEnv["CASDOOR_SIGNING_CERT"] != firstCertificate {
		t.Fatal("repeated calculate changed managed signing material")
	}
}

func TestCalculateMigratesLegacySigningKeypairWithoutChangingCertificate(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	certificate, err := newSigningCertificate(key, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	privateKey := string(pem.EncodeToMemory(&pem.Block{
		Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key),
	}))
	secrets := &secretStore{values: map[string]string{
		"CASDOOR_SIGNING_KEY": privateKey, "CASDOOR_SIGNING_CERT": certificate,
	}}
	env := map[string]string{}
	if err := ensureSigningKeypair(env, secrets); err != nil {
		t.Fatal(err)
	}
	bundle, err := parseSigningMaterial(secrets.values["CASDOOR_SIGNING_MATERIAL"])
	if err != nil {
		t.Fatal(err)
	}
	if bundle.PrivateKey != privateKey || bundle.Certificate != certificate || env["CASDOOR_SIGNING_CERT"] != certificate {
		t.Fatal("legacy signing keypair changed during managed-material migration")
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

func TestRenderPhasePublishesManagedGroupsAfterConsumerAggregation(t *testing.T) {
	e := casdoorTestEnv()
	e["CASDOOR_DB_HOST"] = "postgres"
	e["CASDOOR_DB_PORT"] = "5432"
	e["CASDOOR_DB_USERNAME"] = "casdoor"
	e["CASDOOR_DB_PASSWORD"] = "db-secret"
	e["CASDOOR_DB_NAME"] = "casdoor"
	e["CASDOOR_DEFAULT_LANGUAGE"] = "en"

	response, err := handle(hookRequest{ABI: "anas.module-hook/v1", Phase: "render_env", Module: "casdoor", Env: e})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := response.Env["CASDOOR_DIRWATCH_MANAGED_GROUPS"], "APP_nextcloud,APP_paperless"; got != want {
		t.Fatalf("managed groups = %q, want %q", got, want)
	}
}
