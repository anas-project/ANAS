package main

import "testing"

func TestPostgresPasswordIsStableRandomSecret(t *testing.T) {
	secrets := &secretStore{values: map[string]string{}}
	env := map[string]string{"NETWORK_PREFIX": "anas_", "CONTAINER_PREFIX": "anas_", "POSTGRES_USERNAME": "postgres", "DEFAULT_SERVICE_ROOT_PASSWORD": "HumanAdmin1!"}
	if err := calcPostgres(env, "", secrets); err != nil {
		t.Fatal(err)
	}
	if env["POSTGRES_PASSWORD"] == "HumanAdmin1!" || env["POSTGRES_PASSWORD"] == "" {
		t.Fatal("PostgreSQL must use a separate random service password")
	}
	if secrets.values["POSTGRES_PASSWORD"] != env["POSTGRES_PASSWORD"] {
		t.Fatal("PostgreSQL password was not persisted")
	}
}

func TestPostgresDoesNotScanConsumerDatabaseVariables(t *testing.T) {
	env := map[string]string{
		"NETWORK_PREFIX": "anas_", "CONTAINER_PREFIX": "anas_", "POSTGRES_USERNAME": "postgres",
		"NEXTCLOUD_DB_NAME": "nextcloud", "NEXTCLOUD_DB_HOST": "anas_postgres",
	}
	if err := calcPostgres(env, "", &secretStore{values: map[string]string{}}); err != nil {
		t.Fatal(err)
	}
	for _, disabled := range disabledServices("postgres", env) {
		if disabled == "anas_postgres_provision" {
			t.Fatal("the provider operation service is runner-owned, not hook-selected")
		}
	}
	if env["NEXTCLOUD_DB_NAME"] != "nextcloud" || env["NEXTCLOUD_DB_HOST"] != "anas_postgres" {
		t.Fatal("provider hook modified consumer resource declarations")
	}
}
