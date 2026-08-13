package runner

import (
	"path/filepath"
	"strings"
	"testing"
)

// The inventory has to reach parameters that no `defaults` block mentions.
// default_service_root_password is declared only under the global schema's
// `changes`, and it is the parameter an operator is most likely to go looking
// for, so building the list from defaults alone would miss exactly the case
// the command exists for.
func TestConfigListIncludesParametersDeclaredOnlyByPolicy(t *testing.T) {
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

	password, ok := byPath["global.default_service_root_password"]
	if !ok {
		t.Fatal("global.default_service_root_password is missing from the inventory")
	}
	if password.EnvKey != "DEFAULT_SERVICE_ROOT_PASSWORD" {
		t.Fatalf("password env key = %q", password.EnvKey)
	}
	if password.Policy.Effect != "credential_rotate" || !password.Policy.Sensitive {
		t.Fatalf("password policy = %+v", password.Policy)
	}

	// virtual_domain has no default and no change policy: it exists only as a
	// field of config.Global, which is the other declaration site.
	if _, ok := byPath["global.virtual_domain"]; !ok {
		t.Fatal("global.virtual_domain is missing from the inventory")
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
	settings := map[string]string{"global.default_service_root_password": "ChangeMe1!"}
	entries, err := collectConfigParameters(reg, settings)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.Path != "global.default_service_root_password" {
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
	t.Fatal("global.default_service_root_password is missing from the inventory")
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
		// A parameter with no declaration accepts anything, as everything did
		// before types existed: requiring a declaration would make every module
		// that has not been annotated unconfigurable.
		{"traefik", "domain_prefix", "anything at all"},
	} {
		if err := validateParameterValue(good.module, good.parameter, good.value, reg); err != nil {
			t.Errorf("%s.%s rejected %q: %v", good.module, good.parameter, good.value, err)
		}
	}
}
