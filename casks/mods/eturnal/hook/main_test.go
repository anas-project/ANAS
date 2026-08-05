package main

import "testing"

func TestCalcEturnalExportsDomainForDNSRegistration(t *testing.T) {
	env := map[string]string{
		"BASE_DOMAIN":        "nas.test",
		"CONTAINER_PREFIX":   "anas_",
		"TURN_DOMAIN_PREFIX": "turn",
		"TURN_PORT":          "3478",
	}
	secrets := &secretStore{values: map[string]string{}}

	if err := calcEturnal(env, "", secrets); err != nil {
		t.Fatal(err)
	}
	if env["TURN_DOMAIN"] != "turn.nas.test" {
		t.Fatalf("TURN_DOMAIN = %q", env["TURN_DOMAIN"])
	}
	if env["ETURNAL_DOMAIN"] != env["TURN_DOMAIN"] {
		t.Fatalf("ETURNAL_DOMAIN = %q", env["ETURNAL_DOMAIN"])
	}
}
