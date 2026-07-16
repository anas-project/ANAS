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

func TestPostgresDependentDatabaseReconcilerRunsAfterInitialization(t *testing.T) {
	env := map[string]string{
		"POSTGRES_HOST": "anas_postgres", "POSTGRES_USER": "postgres",
		"NEXTCLOUD_DB_NAME": "nextcloud", "NEXTCLOUD_DB_HOST": "anas_postgres",
	}
	script := initDatabasesScript(env)
	if script == "" {
		t.Fatal("expected online database reconciliation script")
	}
	for _, disabled := range disabledServices("postgres", env) {
		if disabled == "anas_postgres_reconcile" {
			t.Fatal("reconciler was disabled despite a dependent database")
		}
	}
}
