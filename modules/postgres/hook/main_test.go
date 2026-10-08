package main

import "testing"

func TestPostgresAuthenticationAndMaintenanceBarrier(t *testing.T) {
	env := map[string]string{"POSTGRES_HOST": "anas_test_postgres"}
	if _, err := renderEnv("postgres", env, ""); err != nil {
		t.Fatal(err)
	}
	if env["POSTGRES_HOST_AUTH_METHOD"] != "scram-sha-256" {
		t.Fatal("network authentication must verify passwords")
	}
	args, err := maintenanceCommand(env)
	if err != nil || args[len(args)-1] != "inspect" {
		t.Fatalf("ordinary start must inspect without upgrade: %v %v", args, err)
	}
	env["ANAS_POSTGRES_EXTENSION_MAINTENANCE"] = "true"
	args, err = maintenanceCommand(env)
	if err != nil || args[len(args)-1] != "upgrade" {
		t.Fatalf("controlled provider update must run maintenance: %v %v", args, err)
	}
}

func TestPostgresPasswordIsStableRandomSecret(t *testing.T) {
	secrets := &secretStore{values: map[string]string{}}
	env := map[string]string{"NETWORK_PREFIX": "anas_", "CONTAINER_PREFIX": "anas_", "POSTGRES_USERNAME": "postgres"}
	if err := calcPostgres(env, "", secrets); err != nil {
		t.Fatal(err)
	}
	if env["POSTGRES_PASSWORD"] == "" {
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
