package runner

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/anas-project/ANAS/internal/config"
)

// The inventory has to reach parameters that no `defaults` block mentions,
// whether they are declared by lifecycle policy or only by accepted type.
func TestConfigListIncludesParametersDeclaredWithoutDefaults(t *testing.T) {
	t.Setenv("TZ", "Asia/Tokyo")
	t.Setenv("LC_ALL", "")
	t.Setenv("LC_MESSAGES", "")
	t.Setenv("LC_TIME", "")
	t.Setenv("LANG", "pt_BR.UTF-8")
	reg, err := loadRegistry(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	entries, err := collectConfigParameters(reg, map[string]string{})
	if err != nil {
		t.Fatal(err)
	}
	byPath := map[string]configListEntry{}
	for _, entry := range entries {
		byPath[entry.Path] = entry
	}

	password, ok := byPath["lam.admin_password"]
	if !ok {
		t.Fatal("LAM admin_password is missing from the inventory")
	}
	if password.EnvKey != "LAM_ADMIN_PASSWORD" {
		t.Fatalf("password env key = %q", password.EnvKey)
	}
	if password.Policy.Effect != "container_recreate" || !password.Policy.Sensitive {
		t.Fatalf("password policy = %+v", password.Policy)
	}
	zone, ok := byPath["ddns_updater.zone_identifier"]
	if !ok {
		t.Fatal("typed-only ddns_updater.zone_identifier is missing from the inventory")
	}
	if zone.EnvKey != "DDNS_UPDATER_ZONE_IDENTIFIER" {
		t.Fatalf("zone identifier env key = %q", zone.EnvKey)
	}

	// virtual_domain has no default and no change policy: it exists only as a
	// field of config.Global, which is the other declaration site.
	if _, ok := byPath["global.virtual_domain"]; !ok {
		t.Fatal("global.virtual_domain is missing from the inventory")
	}
	for path, want := range map[string]string{
		"global.timezone":         "Asia/Tokyo",
		"global.default_language": "pt-BR",
		"global.default_locale":   "pt-BR",
	} {
		if got := byPath[path].Default; got != want {
			t.Errorf("%s inherited default = %q, want %q", path, got, want)
		}
	}

	// A module parameter published under a bare env name is addressed through
	// the top level env: block, and keeps its own module's policy rather than
	// the default one.
	share, ok := byPath["env.SHARE_DIR_NAME"]
	if !ok {
		t.Fatal("env.SHARE_DIR_NAME is missing from the inventory")
	}
	if share.Module != "samba_fs" || share.Policy.Effect != "data_migrate" {
		t.Fatalf("share module = %q effect = %q", share.Module, share.Policy.Effect)
	}
}

// Every path the inventory prints is a path `config set` accepts. A listing
// that advertises an unusable path is worse than no listing.
func TestConfigListPathsAreSettable(t *testing.T) {
	reg, err := loadRegistry(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	entries, err := collectConfigParameters(reg, map[string]string{})
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) == 0 {
		t.Fatal("the inventory is empty")
	}
	for _, entry := range entries {
		target, err := resolveConfigTarget(entry.Path, reg)
		if err != nil {
			t.Errorf("listed path %q does not resolve: %v", entry.Path, err)
			continue
		}
		if target.Display != entry.Path {
			t.Errorf("listed path %q resolves to a different path %q", entry.Path, target.Display)
		}
		// A global path must name a field config.Global actually has: the
		// decoder uses KnownFields, so writing any other name into the global
		// block makes every later command fail to load the config.
		if head, rest, _ := strings.Cut(target.Display, "."); head == "global" && !isGlobalParameter(rest) {
			t.Errorf("listed path %q writes into global: but config.Global has no such field", entry.Path)
		}
	}
}

// Values of sensitive parameters are reported as present or absent, never
// printed. `config secret get` is the command that hands over a credential.
func TestConfigListWithholdsSensitiveValues(t *testing.T) {
	reg, err := loadRegistry(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	settings := map[string]string{"modules.lam.config.admin_password": "ChangeMe1!"}
	entries, err := collectConfigParameters(reg, settings)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.Path != "lam.admin_password" {
			continue
		}
		if !entry.Set {
			t.Fatal("a configured password should be reported as set")
		}
		if got := configListValue(entry); got != "<set>" {
			t.Fatalf("sensitive value rendered as %q", got)
		}
		return
	}
	t.Fatal("LAM admin_password is missing from the inventory")
}

// A misspelled parameter used to be written into the config and rendered into
// an environment variable nothing reads: the command succeeded, the value was
// visible in the file, and the setting simply had no effect.
func TestConfigTargetRejectsUndeclaredParameters(t *testing.T) {
	reg, err := loadRegistry(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{
		"traefik.totally_made_up",
		"global.made_up_thing",
		"services.samba_fs.no_such_parameter",
	} {
		if _, err := resolveConfigTarget(path, reg); err == nil {
			t.Errorf("%s was accepted", path)
		}
	}

	// A near miss names the parameter that was probably meant. A wrong guess
	// would be worse than none, so only close names are offered.
	_, err = resolveConfigTarget("traefik.domain_prefx", reg)
	if err == nil {
		t.Fatal("a misspelled parameter was accepted")
	}
	if !strings.Contains(err.Error(), "traefik.domain_prefix") {
		t.Errorf("the error does not suggest the intended parameter: %v", err)
	}

	// Declared parameters still resolve, including one a module publishes under a
	// bare env name.
	for _, path := range []string{"traefik.domain_prefix", "global.timezone", "samba_fs.share_dir_name"} {
		if _, err := resolveConfigTarget(path, reg); err != nil {
			t.Errorf("%s was rejected: %v", path, err)
		}
	}
}

func TestWholeConfigRejectsUndeclaredAndInvalidModuleParameters(t *testing.T) {
	reg, err := loadRegistry(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	cfg := &config.File{Modules: config.NewModuleSelection("traefik")}
	selected := cfg.Modules.Values["traefik"]
	selected.Config = map[string]any{"subnet": "172.23.0.0/16"}
	cfg.Modules.Values["traefik"] = selected
	if err := validateConfiguredParameters(cfg, reg); err == nil || !strings.Contains(err.Error(), "no parameter") {
		t.Fatalf("undeclared YAML parameter error = %v", err)
	}

	cfg = &config.File{Modules: config.NewModuleSelection("ddns_updater")}
	selected = cfg.Modules.Values["ddns_updater"]
	selected.Config = map[string]any{"zone_identifier": "test-zone"}
	cfg.Modules.Values["ddns_updater"] = selected
	if err := validateConfiguredParameters(cfg, reg); err != nil {
		t.Fatalf("typed-only parameter was rejected: %v", err)
	}
}

// env.<KEY> is the escape hatch and stays permissive. Validating it would leave
// an operator who needs an undeclared value with nowhere to put it.
func TestRawEnvPathAcceptsUndeclaredKeys(t *testing.T) {
	reg, err := loadRegistry(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	target, err := resolveConfigTarget("env.SOMETHING_NOTHING_DECLARES", reg)
	if err != nil {
		t.Fatalf("the escape hatch was closed: %v", err)
	}
	if got := strings.Join(target.YAMLPath, "."); got != "env.SOMETHING_NOTHING_DECLARES" {
		t.Errorf("YAML path = %q", got)
	}
	cfg := &config.File{Env: map[string]any{"SOMETHING_NOTHING_DECLARES": "free form"}}
	if err := validateConfiguredParameters(cfg, reg); err != nil {
		t.Fatalf("undeclared raw env value was rejected: %v", err)
	}
}

func TestRawEnvCannotBypassDeclaredModuleTypes(t *testing.T) {
	reg, err := loadRegistry(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		key   string
		value string
		want  string
	}{
		// A bare exported key and an ordinary prefixed key take the same generic
		// owner/type path; neither needs a Module-specific import adapter.
		{key: "SHARE_ACCESS_MODE", value: "not-a-mode", want: "all_read_group_write"},
		{key: "AUTHENTIK_LDAP_ENABLED", value: "maybe", want: "true or false"},
	} {
		cfg := &config.File{Env: map[string]any{test.key: test.value}}
		err := validateConfiguredParameters(cfg, reg)
		if err == nil || !strings.Contains(err.Error(), test.want) {
			t.Errorf("env.%s validation error = %v, want %q", test.key, err, test.want)
		}
	}
}

// The scope argument exists because the rejection message points here.
func TestConfigListScopeSelectsOneModule(t *testing.T) {
	reg, err := loadRegistry(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	entries, err := collectConfigParameters(reg, map[string]string{})
	if err != nil {
		t.Fatal(err)
	}
	traefik, global := 0, 0
	for _, entry := range entries {
		switch entry.Module {
		case "traefik":
			traefik++
		case globalModuleName:
			global++
		}
	}
	if traefik == 0 || global == 0 {
		t.Fatalf("expected both scopes to be populated: traefik=%d global=%d", traefik, global)
	}
	if _, known := declaredParametersFor("nosuchmodule", reg); known {
		t.Error("an unknown module reported known parameters")
	}
	if _, known := declaredParametersFor(globalModuleName, reg); !known {
		t.Error("global is not recognised as a scope")
	}
}

// A value a module cannot use is refused when it is set, not at the next render.
// share_access_mode was already checked by the calculate hook, so a wrong value
// was accepted, written to the config, and rejected much later -- long after
// the person who chose it had moved on.
func TestConfigSetRejectsValuesOutsideTheDeclaredType(t *testing.T) {
	reg, err := loadRegistry(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	for _, bad := range []struct{ module, parameter, value, want string }{
		{"samba_fs", "share_access_mode", "all_rw_typo", "all_read_group_write"},
		{"samba_fs", "share_guest_read_only", "true", "Yes"},
		{"samba_fs", "log_level", "abc", "whole number"},
		{"ddns_go", "web_enabled", "maybe", "true or false"},
		{globalModuleName, "ipv6", "flase", "true or false"},
	} {
		err := validateParameterValue(bad.module, bad.parameter, bad.value, reg)
		if err == nil {
			t.Errorf("%s.%s accepted %q", bad.module, bad.parameter, bad.value)
			continue
		}
		// The rejection lists what is allowed; one that does not sends the
		// reader to the manifest to find out.
		if !strings.Contains(err.Error(), bad.want) {
			t.Errorf("%s.%s error does not say what is accepted: %v", bad.module, bad.parameter, err)
		}
	}

	for _, good := range []struct{ module, parameter, value string }{
		{"samba_fs", "share_access_mode", "all_rw"},
		{"samba_fs", "share_guest_read_only", "No"},
		{"samba_fs", "log_level", "3"},
		{"ddns_go", "web_enabled", "false"},
		{globalModuleName, "base_domain", "nas.example.com"},
		// An explicitly declared string remains unconstrained text.
		{"traefik", "domain_prefix", "anything at all"},
	} {
		if err := validateParameterValue(good.module, good.parameter, good.value, reg); err != nil {
			t.Errorf("%s.%s rejected %q: %v", good.module, good.parameter, good.value, err)
		}
	}

	// A parameter with no declaration accepts anything, as everything did
	// before types existed. Runtime compatibility for legacy and third-party
	// bundles is separate from the repository gate for current Modules.
	legacy := map[string]Module{"legacy": {Name: "legacy"}}
	if err := validateParameterValue("legacy", "free_form", "anything at all", legacy); err != nil {
		t.Fatalf("legacy undeclared parameter was rejected: %v", err)
	}
}

func TestTypedValuesNormalizeToRuntimeSpelling(t *testing.T) {
	reg, err := loadRegistry(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		module, parameter, value, want string
	}{
		{globalModuleName, "ipv6", " TRUE ", "true"},
		{"nextcloud", "db_type", " MARIADB ", "mariadb"},
		{"samba_fs", "share_guest_read_only", "yes", "Yes"},
		{"ddns_go", "ipv4_gettype", "NETINTERFACE", "netInterface"},
		{"samba_dc", "user_min_pass_length", " 08 ", "08"},
		{"traefik", "domain_prefix", " Mixed Case ", " Mixed Case "},
	} {
		got, err := normalizeParameterValue(test.module, test.parameter, test.value, reg)
		if err != nil {
			t.Errorf("%s.%s rejected %q: %v", test.module, test.parameter, test.value, err)
			continue
		}
		if got != test.want {
			t.Errorf("%s.%s normalized %q to %q, want %q", test.module, test.parameter, test.value, got, test.want)
		}
	}
}

func TestTypedEmptyValuesRemainUnsetForDefaults(t *testing.T) {
	for _, test := range []struct {
		name string
		spec ParamType
	}{
		{name: "bool", spec: ParamType{Kind: "bool"}},
		{name: "int", spec: ParamType{Kind: "int"}},
		{name: "enum", spec: ParamType{Kind: "enum", Enum: []string{"safe", "fast"}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := normalizeValueAgainstParamType(" \t ", test.spec)
			if err != nil {
				t.Fatalf("empty value was rejected before defaults: %v", err)
			}
			if got != "" {
				t.Fatalf("empty value normalized to %q, want unset", got)
			}
		})
	}

	const raw = " \t "
	got, err := normalizeValueAgainstParamType(raw, ParamType{Kind: "string"})
	if err != nil || got != raw {
		t.Fatalf("explicit string normalized to %q, %v; want original whitespace", got, err)
	}

	mod := Module{
		Name:       "demo",
		EnvPrefix:  "DEMO",
		Parameters: []string{"enabled", "workers", "mode", "required_mode"},
		Types: map[string]ParamType{
			"enabled":       {Kind: "bool"},
			"workers":       {Kind: "int"},
			"mode":          {Kind: "enum", Enum: []string{"safe", "fast"}},
			"required_mode": {Kind: "enum", Enum: []string{"safe", "fast"}},
		},
		Defaults: map[string]string{
			"DEMO_ENABLED": "true", "DEMO_WORKERS": "2", "DEMO_MODE": "safe",
		},
		Required: []string{"DEMO_REQUIRED_MODE"},
	}
	cfg := &config.File{Modules: config.NewModuleSelection("demo"), Env: map[string]any{"DEMO_MODE": ""}}
	selected := cfg.Modules.Values["demo"]
	selected.Config = map[string]any{
		"enabled": "", "workers": " ", "mode": "", "required_mode": "",
	}
	cfg.Modules.Values["demo"] = selected
	reg := map[string]Module{"demo": mod}
	if err := validateConfiguredParameters(cfg, reg); err != nil {
		t.Fatalf("whole-config validation rejected unset typed values: %v", err)
	}
	env, owners := cfg.BaseEnvWithOwners()
	if err := normalizeConfiguredParameterEnv(env, reg); err != nil {
		t.Fatalf("runtime normalization rejected unset typed values: %v", err)
	}
	a := &app{env: env, envOwner: owners, order: []string{"demo"}, reg: reg}
	a.applyModuleDefaults()
	for key, want := range mod.Defaults {
		if got := a.env[key]; got != want {
			t.Errorf("%s after defaults = %q, want %q", key, got, want)
		}
	}
	if err := requireKeys(a.env, mod.Required); err == nil {
		t.Fatal("an unset required value without a default was not rejected after the default merge")
	}
}

func TestEnumNormalizationPreservesCaseDistinctLegacyValues(t *testing.T) {
	spec := ParamType{Kind: "enum", Enum: []string{"prod", "PROD"}}
	for _, exact := range spec.Enum {
		got, err := normalizeValueAgainstParamType(exact, spec)
		if err != nil || got != exact {
			t.Fatalf("exact value %q normalized to %q, %v", exact, got, err)
		}
	}
	if _, err := normalizeValueAgainstParamType("ProD", spec); err == nil || !strings.Contains(err.Error(), "case-sensitive") {
		t.Fatalf("ambiguous folded value error = %v", err)
	}
}
