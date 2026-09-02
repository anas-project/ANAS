package runner

import (
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/anas-project/ANAS/internal/config"
	"github.com/anas-project/ANAS/internal/configschema"
)

// The CLI is the current public projection of the common schema. Testing the
// structs alone would miss adapter drift such as a lost pointer boundary,
// omitted false value, or has_default being inferred from the default string.
func TestConfigListProjectsCompleteParameterSchema(t *testing.T) {
	t.Setenv("TZ", "Asia/Singapore")
	t.Setenv("LC_ALL", "")
	t.Setenv("LC_MESSAGES", "")
	t.Setenv("LC_TIME", "")
	t.Setenv("LANG", "pt_BR.UTF-8")

	stdout, stderr, exit := capture(t, "config", "list", "--root", repoRoot(t), "--json")
	if exit != 0 || stderr != "" {
		t.Fatalf("config list exit=%d stderr=%q", exit, stderr)
	}
	document := requireSingleDocument(t, "config list schema", stdout)
	raw, ok := document["parameters"].([]any)
	if !ok {
		t.Fatalf("parameters = %T, want array", document["parameters"])
	}
	wantInventory, err := LoadConfigParameterInventory(repoRoot(t))
	if err != nil {
		t.Fatal(err)
	}
	wantPaths := make(map[string]bool, len(wantInventory))
	for _, entry := range wantInventory {
		wantPaths[entry.Path] = true
	}

	byPath := map[string]map[string]any{}
	validSources := map[string]bool{
		"none": true, "static": true, "host": true, "runtime": true,
		"generated": true, "inherited": true,
	}
	validConstraintKeys := map[string]bool{
		"minimum": true, "maximum": true, "min_length": true,
		"max_length": true, "pattern": true, "format": true,
	}
	for _, value := range raw {
		item, ok := value.(map[string]any)
		if !ok {
			t.Fatalf("parameter = %T, want object", value)
		}
		path, _ := item["path"].(string)
		if path == "" {
			t.Fatalf("parameter has no path: %#v", item)
		}
		if _, exists := byPath[path]; exists {
			t.Errorf("parameter path %s appears more than once", path)
		}
		byPath[path] = item
		for _, field := range []string{"required", "input_required", "must_resolve", "has_default"} {
			if _, ok := item[field].(bool); !ok {
				t.Errorf("%s %s = %T, want boolean", path, field, item[field])
			}
		}
		if item["required"] != item["input_required"] {
			t.Errorf("%s required=%v input_required=%v", path, item["required"], item["input_required"])
		}
		source, ok := item["default_source"].(string)
		if !ok || !validSources[source] {
			t.Errorf("%s default_source=%v", path, item["default_source"])
		}
		hasDefault, _ := item["has_default"].(bool)
		if hasDefault != (source == "static") {
			t.Errorf("%s has_default=%t source=%q; literal default and static source must agree", path, hasDefault, source)
		}
		if required, _ := item["input_required"].(bool); required && (hasDefault || source != "none") {
			t.Errorf("%s is input-required but has default metadata: has=%t source=%q", path, hasDefault, source)
		}
		if constraints, exists := item["constraints"]; exists {
			object, ok := constraints.(map[string]any)
			if !ok || len(object) == 0 {
				t.Errorf("%s constraints=%#v, want non-empty object", path, constraints)
				continue
			}
			for key := range object {
				if !validConstraintKeys[key] {
					t.Errorf("%s exposes unsupported constraint %q", path, key)
				}
			}
		}
	}
	if gotPaths := pathSet(byPath); !reflect.DeepEqual(gotPaths, wantPaths) {
		t.Errorf("config list paths = %v, want repository inventory paths %v", sortedBoolKeys(gotPaths), sortedBoolKeys(wantPaths))
	}

	assertParameterJSON := func(path string, want map[string]any) {
		t.Helper()
		got, ok := byPath[path]
		if !ok {
			t.Fatalf("missing %s", path)
		}
		for key, value := range want {
			if !reflect.DeepEqual(got[key], value) {
				t.Errorf("%s %s=%#v, want %#v", path, key, got[key], value)
			}
		}
	}
	assertParameterJSON("global.base_domain", map[string]any{
		"required": true, "input_required": true, "must_resolve": true,
		"has_default": false, "default_source": "none",
		"constraints": map[string]any{"format": "dns_name"},
	})
	assertParameterJSON("global.timezone", map[string]any{
		"required": false, "input_required": false, "must_resolve": true,
		"has_default": false, "default_source": "host",
		"default": "Asia/Singapore", "constraints": map[string]any{"format": "iana_timezone"},
	})
	assertParameterJSON("global.ipv6", map[string]any{
		"required": false, "must_resolve": false, "has_default": true,
		"default_source": "static", "default": "true",
	})
	assertParameterJSON("collabora.admin_password", map[string]any{
		"required": false, "must_resolve": true, "has_default": false,
		"default_source": "generated", "default": "",
	})
	assertParameterJSON("eturnal.port", map[string]any{
		"has_default": true, "default_source": "static",
		"constraints": map[string]any{"minimum": float64(1), "maximum": float64(65535)},
	})
	// TTL is positive only for one provider. A generic single-field minimum
	// would reject configurations in which another provider ignores it.
	if _, exists := byPath["ddns_updater.ttl"]["constraints"]; exists {
		t.Error("conditional ddns_updater.ttl rule was incorrectly projected as an unconditional constraint")
	}
}

func pathSet(entries map[string]map[string]any) map[string]bool {
	paths := make(map[string]bool, len(entries))
	for path := range entries {
		paths[path] = true
	}
	return paths
}

func sortedBoolKeys(entries map[string]bool) []string {
	keys := make([]string, 0, len(entries))
	for key := range entries {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func TestConfigExplainUsesTheSameSchemaProjection(t *testing.T) {
	t.Setenv("TZ", "Asia/Singapore")
	stdout, stderr, exit := capture(t, "config", "explain", "global.timezone", "--root", repoRoot(t), "--json")
	if exit != 0 || stderr != "" {
		t.Fatalf("config explain exit=%d stderr=%q", exit, stderr)
	}
	document := requireSingleDocument(t, "config explain schema", stdout)
	setting, ok := document["setting"].(map[string]any)
	if !ok {
		t.Fatalf("setting = %T", document["setting"])
	}
	want := map[string]any{
		"type": "string", "required": false, "input_required": false,
		"must_resolve": true, "has_default": false, "default_source": "host",
		"default": "Asia/Singapore", "constraints": map[string]any{"format": "iana_timezone"},
	}
	for key, value := range want {
		if !reflect.DeepEqual(setting[key], value) {
			t.Errorf("setting.%s=%#v, want %#v", key, setting[key], value)
		}
	}
}

func TestHumanConfigOutputDistinguishesAnEmptyDefaultFromNoDefault(t *testing.T) {
	stdout, stderr, exit := capture(t, "config", "list", "samba_dc", "--root", repoRoot(t))
	if exit != 0 || stderr != "" {
		t.Fatalf("config list exit=%d stderr=%q stdout=%q", exit, stderr, stdout)
	}
	found := false
	for _, line := range strings.Split(stdout, "\n") {
		if !strings.HasPrefix(line, "samba_dc.dns_forwarders ") {
			continue
		}
		found = true
		if !strings.Contains(line, `  ""  `) {
			t.Errorf("explicit empty default row does not show an empty string: %q", line)
		}
	}
	if !found {
		t.Fatal("human config list omitted samba_dc.dns_forwarders")
	}

	stdout, stderr, exit = capture(t, "config", "explain", "samba_dc.dns_forwarders", "--root", repoRoot(t))
	if exit != 0 || stderr != "" {
		t.Fatalf("config explain exit=%d stderr=%q stdout=%q", exit, stderr, stdout)
	}
	for _, want := range []string{"has default: true\n", "default source: static\n", "default: \"\"\n"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("empty-default explain output is missing %q:\n%s", want, stdout)
		}
	}

	stdout, stderr, exit = capture(t, "config", "explain", "samba_dc.realm", "--root", repoRoot(t))
	if exit != 0 || stderr != "" {
		t.Fatalf("config explain inherited exit=%d stderr=%q stdout=%q", exit, stderr, stdout)
	}
	for _, want := range []string{"has default: false\n", "default source: inherited\n", "default: -\n"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("no-literal-default explain output is missing %q:\n%s", want, stdout)
		}
	}
}

func TestConstraintProjectionCoversEveryPortableField(t *testing.T) {
	minimum, maximum, minLength, maxLength := 1, 30, 2, 12
	integer := paramConstraintsDocument(ParamType{Kind: "int", Constraints: configschema.Constraints{
		Minimum: &minimum, Maximum: &maximum,
	}})
	if want := map[string]any{"minimum": 1, "maximum": 30}; !reflect.DeepEqual(integer, want) {
		t.Fatalf("integer constraints = %#v, want %#v", integer, want)
	}
	text := paramConstraintsDocument(ParamType{Kind: "string", Constraints: configschema.Constraints{
		MinLength: &minLength, MaxLength: &maxLength,
		Pattern: `^[a-z]+$`, Format: "language_tag",
	}})
	wantText := map[string]any{
		"min_length": 2, "max_length": 12,
		"pattern": `^[a-z]+$`, "format": "language_tag",
	}
	if !reflect.DeepEqual(text, wantText) {
		t.Fatalf("string constraints = %#v, want %#v", text, wantText)
	}
	if got := paramConstraintsDocument(ParamType{Kind: "bool"}); len(got) != 0 {
		t.Fatalf("unconstrained parameter projected %#v", got)
	}
}

func TestConfigInputValidationCoversRequiredAndConstraintBoundaries(t *testing.T) {
	reg, err := loadRegistry(repoRoot(t))
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		path  string
		value string
		want  string
	}{
		{path: "global.base_domain", value: " \t ", want: "required input"},
		{path: "global.timezone", value: "UTC+8", want: "IANA timezone"},
		{path: "global.host_ip", value: "2001:db8::1", want: "IPv4"},
		{path: "eturnal.port", value: "0", want: "at least 1"},
		{path: "eturnal.port", value: "65536", want: "at most 65535"},
		{path: "samba_dc.max_log_size", value: "0", want: "at least 1"},
	} {
		target, err := resolveConfigTarget(test.path, reg)
		if err != nil {
			t.Fatal(err)
		}
		_, err = normalizeConfigInputValue(target, test.value, reg)
		if err == nil || !strings.Contains(err.Error(), test.want) {
			t.Errorf("%s accepted %q or returned the wrong error: %v", test.path, test.value, err)
		}
	}
	for _, test := range []struct {
		path, value, want string
	}{
		{path: "global.host_ip", value: " \t ", want: ""},
		{path: "global.timezone", value: ":Asia/Singapore", want: "Asia/Singapore"},
		{path: "global.default_language", value: "pt_BR.UTF-8", want: "pt-BR"},
		{path: "global.host_ip", value: " 192.0.2.10 ", want: "192.0.2.10"},
		{path: "eturnal.port", value: "1", want: "1"},
		{path: "eturnal.port", value: "65535", want: "65535"},
		{path: "samba_dc.max_log_size", value: "1", want: "1"},
	} {
		target, err := resolveConfigTarget(test.path, reg)
		if err != nil {
			t.Fatal(err)
		}
		got, err := normalizeConfigInputValue(target, test.value, reg)
		if err != nil || got != test.want {
			t.Errorf("%s normalize(%q)=%q, %v, want %q", test.path, test.value, got, err, test.want)
		}
	}
}

func TestConfigImportAllowsOptionalFormattedStringToReturnToDefaultSource(t *testing.T) {
	reg, err := loadRegistry(repoRoot(t))
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name, value string
	}{
		{name: "empty string", value: `""`},
		{name: "yaml null", value: `null`},
	} {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.yml")
			body := "modules:\n  traefik: {}\nglobal:\n  base_domain: nas.test\n  email: admin@example.com\n  host_ip: " + test.value + "\n"
			if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
				t.Fatal(err)
			}
			result, err := normalizeImportedConfig(path, reg)
			if err != nil {
				t.Fatalf("optional runtime value was rejected: %v", err)
			}
			if err := validateNormalizedImportedConfig(result.Normalized, reg); err != nil {
				t.Fatalf("normalized optional runtime value was rejected: %v", err)
			}
		})
	}
}

func TestConfigImportRetagsDeclaredStringScalarsWithoutChangingText(t *testing.T) {
	reg, err := loadRegistry(repoRoot(t))
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "config.yml")
	if err := os.WriteFile(path, []byte(`modules:
  traefik:
    config:
      domain_prefix: 001
global:
  base_domain: nas.test
  email: admin@example.com
env:
  SAMBA_DC_REALM: true
`), 0o600); err != nil {
		t.Fatal(err)
	}
	result, err := normalizeImportedConfig(path, reg)
	if err != nil {
		t.Fatal(err)
	}
	body := string(result.Normalized)
	for _, want := range []string{`domain_prefix: "001"`, `SAMBA_DC_REALM: "true"`} {
		if !strings.Contains(body, want) {
			t.Errorf("normalized string import is missing %q:\n%s", want, body)
		}
	}
	normalizedPath := filepath.Join(t.TempDir(), "normalized.yml")
	if err := os.WriteFile(normalizedPath, result.Normalized, 0o600); err != nil {
		t.Fatal(err)
	}
	loaded, err := config.Load(normalizedPath)
	if err != nil {
		t.Fatal(err)
	}
	env := loaded.BaseEnv()
	if env["TRAEFIK_DOMAIN_PREFIX"] != "001" || env["SAMBA_DC_REALM"] != "true" {
		t.Fatalf("runtime strings = domain %q realm %q", env["TRAEFIK_DOMAIN_PREFIX"], env["SAMBA_DC_REALM"])
	}
}

func TestConfigImportValidatesSensitiveParametersBeforeExtractionWithoutEchoing(t *testing.T) {
	minimum := 8
	reg := map[string]Module{
		"demo": {
			Name: "demo", EnvPrefix: "DEMO", Parameters: []string{"token"},
			Types: map[string]ParamType{
				"token": {Kind: "string", Constraints: configschema.Constraints{MinLength: &minimum}},
			},
			Changes: map[string]ChangePolicy{
				"token": {Effect: "credential_rotate", Sensitive: true},
			},
		},
	}
	write := func(body string) string {
		t.Helper()
		path := filepath.Join(t.TempDir(), "config.yml")
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	for _, body := range []string{
		"modules:\n  demo:\n    config:\n      token: hush\nglobal:\n  base_domain: nas.test\n  email: admin@example.com\n",
		"modules:\n  demo: {}\nglobal:\n  base_domain: nas.test\n  email: admin@example.com\nenv:\n  DEMO_TOKEN: hush\n",
	} {
		_, err := normalizeImportedConfig(write(body), reg)
		if err == nil || !strings.Contains(err.Error(), "declared string type or constraints") {
			t.Fatalf("short sensitive value error = %v", err)
		}
		if strings.Contains(err.Error(), "hush") {
			t.Fatalf("sensitive validation error echoed the rejected credential: %v", err)
		}
	}
	result, err := normalizeImportedConfig(write("modules:\n  demo:\n    config:\n      token: long-enough\nglobal:\n  base_domain: nas.test\n  email: admin@example.com\n"), reg)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Secrets) != 1 || result.Secrets[0].Value != "long-enough" {
		t.Fatalf("validated sensitive import = %+v", result.Secrets)
	}
}

func TestSensitiveValidationErrorsNeverEchoTheRejectedValue(t *testing.T) {
	minimum := 8
	reg := map[string]Module{
		"demo": {
			Name: "demo", EnvPrefix: "DEMO", Parameters: []string{"token"},
			Types: map[string]ParamType{
				"token": {Kind: "string", Constraints: configschema.Constraints{MinLength: &minimum}},
			},
			Changes: map[string]ChangePolicy{
				"token": {Effect: "container_recreate", Sensitive: true},
			},
		},
	}
	target := configTarget{Display: "demo.token", Module: "demo", Parameter: "token"}
	_, err := normalizeConfigInputForPolicy(target, "hush", policyForTarget(target, reg), reg)
	if err == nil || !strings.Contains(err.Error(), "declared type or constraints") {
		t.Fatalf("sensitive set validation error = %v", err)
	}
	if strings.Contains(err.Error(), "hush") {
		t.Fatalf("sensitive set validation echoed the rejected value: %v", err)
	}

	cfg := &config.File{
		Modules: config.ModuleSelection{Order: []string{"demo"}, Values: map[string]config.ModuleConfig{
			"demo": {Config: map[string]any{"token": "hush"}},
		}},
	}
	err = validateConfiguredParameters(cfg, reg)
	if err == nil || !strings.Contains(err.Error(), "declared type or constraints") {
		t.Fatalf("sensitive whole-config validation error = %v", err)
	}
	if strings.Contains(err.Error(), "hush") {
		t.Fatalf("sensitive whole-config validation echoed the rejected value: %v", err)
	}
}

func TestConfigPlanValidatesTheCurrentSchemaBeforeReportingChanges(t *testing.T) {
	minimum := 1
	reg := map[string]Module{
		"demo": {
			Name: "demo", EnvPrefix: "DEMO", Parameters: []string{"port"},
			Types: map[string]ParamType{
				"port": {Kind: "int", Constraints: configschema.Constraints{Minimum: &minimum}},
			},
		},
	}
	workspace := t.TempDir()
	configPath := workspaceConfigPath(workspace)
	if err := os.WriteFile(configPath, []byte(`modules:
  demo:
    config:
      port: 0
global:
  base_domain: nas.test
  email: admin@example.com
`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := writeManagedConfigState(workspace, "schema-test"); err != nil {
		t.Fatal(err)
	}
	err := reportConfigPlan(workspace, configPath, stateDir(workspace), reg, true)
	if err == nil || !strings.Contains(err.Error(), "at least 1") {
		t.Fatalf("config plan schema error = %v", err)
	}
}

func TestInputRequiredIsEnforcedByImportInitAndDeploymentPlan(t *testing.T) {
	source := filepath.Join(t.TempDir(), "missing-input.yml")
	input := []byte(`modules:
  traefik: {}
global:
  email: admin@example.com
`)
	if err := os.WriteFile(source, input, 0o600); err != nil {
		t.Fatal(err)
	}

	// Import validates before its transactional write set is staged. A failed
	// replacement must preserve both the desired config and its managed state.
	workspace := newWorkspace(t)
	configPath := workspaceConfigPath(workspace)
	statePath := managedConfigStatePath(stateDir(workspace))
	beforeConfig, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	beforeState, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatal(err)
	}
	stdout, _, exit := capture(t, "config", "import", source,
		"-w", workspace, "--root", repoRoot(t), "--json")
	if exit != exitPrecondition || !strings.Contains(stdout, "BASE_DOMAIN") {
		t.Fatalf("config import exit=%d stdout=%q, want missing BASE_DOMAIN", exit, stdout)
	}
	afterConfig, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	afterState, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(afterConfig, beforeConfig) || !reflect.DeepEqual(afterState, beforeState) {
		t.Fatal("rejected input-required import changed the managed workspace")
	}

	// Init uses the same import boundary and must not leave a half-created
	// workspace behind.
	initWorkspace := filepath.Join(t.TempDir(), "workspace")
	stdout, _, exit = capture(t, "init", initWorkspace, "--config", source,
		"--module-root", repoRoot(t), "-y", "--json")
	if exit != exitPrecondition || !strings.Contains(stdout, "BASE_DOMAIN") {
		t.Fatalf("init exit=%d stdout=%q, want missing BASE_DOMAIN", exit, stdout)
	}
	if exists(filepath.Join(initWorkspace, workspaceStateDir)) {
		t.Fatal("rejected input-required init created workspace state")
	}

	// Deployment plan resolves the actual Module order before accepting the
	// input contract. This remains the authoritative check for Modules brought
	// in transitively by dependencies or capabilities.
	planWorkspace := newWorkspace(t)
	if err := os.WriteFile(workspaceConfigPath(planWorkspace), input, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := writeManagedConfigState(planWorkspace, "input-required-test"); err != nil {
		t.Fatal(err)
	}
	stdout, _, exit = capture(t, "plan", "-w", planWorkspace,
		"--root", repoRoot(t), "--json")
	if exit != exitPrecondition || !strings.Contains(stdout, "BASE_DOMAIN") {
		t.Fatalf("deployment plan exit=%d stdout=%q, want missing BASE_DOMAIN", exit, stdout)
	}
	stdout, _, exit = capture(t, "lock", "-w", planWorkspace,
		"--root", repoRoot(t), "--json")
	if exit != exitPrecondition || !strings.Contains(stdout, "BASE_DOMAIN") {
		t.Fatalf("lock exit=%d stdout=%q, want missing BASE_DOMAIN", exit, stdout)
	}
}

func TestConfigSetRejectsRequiredAndConstrainedValuesBeforeWrite(t *testing.T) {
	workspace := newWorkspace(t)
	configPath := workspaceConfigPath(workspace)
	before, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		path, value, want string
	}{
		{path: "global.email", value: "  ", want: "required input"},
		{path: "global.timezone", value: "UTC+8", want: "IANA timezone"},
		{path: "eturnal.port", value: "0", want: "at least 1"},
	} {
		stdout, _, exit := capture(t,
			"config", "set", test.path, test.value,
			"-w", workspace, "--root", repoRoot(t), "--json",
		)
		if exit != 2 || !strings.Contains(stdout, test.want) {
			t.Errorf("config set %s=%q exit=%d stdout=%q, want usage error containing %q",
				test.path, test.value, exit, stdout, test.want)
		}
		after, err := os.ReadFile(configPath)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(after, before) {
			t.Fatalf("rejected config set %s modified managed config", test.path)
		}
	}
}

func TestConfigSetPersistsCanonicalValueAndReturnsSchemaMetadata(t *testing.T) {
	workspace := newWorkspace(t)
	stdout, stderr, exit := capture(t,
		"config", "set", "global.default_language", "pt_BR.UTF-8",
		"-w", workspace, "--root", repoRoot(t), "--json",
	)
	if exit != 0 || stderr != "" {
		t.Fatalf("config set exit=%d stderr=%q stdout=%q", exit, stderr, stdout)
	}
	document := requireSingleDocument(t, "config set schema", stdout)
	setting, ok := document["setting"].(map[string]any)
	if !ok {
		t.Fatalf("setting = %T", document["setting"])
	}
	want := map[string]any{
		"type": "string", "required": false, "input_required": false,
		"must_resolve": true, "has_default": false, "default_source": "host",
		"constraints": map[string]any{"format": "language_tag"},
	}
	for key, value := range want {
		if !reflect.DeepEqual(setting[key], value) {
			t.Errorf("setting.%s=%#v, want %#v", key, setting[key], value)
		}
	}
	settings, err := config.Settings(workspaceConfigPath(workspace))
	if err != nil {
		t.Fatal(err)
	}
	if got := settings["global.default_language"]; got != "pt-BR" {
		t.Fatalf("stored global.default_language=%q, want pt-BR", got)
	}
}

func TestConfigImportAppliesCommonConstraintsToStructuredAndRawValues(t *testing.T) {
	reg, err := loadRegistry(repoRoot(t))
	if err != nil {
		t.Fatal(err)
	}
	writeSource := func(body string) string {
		t.Helper()
		path := filepath.Join(t.TempDir(), "config.yml")
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		return path
	}

	for _, test := range []struct {
		name, body, want string
	}{
		{
			name: "structured integer",
			body: "modules:\n  eturnal:\n    config:\n      port: 0\nglobal:\n  base_domain: nas.test\n  email: admin@example.com\n",
			want: "at least 1",
		},
		{
			name: "raw declared integer",
			body: "modules:\n  traefik: {}\nglobal:\n  base_domain: nas.test\n  email: admin@example.com\nenv:\n  TRAEFIK_BASE_PORT: 65536\n",
			want: "at most 65535",
		},
		{
			name: "global format",
			body: "modules: {}\nglobal:\n  base_domain: nas.test\n  email: admin@example.com\n  timezone: UTC+8\n",
			want: "IANA timezone",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := normalizeImportedConfig(writeSource(test.body), reg)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("import error = %v, want %q", err, test.want)
			}
		})
	}

	valid := writeSource("modules:\n  eturnal:\n    config:\n      port: 65535\nglobal:\n  base_domain: nas.test\n  email: admin@example.com\n  default_locale: pt_BR.UTF-8\n")
	result, err := normalizeImportedConfig(valid, reg)
	if err != nil {
		t.Fatal(err)
	}
	body := string(result.Normalized)
	for _, want := range []string{"port: 65535", "default_locale: pt-BR"} {
		if !strings.Contains(body, want) {
			t.Errorf("normalized import is missing %q:\n%s", want, body)
		}
	}
}

func TestBundledInputRequiredNeverClaimsADefault(t *testing.T) {
	reg, err := loadRegistry(repoRoot(t))
	if err != nil {
		t.Fatal(err)
	}
	entries, err := collectConfigParameters(reg, nil)
	if err != nil {
		t.Fatal(err)
	}
	required := []string{}
	for _, entry := range entries {
		if !parameterInputRequired(entry.Module, entry.Parameter, reg) {
			continue
		}
		required = append(required, entry.Path)
		if entry.HasDefault || entry.DefaultSource != "none" {
			t.Errorf("%s input-required with has_default=%t source=%s", entry.Path, entry.HasDefault, entry.DefaultSource)
		}
		if !parameterMustResolve(entry.Module, entry.Parameter, reg) {
			t.Errorf("%s input-required but not must_resolve", entry.Path)
		}
	}
	sort.Strings(required)
	want := []string{"global.base_domain", "global.email"}
	if !reflect.DeepEqual(required, want) {
		t.Fatalf("input-required paths = %v, want %v", required, want)
	}
}

func TestLegacyRequiredWithDefaultProjectsAsMustResolveNotInputRequired(t *testing.T) {
	reg := map[string]Module{
		"legacy": {
			Name: "legacy", EnvPrefix: "LEGACY", Parameters: []string{"mode", "token"},
			Required: []string{"LEGACY_MODE", "LEGACY_TOKEN"},
			Defaults: map[string]string{"LEGACY_MODE": "safe"},
			Types: map[string]ParamType{
				"mode":  {Kind: "string"},
				"token": {Kind: "string", DefaultSource: "generated"},
			},
		},
	}
	for _, parameter := range []string{"mode", "token"} {
		if parameterInputRequired("legacy", parameter, reg) {
			t.Errorf("legacy.%s was projected as input-required despite its default source", parameter)
		}
		if !parameterMustResolve("legacy", parameter, reg) {
			t.Errorf("legacy.%s lost its final must-resolve invariant", parameter)
		}
	}
	value, hasDefault, source := parameterDefaultMetadata("legacy", "mode", reg, nil)
	if value != "safe" || !hasDefault || source != "static" {
		t.Fatalf("legacy.mode default metadata = %q, %t, %q", value, hasDefault, source)
	}
	value, hasDefault, source = parameterDefaultMetadata("legacy", "token", reg, nil)
	if value != "" || hasDefault || source != "generated" {
		t.Fatalf("legacy.token default metadata = %q, %t, %q", value, hasDefault, source)
	}
}
