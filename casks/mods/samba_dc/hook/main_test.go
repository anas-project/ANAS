package main

import "testing"

func TestCalcSambaDCHostIPDefaultsToHostIP(t *testing.T) {
	env := map[string]string{
		"BASE_DOMAIN": "nas.test",
		"SERVER_NAME": "fengoffice",
		"HOST_IP":     "192.0.2.10",
	}
	if err := calcSambaDC(env, "", nil); err != nil {
		t.Fatal(err)
	}
	if got := env["SAMBA_DC_HOST_IP"]; got != "192.0.2.10" {
		t.Fatalf("SAMBA_DC_HOST_IP = %q", got)
	}
}

func TestCalcSambaDCHostIPCanBeOverridden(t *testing.T) {
	env := map[string]string{
		"BASE_DOMAIN":      "nas.test",
		"SERVER_NAME":      "fengoffice",
		"HOST_IP":          "10.254.0.2",
		"SAMBA_DC_HOST_IP": "10.254.0.1",
	}
	if err := calcSambaDC(env, "", nil); err != nil {
		t.Fatal(err)
	}
	if got := env["SAMBA_DC_HOST_IP"]; got != "10.254.0.1" {
		t.Fatalf("SAMBA_DC_HOST_IP = %q", got)
	}
}
