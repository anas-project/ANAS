package runner

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestComputeControlNetworkProjectionIsConsumerScopedAndProviderDerived(t *testing.T) {
	for _, name := range []string{"", "anas-incus-control", "future_compute-control"} {
		t.Run(name, func(t *testing.T) {
			a := computeApp(t, map[string]string{"forgejo": "anas-fj", "second": "anas-second"})
			a.env["INCUS_CONTROL_NETWORK_NAME"] = name
			if err := a.materializeResourceSecrets(); err != nil {
				t.Fatal(err)
			}
			if err := a.publishModuleResources("forgejo"); err != nil {
				t.Fatal(err)
			}
			prefix := computeResourcePrefix("forgejo", "runners")
			wantExternal := "false"
			if name != "" {
				wantExternal = "true"
			}
			if a.env[prefix+"CONTROL_NETWORK_NAME"] != name || a.env[prefix+"CONTROL_NETWORK_EXTERNAL"] != wantExternal ||
				(name != "" && a.envOwner[prefix+"CONTROL_NETWORK_NAME"] != "forgejo") {
				t.Fatal("network projection was not scoped to the compute consumer")
			}
			if _, exists := a.env[computeResourcePrefix("second", "runners")+"CONTROL_NETWORK_NAME"]; exists {
				t.Fatal("published another consumer's network prematurely")
			}
		})
	}
}

func TestComputeControlNetworkProjectionRejectsUnsafeNames(t *testing.T) {
	for _, name := range []string{"../../host", "name\nextra", "${OVERRIDE}", "--host", strings.Repeat("a", 64)} {
		a := computeApp(t, map[string]string{"forgejo": "anas-fj"})
		a.env["INCUS_CONTROL_NETWORK_NAME"] = name
		if err := a.materializeResourceSecrets(); err != nil {
			t.Fatal(err)
		}
		if err := a.publishModuleResources("forgejo"); err == nil || strings.Contains(err.Error(), name) {
			t.Fatal("invalid network accepted or reflected")
		}
	}
}

func TestBundledComputeConsumersJoinControlNetworkWithoutReplacingBusinessNetwork(t *testing.T) {
	for _, tc := range []struct{ module, service, business, prefix string }{
		{"forgejo", "anas_forgejo_actions_preflight", "actions-control", "ANAS_COMPUTE_RESOURCE__FORGEJO__RUNNERS__"},
		{"forgejo", "anas_forgejo_actions_controller", "actions-control", "ANAS_COMPUTE_RESOURCE__FORGEJO__RUNNERS__"},
		{"ai_agent", "anas_ai_agent", "traefik", "ANAS_COMPUTE_RESOURCE__AI_AGENT__WORK_INSTANCES__"},
	} {
		t.Run(tc.service, func(t *testing.T) {
			body, err := os.ReadFile(filepath.Join("..", "..", "modules", tc.module, "docker-compose.yml"))
			if err != nil {
				t.Fatal(err)
			}
			var doc struct {
				Services map[string]struct {
					Networks map[string]struct {
						GatewayPriority int `yaml:"gw_priority"`
					} `yaml:"networks"`
				} `yaml:"services"`
				Networks map[string]map[string]any `yaml:"networks"`
			}
			// Other services still use the valid list form; inspect only the one
			// under test so no unrelated service syntax gets constrained here.
			var raw struct {
				Services map[string]yaml.Node      `yaml:"services"`
				Networks map[string]map[string]any `yaml:"networks"`
			}
			if err := yaml.Unmarshal(body, &raw); err != nil {
				t.Fatal(err)
			}
			service, ok := raw.Services[tc.service]
			if !ok {
				t.Fatal("missing consumer service")
			}
			var selected struct {
				Networks map[string]struct {
					GatewayPriority int `yaml:"gw_priority"`
				} `yaml:"networks"`
			}
			if err := service.Decode(&selected); err != nil {
				t.Fatal(err)
			}
			doc.Networks = raw.Networks
			if _, ok := selected.Networks["compute-control"]; !ok || selected.Networks[tc.business].GatewayPriority != 1 {
				t.Fatal("compute control attachment changed the business gateway")
			}
			network := doc.Networks["compute-control"]
			if len(network) != 2 || !strings.Contains(network["name"].(string), tc.prefix+"CONTROL_NETWORK_NAME") ||
				network["external"] != "${"+tc.prefix+"CONTROL_NETWORK_EXTERNAL:-false}" {
				t.Fatal("network lifecycle bypasses the frozen resource projection")
			}
		})
	}
}
