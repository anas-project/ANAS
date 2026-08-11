package runner

import (
	"strings"
	"testing"

	"github.com/anas-project/ANAS/internal/config"
)

func TestMatchEnvPattern(t *testing.T) {
	patterns := []string{"EXACT_KEY", "APPS_LIST*", "*_DB_NAME"}
	matches := []string{"EXACT_KEY", "APPS_LIST", "APPS_LIST__NC__NAME", "NEXTCLOUD_DB_NAME"}
	for _, key := range matches {
		if !matchEnvPattern(patterns, key) {
			t.Errorf("expected %q to match", key)
		}
	}
	misses := []string{"EXACT_KEY_MORE", "OTHER", "DB_NAME_SUFFIXED"}
	for _, key := range misses {
		if matchEnvPattern(patterns, key) {
			t.Errorf("expected %q not to match", key)
		}
	}
}

func scopeTestApp() *app {
	return &app{
		reg: map[string]Module{
			"core":      {Name: "core", EnvPrefix: "CORE"},
			"traefik":   {Name: "traefik", EnvPrefix: "TRAEFIK"},
			"postgres":  {Name: "postgres", EnvPrefix: "POSTGRES"},
			"nextcloud": {Name: "nextcloud", EnvPrefix: "NEXTCLOUD", Consumes: []string{"EXTRA_CONTRACT_*", "ANAS_IDENTITY_SAML_CLIENTS"}},
			// Two DDNS implementations coexist, and their credentials are told
			// apart by env prefix alone. ddns_updater claims one canonical
			// vendor key through consumes; everything else relies on the
			// prefix rule.
			"ddns_updater": {Name: "ddns_updater", EnvPrefix: "DDNS_UPDATER", Consumes: []string{"DNSPOD_API_KEY"}},
			"ddns_go":      {Name: "ddns_go", EnvPrefix: "DDNS_GO"},
		},
		deps: map[string][]string{
			"traefik":      {"core"},
			"postgres":     {"core", "traefik"},
			"nextcloud":    {"core", "traefik", "postgres"},
			"ddns_updater": {"core", "traefik"},
			"ddns_go":      {"core", "traefik"},
		},
		env: map[string]string{
			"BASE_DOMAIN":                          "nas.example.com",
			"HOST_IP":                              "192.0.2.10",
			"POSTGRES_PASSWORD":                    "db-secret",
			"NEXTCLOUD_DB_NAME":                    "nextcloud",
			"DDNS_UPDATER_CONFIG":                  "ddns-secret",
			"DNSPOD_API_KEY":                       "token",
			"DDNS_UPDATER_TENCENTCLOUD_SECRET_KEY": "updater-only",
			"DDNS_GO_TENCENTCLOUD_SECRET_KEY":      "ddns-go-only",
			"EXTRA_CONTRACT_KEY":                   "shared",
			"UNRELATED_HOOK_KEY":                   "private",
			"SMAL_SP_APPS":                         "nextcloud",
			"DEFAULT_GATEWAY_IP":                   "192.0.2.1",
			"NEXTCLOUD_ADMIN_PWD":                  "pw",
			"ANAS_IDENTITY_CLIENTS":                "nextcloud",
			"ANAS_IDENTITY_SAML_CLIENTS":           "nextcloud",
		},
		envOwner: map[string]string{
			"BASE_DOMAIN":                          "",
			"HOST_IP":                              "core",
			"POSTGRES_PASSWORD":                    "postgres",
			"NEXTCLOUD_DB_NAME":                    "nextcloud",
			"DDNS_UPDATER_CONFIG":                  "ddns_updater",
			"DNSPOD_API_KEY":                       config.OwnerUserSecret,
			"DDNS_UPDATER_TENCENTCLOUD_SECRET_KEY": config.OwnerUserSecret,
			"DDNS_GO_TENCENTCLOUD_SECRET_KEY":      config.OwnerUserSecret,
			"EXTRA_CONTRACT_KEY":                   "ddns_updater",
			"UNRELATED_HOOK_KEY":                   "ddns_updater",
			"SMAL_SP_APPS":                         "nextcloud",
			"DEFAULT_GATEWAY_IP":                   "core",
			"NEXTCLOUD_ADMIN_PWD":                  "nextcloud",
			"ANAS_IDENTITY_CLIENTS":                "runner",
			"ANAS_IDENTITY_SAML_CLIENTS":           "runner",
		},
	}
}

func TestScopedEnvFiltersByClosureAndConsumes(t *testing.T) {
	a := scopeTestApp()
	env := a.scopedEnv("nextcloud")
	for _, want := range []string{"BASE_DOMAIN", "HOST_IP", "POSTGRES_PASSWORD", "NEXTCLOUD_DB_NAME", "EXTRA_CONTRACT_KEY", "SMAL_SP_APPS", "ANAS_IDENTITY_SAML_CLIENTS"} {
		if _, ok := env[want]; !ok {
			t.Errorf("nextcloud scope is missing %s", want)
		}
	}
	for _, banned := range []string{"DDNS_UPDATER_CONFIG", "DNSPOD_API_KEY", "UNRELATED_HOOK_KEY"} {
		if _, ok := env[banned]; ok {
			t.Errorf("nextcloud scope leaks %s", banned)
		}
	}
	if _, ok := env["ANAS_IDENTITY_CLIENTS"]; ok {
		t.Error("nextcloud received an undeclared runner identity contract")
	}
	if _, ok := a.scopedEnv("traefik")["ANAS_IDENTITY_SAML_CLIENTS"]; ok {
		t.Error("traefik received identity topology without declaring consumes")
	}
}

func TestScopedEnvUserSecretsRequireClaim(t *testing.T) {
	a := scopeTestApp()
	if _, ok := a.scopedEnv("ddns_updater")["DNSPOD_API_KEY"]; !ok {
		t.Error("ddns_updater declared DNSPOD_API_KEY in consumes but did not receive it")
	}
	if _, ok := a.scopedEnv("traefik")["DNSPOD_API_KEY"]; ok {
		t.Error("traefik received an unclaimed user secret")
	}
}

// Per-engine DNS credentials are separated by env prefix alone, with no
// consumes entry on either side. This is what lets a deployment run two DDNS
// implementations against the same vendor with different accounts, and it is
// also why the updater cask is named ddns_updater rather than ddns: isOwn
// matches on a prefix, so a cask named ddns would own every DDNS_GO_* key.
func TestScopedEnvSeparatesPerEngineCredentials(t *testing.T) {
	a := scopeTestApp()
	updater := a.scopedEnv("ddns_updater")
	ddnsGo := a.scopedEnv("ddns_go")

	if _, ok := updater["DDNS_UPDATER_TENCENTCLOUD_SECRET_KEY"]; !ok {
		t.Error("ddns_updater did not receive its own prefixed credential")
	}
	if _, ok := ddnsGo["DDNS_GO_TENCENTCLOUD_SECRET_KEY"]; !ok {
		t.Error("ddns_go did not receive its own prefixed credential")
	}
	if _, ok := updater["DDNS_GO_TENCENTCLOUD_SECRET_KEY"]; ok {
		t.Error("ddns_updater received ddns_go's credential")
	}
	if _, ok := ddnsGo["DDNS_UPDATER_TENCENTCLOUD_SECRET_KEY"]; ok {
		t.Error("ddns_go received ddns_updater's credential")
	}
	// ddns_go declares no consumes at all, so an unprefixed canonical secret
	// must not reach it.
	if _, ok := ddnsGo["DNSPOD_API_KEY"]; ok {
		t.Error("ddns_go received an unclaimed canonical user secret")
	}
}

func TestCoreScopeIsGlobalOnly(t *testing.T) {
	a := scopeTestApp()
	env := a.scopedEnv("core")
	if _, ok := env["POSTGRES_PASSWORD"]; ok {
		t.Error("core scope must not contain other casks' secrets")
	}
	if _, ok := env["HOST_IP"]; !ok {
		t.Error("core scope must contain core-derived keys")
	}
	if _, ok := env["BASE_DOMAIN"]; !ok {
		t.Error("core scope must contain global keys")
	}
}

func TestApplyCalculatePatchEnforcesExports(t *testing.T) {
	a := scopeTestApp()
	nc := a.reg["nextcloud"]

	if err := a.applyCalculatePatch(nc, map[string]string{"NEXTCLOUD_DOMAIN": "nc.example.com"}); err != nil {
		t.Fatalf("own-prefix write rejected: %v", err)
	}
	if a.envOwner["NEXTCLOUD_DOMAIN"] != "nextcloud" {
		t.Fatalf("owner = %q, want nextcloud", a.envOwner["NEXTCLOUD_DOMAIN"])
	}

	err := a.applyCalculatePatch(nc, map[string]string{"FOREIGN_KEY": "x"})
	if err == nil || !strings.Contains(err.Error(), "FOREIGN_KEY") {
		t.Fatalf("undeclared write not rejected: %v", err)
	}
	if _, ok := a.env["FOREIGN_KEY"]; ok {
		t.Fatal("rejected key must not be merged")
	}

	nc.Exports = []string{"SMAL_SP_*"}
	if err := a.applyCalculatePatch(nc, map[string]string{"SMAL_SP__NC__DOMAIN": "nc"}); err != nil {
		t.Fatalf("exported write rejected: %v", err)
	}

	core := a.reg["core"]
	if err := a.applyCalculatePatch(core, map[string]string{"ANY_GLOBAL": "v"}); err != nil {
		t.Fatalf("core write rejected: %v", err)
	}
	if a.envOwner["ANY_GLOBAL"] != "core" {
		t.Fatalf("core-written key owner = %q", a.envOwner["ANY_GLOBAL"])
	}
}

func TestCaskRootPasswordGeneratedPerCask(t *testing.T) {
	a := scopeTestApp()
	a.cfg = &config.File{}
	a.secrets = &secretStore{values: map[string]string{}}
	envA := map[string]string{}
	if err := a.applyCaskRootPassword(envA, "nextcloud"); err != nil {
		t.Fatal(err)
	}
	envB := map[string]string{}
	if err := a.applyCaskRootPassword(envB, "postgres"); err != nil {
		t.Fatal(err)
	}
	pa, pb := envA["DEFAULT_SERVICE_ROOT_PASSWORD"], envB["DEFAULT_SERVICE_ROOT_PASSWORD"]
	if len(pa) < 16 || len(pb) < 16 {
		t.Fatalf("generated passwords too short: %q %q", pa, pb)
	}
	if pa == pb {
		t.Fatal("per-cask passwords must differ")
	}
	if a.secrets.values["NEXTCLOUD_DEFAULT_ROOT_PASSWORD"] != pa {
		t.Fatal("generated password not persisted under the cask prefix")
	}
	// A configured shared password takes precedence.
	a.cfg.Global.DefaultServiceRootPassword = "SharedPass1!"
	envC := map[string]string{}
	if err := a.applyCaskRootPassword(envC, "nextcloud"); err != nil {
		t.Fatal(err)
	}
	if envC["DEFAULT_SERVICE_ROOT_PASSWORD"] != "SharedPass1!" {
		t.Fatal("configured shared password must win")
	}
}
