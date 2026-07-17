package main

import "testing"

func TestTalkSessionKeysAreStableGeneratedSecrets(t *testing.T) {
	secrets := &secretStore{values: map[string]string{}}
	base := map[string]string{
		"DATA_PATH": "./data", "CONTAINER_PREFIX": "anas_", "NETWORK_PREFIX": "anas_",
		"NEXTCLOUD_DOMAIN_PREFIX": "nc", "BASE_DOMAIN": "nas.test", "TRAEFIK_BASE_PORT": "9000",
		"NEXTCLOUD_DB_TYPE": "postgres", "POSTGRES_NETWORK_NAME": "anas_postgres",
	}
	first := cloneMap(base)
	if err := calcNextcloud(first, "", secrets); err != nil {
		t.Fatal(err)
	}
	second := cloneMap(base)
	if err := calcNextcloud(second, "", secrets); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"TALK_HASH_KEY", "TALK_BLOCK_KEY"} {
		if first[key] == "" || first[key] != second[key] {
			t.Fatalf("%s is not stable", key)
		}
		if secrets.values[key] != first[key] {
			t.Fatalf("%s was not persisted", key)
		}
	}
	if first["TALK_HASH_KEY"] == first["TALK_BLOCK_KEY"] {
		t.Fatal("Talk hash and block keys must be distinct")
	}
}
