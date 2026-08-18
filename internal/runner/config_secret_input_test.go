package runner

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/anas-project/ANAS/internal/configschema"
)

func lifecycleInputTestRegistry() map[string]Module {
	minimum := 16
	return map[string]Module{
		"demo": {
			Name:          "demo",
			EnvPrefix:     "DEMO",
			Parameters:    []string{"token"},
			InputRequired: []string{"DEMO_TOKEN"},
			Types: map[string]ParamType{
				"token": {Kind: "string", Constraints: configschema.Constraints{MinLength: &minimum}},
			},
			Changes: map[string]ChangePolicy{
				"token": {Effect: "credential_rotate", Sensitive: true, Apply: "demo rotate-token"},
			},
		},
	}
}

func writeLifecycleInputSource(t *testing.T, input string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yml")
	body := "modules:\n  demo:" + input + "\nglobal:\n  base_domain: nas.test\n  email: admin@nas.test\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func captureRunnerStdout(t *testing.T, run func() error) (string, error) {
	t.Helper()
	out, err := os.CreateTemp(t.TempDir(), "stdout")
	if err != nil {
		t.Fatal(err)
	}
	real := os.Stdout
	os.Stdout = out
	defer func() { os.Stdout = real }()
	runErr := run()
	if _, err := out.Seek(0, io.SeekStart); err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(out)
	if err != nil {
		t.Fatal(err)
	}
	if err := out.Close(); err != nil {
		t.Fatal(err)
	}
	return string(body), runErr
}

func TestInputRequiredLifecycleSecretImportsFromEverySupportedPath(t *testing.T) {
	const secret = "correct-horse-battery"
	tests := map[string]string{
		"module config": "\n    config:\n      token: " + secret,
		"raw env":       " {}\nenv:\n  demo_token: " + secret,
		"secrets":       " {}\nsecrets:\n  demo_token: " + secret,
	}
	for name, input := range tests {
		t.Run(name, func(t *testing.T) {
			workspace := t.TempDir()
			result, err := importConfigIntoWorkspace(workspace, writeLifecycleInputSource(t, input), lifecycleInputTestRegistry())
			if err != nil {
				t.Fatalf("importing required lifecycle secret: %v", err)
			}
			if len(result.Secrets) != 1 || result.Secrets[0].Key != "DEMO_TOKEN" || result.Secrets[0].Value != secret {
				t.Fatalf("extracted secrets = %+v", result.Secrets)
			}
			if strings.Contains(string(result.Normalized), secret) {
				t.Fatal("normalized config retained lifecycle secret plaintext")
			}
			store, err := loadSecretStore(stateDir(workspace))
			if err != nil {
				t.Fatal(err)
			}
			if store.values["DEMO_TOKEN"] != secret || store.metadata["DEMO_TOKEN"].Kind != "lifecycle_managed" {
				t.Fatalf("stored lifecycle secret = %q, metadata=%+v", store.values["DEMO_TOKEN"], store.metadata["DEMO_TOKEN"])
			}
		})
	}
}

func TestInputRequiredLifecycleSecretImportFailsBeforeWritingWhenMissing(t *testing.T) {
	workspace := t.TempDir()
	_, err := importConfigIntoWorkspace(workspace, writeLifecycleInputSource(t, " {}"), lifecycleInputTestRegistry())
	if err == nil || !strings.Contains(err.Error(), "DEMO_TOKEN") {
		t.Fatalf("missing lifecycle input error = %v", err)
	}
	if exists(workspaceConfigPath(workspace)) || exists(filepath.Join(stateDir(workspace), "secrets.yml")) {
		t.Fatal("rejected import wrote managed state")
	}
}

func TestLifecycleSecretSchemaErrorsNeverEchoValues(t *testing.T) {
	const rejected = "LEAK-ME"
	for name, input := range map[string]string{
		"module config": "\n    config:\n      token: " + rejected,
		"raw env":       " {}\nenv:\n  DEMO_TOKEN: " + rejected,
		"secrets":       " {}\nsecrets:\n  demo_token: " + rejected,
	} {
		t.Run(name, func(t *testing.T) {
			_, err := normalizeImportedConfig(writeLifecycleInputSource(t, input), lifecycleInputTestRegistry())
			if err == nil || !strings.Contains(err.Error(), "declared string type or constraints") {
				t.Fatalf("schema error = %v", err)
			}
			if strings.Contains(err.Error(), rejected) {
				t.Fatalf("schema error echoed secret value: %v", err)
			}
		})
	}
}

func TestImportedSecretCanonicalizationRejectsCollisionsWithoutValues(t *testing.T) {
	for name, body := range map[string]string{
		"normalized secret keys": " {}\nsecrets:\n  demo_token: first-secret-value\n  DEMO_TOKEN: second-secret-value",
		"secret and module":      "\n    config:\n      token: first-secret-value\nsecrets:\n  DEMO_TOKEN: second-secret-value",
		"secret and env":         " {}\nenv:\n  DEMO_TOKEN: first-secret-value\nsecrets:\n  demo_token: second-secret-value",
	} {
		t.Run(name, func(t *testing.T) {
			_, err := normalizeImportedConfig(writeLifecycleInputSource(t, body), lifecycleInputTestRegistry())
			if err == nil || !strings.Contains(err.Error(), "DEMO_TOKEN") {
				t.Fatalf("collision error = %v", err)
			}
			for _, value := range []string{"first-secret-value", "second-secret-value"} {
				if strings.Contains(err.Error(), value) {
					t.Fatalf("collision error echoed secret value: %v", err)
				}
			}
		})
	}
}

func TestConfigPlanUsesOnlyLifecycleManagedSecretInputs(t *testing.T) {
	const validSecret = "correct-horse-battery"
	for _, test := range []struct {
		name, kind, value string
		wantOK            bool
	}{
		{name: "lifecycle secret present", kind: "lifecycle_managed", value: validSecret, wantOK: true},
		{name: "secret absent"},
		{name: "generated secret is not caller input", kind: "generated", value: validSecret},
		{name: "local admin secret is not caller input", kind: "local_admin", value: validSecret},
		{name: "invalid lifecycle secret", kind: "lifecycle_managed", value: "LEAK-ME"},
	} {
		t.Run(test.name, func(t *testing.T) {
			workspace := t.TempDir()
			configPath := writeLifecycleInputSource(t, " {}")
			managedPath := workspaceConfigPath(workspace)
			body, err := os.ReadFile(configPath)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(managedPath, body, 0o600); err != nil {
				t.Fatal(err)
			}
			base := stateDir(workspace)
			if err := os.MkdirAll(base, 0o700); err != nil {
				t.Fatal(err)
			}
			if test.kind != "" {
				store := &secretStore{
					path: filepath.Join(base, "secrets.yml"), values: map[string]string{"DEMO_TOKEN": test.value},
					metadata: map[string]secretMetadata{"DEMO_TOKEN": {Owner: "demo", Kind: test.kind, Provenance: "test"}}, dirty: true,
				}
				if err := store.Save(); err != nil {
					t.Fatal(err)
				}
			}
			stdout, err := captureRunnerStdout(t, func() error {
				return reportConfigPlan(workspace, managedPath, base, lifecycleInputTestRegistry(), true)
			})
			if test.wantOK {
				if err != nil {
					t.Fatalf("plan rejected lifecycle input: %v", err)
				}
				if strings.Contains(stdout, validSecret) {
					t.Fatal("plan output exposed lifecycle secret")
				}
				return
			}
			if err == nil {
				t.Fatalf("plan unexpectedly accepted kind=%q value=%q", test.kind, test.value)
			}
			if test.kind == "lifecycle_managed" {
				if strings.Contains(err.Error(), test.value) || !strings.Contains(err.Error(), "declared type or constraints") {
					t.Fatalf("invalid lifecycle secret error = %v", err)
				}
			} else if !strings.Contains(err.Error(), "DEMO_TOKEN") {
				t.Fatalf("missing lifecycle input error = %v", err)
			}
		})
	}
}

func TestConfigListUsesCanonicalEffectiveInputsAndHidesLifecycleSecrets(t *testing.T) {
	workspace := t.TempDir()
	configPath := workspaceConfigPath(workspace)
	if err := os.WriteFile(configPath, []byte(`modules:
  traefik: {}
  samba_dc: {}
env:
  TRAEFIK_BASE_PORT: 12345
  BASE_DOMAIN: env.nas.test
  EMAIL: admin@nas.test
`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := writeManagedConfigState(workspace, "canonical-list-test"); err != nil {
		t.Fatal(err)
	}
	reg, err := loadRegistry(repoRoot(t))
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := captureRunnerStdout(t, func() error {
		return reportConfigList(configPath, reg, "", true, stateDir(workspace))
	})
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err := json.Unmarshal([]byte(stdout), &document); err != nil {
		t.Fatalf("config list JSON = %q: %v", stdout, err)
	}
	byPath := map[string]map[string]any{}
	for _, raw := range document["parameters"].([]any) {
		entry := raw.(map[string]any)
		byPath[entry["path"].(string)] = entry
	}
	for path, want := range map[string]string{
		"traefik.base_port":  "12345",
		"global.base_domain": "env.nas.test",
	} {
		entry := byPath[path]
		if entry["set"] != true || entry["value"] != want {
			t.Errorf("%s list projection = %#v, want value %q", path, entry, want)
		}
	}

	// Import a lifecycle-managed value through the normal public boundary, then
	// verify both projections expose presence and never plaintext.
	secretSource := filepath.Join(t.TempDir(), "secret.yml")
	const secret = "Directory-Admin-Initial-1!"
	if err := os.WriteFile(secretSource, []byte(`modules:
  samba_dc:
    config:
      admin_password: `+secret+`
global:
  base_domain: nas.test
  email: admin@nas.test
`), 0o600); err != nil {
		t.Fatal(err)
	}
	secretWorkspace := t.TempDir()
	if _, err := importConfigIntoWorkspace(secretWorkspace, secretSource, reg); err != nil {
		t.Fatal(err)
	}
	for _, jsonMode := range []bool{false, true} {
		stdout, err := captureRunnerStdout(t, func() error {
			return reportConfigList(workspaceConfigPath(secretWorkspace), reg, "samba_dc", jsonMode, stateDir(secretWorkspace))
		})
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(stdout, secret) {
			t.Fatalf("config list exposed lifecycle secret (json=%t): %s", jsonMode, stdout)
		}
		if jsonMode {
			var doc map[string]any
			if err := json.Unmarshal([]byte(stdout), &doc); err != nil {
				t.Fatal(err)
			}
			found := false
			for _, raw := range doc["parameters"].([]any) {
				entry := raw.(map[string]any)
				if entry["path"] == "samba_dc.admin_password" {
					found = true
					if entry["set"] != true {
						t.Fatalf("lifecycle secret JSON entry = %#v", entry)
					}
					if _, exposed := entry["value"]; exposed {
						t.Fatalf("lifecycle secret JSON contains value: %#v", entry)
					}
				}
			}
			if !found {
				t.Fatal("JSON list omitted samba_dc.admin_password")
			}
		} else if !strings.Contains(stdout, "samba_dc.admin_password") || !strings.Contains(stdout, "<set>") {
			t.Fatalf("human list did not report lifecycle secret presence:\n%s", stdout)
		}
	}
}
