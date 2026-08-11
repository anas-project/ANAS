package runner

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/anas-project/ANAS/internal/config"
)

func TestPlanDoesNotCreateRuntimeState(t *testing.T) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	root := filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
	workspace := t.TempDir()
	if err := Main([]string{"plan", "-c", filepath.Join(root, "config.example.yml"), "--root", root, "-w", workspace}); err != nil {
		t.Fatal(err)
	}
	// plan accepts -w only for symmetry: it must neither require a workspace
	// nor leave any state behind in one.
	if _, err := os.Stat(stateDir(workspace)); !os.IsNotExist(err) {
		t.Fatalf("plan created runtime state at %s", stateDir(workspace))
	}
}

func TestDisabledModuleIsExcluded(t *testing.T) {
	disabled := false
	a := &app{
		cfg: &config.File{Services: map[string]config.Service{"app": {Enabled: &disabled}}},
		reg: map[string]Module{"core": {Name: "core"}, "app": {Name: "app"}},
	}
	order, err := a.resolveOrder([]string{"app"})
	if err != nil {
		t.Fatal(err)
	}
	if len(order) != 0 {
		t.Fatalf("order = %v, want no disabled modules", order)
	}
}

func TestDisabledRequiredModuleIsRejected(t *testing.T) {
	disabled := false
	a := &app{
		cfg: &config.File{Services: map[string]config.Service{"database": {Enabled: &disabled}}},
		reg: map[string]Module{
			"core": {Name: "core"}, "app": {Name: "app", Requires: []Dependency{{Name: "database"}}}, "database": {Name: "database"},
		},
	}
	if _, err := a.resolveOrder([]string{"app"}); err == nil {
		t.Fatal("expected disabled dependency error")
	}
}

func TestAlternativeDependencyDefaultsToPostgresAndUsesLockedBinding(t *testing.T) {
	dep := AlternativeDependency{
		Capability: "relational_database", SelectedBy: "db_type",
		Providers: []string{"postgres", "mariadb"}, Default: "postgres",
	}
	mod := Module{Name: "app", EnvPrefix: "APP"}
	a := &app{
		env:  map[string]string{"APP_DB_TYPE": "auto"},
		reg:  map[string]Module{"postgres": {Name: "postgres"}, "mariadb": {Name: "mariadb"}},
		lock: &caskLock{Bindings: map[string]map[string]string{}},
	}
	provider, err := a.resolveAlternativeDependency("app", mod, dep)
	if err != nil {
		t.Fatal(err)
	}
	if provider != "postgres" || a.env["APP_DB_TYPE"] != "postgres" {
		t.Fatalf("provider = %q, env = %q; want postgres", provider, a.env["APP_DB_TYPE"])
	}

	a.env["APP_DB_TYPE"] = "auto"
	a.lock.Bindings["app"] = map[string]string{"relational_database": "mariadb"}
	provider, err = a.resolveAlternativeDependency("app", mod, dep)
	if err != nil {
		t.Fatal(err)
	}
	if provider != "mariadb" || a.env["APP_DB_TYPE"] != "mariadb" {
		t.Fatalf("provider = %q, env = %q; want locked mariadb", provider, a.env["APP_DB_TYPE"])
	}
}

func TestAlternativeDependencyRejectsUnknownSelection(t *testing.T) {
	a := &app{
		env: map[string]string{"APP_DB_TYPE": "sqlite"},
		reg: map[string]Module{"postgres": {Name: "postgres"}, "mariadb": {Name: "mariadb"}},
	}
	_, err := a.resolveAlternativeDependency("app", Module{Name: "app", EnvPrefix: "APP"}, AlternativeDependency{
		Capability: "relational_database", SelectedBy: "db_type",
		Providers: []string{"postgres", "mariadb"}, Default: "postgres",
	})
	if err == nil {
		t.Fatal("expected unsupported provider error")
	}
}

func TestStableModuleOrderPreservesHardDependencyWhenProviderRunsAfterProxy(t *testing.T) {
	initial := []string{"core", "postgres", "app", "traefik"}
	deps := map[string][]string{
		"postgres": {"core"},
		"app":      {"core", "postgres"},
		"traefik":  {"core"},
	}
	reg := map[string]Module{
		"core":     {Name: "core"},
		"postgres": {Name: "postgres", RunAfter: []string{"traefik"}},
		"app":      {Name: "app", RunAfter: []string{"traefik"}},
		"traefik":  {Name: "traefik"},
	}
	order, err := stableModuleOrder(initial, deps, reg)
	if err != nil {
		t.Fatal(err)
	}
	if index(order, "traefik") > index(order, "postgres") || index(order, "postgres") > index(order, "app") {
		t.Fatalf("order = %v, want traefik before postgres before app", order)
	}
}

func TestNextcloudAutoAddsAndLocksOneDatabaseProvider(t *testing.T) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	root := filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
	reg, err := loadRegistry(root)
	if err != nil {
		t.Fatal(err)
	}
	newApp := func(binding string) *app {
		lock := &caskLock{Bindings: map[string]map[string]string{}}
		if binding != "" {
			lock.Bindings["nextcloud"] = map[string]string{"relational_database": binding}
		}
		return &app{
			cfg: &config.File{Modules: []string{"nextcloud"}, IAM: config.IAM{Provider: "llng"}, Services: map[string]config.Service{}}, reg: reg,
			env: map[string]string{"NEXTCLOUD_DB_TYPE": "auto"}, lock: lock,
		}
	}

	a := newApp("")
	order, err := a.resolveOrder([]string{"nextcloud"})
	if err != nil {
		t.Fatal(err)
	}
	if !contains(order, "postgres") || contains(order, "mariadb") || index(order, "postgres") > index(order, "nextcloud") {
		t.Fatalf("default order = %v, want only postgres before nextcloud", order)
	}

	a = newApp("mariadb")
	order, err = a.resolveOrder([]string{"nextcloud"})
	if err != nil {
		t.Fatal(err)
	}
	// nextcloud honours its locked mariadb binding. postgres may still appear:
	// the IAM this deployment binds to resolves its own database separately.
	if got := a.resolvedBindings["nextcloud"]["relational_database"]; got != "mariadb" {
		t.Fatalf("nextcloud database binding = %q, want the locked mariadb", got)
	}
	if !contains(order, "mariadb") || index(order, "mariadb") > index(order, "nextcloud") {
		t.Fatalf("locked order = %v, want mariadb before nextcloud", order)
	}

	a = newApp("")
	a.cfg.Modules = []string{"mariadb", "nextcloud"}
	order, err = a.resolveOrder(a.cfg.Modules)
	if err != nil {
		t.Fatal(err)
	}
	if !contains(order, "mariadb") || contains(order, "postgres") {
		t.Fatalf("configured order = %v, want the only configured provider mariadb", order)
	}
}

func TestNetbirdBindsToTheSelectedIAM(t *testing.T) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	root := filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
	reg, err := loadRegistry(root)
	if err != nil {
		t.Fatal(err)
	}
	a := &app{
		cfg:  &config.File{Modules: []string{"netbird"}, IAM: config.IAM{Provider: "llng"}, Services: map[string]config.Service{}},
		reg:  reg,
		env:  map[string]string{},
		lock: &caskLock{Bindings: map[string]map[string]string{}},
	}
	order, err := a.resolveOrder(a.cfg.Modules)
	if err != nil {
		t.Fatal(err)
	}
	if !contains(order, "llng") || index(order, "llng") > index(order, "netbird") {
		t.Fatalf("order = %v, want llng before netbird", order)
	}
	for _, required := range []string{"traefik", "samba_dc", "postgres"} {
		if !contains(order, required) {
			t.Fatalf("order = %v, missing transitive dependency %s", order, required)
		}
	}
}

func TestEveryCaskResolvesAsAStandaloneSelection(t *testing.T) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	root := filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
	reg, err := loadRegistry(root)
	if err != nil {
		t.Fatal(err)
	}
	for name := range reg {
		t.Run(name, func(t *testing.T) {
			// An IAM cask selected on its own is its own provider; selecting
			// any other one alongside it would be the two-IAM error.
			provider := "llng"
			if _, ok := reg[name].providedCapability(capabilityIAM); ok {
				provider = name
			}
			a := &app{
				cfg:  &config.File{Modules: []string{name}, IAM: config.IAM{Provider: provider}, Services: map[string]config.Service{}},
				reg:  reg,
				env:  map[string]string{},
				lock: &caskLock{Bindings: map[string]map[string]string{}},
			}
			order, err := a.resolveOrder(a.cfg.Modules)
			if err != nil {
				t.Fatal(err)
			}
			if len(order) == 0 || order[len(order)-1] != name {
				t.Fatalf("order = %v, want selected cask %s last", order, name)
			}
		})
	}
}

func TestBuiltInHardDependencyClosure(t *testing.T) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	root := filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
	reg, err := loadRegistry(root)
	if err != nil {
		t.Fatal(err)
	}
	expected := map[string][]string{
		"traefik":      {"lego"},
		"samba_dc":     {"lego"},
		"samba_fs":     {"samba_dc"},
		"postgres":     {"traefik"},
		"mariadb":      {"traefik"},
		"eturnal":      {"traefik"},
		"ddns_updater": {"traefik"},
		"llng":         {"traefik", "samba_dc", "postgres"},
		"nextcloud":    {"traefik", "eturnal", "samba_dc", "postgres"},
		"collabora":    {"nextcloud"},
		"meshcentral":  {"traefik", "mariadb", "samba_dc"},
		"lam":          {"traefik", "samba_dc"},
		"netbird":      {"traefik", "llng"},
		"freeradius":   {"lego"},
	}
	for name, dependencies := range expected {
		t.Run(name, func(t *testing.T) {
			a := &app{
				cfg:  &config.File{Modules: []string{name}, IAM: config.IAM{Provider: "llng"}, Services: map[string]config.Service{}},
				reg:  reg,
				env:  map[string]string{},
				lock: &caskLock{Bindings: map[string]map[string]string{}},
			}
			order, err := a.resolveOrder(a.cfg.Modules)
			if err != nil {
				t.Fatal(err)
			}
			for _, dependency := range dependencies {
				if !contains(order, dependency) || index(order, dependency) > index(order, name) {
					t.Fatalf("order = %v, want %s before %s", order, dependency, name)
				}
			}
		})
	}
}

func TestWriteEnvIsPrivate(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".env")
	if err := os.WriteFile(path, []byte("old"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := writeEnv(path, map[string]string{"PASSWORD": "secret"}); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0600 {
		t.Fatalf(".env mode = %o, want 600", got)
	}
}

func TestPromoteReleaseReplacesOldTree(t *testing.T) {
	base := t.TempDir()
	release := filepath.Join(base, "release")
	staging := filepath.Join(base, "tmp")
	if err := os.MkdirAll(release, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(release, "stale"), []byte("old"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(staging, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(staging, "current"), []byte("new"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := promoteRelease(staging, release); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(release, "current")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(release, "stale")); !os.IsNotExist(err) {
		t.Fatal("stale release file survived promotion")
	}
}
