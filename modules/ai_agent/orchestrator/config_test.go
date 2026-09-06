package main

import (
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"net/url"
	"strings"
	"testing"
)

func envFor(overrides map[string]string) func(string) string {
	base := map[string]string{
		"AI_AGENT_ENABLED":                "true",
		"AI_AGENT_FORGEJO_URL":            "https://git.example:8443",
		"AI_AGENT_FORGEJO_ADMIN_USERNAME": "anas_ai_agent",
		"AI_AGENT_FORGEJO_ADMIN_PASSWORD": "an-administrator-password",
		"AI_AGENT_WEBHOOK_SECRET":         testSecret,
		"AI_AGENT_WEBHOOK_URL":            "https://agent.example:8443" + WebhookPath,
		"AI_AGENT_REPOSITORY_ALLOWLIST":   "anas-project/ANAS",
		"AI_AGENT_RUNTIMES":               "codex",
		"AI_AGENT_RUNTIME_IMAGES":         "codex=" + strings.Repeat("a", 64),
		"AI_AGENT_DB_HOST":                "postgres",
		"AI_AGENT_DB_NAME":                "ai_agent",
		"AI_AGENT_DB_USERNAME":            "ai_agent",
		"AI_AGENT_DB_PASSWORD":            "a-database-password",
		"ANAS_COMPUTE_RESOURCE__AI_AGENT__WORK_INSTANCES__INTERFACE":               "incus_container",
		"ANAS_COMPUTE_RESOURCE__AI_AGENT__WORK_INSTANCES__ENDPOINT":                "https://incus.example:8443",
		"ANAS_COMPUTE_RESOURCE__AI_AGENT__WORK_INSTANCES__SANDBOX":                 "anas-ai-agent",
		"ANAS_COMPUTE_RESOURCE__AI_AGENT__WORK_INSTANCES__INSTANCE_PREFIX":         "anas-agent-",
		"ANAS_COMPUTE_RESOURCE__AI_AGENT__WORK_INSTANCES__SERVER_CERT":             "c2VydmVy",
		"ANAS_COMPUTE_RESOURCE__AI_AGENT__WORK_INSTANCES__PROFILE":                 "anas-lease",
		"ANAS_COMPUTE_RESOURCE__AI_AGENT__WORK_INSTANCES__SERVER_CERT_FINGERPRINT": strings.Repeat("b", 64),
		"ANAS_COMPUTE_RESOURCE__AI_AGENT__WORK_INSTANCES__CLIENT_CERT":             "Y2xpZW50",
		"ANAS_COMPUTE_RESOURCE__AI_AGENT__WORK_INSTANCES__CLIENT_KEY":              "a2V5",
		"ANAS_COMPUTE_RESOURCE__AI_AGENT__WORK_INSTANCES__IMAGE_ALLOWLIST":         strings.Repeat("a", 64),
		"ANAS_COMPUTE_RESOURCE__AI_AGENT__WORK_INSTANCES__MAX_INSTANCES":           "8",
		"ANAS_COMPUTE_RESOURCE__AI_AGENT__WORK_INSTANCES__CPU":                     "4",
		"ANAS_COMPUTE_RESOURCE__AI_AGENT__WORK_INSTANCES__MEMORY_MIB":              "8192",
		"ANAS_COMPUTE_RESOURCE__AI_AGENT__WORK_INSTANCES__DISK_GIB":                "40",
	}
	for key, value := range overrides {
		if value == "" {
			delete(base, key)
			base[key] = ""
			continue
		}
		base[key] = value
	}
	return func(key string) string { return base[key] }
}

func TestLoadConfigAcceptsACompleteDeployment(t *testing.T) {
	cfg, err := loadConfig(envFor(nil))
	if err != nil {
		t.Fatalf("loadConfig: %v", err)
	}
	if !cfg.Enabled || len(cfg.RepositoryAllow) != 1 || cfg.Runtimes.Len() != 1 {
		t.Fatalf("config = %+v, want one repository and one runtime enabled", cfg)
	}
	if cfg.LeaseError != nil {
		t.Fatalf("lease error = %v, want a usable compute lease", cfg.LeaseError)
	}
	// The password must not survive into the value that gets logged.
	if strings.Contains(cfg.DatabaseRedacted, "a-database-password") {
		t.Fatalf("redacted DSN still holds the password: %q", cfg.DatabaseRedacted)
	}
	if !strings.Contains(cfg.DatabaseURL, "a-database-password") {
		t.Fatal("the real DSN lost the password")
	}
}

// AGENT-R-004: `enabled` is the only switch, and it may not be on without every
// precondition. Each one is named so an operator is not left guessing.
func TestEnabledRequiresEveryPrecondition(t *testing.T) {
	for key, want := range map[string]string{
		"AI_AGENT_FORGEJO_ADMIN_PASSWORD":                           "administrator credential",
		"AI_AGENT_WEBHOOK_SECRET":                                   "webhook secret",
		"AI_AGENT_WEBHOOK_URL":                                      "webhook URL",
		"AI_AGENT_REPOSITORY_ALLOWLIST":                             "repository allowlist",
		"AI_AGENT_RUNTIMES":                                         "agent runtime",
		"AI_AGENT_DB_HOST":                                          "database",
		"ANAS_COMPUTE_RESOURCE__AI_AGENT__WORK_INSTANCES__ENDPOINT": "compute binding",
	} {
		t.Run(key, func(t *testing.T) {
			_, err := loadConfig(envFor(map[string]string{key: ""}))
			if err == nil {
				t.Fatalf("loadConfig succeeded without %s", key)
			}
			if !strings.Contains(err.Error(), want) {
				t.Fatalf("error %q does not name the missing %q", err, want)
			}
		})
	}
}

// A disabled deployment must load even when nothing else is configured: that is
// the state a fresh install sits in.
func TestDisabledDeploymentLoadsWithNothingConfigured(t *testing.T) {
	cfg, err := loadConfig(func(key string) string {
		if key == "AI_AGENT_ENABLED" {
			return "false"
		}
		return ""
	})
	if err != nil {
		t.Fatalf("loadConfig: %v", err)
	}
	if cfg.Enabled {
		t.Fatal("config reports enabled")
	}
	if cfg.Listen == "" || cfg.ReconcileEvery == 0 {
		t.Fatalf("config = %+v, want the documented defaults applied", cfg)
	}
}

// AGENT-R-001: a runtime without a pinned fingerprint cannot be enabled, and a
// tag is not a fingerprint.
func TestRuntimeMustBePinnedToAFingerprint(t *testing.T) {
	for name, images := range map[string]string{
		"absent":     "",
		"a tag":      "codex=v1.2.3",
		"short":      "codex=" + strings.Repeat("a", 40),
		"not hex":    "codex=" + strings.Repeat("z", 64),
		"other name": "claude_code=" + strings.Repeat("a", 64),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := loadConfig(envFor(map[string]string{"AI_AGENT_RUNTIME_IMAGES": images})); err == nil {
				t.Fatalf("loadConfig accepted image pins %q", images)
			}
		})
	}
}

func TestUnknownRuntimeIsRefusedWithTheKnownList(t *testing.T) {
	_, err := loadConfig(envFor(map[string]string{
		"AI_AGENT_RUNTIMES":       "not_a_runtime",
		"AI_AGENT_RUNTIME_IMAGES": "not_a_runtime=" + strings.Repeat("a", 64),
	}))
	if err == nil {
		t.Fatal("loadConfig accepted an unknown runtime")
	}
	if !strings.Contains(err.Error(), "codex") {
		t.Fatalf("error %q does not list the known runtimes", err)
	}
}

func TestRepositoryAllowlistIsParsedAndDeduplicated(t *testing.T) {
	if _, err := loadConfig(envFor(map[string]string{
		"AI_AGENT_REPOSITORY_ALLOWLIST": "anas-project/ANAS,anas-project/ANAS",
	})); err == nil {
		t.Fatal("loadConfig accepted a duplicated repository")
	}
	if _, err := loadConfig(envFor(map[string]string{
		"AI_AGENT_REPOSITORY_ALLOWLIST": "not-a-repo",
	})); err == nil {
		t.Fatal("loadConfig accepted a malformed repository")
	}
}

func TestForgejoURLMustBeHTTPS(t *testing.T) {
	if _, err := loadConfig(envFor(map[string]string{
		"AI_AGENT_FORGEJO_URL": "http://git.example",
	})); err == nil {
		t.Fatal("loadConfig accepted a plaintext Forgejo URL")
	}
}

func TestAllowsRepoOnlyMatchesListedRepositories(t *testing.T) {
	cfg, err := loadConfig(envFor(nil))
	if err != nil {
		t.Fatalf("loadConfig: %v", err)
	}
	if !cfg.AllowsRepo(testRepo(t)) {
		t.Fatal("the enabled repository is not allowed")
	}
	other, _ := ParseRepo("anas-project/other")
	if cfg.AllowsRepo(other) {
		t.Fatal("an unlisted repository is allowed")
	}
}

// AGENT-R-060: the control plane must not branch on a runtime's name. The
// registry is the only place a runtime id may appear, so this walks the
// package's own syntax trees looking for the ids anywhere else.
func TestControlPlaneDoesNotBranchOnRuntimeName(t *testing.T) {
	ids := make([]string, 0, len(builtinRuntimes))
	for _, runtime := range builtinRuntimes {
		ids = append(ids, runtime.ID)
	}
	fset := token.NewFileSet()
	packages, err := parser.ParseDir(fset, ".", func(info fs.FileInfo) bool {
		name := info.Name()
		// The registry declares the ids; the tests have to name them to assert
		// on behaviour. Everything else is control plane code.
		return name != "registry.go" && !strings.HasSuffix(name, "_test.go")
	}, 0)
	if err != nil {
		t.Fatalf("parse package: %v", err)
	}
	for _, pkg := range packages {
		for path, file := range pkg.Files {
			ast.Inspect(file, func(node ast.Node) bool {
				literal, ok := node.(*ast.BasicLit)
				if !ok || literal.Kind != token.STRING {
					return true
				}
				value := strings.Trim(literal.Value, "`\"")
				for _, id := range ids {
					if value == id || strings.Contains(value, "agent-"+id) {
						t.Errorf("%s:%d names the runtime %q; runtime differences belong in the registry and the adapter",
							path, fset.Position(literal.Pos()).Line, id)
					}
				}
				return true
			})
		}
	}
}

// AGENT-R-061: effort levels use each runtime's own names, and a rejection
// says what the alternatives are.
func TestEffortUsesNativeValuesAndExplainsRejections(t *testing.T) {
	registry, err := NewRegistry([]string{"codex", "claude_code", "pi"}, map[string]string{
		"codex": strings.Repeat("a", 64), "claude_code": strings.Repeat("b", 64), "pi": strings.Repeat("c", 64),
	})
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	codex, _ := registry.Lookup("codex")
	claude, _ := registry.Lookup("claude_code")
	pi, _ := registry.Lookup("pi")

	if err := codex.ValidateEffort(codex.EffortLevels[0]); err != nil {
		t.Fatalf("codex rejected its own effort level: %v", err)
	}
	// A level that exists on one runtime must not be accepted on another just
	// because it is spelled the same way somewhere.
	err = claude.ValidateEffort("xhigh")
	if err == nil {
		t.Fatal("claude_code accepted an effort level it does not have")
	}
	for _, level := range claude.EffortLevels {
		if !strings.Contains(err.Error(), level) {
			t.Fatalf("rejection %q does not list the allowed level %q", err, level)
		}
	}
	// A runtime with no effort dimension accepts only the empty value.
	if err := pi.ValidateEffort(""); err != nil {
		t.Fatalf("a runtime without effort levels rejected the empty value: %v", err)
	}
	if err := pi.ValidateEffort("high"); err == nil {
		t.Fatal("a runtime without effort levels accepted a level")
	}
}

func TestRegistryDerivesCapabilityGroupsAndAccounts(t *testing.T) {
	registry, err := NewRegistry([]string{"codex"}, map[string]string{"codex": strings.Repeat("a", 64)})
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	groups := registry.CapabilityGroups()
	if len(groups) != 1 || !strings.HasPrefix(groups[0], "CAP_ai_agent_") {
		t.Fatalf("capability groups = %v, want one generated CAP_ai_agent_* group", groups)
	}
	if _, ok := registry.ByAccount(registry.Accounts()[0]); !ok {
		t.Fatal("the registry cannot find a runtime by its own account name")
	}
	if _, ok := registry.ByAccount("someone-else"); ok {
		t.Fatal("an unrelated account resolved to a runtime")
	}
}

func TestRegistryRefusesDuplicateRuntimes(t *testing.T) {
	if _, err := NewRegistry([]string{"codex", "codex"}, map[string]string{
		"codex": strings.Repeat("a", 64),
	}); err == nil {
		t.Fatal("NewRegistry accepted the same runtime twice")
	}
}

func TestModelValidationListsAlternatives(t *testing.T) {
	registry, _ := NewRegistry([]string{"codex"}, map[string]string{"codex": strings.Repeat("a", 64)})
	codex, _ := registry.Lookup("codex")
	if err := codex.ValidateModel(codex.DefaultModel); err != nil {
		t.Fatalf("the default model was rejected: %v", err)
	}
	err := codex.ValidateModel("some-other-model")
	if err == nil || !strings.Contains(err.Error(), codex.DefaultModel) {
		t.Fatalf("error = %v, want the allowed models listed", err)
	}
}

// The memory store is a test double, but the semantics it implements are the
// ones the requirement is about, so they are asserted directly too.
func TestMemoryStoreDeduplicatesByDeliveryID(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()
	event := InboxEvent{DeliveryID: "d1", Event: "issues", Repo: testRepo(t)}
	fresh, err := store.RecordDelivery(ctx, event)
	if err != nil || !fresh {
		t.Fatalf("first RecordDelivery = %v, %v", fresh, err)
	}
	fresh, err = store.RecordDelivery(ctx, event)
	if err != nil || fresh {
		t.Fatalf("second RecordDelivery = %v, %v; want it reported as a duplicate", fresh, err)
	}
}

// A generated database password may legally begin or end with whitespace.
// Trimming it produces an authentication failure whose error says nothing about
// the cause, so the password is read raw while the rest of the binding is not.
func TestDatabasePasswordIsNotTrimmed(t *testing.T) {
	cfg, err := loadConfig(envFor(map[string]string{
		"AI_AGENT_DB_PASSWORD": "  spaced-password  ",
		"AI_AGENT_DB_HOST":     "  postgres  ",
	}))
	if err != nil {
		t.Fatalf("loadConfig: %v", err)
	}
	parsed, err := url.Parse(cfg.DatabaseURL)
	if err != nil {
		t.Fatalf("the DSN is not a URL: %v", err)
	}
	password, _ := parsed.User.Password()
	if password != "  spaced-password  " {
		t.Fatalf("password = %q, want it preserved exactly", password)
	}
	if parsed.Host != "postgres:5432" {
		t.Fatalf("host = %q, want the surrounding whitespace trimmed", parsed.Host)
	}
}

func TestIncompleteDatabaseBindingIsRefused(t *testing.T) {
	if _, err := loadConfig(envFor(map[string]string{"AI_AGENT_DB_USERNAME": ""})); err == nil {
		t.Fatal("loadConfig accepted a half-configured database binding")
	}
}
