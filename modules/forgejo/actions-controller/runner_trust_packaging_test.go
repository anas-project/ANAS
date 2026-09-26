package main

import (
	"os"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestBothControllerServicesProjectOnlyThePublicTrustFile(t *testing.T) {
	body, err := os.ReadFile("../docker-compose.yml")
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Services map[string]struct {
			User     string
			ReadOnly bool `yaml:"read_only"`
			Volumes  []string
			CapDrop  []string `yaml:"cap_drop"`
		} `yaml:"services"`
	}
	if err := yaml.Unmarshal(body, &doc); err != nil {
		t.Fatal(err)
	}
	want := "${ANAS_TLS_CERTS_DIR}/${ANAS_TLS_INTERNAL_CA_NAME}:" + runnerTrustPath + ":ro"
	for _, service := range []string{"anas_forgejo_actions_preflight", "anas_forgejo_actions_controller"} {
		v, ok := doc.Services[service]
		if !ok || v.User != "65532:65532" || !v.ReadOnly || len(v.CapDrop) != 1 || v.CapDrop[0] != "ALL" {
			t.Fatal("trust projection changed the controller process boundary")
		}
		found := 0
		for _, volume := range v.Volumes {
			if strings.Contains(volume, "ANAS_TLS_") || strings.Contains(volume, "/etc/ssl/") {
				if volume != want {
					t.Fatal("a broader TLS directory or private file was projected")
				}
				found++
			}
		}
		if found != 1 {
			t.Fatal("controller/preflight missing its fixed read-only public CA")
		}
	}
}
