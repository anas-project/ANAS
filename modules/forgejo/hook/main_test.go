package main

import (
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func forgejoCalculateEnv() map[string]string {
	return map[string]string{
		"FORGEJO_DOMAIN_PREFIX":     "git",
		"FORGEJO_DB_TYPE":           "postgres",
		"FORGEJO_SSH_PORT":          "2222",
		"BASE_DOMAIN":               "example.test",
		"TRAEFIK_BASE_PORT":         "443",
		"DEFAULT_LANGUAGE":          "zh-Hant-HK",
		"SAMBA_DC_APP_FILTER":       "true",
		"SAMBA_DC_ADMIN_GROUP_NAME": "Admins",
	}
}

func TestCalculatePublishesForgejoOIDCAndLauncher(t *testing.T) {
	env := forgejoCalculateEnv()
	secrets := &secretStore{values: map[string]string{}}
	if _, err := calculate(env, secrets); err != nil {
		t.Fatal(err)
	}
	if got := env["FORGEJO_DOMAIN_FULL"]; got != "https://git.example.test:443" {
		t.Fatalf("domain = %q", got)
	}
	if got := env[iamClientPrefix+"REDIRECT_URIS"]; got != "https://git.example.test:443/user/oauth2/anas/callback" {
		t.Fatalf("redirect URI = %q", got)
	}
	if got := env[iamClientPrefix+"ALLOW_GROUPS"]; got != "APP_forgejo,APP_all,Admins" {
		t.Fatalf("allow groups = %q", got)
	}
	if got := env["APPS_LIST__FORGEJO__URI"]; got != "https://git.example.test:443/user/oauth2/anas" {
		t.Fatalf("launcher URI = %q", got)
	}
	if env["FORGEJO_OIDC_CLIENT_SECRET"] == "" || env["FORGEJO_SECRET_KEY"] == "" || env["FORGEJO_ACTIONS_CONTROLLER_PASSWORD"] == "" {
		t.Fatal("managed secrets were not generated")
	}
	if env["FORGEJO_OIDC_CLIENT_SECRET"] == env["FORGEJO_SECRET_KEY"] {
		t.Fatal("OIDC and application secrets must be distinct")
	}
	if got := env["FORGEJO_LANGUAGE"]; got != "zh-HK" && got != "zh-TW" {
		t.Fatalf("language = %q, want a traditional-Chinese Forgejo locale", got)
	}
}

func TestCalculatePreservesManagedSecrets(t *testing.T) {
	secrets := &secretStore{values: map[string]string{
		"FORGEJO_OIDC_CLIENT_SECRET": "stable-oidc",
		"FORGEJO_SECRET_KEY":         "stable-app",
	}}
	env := forgejoCalculateEnv()
	if _, err := calculate(env, secrets); err != nil {
		t.Fatal(err)
	}
	if env["FORGEJO_OIDC_CLIENT_SECRET"] != "stable-oidc" || env["FORGEJO_SECRET_KEY"] != "stable-app" {
		t.Fatalf("secrets changed: %+v", secrets.values)
	}
}

func TestRenderEnvMapsDatabasesAndSafeDefaults(t *testing.T) {
	for _, test := range []struct{ selected, upstream string }{{"postgres", "postgres"}, {"mariadb", "mysql"}} {
		t.Run(test.selected, func(t *testing.T) {
			env := map[string]string{
				iamBindingPrefix + "INTERFACE":          "oidc",
				iamBindingPrefix + "OIDC_ISSUER_URL":    "https://id.example.test",
				iamBindingPrefix + "OIDC_DISCOVERY_URL": "https://id.example.test/.well-known/openid-configuration",
				"FORGEJO_OIDC_CLIENT_ID":                "forgejo",
				"FORGEJO_OIDC_CLIENT_SECRET":            "oidc-secret",
				"FORGEJO_SECRET_KEY":                    "app-secret",
				"FORGEJO_DB_TYPE":                       test.selected,
				"FORGEJO_DB_HOST":                       "db",
				"FORGEJO_DB_PORT":                       "5432",
				"FORGEJO_DB_NAME":                       "forgejo",
				"FORGEJO_DB_USERNAME":                   "forgejo",
				"FORGEJO_DB_PASSWORD":                   "db-secret",
				"FORGEJO_NETWORK_DB":                    "anas_db",
				"FORGEJO_DOMAIN":                        "git.example.test",
				"FORGEJO_DOMAIN_FULL":                   "https://git.example.test:443",
				"FORGEJO_SSH_PORT":                      "2222",
				"FORGEJO_LANGUAGE":                      "en-US",
				"FORGEJO_CUSTOM_GIT_HOOKS_ENABLED":      "false",
				"FORGEJO_LOCAL_PATH_IMPORT_ENABLED":     "false",
				"FORGEJO_ACTIONS_ENABLED":               "false",
				"TZ":                                    "Asia/Singapore",
			}
			if err := renderEnv(env); err != nil {
				t.Fatal(err)
			}
			if got := env["FORGEJO__DATABASE__DB_TYPE"]; got != test.upstream {
				t.Fatalf("database type = %q", got)
			}
			if got := env["FORGEJO__ACTIONS__ENABLED"]; got != "false" {
				t.Fatalf("Actions enabled = %q", got)
			}
			if got := env["FORGEJO__SECURITY__DISABLE_GIT_HOOKS"]; got != "true" {
				t.Fatalf("Git hooks disabled = %q", got)
			}
			if got := env["FORGEJO__SECURITY__IMPORT_LOCAL_PATHS"]; got != "false" {
				t.Fatalf("local-path import enabled = %q", got)
			}
			if env["FORGEJO__SERVICE__ENABLE_INTERNAL_SIGNIN"] != "true" || env["FORGEJO__SERVICE__DISABLE_REGISTRATION"] != "true" {
				t.Fatal("local recovery or registration safety settings are wrong")
			}
			if proxies := env["FORGEJO__SECURITY__REVERSE_PROXY_TRUSTED_PROXIES"]; proxies == "" || strings.Contains(proxies, "*") {
				t.Fatalf("unsafe reverse-proxy trust = %q", proxies)
			}
			if got := env["FORGEJO__SECURITY__PASSWORD_HASH_ALGO"]; got != "bcrypt" {
				t.Fatalf("local-account hash algorithm = %q", got)
			}
			if first := strings.Split(env["FORGEJO__I18N__LANGS"], ",")[0]; first != "en-US" {
				t.Fatalf("default locale = %q", first)
			}
			for key := range env {
				if strings.HasPrefix(key, "FORGEJO__") && key != strings.ToUpper(key) {
					t.Fatalf("Forgejo upstream environment key is not ANAS-normalized: %q", key)
				}
			}
		})
	}
}

func TestRenderEnvMapsHighRiskFeaturesIndependently(t *testing.T) {
	tests := []struct {
		name                   string
		hooksEnabled           string
		localPathImportEnabled string
		wantDisableGitHooks    string
		wantImportLocalPaths   string
	}{
		{"both-disabled", "false", "false", "true", "false"},
		{"hooks-only", "true", "false", "false", "false"},
		{"local-import-only", "false", "true", "true", "true"},
		{"both-enabled", "true", "true", "false", "true"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			env := forgejoRenderEnv()
			env["FORGEJO_CUSTOM_GIT_HOOKS_ENABLED"] = test.hooksEnabled
			env["FORGEJO_LOCAL_PATH_IMPORT_ENABLED"] = test.localPathImportEnabled
			if err := renderEnv(env); err != nil {
				t.Fatal(err)
			}
			if got := env["FORGEJO__SECURITY__DISABLE_GIT_HOOKS"]; got != test.wantDisableGitHooks {
				t.Fatalf("DISABLE_GIT_HOOKS = %q, want %q", got, test.wantDisableGitHooks)
			}
			if got := env["FORGEJO__SECURITY__IMPORT_LOCAL_PATHS"]; got != test.wantImportLocalPaths {
				t.Fatalf("IMPORT_LOCAL_PATHS = %q, want %q", got, test.wantImportLocalPaths)
			}
		})
	}
}

func TestRenderEnvRejectsInvalidHighRiskFeatureFlags(t *testing.T) {
	for _, key := range []string{"FORGEJO_CUSTOM_GIT_HOOKS_ENABLED", "FORGEJO_LOCAL_PATH_IMPORT_ENABLED"} {
		t.Run(key, func(t *testing.T) {
			env := forgejoRenderEnv()
			env[key] = "yes"
			if err := renderEnv(env); err == nil || !strings.Contains(err.Error(), key+" must be true or false") {
				t.Fatalf("renderEnv error = %v", err)
			}
		})
	}
}

func TestRenderEnvUsesOneActionsSwitchAndRequiresExecutionPlane(t *testing.T) {
	env := forgejoRenderEnv()
	env["FORGEJO_ACTIONS_ENABLED"] = "true"
	if err := renderEnv(env); err == nil || !strings.Contains(err.Error(), "FORGEJO_ACTIONS_ALLOWED_SCOPES is required") {
		t.Fatalf("missing execution-plane prerequisites error = %v", err)
	}
	env["FORGEJO_ACTIONS_ALLOWED_SCOPES"] = "trusted,team/repo"
	env["FORGEJO_ACTIONS_CONTROLLER_PASSWORD"] = "controller-secret"
	env["FORGEJO_ACTIONS_INCUS_ENDPOINT"] = "https://incus.example.test:8443"
	env["FORGEJO_ACTIONS_INCUS_CLIENT_CERT_B64"] = "Y2VydA=="
	env["FORGEJO_ACTIONS_INCUS_CLIENT_KEY_B64"] = "a2V5"
	env["FORGEJO_ACTIONS_INCUS_SERVER_CERT_B64"] = "c2VydmVy"
	env["FORGEJO_ACTIONS_RUNNER_IMAGE"] = strings.Repeat("a", 64)
	if err := renderEnv(env); err != nil {
		t.Fatal(err)
	}
	if got := env["FORGEJO__ACTIONS__ENABLED"]; got != "true" {
		t.Fatalf("Actions enabled = %q", got)
	}
	env = forgejoRenderEnv()
	env["FORGEJO_ACTIONS_ENABLED"] = "true"
	env["FORGEJO_ACTIONS_ALLOWED_SCOPES"] = "team/repo"
	env["FORGEJO_ACTIONS_CONTROLLER_PASSWORD"] = "controller-secret"
	env["FORGEJO_ACTIONS_INCUS_ENDPOINT"] = "http://incus.example.test:8443"
	env["FORGEJO_ACTIONS_INCUS_CLIENT_CERT_B64"] = "Y2VydA=="
	env["FORGEJO_ACTIONS_INCUS_CLIENT_KEY_B64"] = "a2V5"
	env["FORGEJO_ACTIONS_INCUS_SERVER_CERT_B64"] = "c2VydmVy"
	env["FORGEJO_ACTIONS_RUNNER_IMAGE"] = strings.Repeat("a", 64)
	if err := renderEnv(env); err == nil || !strings.Contains(err.Error(), "HTTPS") {
		t.Fatalf("insecure Incus endpoint error = %v", err)
	}

	env = forgejoRenderEnv()
	env["FORGEJO_ACTIONS_ENABLED"] = "true"
	env["FORGEJO_ACTIONS_ALLOWED_SCOPES"] = "*"
	env["FORGEJO_ACTIONS_CONTROLLER_PASSWORD"] = "controller-secret"
	env["FORGEJO_ACTIONS_INCUS_ENDPOINT"] = "https://incus.example.test:8443"
	env["FORGEJO_ACTIONS_INCUS_CLIENT_CERT_B64"] = "Y2VydA=="
	env["FORGEJO_ACTIONS_INCUS_CLIENT_KEY_B64"] = "a2V5"
	env["FORGEJO_ACTIONS_INCUS_SERVER_CERT_B64"] = "c2VydmVy"
	env["FORGEJO_ACTIONS_RUNNER_IMAGE"] = strings.Repeat("a", 64)
	if err := renderEnv(env); err == nil || !strings.Contains(err.Error(), "scope") {
		t.Fatalf("global scope error = %v", err)
	}
}

func TestComposeDoesNotMountArbitraryImportPaths(t *testing.T) {
	body, err := os.ReadFile("../docker-compose.yml")
	if err != nil {
		t.Fatal(err)
	}
	var compose struct {
		Services map[string]struct {
			Volumes []string `yaml:"volumes"`
		} `yaml:"services"`
	}
	if err := yaml.Unmarshal(body, &compose); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"${ANAS_TLS_CERTS_DIR}/${ANAS_TLS_INTERNAL_CA_NAME}:/etc/ssl/certs/anas-internal-ca.crt:ro",
		"${DATA_PATH}/forgejo:/var/lib/gitea",
	}
	if got := compose.Services["anas_forgejo"].Volumes; !reflect.DeepEqual(got, want) {
		t.Fatalf("Forgejo volumes = %#v, want only the managed CA and data mounts %#v", got, want)
	}
}

func TestModuleHighRiskFeatureDefaultsAndEffects(t *testing.T) {
	body, err := os.ReadFile("../module.yml")
	if err != nil {
		t.Fatal(err)
	}
	var manifest struct {
		Config struct {
			Defaults map[string]any `yaml:"defaults"`
			Types    map[string]any `yaml:"types"`
			Changes  map[string]struct {
				Effect string `yaml:"effect"`
				Apply  string `yaml:"apply"`
			} `yaml:"changes"`
		} `yaml:"config"`
	}
	if err := yaml.Unmarshal(body, &manifest); err != nil {
		t.Fatal(err)
	}
	for _, parameter := range []string{"custom_git_hooks_enabled", "local_path_import_enabled"} {
		if value, ok := manifest.Config.Defaults[parameter]; !ok || value != false {
			t.Errorf("%s default = %#v, want false", parameter, value)
		}
		if kind, ok := manifest.Config.Types[parameter]; !ok || kind != "bool" {
			t.Errorf("%s type = %#v, want bool", parameter, kind)
		}
		change := manifest.Config.Changes[parameter]
		if change.Effect != "container_recreate" || change.Apply != "recreate-forgejo" {
			t.Errorf("%s change policy = %+v", parameter, change)
		}
	}
	if value, ok := manifest.Config.Defaults["actions_enabled"]; !ok || value != false {
		t.Errorf("actions_enabled default = %#v, want false", value)
	}
	if kind := manifest.Config.Types["actions_enabled"]; kind != "bool" {
		t.Errorf("actions_enabled type = %#v, want bool", kind)
	}
	for _, field := range []map[string]any{manifest.Config.Defaults, manifest.Config.Types} {
		for _, forbidden := range []string{"runner_enabled", "forgejo_runner_enabled"} {
			if _, exposed := field[forbidden]; exposed {
				t.Errorf("second Actions switch %s must not be exposed", forbidden)
			}
		}
	}
}

func forgejoRenderEnv() map[string]string {
	return map[string]string{
		iamBindingPrefix + "INTERFACE":          "oidc",
		iamBindingPrefix + "OIDC_ISSUER_URL":    "https://id.example.test",
		iamBindingPrefix + "OIDC_DISCOVERY_URL": "https://id.example.test/.well-known/openid-configuration",
		"FORGEJO_OIDC_CLIENT_ID":                "forgejo",
		"FORGEJO_OIDC_CLIENT_SECRET":            "oidc-secret",
		"FORGEJO_SECRET_KEY":                    "app-secret",
		"FORGEJO_DB_TYPE":                       "postgres",
		"FORGEJO_DB_HOST":                       "db",
		"FORGEJO_DB_PORT":                       "5432",
		"FORGEJO_DB_NAME":                       "forgejo",
		"FORGEJO_DB_USERNAME":                   "forgejo",
		"FORGEJO_DB_PASSWORD":                   "db-secret",
		"FORGEJO_NETWORK_DB":                    "anas_db",
		"FORGEJO_DOMAIN":                        "git.example.test",
		"FORGEJO_DOMAIN_FULL":                   "https://git.example.test:443",
		"FORGEJO_SSH_PORT":                      "2222",
		"FORGEJO_LANGUAGE":                      "en-US",
		"FORGEJO_CUSTOM_GIT_HOOKS_ENABLED":      "false",
		"FORGEJO_LOCAL_PATH_IMPORT_ENABLED":     "false",
		"FORGEJO_ACTIONS_ENABLED":               "false",
		"TZ":                                    "Asia/Singapore",
	}
}

func TestAfterStartPassesOIDCSecretOnlyThroughStdin(t *testing.T) {
	original := runContainerHelper
	defer func() { runContainerHelper = original }()
	runContainerHelper = func(payload []byte, name string, args ...string) ([]byte, error) {
		joined := strings.Join(append([]string{name}, args...), " ")
		if strings.Contains(joined, "do-not-leak") {
			t.Fatal("OIDC secret leaked into docker argv")
		}
		var input oidcInput
		if err := json.Unmarshal(payload, &input); err != nil {
			t.Fatal(err)
		}
		if input.ClientSecret != "do-not-leak" || !strings.Contains(joined, "anas-forgejo-entrypoint oidc") {
			t.Fatalf("input = %+v, command = %s", input, joined)
		}
		return nil, nil
	}
	env := map[string]string{
		"CONTAINER_PREFIX": "anas_", "FORGEJO_OIDC_CLIENT_ID": "forgejo", "FORGEJO_OIDC_CLIENT_SECRET": "do-not-leak",
		iamBindingPrefix + "OIDC_DISCOVERY_URL": "https://id.example.test/.well-known/openid-configuration",
	}
	if err := reconcileOIDC(env); err != nil {
		t.Fatal(err)
	}
}

func TestActionsAccountPasswordPassesOnlyThroughStdin(t *testing.T) {
	original := runContainerHelper
	defer func() { runContainerHelper = original }()
	runContainerHelper = func(payload []byte, name string, args ...string) ([]byte, error) {
		joined := strings.Join(append([]string{name}, args...), " ")
		if strings.Contains(joined, "actions-secret") {
			t.Fatal("Actions controller password leaked into docker argv")
		}
		var input localAdminInput
		if err := json.Unmarshal(payload, &input); err != nil {
			t.Fatal(err)
		}
		if input.Username != "anas_actions_controller" || input.Password != "actions-secret" {
			t.Fatalf("input = %+v", input)
		}
		return nil, nil
	}
	if err := reconcileActionsAccount(map[string]string{
		"CONTAINER_PREFIX": "anas_", "FORGEJO_ACTIONS_ENABLED": "true",
		"FORGEJO_ACTIONS_CONTROLLER_PASSWORD": "actions-secret",
	}); err != nil {
		t.Fatal(err)
	}
}

func TestComposeKeepsIncusCredentialsOutOfForgejo(t *testing.T) {
	body, err := os.ReadFile("../docker-compose.yml")
	if err != nil {
		t.Fatal(err)
	}
	var compose struct {
		Services map[string]struct {
			EnvFile     []string          `yaml:"env_file"`
			Environment map[string]string `yaml:"environment"`
		} `yaml:"services"`
	}
	if err := yaml.Unmarshal(body, &compose); err != nil {
		t.Fatal(err)
	}
	forgejo := compose.Services["anas_forgejo"]
	if len(forgejo.EnvFile) != 0 {
		t.Fatal("Forgejo app must not consume the module-wide env file")
	}
	for key := range forgejo.Environment {
		if strings.Contains(key, "INCUS") || key == "FORGEJO_ACTIONS_CONTROLLER_PASSWORD" {
			t.Fatalf("Forgejo app consumes controller credential %s", key)
		}
	}
	controller := compose.Services["anas_forgejo_actions_controller"]
	preflight := compose.Services["anas_forgejo_actions_preflight"]
	for _, service := range []struct {
		name string
		env  map[string]string
	}{{"controller", controller.Environment}, {"preflight", preflight.Environment}} {
		for _, key := range []string{"INCUS_CLIENT_CERT_B64", "INCUS_CLIENT_KEY_B64", "INCUS_SERVER_CERT_B64"} {
			if service.env[key] == "" {
				t.Errorf("%s environment is missing %s", service.name, key)
			}
		}
	}
}

func TestLocalAccountApplyPassesPasswordOnlyThroughStdin(t *testing.T) {
	original := runContainerHelper
	defer func() { runContainerHelper = original }()
	runContainerHelper = func(payload []byte, name string, args ...string) ([]byte, error) {
		joined := strings.Join(append([]string{name}, args...), " ")
		if strings.Contains(joined, "recovery-secret") {
			t.Fatal("local recovery password leaked into docker argv")
		}
		var input localAdminInput
		if err := json.Unmarshal(payload, &input); err != nil {
			t.Fatal(err)
		}
		if input.Username != "admin_forgejo" || input.Password != "recovery-secret" {
			t.Fatalf("input = %+v", input)
		}
		return nil, nil
	}
	req := hookRequest{
		Module: "forgejo", Phase: "local_account_apply", Env: map[string]string{"CONTAINER_PREFIX": "anas_"},
		Secrets:      map[string]string{"candidate": "recovery-secret"},
		LocalAccount: &localAccountOperation{Handler: "apply-forgejo-break-glass", AccountID: "break_glass", Username: "admin_forgejo", CandidateSecretKey: "candidate"},
	}
	if err := handleLocalAccount(req); err != nil {
		t.Fatal(err)
	}
}

func TestCalculateDoesNotPublishLocalAdminPlaintext(t *testing.T) {
	env := forgejoCalculateEnv()
	env["FORGEJO_LOCAL_ADMIN__BREAK_GLASS_PASSWORD"] = "must-stay-in-hook-input"
	resp, err := handle(hookRequest{Module: "forgejo", Phase: "calculate", Env: env, Secrets: map[string]string{}})
	if err != nil {
		t.Fatal(err)
	}
	for key, value := range resp.Env {
		if value == "must-stay-in-hook-input" || strings.Contains(key, "LOCAL_ADMIN") && strings.Contains(key, "PASSWORD") {
			t.Fatalf("plaintext published as %s", key)
		}
	}
}
