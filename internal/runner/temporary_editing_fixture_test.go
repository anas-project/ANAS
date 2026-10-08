package runner

// TEST_CASES: TEMP-T-021 TEMP-T-023

import (
	"bytes"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/anas-project/ANAS/internal/config"
)

// Validate the real rendered editing fixture against the shipped registry.
// This does not run Hooks, Docker, host discovery or document editing.
func temporaryEditingFixture(t *testing.T) (string, map[string]Module) {
	t.Helper()
	root := repoRoot(t)
	registry, err := loadRegistryDir(filepath.Join(root, "modules"))
	if err != nil {
		t.Fatal(err)
	}
	template, err := os.ReadFile(filepath.Join(root, "test-env", "server-workspace-temp-storage-editing.yml.in"))
	if err != nil {
		t.Fatal(err)
	}
	directory := t.TempDir()
	body := strings.NewReplacer(
		"@ENTRY_PORT@", "19443", "@TURN_PORT@", "13478",
		"@DOMAIN@", "tempedit.anas.test", "@PREFIX@", "anas_tempedit_",
		"@ENTRY_IP@", "10.253.71.2", "@TEMP_A@", filepath.Join(directory, "temp-a"),
		"@DOCKER_SOCKET@", "/run/anas-temp-e2e-184107.sock",
		"@NETWORK_NAMESPACE@", "/run/netns/anas-temp-184107",
		"@INTERFACE@", "temp-veth", "@GATEWAY@", "10.253.71.1", "@PREFIX_LENGTH@", "24",
	).Replace(string(template))
	if regexp.MustCompile(`@[A-Z_]+@`).MatchString(body) {
		t.Fatal("rendered fixture still contains an unresolved placeholder")
	}
	source := filepath.Join(directory, "editing.yml")
	if err := os.WriteFile(source, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return source, registry
}

func TestTemporaryEditingFixtureImport(t *testing.T) {
	source, registry := temporaryEditingFixture(t)
	before, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	if err := validateConfigImportSource(source, registry); err != nil {
		t.Fatalf("rendered editing fixture cannot initialize a workspace: %v", err)
	}
	result, err := normalizeImportedConfig(source, registry)
	if err != nil {
		t.Fatal(err)
	}
	normalized := filepath.Join(t.TempDir(), "normalized.yml")
	if err := os.WriteFile(normalized, result.Normalized, 0o600); err != nil {
		t.Fatal(err)
	}
	loaded, err := config.Load(normalized)
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.Modules.Order) != 8 || loaded.Global.HostIP != "10.253.71.2" || loaded.Global.DNSServer != "223.5.5.5" {
		t.Fatalf("editing fixture lost its eight Modules or configured host/DNS: %+v", loaded.Global)
	}
	for key, expected := range map[string]string{
		"DOCKER_SOCKET_PATH":     "/run/anas-temp-e2e-184107.sock",
		"NETWORK_NAMESPACE_PATH": "/run/netns/anas-temp-184107",
		"SAMBA_DC_HOST_IP":       "10.253.71.2", "SAMBA_DC_INTERFACES": "temp-veth",
	} {
		if got := config.Scalar(loaded.Env[key]); got != expected {
			t.Errorf("env.%s = %q, want retained fixture override %q", key, got, expected)
		}
	}
	for module, parameter := range map[string]string{"postgres": "adminer_enabled", "llng": "enable_test", "nextcloud": "talk_enabled"} {
		if got := config.Scalar(loaded.Modules.Values[module].Config[parameter]); got != "false" {
			t.Errorf("%s.%s = %q, want disabled", module, parameter, got)
		}
	}
	if got := config.Scalar(loaded.Modules.Values["nextcloud"].Config["memories_enabled"]); got != "false" {
		t.Errorf("nextcloud.memories_enabled = %q, want disabled", got)
	}
	after, err := os.ReadFile(source)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatalf("read-only config validation changed the input fixture: %v", err)
	}
}

func TestTemporaryEditingFixtureRejectsDuplicateHostIP(t *testing.T) {
	source, registry := temporaryEditingFixture(t)
	body, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	duplicated := strings.Replace(string(body), "env:\n", "env:\n  HOST_IP: 10.253.71.2\n", 1)
	if err := os.WriteFile(source, []byte(duplicated), 0o600); err != nil {
		t.Fatal(err)
	}
	err = validateConfigImportSource(source, registry)
	if err == nil || !strings.Contains(err.Error(), "env.HOST_IP") || !strings.Contains(err.Error(), "global.host_ip") || !strings.Contains(err.Error(), "runtime key HOST_IP") {
		t.Fatalf("equal-value duplicate host address error = %v, want canonical runtime-key collision", err)
	}
}

func TestTemporaryEditingFixtureRejectsDerivedHostInputs(t *testing.T) {
	for key, value := range map[string]string{
		"INTERFACE": "temp-veth", "DEFAULT_GATEWAY_IP": "10.253.71.1", "HOST_SUBNET_MASK": "24",
		"LOCAL_DNS_SERVER": "10.253.71.2", "HOST_DNS_SERVER": "223.5.5.5", "SERVER_NAME": "TEMPEDIT",
	} {
		t.Run(key, func(t *testing.T) {
			source, registry := temporaryEditingFixture(t)
			body, err := os.ReadFile(source)
			if err != nil {
				t.Fatal(err)
			}
			forbidden := strings.Replace(string(body), "env:\n", "env:\n  "+key+": \""+value+"\"\n", 1)
			if err := os.WriteFile(source, []byte(forbidden), 0o600); err != nil {
				t.Fatal(err)
			}
			err = validateConfigImportSource(source, registry)
			if err == nil || !strings.Contains(err.Error(), "env."+key+" is runner-owned") {
				t.Fatalf("derived host field error = %v, want caller-forbidden %s", err, key)
			}
		})
	}
}
