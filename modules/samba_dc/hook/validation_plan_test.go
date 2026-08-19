package main

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestValidateHookReturnsDomainDNSPlanMetadata(t *testing.T) {
	const secret = "samba-validation-secret"
	resp, err := handle(hookRequest{
		ABI:    "anas.module-hook/v1",
		Phase:  "validate",
		Module: "samba_dc",
		Env: map[string]string{
			"BASE_DOMAIN":                     "Apps.Example.NET.",
			"SAMBA_DC_DOMAIN":                 "Corp.Example.COM.",
			"SAMBA_DC_APPLICATION_DNS_MODE":   "auto",
			"SAMBA_DC_ADMINISTRATOR_PASSWORD": secret,
		},
		Secrets: map[string]string{"SAMBA_DC_ADMINISTRATOR_PASSWORD": secret},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"requested_mode": "auto",
		"resolved_mode":  "separate_zone",
		"zone":           "apps.example.net",
	}
	if !reflect.DeepEqual(resp.Plan, want) {
		t.Fatalf("validate plan = %#v, want %#v", resp.Plan, want)
	}
	if len(resp.Env) != 0 || len(resp.Secrets) != 0 || len(resp.Files) != 0 ||
		len(resp.DisableServices) != 0 || len(resp.DockerCopies) != 0 {
		t.Fatalf("validate returned mutation fields: %#v", resp)
	}
	body, err := json.Marshal(resp)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), secret) {
		t.Fatalf("validate response exposed secret plaintext: %s", body)
	}
}
