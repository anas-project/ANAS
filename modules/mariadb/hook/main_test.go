package main

import "testing"

func TestMariaDBPasswordIsStableRandomSecret(t *testing.T) {
	secrets := &secretStore{values: map[string]string{}}
	env := map[string]string{"NETWORK_PREFIX": "anas_", "CONTAINER_PREFIX": "anas_"}
	if err := calcMariaDB(env, "", secrets); err != nil {
		t.Fatal(err)
	}
	if env["MARIADB_ROOT_PASSWORD"] == "" {
		t.Fatal("MariaDB must use a separate random service password")
	}
	if secrets.values["MARIADB_ROOT_PASSWORD"] != env["MARIADB_ROOT_PASSWORD"] {
		t.Fatal("MariaDB password was not persisted")
	}
}
