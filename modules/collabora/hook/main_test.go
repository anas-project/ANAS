package main

import "testing"

func TestCollaboraUsesRuleUsernameAndOwnStableRandomPassword(t *testing.T) {
	secrets := &secretStore{values: map[string]string{}}
	env := map[string]string{"COLLABORA_DOMAIN_PREFIX": "office", "BASE_DOMAIN": "nas.test", "TRAEFIK_BASE_PORT": "443", "CONTAINER_PREFIX": "anas_", "COLLABORA_ADMIN_USERNAME": "admin_collabora"}
	if err := calculate("collabora", env, "", secrets); err != nil {
		t.Fatal(err)
	}
	if env["COLLABORA_ADMIN_USERNAME"] != "admin_collabora" {
		t.Fatal("Collabora username did not follow module default")
	}
	if env["COLLABORA_ADMIN_PASSWORD"] == "" || secrets.values["COLLABORA_ADMIN_PASSWORD"] != env["COLLABORA_ADMIN_PASSWORD"] {
		t.Fatal("Collabora random password was not persisted")
	}
}

func TestCollaboraKeepsExplicitModulePassword(t *testing.T) {
	env := map[string]string{"COLLABORA_DOMAIN_PREFIX": "office", "BASE_DOMAIN": "nas.test", "TRAEFIK_BASE_PORT": "443", "CONTAINER_PREFIX": "anas_", "COLLABORA_ADMIN_PASSWORD": "Collabora-Explicit-1!"}
	if err := calculate("collabora", env, "", &secretStore{values: map[string]string{}}); err != nil {
		t.Fatal(err)
	}
	if env["COLLABORA_ADMIN_PASSWORD"] != "Collabora-Explicit-1!" {
		t.Fatal("explicit Collabora password was overwritten")
	}
}
