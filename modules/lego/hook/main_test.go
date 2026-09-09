package main

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestCalculateProviderAndNeutralTLSContract(t *testing.T) {
	for _, tc := range []struct {
		name, virtual, provider string
		wantError               bool
	}{
		{"public missing provider", "false", "", true},
		{"virtual needs no DNS", "true", "", false},
		{"unknown provider rejected", "true", "unknown", true},
		{"cloudflare", "false", " cloudflare ", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := hookRequest{Phase: "calculate", Module: "lego", Env: map[string]string{"VIRTUAL_DOMAIN": tc.virtual, "LEGO_DNS_PROVIDER": tc.provider, "BASE_DOMAIN": "example.test", "DATA_PATH": "/data", "EMAIL": "admin@example.test", "LEGO_CLOUDFLARE_DNS_API_TOKEN": "PRIVATE"}, Secrets: map[string]string{"token": "PRIVATE"}}
			response, err := handle(req)
			if (err != nil) != tc.wantError {
				t.Fatalf("unexpected error: %v", err)
			}
			if err != nil {
				return
			}
			if response.Env["ANAS_TLS_CERT_NAME"] != "example.test.crt" || response.Env["ANAS_TLS_TRUST_BUNDLE_NAME"] != "anas-trust-bundle.crt" || response.Env["LEGO_EMAIL"] != "admin@example.test" {
				t.Fatalf("TLS contract: %#v", response.Env)
			}
			if tc.provider != "" && (response.Env["LEGO_PROVIDER_CODE"] != "cloudflare" || response.Env["LEGO_DNS_CRED_KEYS"] != "CLOUDFLARE_DNS_API_TOKEN") {
				t.Fatalf("provider translation: %#v", response.Env)
			}
			encoded, _ := json.Marshal(response)
			if strings.Contains(string(encoded), "PRIVATE") || len(response.Secrets) != 0 {
				t.Fatal("unchanged credentials returned")
			}
			if _, mutated := req.Env["ANAS_TLS_CERT_NAME"]; mutated {
				t.Fatal("input modified")
			}
		})
	}
}
