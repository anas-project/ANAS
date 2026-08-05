package main

import "testing"

func TestCalcNextcloudUsesServiceContainerNames(t *testing.T) {
	env := map[string]string{
		"CONTAINER_PREFIX":  "anas_test_",
		"NEXTCLOUD_DB_TYPE": "postgres",
	}
	secrets := &secretStore{values: map[string]string{}}

	if err := calcNextcloud(env, "", secrets); err != nil {
		t.Fatal(err)
	}
	if got := env["NEXTCLOUD_IMAGINARY_HOSTNAME"]; got != "anas_test_nextcloud_imaginary" {
		t.Fatalf("imaginary hostname = %q", got)
	}
	if got := env["NEXTCLOUD_PUSH_HOSTNAME"]; got != "anas_test_nextcloud_push" {
		t.Fatalf("push hostname = %q", got)
	}
}

func TestServiceKeysAreStableGeneratedSecrets(t *testing.T) {
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
	keys := []string{"NEXTCLOUD_TALK_INTERNAL_SECRET", "TALK_SIGNALING_SECRET", "NEXTCLOUD_IMAGINARY_SECRET"}
	for _, key := range keys {
		if first[key] == "" || first[key] != second[key] {
			t.Fatalf("%s is not stable", key)
		}
		if secrets.values[key] != first[key] {
			t.Fatalf("%s was not persisted", key)
		}
	}
	if first[keys[0]] == first[keys[1]] || first[keys[1]] == first[keys[2]] || first[keys[0]] == first[keys[2]] {
		t.Fatal("service secrets must be distinct")
	}
}
