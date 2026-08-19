package runner

import (
	"os"
	"path/filepath"
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
			"traefik":  {Name: "traefik", EnvPrefix: "TRAEFIK"},
			"postgres": {Name: "postgres", EnvPrefix: "POSTGRES"},
			"samba_dc": {Name: "samba_dc", EnvPrefix: "SAMBA_DC", Consumes: []string{"DOMAINS"}},
			// POSTGRES_PASSWORD is declared rather than inherited: being a
			// dependency of postgres no longer carries postgres's values along
			// with it, which is the whole point of the declaration.
			"nextcloud": {Name: "nextcloud", EnvPrefix: "NEXTCLOUD", Consumes: []string{"EXTRA_CONTRACT_*", "ANAS_IDENTITY_SAML_CLIENTS", "POSTGRES_PASSWORD"}},
			// Two DDNS implementations coexist, and their credentials are told
			// apart by env prefix alone. ddns_updater claims one canonical
			// vendor key through consumes; everything else relies on the
			// prefix rule.
			"ddns_updater": {Name: "ddns_updater", EnvPrefix: "DDNS_UPDATER", Consumes: []string{"DNSPOD_API_KEY"}},
			"ddns_go":      {Name: "ddns_go", EnvPrefix: "DDNS_GO"},
		},
		deps: map[string][]string{
			"postgres":     {"traefik"},
			"nextcloud":    {"traefik", "postgres"},
			"ddns_updater": {"traefik"},
			"ddns_go":      {"traefik"},
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
			"DOMAINS":                              "inner/nas/samba_dc,inner/nc/nextcloud",
		},
		envOwner: map[string]string{
			"BASE_DOMAIN":                          "",
			"HOST_IP":                              globalScope,
			"POSTGRES_PASSWORD":                    "postgres",
			"NEXTCLOUD_DB_NAME":                    "nextcloud",
			"DDNS_UPDATER_CONFIG":                  "ddns_updater",
			"DNSPOD_API_KEY":                       config.OwnerUserSecret,
			"DDNS_UPDATER_TENCENTCLOUD_SECRET_KEY": config.OwnerUserSecret,
			"DDNS_GO_TENCENTCLOUD_SECRET_KEY":      config.OwnerUserSecret,
			"EXTRA_CONTRACT_KEY":                   "ddns_updater",
			"UNRELATED_HOOK_KEY":                   "ddns_updater",
			"SMAL_SP_APPS":                         "nextcloud",
			"DEFAULT_GATEWAY_IP":                   globalScope,
			"NEXTCLOUD_ADMIN_PWD":                  "nextcloud",
			"ANAS_IDENTITY_CLIENTS":                "runner",
			"ANAS_IDENTITY_SAML_CLIENTS":           "runner",
			"DOMAINS":                              runnerScope,
		},
	}
}

func TestRunnerTopologyOnlyReachesDeclaredConsumer(t *testing.T) {
	a := scopeTestApp()
	if got := a.scopedEnv("samba_dc")["DOMAINS"]; got == "" {
		t.Fatal("samba_dc declared DOMAINS but did not receive it")
	}
	for _, name := range []string{"traefik", "postgres", "nextcloud"} {
		if _, ok := a.scopedEnv(name)["DOMAINS"]; ok {
			t.Errorf("%s received runner-owned DOMAINS without declaring it", name)
		}
	}
}

// A module receives global values, its own, and exactly what it declares. The
// dependency closure decides start order and nothing else: depending on
// postgres is not a reason to be handed every postgres variable.
func TestScopedEnvFiltersByDeclarationRatherThanClosure(t *testing.T) {
	a := scopeTestApp()
	env := a.scopedEnv("nextcloud")

	// SMAL_SP_APPS is nextcloud's own by ownership, POSTGRES_PASSWORD is
	// reached only through consumes.
	undeclared := scopeTestApp()
	undeclared.reg["nextcloud"] = Module{Name: "nextcloud", EnvPrefix: "NEXTCLOUD"}
	if _, ok := undeclared.scopedEnv("nextcloud")["POSTGRES_PASSWORD"]; ok {
		t.Error("a dependency's value crossed the boundary without being declared")
	}
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

func TestSensitiveScopeDoesNotTrustAnOverlappingPrefix(t *testing.T) {
	a := &app{
		reg: map[string]Module{
			"one": {Name: "one", EnvPrefix: "SHARED", Parameters: []string{"alpha"}, Types: map[string]ParamType{"alpha": {Kind: "string"}}},
			"two": {
				Name: "two", EnvPrefix: "SHARED", Parameters: []string{"token"}, Types: map[string]ParamType{"token": {Kind: "string"}},
				Changes: map[string]ChangePolicy{"token": {Sensitive: true}},
			},
		},
		env: map[string]string{"SHARED_TOKEN": "secret"}, envOwner: map[string]string{"SHARED_TOKEN": "two"},
	}
	if _, leaked := a.scopedEnv("one")["SHARED_TOKEN"]; leaked {
		t.Fatal("module received another owner's sensitive value through an overlapping prefix")
	}
	if got := a.scopedEnv("two")["SHARED_TOKEN"]; got != "secret" {
		t.Fatal("the declared owner did not receive its own sensitive value")
	}
}

func TestSensitiveEnvKeySetTaintsEqualValueAliases(t *testing.T) {
	const secret = "same-secret-plaintext"
	for name, app := range map[string]*app{
		"lifecycle provenance": {
			env:             map[string]string{"SOURCE_SECRET": secret, "DEMO_SELECTOR": secret},
			runnerSensitive: map[string]bool{"SOURCE_SECRET": true},
		},
		"config secrets provenance": {
			cfg: &config.File{Secrets: map[string]any{"SOURCE_SECRET": secret}},
			env: map[string]string{"SOURCE_SECRET": secret, "DEMO_SELECTOR": secret},
		},
	} {
		t.Run(name, func(t *testing.T) {
			if !app.sensitiveEnvKeySet()["DEMO_SELECTOR"] {
				t.Fatal("an equal-value alias lost secret source provenance")
			}
		})
	}
}

func TestCalculatePatchRejectsCrossOwnerOverwriteAtomically(t *testing.T) {
	a := &app{
		env:      map[string]string{"HOST_IP": "192.0.2.10"},
		envOwner: map[string]string{"HOST_IP": globalScope},
	}
	mod := Module{Name: "malicious", EnvPrefix: "HOST", Exports: []string{"EXPORTED_OK"}}
	err := a.applyCalculatePatch(mod, map[string]string{"EXPORTED_OK": "must-not-land", "HOST_IP": "203.0.113.9"})
	if err == nil || !strings.Contains(err.Error(), "owned by another source") {
		t.Fatalf("cross-owner patch error = %v", err)
	}
	if a.env["HOST_IP"] != "192.0.2.10" {
		t.Fatalf("Hook overwrote global HOST_IP: %q", a.env["HOST_IP"])
	}
	if _, landed := a.env["EXPORTED_OK"]; landed {
		t.Fatal("rejected Hook patch was applied partially")
	}
}

func TestCalculatePatchMergesApplicationListWithoutGivingOneModuleOwnership(t *testing.T) {
	a := &app{
		env:      map[string]string{"APPS_LIST": ""},
		envOwner: map[string]string{"APPS_LIST": globalScope},
	}
	nextcloud := Module{Name: "nextcloud", EnvPrefix: "NEXTCLOUD", Exports: []string{"APPS_LIST*"}}
	meshcentral := Module{Name: "meshcentral", EnvPrefix: "MESHCENTRAL", Exports: []string{"APPS_LIST*"}}

	if err := a.applyCalculatePatch(nextcloud, map[string]string{
		"APPS_LIST": "nextcloud", "APPS_LIST__NEXTCLOUD__NAME": "Nextcloud",
	}); err != nil {
		t.Fatal(err)
	}
	if err := a.applyCalculatePatch(meshcentral, map[string]string{
		"APPS_LIST": "nextcloud,meshcentral", "APPS_LIST__MESHCENTRAL__NAME": "MeshCentral",
	}); err != nil {
		t.Fatal(err)
	}
	if got := a.env["APPS_LIST"]; got != "nextcloud,meshcentral" {
		t.Fatalf("APPS_LIST = %q", got)
	}
	if got := a.envOwner["APPS_LIST"]; got != runnerScope {
		t.Fatalf("APPS_LIST owner = %q, want runner", got)
	}
	if got := a.envOwner["APPS_LIST__NEXTCLOUD__NAME"]; got != "nextcloud" {
		t.Fatalf("Nextcloud metadata owner = %q", got)
	}
	if got := a.envOwner["APPS_LIST__MESHCENTRAL__NAME"]; got != "meshcentral" {
		t.Fatalf("MeshCentral metadata owner = %q", got)
	}

	before := a.env["APPS_LIST"]
	for name, value := range map[string]string{
		"delete another module": "meshcentral",
		"reorder entries":       "meshcentral,nextcloud",
		"append foreign entry":  "nextcloud,meshcentral,foreign",
		"duplicate entry":       "nextcloud,meshcentral,meshcentral",
	} {
		t.Run(name, func(t *testing.T) {
			err := a.applyCalculatePatch(meshcentral, map[string]string{"APPS_LIST": value})
			if err == nil || !strings.Contains(err.Error(), "must preserve APPS_LIST") {
				t.Fatalf("invalid APPS_LIST error = %v", err)
			}
			if got := a.env["APPS_LIST"]; got != before {
				t.Fatalf("rejected patch changed APPS_LIST to %q", got)
			}
		})
	}
}

// Per-engine DNS credentials are separated by env prefix alone, with no
// consumes entry on either side. This is what lets a deployment run two DDNS
// implementations against the same vendor with different accounts, and it is
// also why the updater module is named ddns_updater rather than ddns: isOwn
// matches on a prefix, so a module named ddns would own every DDNS_GO_* key.
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

// The deployment-wide environment is what artifact start, stop and rollback
// read back, so it must carry the global keys and nothing a module owns.
func TestGlobalEnvIsGlobalOnly(t *testing.T) {
	a := scopeTestApp()
	env := a.globalEnv()
	if _, ok := env["POSTGRES_PASSWORD"]; ok {
		t.Error("global env must not contain a module's secrets")
	}
	for _, want := range []string{"HOST_IP", "DEFAULT_GATEWAY_IP", "BASE_DOMAIN"} {
		if _, ok := env[want]; !ok {
			t.Errorf("global env is missing %s", want)
		}
	}
}

// Host facts are visible to every module through their ownership, not through a
// dependency edge each module had to be given. Nothing here depends on whoever
// discovered them.
func TestGlobalKeysReachModulesWithoutAnEdge(t *testing.T) {
	a := scopeTestApp()
	for _, name := range []string{"traefik", "postgres", "nextcloud", "ddns_go"} {
		env := a.scopedEnv(name)
		for _, want := range []string{"HOST_IP", "DEFAULT_GATEWAY_IP", "BASE_DOMAIN"} {
			if _, ok := env[want]; !ok {
				t.Errorf("%s scope is missing globally owned %s", name, want)
			}
		}
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

	// No module may write an unprefixed key it has not declared. This used to be
	// exempted for one module named "core", which is why a deployment-wide value
	// could appear from a bundle with nothing declaring it.
	if err := a.applyCalculatePatch(a.reg["traefik"], map[string]string{"ANY_GLOBAL": "v"}); err == nil {
		t.Fatal("an undeclared unprefixed write was accepted")
	}
}

func TestHookSecretWriteContractAndPersistedOwnerControlScope(t *testing.T) {
	alpha := Module{Name: "alpha", EnvPrefix: "ALPHA"}
	beta := Module{Name: "beta", EnvPrefix: "BETA", Parameters: []string{"token"}}
	store := &secretStore{values: map[string]string{}, metadata: map[string]secretMetadata{}}
	a := &app{
		reg:      map[string]Module{"alpha": alpha, "beta": beta},
		deps:     map[string][]string{},
		env:      map[string]string{},
		envOwner: map[string]string{},
		secrets:  store,
	}

	patch := map[string]string{"BETA_TOKEN": "generated-by-alpha"}
	err := a.validateCalculateSecretPatch(alpha, patch)
	if err == nil || !strings.Contains(err.Error(), "undeclared secret keys") || !strings.Contains(err.Error(), "BETA_TOKEN") {
		t.Fatalf("cross-prefix Secret write error = %v", err)
	}
	if len(store.values) != 0 || store.dirty {
		t.Fatalf("rejected Secret write mutated Store: %#v dirty=%t", store.values, store.dirty)
	}
	if _, leaked := a.scopedSecrets("beta")["BETA_TOKEN"]; leaked {
		t.Fatal("a rejected cross-owner Secret key was later delivered by prefix")
	}

	// Cross-prefix publication remains available through the same exports
	// contract as Env patches. The producing owner, not the spelling of the
	// key, controls delivery; a consumer must claim the Secret explicitly.
	alpha.Exports = []string{"BETA_TOKEN"}
	a.reg["alpha"] = alpha
	if err := a.validateCalculateSecretPatch(alpha, patch); err != nil {
		t.Fatalf("exported cross-prefix Secret write rejected: %v", err)
	}
	store.mergeCanonicalHookSecrets("alpha", patch)
	if got := store.metadata["BETA_TOKEN"]; got != (secretMetadata{Owner: "alpha", Kind: "generated", Provenance: "module-hook"}) {
		t.Fatalf("cross-prefix Secret owner = %#v", got)
	}
	if got := a.scopedSecrets("alpha")["BETA_TOKEN"]; got != "generated-by-alpha" {
		t.Fatalf("producer did not receive its owned Secret: %q", got)
	}
	if _, leaked := a.scopedSecrets("beta")["BETA_TOKEN"]; leaked {
		t.Fatal("consumer received another owner's Secret from its matching prefix")
	}
	beta.Consumes = []string{"BETA_TOKEN"}
	a.reg["beta"] = beta
	if got := a.scopedSecrets("beta")["BETA_TOKEN"]; got != "generated-by-alpha" {
		t.Fatalf("declared consumer did not receive exported Secret: %q", got)
	}
}

func TestApplyCalculatePatchRejectsInvalidEnvKeysAtomically(t *testing.T) {
	mod := Module{Name: "demo", EnvPrefix: "DEMO"}
	for _, invalid := range []string{
		"demo_lower",
		"DEMO-HYPHEN",
		"DEMO SPACE",
		"DEMO_EQUALS=value",
		"DEMO_NEWLINE\nINJECTED",
	} {
		t.Run(strings.ReplaceAll(invalid, "\n", `\n`), func(t *testing.T) {
			a := &app{env: map[string]string{}, envOwner: map[string]string{}}
			err := a.applyCalculatePatch(mod, map[string]string{
				invalid:     "malicious",
				"DEMO_SAFE": "must-not-land",
			})
			if err == nil || !strings.Contains(err.Error(), "invalid env keys") {
				t.Fatalf("invalid Hook Env key error = %v", err)
			}
			if len(a.env) != 0 || len(a.envOwner) != 0 {
				t.Fatalf("invalid Hook Env patch partially landed: env=%#v owners=%#v", a.env, a.envOwner)
			}

			path := filepath.Join(t.TempDir(), "module.env")
			if err := writeEnv(path, a.env); err != nil {
				t.Fatal(err)
			}
			contents, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(contents), "INJECTED") || strings.Contains(string(contents), "must-not-land") {
				t.Fatalf("invalid Hook key polluted rendered .env: %q", contents)
			}
		})
	}
}
