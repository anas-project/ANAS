package runner

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/anas-project/ANAS/internal/configschema"
	"gopkg.in/yaml.v3"
)

func TestLocateModuleRootUsesAnasModuleRoot(t *testing.T) {
	root := filepath.Join(t.TempDir(), "bundles")
	moduleDir := filepath.Join(root, "test-module")
	if err := os.MkdirAll(moduleDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(moduleDir, "module.yml"), []byte("name: test-module\n"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ANAS_MODULE_ROOT", root)

	got, err := locateModuleRoot("")
	if err != nil {
		t.Fatal(err)
	}
	want, err := filepath.Abs(root)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("module root = %q, want %q", got, want)
	}
}

// The flag and the environment variable have to accept the same values. They
// did not: each caller normalised the flag it had just parsed, six copies of
// the same three lines, and the variable — read inside locateModuleRoot rather
// than by any caller — was the one that missed out. A path that worked as
// --module-root failed as an export, and the error named the two as
// interchangeable remedies.
func TestModuleRootAcceptsBundleDirOrItsParent(t *testing.T) {
	prefix := t.TempDir()
	bundles := filepath.Join(prefix, "modules")
	if err := os.MkdirAll(filepath.Join(bundles, "test-module"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bundles, "test-module", "module.yml"), []byte("name: test-module\n"), 0600); err != nil {
		t.Fatal(err)
	}
	want, err := filepath.Abs(bundles)
	if err != nil {
		t.Fatal(err)
	}

	for _, given := range []string{prefix, bundles} {
		t.Run("flag", func(t *testing.T) {
			t.Setenv("ANAS_MODULE_ROOT", "")
			got, err := locateModuleRoot(given)
			if err != nil {
				t.Fatalf("--module-root %s: %v", given, err)
			}
			if got != want {
				t.Fatalf("--module-root %s resolved to %q, want %q", given, got, want)
			}
		})
		t.Run("env", func(t *testing.T) {
			t.Setenv("ANAS_MODULE_ROOT", given)
			got, err := locateModuleRoot("")
			if err != nil {
				t.Fatalf("ANAS_MODULE_ROOT=%s: %v", given, err)
			}
			if got != want {
				t.Fatalf("ANAS_MODULE_ROOT=%s resolved to %q, want %q", given, got, want)
			}
		})
	}
}

func TestModulesUseManifestRule(t *testing.T) {
	root := filepath.Join("..", "..", "modules")
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	checked := 0
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		dir := filepath.Join(root, entry.Name())
		if _, err := os.Stat(filepath.Join(dir, "module.yml")); err != nil {
			t.Fatalf("%s missing module.yml", entry.Name())
		}
		b, err := os.ReadFile(filepath.Join(dir, "module.yml"))
		if err != nil {
			t.Fatal(err)
		}
		var manifest struct {
			APIVersion string `yaml:"api_version"`
			Kind       string `yaml:"kind"`
			Name       string `yaml:"name"`
			Version    string `yaml:"version"`
			Revision   int    `yaml:"revision"`
			ABI        struct {
				Supports []string `yaml:"supports"`
			} `yaml:"abi"`
			Runtime struct {
				Type        string `yaml:"type"`
				ComposeFile string `yaml:"compose_file"`
			} `yaml:"runtime"`
			Dependencies struct {
				Requires    []manifestDependency            `yaml:"requires"`
				RequiresOne []manifestAlternativeDependency `yaml:"requires_one"`
				After       []string                        `yaml:"after"`
			} `yaml:"dependencies"`
			Config struct {
				InputRequired []string                        `yaml:"input_required"`
				Required      []string                        `yaml:"required"`
				MustResolve   []string                        `yaml:"must_resolve"`
				Defaults      map[string]any                  `yaml:"defaults"`
				Types         map[string]manifestParamType    `yaml:"types"`
				Changes       map[string]manifestChangePolicy `yaml:"changes"`
			} `yaml:"config"`
			Services struct {
				Optional []struct {
					Name      string `yaml:"name"`
					EnabledBy string `yaml:"enabled_by"`
				} `yaml:"optional"`
			} `yaml:"services"`
			Logic struct {
				Hook HookConfig `yaml:"hook"`
			} `yaml:"logic"`
		}
		if err := yaml.Unmarshal(b, &manifest); err != nil {
			t.Fatalf("%s module.yml is invalid: %v", entry.Name(), err)
		}
		if manifest.APIVersion != "anas.module/v1" {
			t.Fatalf("%s api_version = %q", entry.Name(), manifest.APIVersion)
		}
		if manifest.Kind != "Module" {
			t.Fatalf("%s kind = %q", entry.Name(), manifest.Kind)
		}
		if manifest.Name != entry.Name() {
			t.Fatalf("%s manifest name = %q", entry.Name(), manifest.Name)
		}
		if manifest.Version == "" {
			t.Fatalf("%s missing version", entry.Name())
		}
		if manifest.Revision < 1 {
			t.Fatalf("%s revision = %d, want at least 1", entry.Name(), manifest.Revision)
		}
		if _, err := parseSemver(manifest.Version); err != nil {
			t.Fatalf("%s version %q is invalid: %v", entry.Name(), manifest.Version, err)
		}
		if !contains(manifest.ABI.Supports, currentModuleABI) {
			t.Fatalf("%s does not support ABI %s", entry.Name(), currentModuleABI)
		}
		if manifest.Runtime.Type != "builtin" && manifest.Runtime.Type != "compose" {
			t.Fatalf("%s runtime type = %q", entry.Name(), manifest.Runtime.Type)
		}
		if manifest.Runtime.Type == "compose" && manifest.Runtime.ComposeFile == "" {
			t.Fatalf("%s compose runtime is missing compose_file", entry.Name())
		}
		for _, dep := range manifest.Dependencies.RequiresOne {
			if looksLikeEnvParam(dep.SelectedBy) {
				t.Fatalf("%s requires_one selected_by %q should use lower snake_case", entry.Name(), dep.SelectedBy)
			}
		}
		for stage, parameters := range map[string][]string{
			"input_required": manifest.Config.InputRequired,
			"required":       manifest.Config.Required,
			"must_resolve":   manifest.Config.MustResolve,
		} {
			for _, key := range parameters {
				if looksLikeEnvParam(key) {
					t.Fatalf("%s %s parameter %q should use lower snake_case", entry.Name(), stage, key)
				}
			}
		}
		for key := range manifest.Config.Defaults {
			if looksLikeEnvParam(key) {
				t.Fatalf("%s default parameter %q should use lower snake_case", entry.Name(), key)
			}
		}
		for key := range manifest.Config.Types {
			if looksLikeEnvParam(key) {
				t.Fatalf("%s typed parameter %q should use lower snake_case", entry.Name(), key)
			}
		}
		for key, policy := range manifest.Config.Changes {
			if looksLikeEnvParam(key) {
				t.Fatalf("%s change parameter %q should use lower snake_case", entry.Name(), key)
			}
			if !validChangeEffect(policy.Effect) {
				t.Fatalf("%s change parameter %q has invalid effect %q", entry.Name(), key, policy.Effect)
			}
		}
		for _, svc := range manifest.Services.Optional {
			if looksLikeEnvParam(svc.EnabledBy) {
				t.Fatalf("%s optional enabled_by %q should use lower snake_case", entry.Name(), svc.EnabledBy)
			}
		}
		if len(manifest.Logic.Hook.Command) > 0 {
			last := manifest.Logic.Hook.Command[len(manifest.Logic.Hook.Command)-1]
			if last != "./hook" {
				t.Fatalf("%s hook command should point at ./hook, got %q", entry.Name(), last)
			}
			if _, err := os.Stat(filepath.Join(dir, "hook", "main.go")); err != nil {
				t.Fatalf("%s hook/main.go missing: %v", entry.Name(), err)
			}
		}
		if _, err := os.Stat(filepath.Join(dir, "runner.rb")); err == nil {
			t.Fatalf("%s still contains legacy runner.rb", entry.Name())
		} else if !os.IsNotExist(err) {
			t.Fatal(err)
		}
		checked++
	}
	if checked == 0 {
		t.Fatal("no modules checked")
	}
}

func TestNextcloudDeclaresProvisioningSeparatelyFromAuthentication(t *testing.T) {
	mod, err := loadModuleManifest(filepath.Join("..", "..", "modules", "nextcloud"), "nextcloud")
	if err != nil {
		t.Fatal(err)
	}
	if mod.IdentityProvisioning == nil || !contains(mod.IdentityProvisioning.AnyOf, "ldaps") {
		t.Fatalf("provisioning = %+v, want required LDAPS", mod.IdentityProvisioning)
	}
	if mod.IdentityAuthentication == nil || !contains(mod.IdentityAuthentication.AnyOf, "oidc") || !contains(mod.IdentityAuthentication.AnyOf, "saml") {
		t.Fatalf("authentication = %+v, want OIDC and SAML", mod.IdentityAuthentication)
	}
	if len(mod.IdentityAuthentication.Prefer) == 0 || mod.IdentityAuthentication.Prefer[0] != "oidc" {
		t.Fatalf("authentication preference = %+v, want OIDC first", mod.IdentityAuthentication.Prefer)
	}
	if mod.IdentityAuthentication.SelectedBy != "iam_protocol" {
		t.Fatalf("authentication selected_by = %q, want iam_protocol", mod.IdentityAuthentication.SelectedBy)
	}
	if mod.IdentityProvisioning.IdentityKey != "anasIdentityAnchor" {
		t.Fatalf("identity key = %q", mod.IdentityProvisioning.IdentityKey)
	}
}

func TestMeshcentralDeclaresOIDCAuthenticationAndLDAPProvisioning(t *testing.T) {
	mod, err := loadModuleManifest(filepath.Join("..", "..", "modules", "meshcentral"), "meshcentral")
	if err != nil {
		t.Fatal(err)
	}
	if mod.IdentityProvisioning == nil || !contains(mod.IdentityProvisioning.AnyOf, "ldaps") {
		t.Fatalf("provisioning = %+v, want required LDAPS", mod.IdentityProvisioning)
	}
	if mod.IdentityAuthentication == nil || len(mod.IdentityAuthentication.AnyOf) != 1 || mod.IdentityAuthentication.AnyOf[0] != "oidc" {
		t.Fatalf("authentication = %+v, want OIDC only", mod.IdentityAuthentication)
	}
	if mod.IdentityAuthentication.SelectedBy != "iam_protocol" {
		t.Fatalf("authentication selected_by = %q, want iam_protocol", mod.IdentityAuthentication.SelectedBy)
	}
	if len(mod.LocalAccounts) != 0 {
		t.Fatalf("MeshCentral must not claim an unimplemented local recovery account: %+v", mod.LocalAccounts)
	}
}

func TestBundledLocalAdministratorCapabilitiesAreRealAndScoped(t *testing.T) {
	for _, tc := range []struct{ module, id, purpose, handler string }{
		{"ddns_go", "primary", "primary", "rotate-ddns-go-local-admin"},
		{"nextcloud", "break_glass", "break_glass", "rotate-nextcloud-break-glass"},
	} {
		mod, err := loadModuleManifest(filepath.Join("..", "..", "modules", tc.module), tc.module)
		if err != nil {
			t.Fatal(err)
		}
		if len(mod.LocalAccounts) != 1 {
			t.Fatalf("%s local accounts = %+v", tc.module, mod.LocalAccounts)
		}
		account := mod.LocalAccounts[0]
		if account.ID != tc.id || account.Purpose != tc.purpose || account.Rotate != tc.handler {
			t.Fatalf("%s account = %+v", tc.module, account)
		}
		if len(mod.Hook.Command) == 0 {
			t.Fatalf("%s declares rotation without a module hook", tc.module)
		}
	}
}

func TestNextcloudAdministratorPasswordIsNotConfiguration(t *testing.T) {
	mod, err := loadModuleManifest(filepath.Join("..", "..", "modules", "nextcloud"), "nextcloud")
	if err != nil {
		t.Fatal(err)
	}
	if contains(mod.Parameters, "admin_password") {
		t.Fatal("Nextcloud managed administrator password is still a config parameter")
	}
}

// A declaration is not a consumer. The runner will happily put any manifest
// parameter in .env, including a typo or a value no process ever reads, so a
// render-only test cannot distinguish configuration from dead text. Keep the
// small set whose key is assembled dynamically explicit; every other parameter
// must have a literal consumer in shipped code, Compose, or a container asset.
//
// An environment variable read only by an upstream image also belongs in this
// exception map, with a pinned source URL in the reason. There are currently no
// such implicit consumers: retained upstream-image settings are translated in
// our hook or named explicitly in Compose, which makes upgrades auditable.
func TestDeclaredParametersHaveRuntimeConsumers(t *testing.T) {
	root := filepath.Join("..", "..")
	reg, err := loadRegistry(root)
	if err != nil {
		t.Fatal(err)
	}

	dynamic := map[string]string{
		"collabora.domain_prefix":             "domainCalc assembles COLLABORA_DOMAIN_PREFIX",
		"ddns_go.dns_provider":                "key() assembles the DDNS_GO namespace",
		"ddns_go.domain_prefix":               "key() assembles the DDNS_GO namespace",
		"ddns_go.web_enabled":                 "key() assembles the DDNS_GO namespace",
		"ddns_updater.forward_auth_interface": "capability interface_selected_by reads the parameter",
		"llng.domain_prefix":                  "identityCalc assembles the LLNG domain key",
		"llng.manager_domain_prefix":          "identityCalc assembles the LLNG manager domain key",
		"llng.test_domain_prefix":             "identityCalc assembles the LLNG test domain key",
		"netbird.iam_protocol":                "capability interface_selected_by reads the parameter",
		"oauth2_proxy.iam_protocol":           "capability interface_selected_by reads the parameter",
		"traefik.domain_prefix":               "domainCalc assembles the TRAEFIK domain key",
	}

	for module, mod := range reg {
		for _, parameter := range mod.Parameters {
			id := module + "." + parameter
			if dynamic[id] != "" {
				continue
			}
			key := moduleParamEnvKey(module, mod.EnvPrefix, mod.Exports, parameter)
			if !treeContainsRuntimeKey(filepath.Join(root, "modules", module), key) {
				t.Errorf("%s produces %s but no shipped runtime file reads it; remove it or document a dynamic/upstream consumer", id, key)
			}
		}
	}
}

func treeContainsRuntimeKey(root, key string) bool {
	found := false
	keyPattern := regexp.MustCompile(`(^|[^A-Z0-9_])` + regexp.QuoteMeta(key) + `([^A-Z0-9_]|$)`)
	_ = filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil || found {
			return err
		}
		if info.IsDir() {
			return nil
		}
		name := info.Name()
		if name == "module.yml" || name == "README.md" || strings.HasSuffix(name, "_test.go") {
			return nil
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		found = keyPattern.Match(content)
		return nil
	})
	return found
}

// A module that forgets upgrade.data_breaking is not obviously broken: it renders,
// deploys and runs. The only symptom is that every rollback across a version
// change is refused as "data compatibility unknown", which surfaces months
// later on the day someone needs to roll back. So it is checked here instead.
func TestBundledModulesDeclareDataBreaking(t *testing.T) {
	root := filepath.Join("..", "..", "modules")
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	checked := 0
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		mod, err := loadModuleManifest(filepath.Join(root, entry.Name()), entry.Name())
		if err != nil {
			t.Fatalf("%s: %v", entry.Name(), err)
		}
		if mod.DataBreaking == nil {
			t.Fatalf("module %s does not declare upgrade.data_breaking; "+
				"every rollback that changes its version will be refused as unknown", entry.Name())
		}
		// Nothing has shipped yet, so the claim each module is making is that no
		// release of it has ever rewritten its data format. That is checkable
		// today; the assertion is here so that the day one of them stops being
		// true, the change is deliberate.
		if len(*mod.DataBreaking) != 0 {
			t.Logf("module %s declares data-breaking versions %v", entry.Name(), *mod.DataBreaking)
		}
		checked++
	}
	if checked == 0 {
		t.Fatal("no modules checked")
	}
}

func TestBundledModulesDeclareLifecycle(t *testing.T) {
	root := filepath.Join("..", "..", "modules")
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		mod, err := loadModuleManifest(filepath.Join(root, entry.Name()), entry.Name())
		if err != nil {
			t.Fatalf("%s: %v", entry.Name(), err)
		}
		if mod.Lifecycle == "" {
			t.Fatalf("module %s has no lifecycle", entry.Name())
		}
	}
}

func TestManifestRejectsUnknownLifecycle(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "example")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	manifest := `api_version: anas.module/v1
kind: Module
name: example
version: 1.0.0
revision: 1
status: experimental
abi:
  supports: [anas.module-hook/v1]
runtime:
  type: builtin
`
	if err := os.WriteFile(filepath.Join(dir, "module.yml"), []byte(manifest), 0600); err != nil {
		t.Fatal(err)
	}
	_, err := loadModuleManifest(dir, "example")
	if err == nil || !strings.Contains(err.Error(), "expected release, developing, or deprecated") {
		t.Fatalf("error = %v, want lifecycle validation error", err)
	}
}

func TestNormalizeParamTypesDistinguishesExplicitStringAndRejectsKindEnumConflict(t *testing.T) {
	types, err := normalizeParamTypes("example", map[string]manifestParamType{
		"title": {Kind: "string"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := types["title"]; !got.Declared() || got.Kind != "string" {
		t.Fatalf("explicit string = %+v", got)
	}
	if types["missing"].Declared() {
		t.Fatal("missing type was reported as declared")
	}

	_, err = normalizeParamTypes("example", map[string]manifestParamType{
		"mode": {Kind: "string", Enum: []string{"one", "two"}},
	})
	if err == nil || !strings.Contains(err.Error(), "combines kind") {
		t.Fatalf("error = %v, want kind/enum conflict", err)
	}
	types, err = normalizeParamTypes("example", map[string]manifestParamType{
		"mode": {Enum: []string{"prod", "PROD"}},
	})
	if err != nil || len(types["mode"].Enum) != 2 {
		t.Fatalf("case-distinct legacy enum = %+v, %v", types["mode"], err)
	}
}

func TestNormalizeParamTypesRejectsEmptyAndDuplicateNormalizedNames(t *testing.T) {
	for _, test := range []struct {
		name  string
		types map[string]manifestParamType
		want  string
	}{
		{name: "empty", types: map[string]manifestParamType{"  ": {Kind: "string"}}, want: "empty parameter"},
		{
			name: "case duplicate",
			types: map[string]manifestParamType{
				"Mode": {Kind: "string"}, " mode ": {Kind: "int"},
			},
			want: "more than once",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := normalizeParamTypes("example", test.types)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestManifestRejectsExplicitEmptyParameterTypes(t *testing.T) {
	for name, declaration := range map[string]string{
		"empty mapping": "{}",
		"null kind":     "{kind: null}",
		"null enum":     "{enum: null}",
	} {
		t.Run(name, func(t *testing.T) {
			dir := writeModuleConfigManifest(t, "  types:\n    mode: "+declaration+"\n")
			_, err := loadModuleManifest(dir, "example")
			if err == nil || !strings.Contains(err.Error(), "empty type") {
				t.Fatalf("error = %v, want explicit empty type rejection", err)
			}
		})
	}
}

func TestNormalizeParamTypesLoadsConstraintsAndDefaultSource(t *testing.T) {
	minimum, maximum := 1, 10
	types, err := normalizeParamTypes("example", map[string]manifestParamType{
		"workers": {
			Kind: "int", Constraints: configschema.Constraints{Minimum: &minimum, Maximum: &maximum},
			DefaultSource: configschema.DefaultSourceRuntime,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	got := types["workers"]
	if got.Kind != "int" || got.Constraints.Minimum == nil || *got.Constraints.Minimum != 1 ||
		got.Constraints.Maximum == nil || *got.Constraints.Maximum != 10 ||
		got.DefaultSource != configschema.DefaultSourceRuntime {
		t.Fatalf("normalized parameter = %+v", got)
	}
}

func TestManifestCanonicalizesCustomEnvPrefix(t *testing.T) {
	dir := writeModuleConfigManifest(t, "  env_prefix: demo-prefix\n  types: {token: string}\n")
	mod, err := loadModuleManifest(dir, "example")
	if err != nil {
		t.Fatal(err)
	}
	if mod.EnvPrefix != "DEMO_PREFIX" {
		t.Fatalf("custom env_prefix = %q, want DEMO_PREFIX", mod.EnvPrefix)
	}
}

func TestManifestRejectsUnaddressableConfigNamesAndPrefixes(t *testing.T) {
	for _, test := range []struct {
		name, configBlock, want string
	}{
		{name: "type name", configBlock: `  types: {"foo.bar": string}
`, want: "lower-snake-case"},
		{name: "default name", configBlock: `  defaults: {"foo bar": value}
`, want: "lower-snake-case"},
		{name: "change name", configBlock: `  changes: {"foo/bar": {effect: container_recreate}}
`, want: "lower-snake-case"},
		{name: "requirement name", configBlock: `  input_required: ["foo.bar"]
  types: {token: string}
`, want: "lower-snake-case or an environment key"},
		{name: "env prefix", configBlock: `  env_prefix: "demo prefix"
  types: {token: string}
`, want: "environment-safe prefix"},
	} {
		t.Run(test.name, func(t *testing.T) {
			dir := writeModuleConfigManifest(t, test.configBlock)
			_, err := loadModuleManifest(dir, "example")
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestManifestRequirementsUseStrictEnvironmentKeySyntax(t *testing.T) {
	fields := []struct {
		name   string
		values func(Module) []string
	}{
		{name: "input_required", values: func(mod Module) []string { return mod.InputRequired }},
		{name: "required", values: func(mod Module) []string { return mod.Required }},
		{name: "must_resolve", values: func(mod Module) []string { return mod.MustResolve }},
	}
	tests := []struct {
		name, value, want string
		valid             bool
	}{
		{name: "lower snake parameter", value: "api_token", want: "EXAMPLE_API_TOKEN", valid: true},
		{name: "exact env key", value: "API_TOKEN_2", want: "API_TOKEN_2", valid: true},
		{name: "single word env key", value: "TZ", want: "TZ", valid: true},
		{name: "space", value: "FOO BAR"},
		{name: "hyphen", value: "FOO-BAR"},
		{name: "leading digit", value: "9FOO"},
		{name: "dot", value: "FOO.BAR"},
		{name: "slash", value: "FOO/BAR"},
		{name: "assignment", value: "FOO=BAR"},
		{name: "wildcard", value: "FOO*"},
	}

	for _, field := range fields {
		for _, test := range tests {
			t.Run(field.name+"/"+test.name, func(t *testing.T) {
				dir := writeModuleConfigManifest(t, fmt.Sprintf("  %s: [%q]\n", field.name, test.value))
				mod, err := loadModuleManifest(dir, "example")
				if !test.valid {
					if err == nil || !strings.Contains(err.Error(), "lower-snake-case or an environment key") {
						t.Fatalf("error = %v, want strict requirement-name rejection", err)
					}
					return
				}
				if err != nil {
					t.Fatal(err)
				}
				if got := field.values(mod); !reflect.DeepEqual(got, []string{test.want}) {
					t.Fatalf("normalized %s = %v, want [%s]", field.name, got, test.want)
				}
			})
		}
	}
}

func TestManifestEnvPatternsUseStrictEnvironmentKeySyntax(t *testing.T) {
	tests := []struct {
		name, value, want string
		valid             bool
	}{
		{name: "exact", value: "API_TOKEN_2", want: "API_TOKEN_2", valid: true},
		{name: "single word", value: "TZ", want: "TZ", valid: true},
		{name: "leading underscore", value: "_PRIVATE", want: "_PRIVATE", valid: true},
		{name: "trailing wildcard", value: "ANAS_IAM_BINDING_*", want: "ANAS_IAM_BINDING_*", valid: true},
		{name: "wildcard without separator", value: "APPS_LIST*", want: "APPS_LIST*", valid: true},
		{name: "leading wildcard", value: "*_DB_NAME", want: "*_DB_NAME", valid: true},
		{name: "trimmed", value: "  ANAS_TLS_*  ", want: "ANAS_TLS_*", valid: true},
		{name: "empty"},
		{name: "lowercase", value: "api_token"},
		{name: "space", value: "FOO BAR"},
		{name: "hyphen", value: "FOO-BAR"},
		{name: "leading digit", value: "9FOO"},
		{name: "bare wildcard", value: "*"},
		{name: "embedded wildcard", value: "FOO*BAR"},
		{name: "multiple wildcards", value: "FOO**"},
	}

	for _, field := range []string{"exports", "consumes"} {
		for _, test := range tests {
			t.Run(field+"/"+test.name, func(t *testing.T) {
				got, err := normalizeEnvPatterns("example", field, []string{test.value})
				if !test.valid {
					if err == nil {
						t.Fatalf("normalizeEnvPatterns(%q) = %v, want error", test.value, got)
					}
					return
				}
				if err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(got, []string{test.want}) {
					t.Fatalf("normalized pattern = %v, want [%s]", got, test.want)
				}
			})
		}
	}
}

func TestBundledManifestEnvironmentDeclarationsUseStrictSyntax(t *testing.T) {
	reg, err := loadRegistry(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	if len(reg) == 0 {
		t.Fatal("no bundled Module manifests loaded")
	}
}

func TestManifestRejectsParametersSharingOneRuntimeKey(t *testing.T) {
	dir := writeModuleConfigManifest(t, `  env_prefix: demo
  exports: [TZ]
  types:
    timezone: string
    tz: string
`)
	_, err := loadModuleManifest(dir, "example")
	if err == nil || !strings.Contains(err.Error(), "both resolve to runtime key TZ") {
		t.Fatalf("runtime-key collision error = %v", err)
	}
}

func TestRegistryRejectsParameterRuntimeKeyCollisionsAcrossModules(t *testing.T) {
	writeRegistry := func(t *testing.T, modules map[string]string) string {
		t.Helper()
		root := t.TempDir()
		for name, configBlock := range modules {
			dir := filepath.Join(root, name)
			if err := os.MkdirAll(dir, 0o700); err != nil {
				t.Fatal(err)
			}
			manifest := `api_version: anas.module/v1
kind: Module
name: ` + name + `
version: 1.0.0
revision: 1
status: release
abi:
  supports: [anas.module-hook/v1]
runtime:
  type: builtin
config:
` + configBlock
			if err := os.WriteFile(filepath.Join(dir, "module.yml"), []byte(manifest), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		return root
	}
	for name, test := range map[string]struct {
		modules map[string]string
		want    string
	}{
		"exact runtime key": {
			modules: map[string]string{
				"one": "  env_prefix: SHARED\n  types: {token: string}\n",
				"two": "  env_prefix: SHARED\n  types: {token: string}\n",
			},
			want: "prefix collision",
		},
		"same prefix different parameters": {
			modules: map[string]string{
				"one": "  env_prefix: SHARED\n  types: {alpha: string}\n",
				"two": "  env_prefix: SHARED\n  types: {beta: string}\n",
			},
			want: "prefix collision",
		},
		"nested prefixes": {
			modules: map[string]string{
				"one": "  env_prefix: SHARED\n  types: {alpha: string}\n",
				"two": "  env_prefix: SHARED_CHILD\n  types: {beta: string}\n",
			},
			want: "prefix collision",
		},
		"global namespace": {
			modules: map[string]string{"one": "  env_prefix: HOST\n  types: {token: string}\n"},
			want:    "HOST_IP",
		},
		"runner namespace": {
			modules: map[string]string{"one": "  env_prefix: ANAS_RUNTIME\n  types: {token: string}\n"},
			want:    "runner-reserved",
		},
		"runner topology key": {
			modules: map[string]string{"one": "  env_prefix: ALL\n  types: {token: string}\n"},
			want:    "ALL_MODS_NAME",
		},
		"bare workspace export": {
			modules: map[string]string{"one": "  exports: [DATA_PATH]\n  types: {data_path: string}\n"},
			want:    "DATA_PATH",
		},
		"bare domains export": {
			modules: map[string]string{"one": "  exports: [DOMAINS]\n  types: {domains: string}\n"},
			want:    "DOMAINS",
		},
		"bare topology export": {
			modules: map[string]string{"one": "  exports: [ALL_MODS_NAME]\n  types: {all_mods_name: string}\n"},
			want:    "ALL_MODS_NAME",
		},
		"export-only workspace literal": {
			modules: map[string]string{"one": "  exports: [DATA_PATH]\n"},
			want:    "DATA_PATH",
		},
		"export-only workspace glob": {
			modules: map[string]string{"one": "  exports: [DATA_*]\n"},
			want:    "DATA_PATH",
		},
		"export-only global glob": {
			modules: map[string]string{"one": "  exports: [T*]\n"},
			want:    "TZ",
		},
		"export-only topology literal": {
			modules: map[string]string{"one": "  exports: [DOMAINS]\n"},
			want:    "DOMAINS",
		},
		"bare config-derived default parameter": {
			modules: map[string]string{"one": "  exports: [ANAS_IMAGE_REGISTRY]\n  types: {anas_image_registry: string}\n"},
			want:    "ANAS_IMAGE_REGISTRY",
		},
		"hook-only config-derived default export": {
			modules: map[string]string{"one": "  exports: [GITHUB_DOWNLOAD_PROXY_PREFIX]\n"},
			want:    "GITHUB_DOWNLOAD_PROXY_PREFIX",
		},
		"hook-only config-derived default glob": {
			modules: map[string]string{"one": "  exports: [NEXTCLOUD_*]\n"},
			want:    "NEXTCLOUD_APPSTORE_URL",
		},
		"hook-only config-derived custom prefix": {
			modules: map[string]string{"one": "  env_prefix: GITHUB_DOWNLOAD_PROXY\n"},
			want:    "GITHUB_DOWNLOAD_PROXY_PREFIX",
		},
		"appstore config-derived custom prefix": {
			modules: map[string]string{"one": "  env_prefix: NEXTCLOUD_APPSTORE\n"},
			want:    "NEXTCLOUD_APPSTORE_URL",
		},
		"registry config-derived custom prefix": {
			modules: map[string]string{"one": "  env_prefix: DOCKER_HUB\n"},
			want:    "DOCKER_HUB_REGISTRY",
		},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := loadRegistryDir(writeRegistry(t, test.modules))
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("registry ownership error = %v, want %q", err, test.want)
			}
		})
	}
	t.Run("consuming a runner key remains allowed", func(t *testing.T) {
		_, err := loadRegistryDir(writeRegistry(t, map[string]string{
			"one": "  consumes: [DOMAINS]\n  types: {token: string}\n",
		}))
		if err != nil {
			t.Fatalf("registry admission rejected read-only consume: %v", err)
		}
	})
}

func TestManifestRejectsUnknownNestedParameterTypeFields(t *testing.T) {
	for _, test := range []struct {
		name, declaration, want string
	}{
		{
			name: "parameter field",
			declaration: `    workers:
      kind: int
      default_sources: runtime
`,
			want: "default_sources",
		},
		{
			name: "constraint field",
			declaration: `    workers:
      kind: int
      constraints:
        minimun: 1
`,
			want: "minimun",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			dir := writeModuleConfigManifest(t, "  types:\n"+test.declaration)
			_, err := loadModuleManifest(dir, "example")
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want unknown field %q", err, test.want)
			}
		})
	}
}

func TestManifestDefaultsUseConstraintAndFormatNormalization(t *testing.T) {
	valid := writeModuleConfigManifest(t, `  defaults:
    address: " 192.0.2.10 "
    workers: "08"
  types:
    address:
      kind: string
      constraints: {format: ipv4}
    workers:
      kind: int
      constraints: {minimum: 1, maximum: 10}
`)
	mod, err := loadModuleManifest(valid, "example")
	if err != nil {
		t.Fatal(err)
	}
	if got := mod.Defaults["EXAMPLE_ADDRESS"]; got != "192.0.2.10" {
		t.Fatalf("formatted default = %q", got)
	}
	if got := mod.Defaults["EXAMPLE_WORKERS"]; got != "08" {
		t.Fatalf("integer default = %q", got)
	}

	invalid := writeModuleConfigManifest(t, `  defaults: {workers: "0"}
  types:
    workers:
      kind: int
      constraints: {minimum: 1}
`)
	if _, err := loadModuleManifest(invalid, "example"); err == nil || !strings.Contains(err.Error(), "at least 1") {
		t.Fatalf("constrained default error = %v", err)
	}
}

func TestManifestSeparatesAllRequirementStages(t *testing.T) {
	dir := writeModuleConfigManifest(t, `  input_required: [token]
  required: [mode, endpoint]
  must_resolve: [address, token]
  defaults:
    mode: safe
  types:
    token: string
    mode: string
    endpoint:
      kind: string
      default_source: runtime
    address:
      kind: string
      default_source: runtime
`)
	mod, err := loadModuleManifest(dir, "example")
	if err != nil {
		t.Fatal(err)
	}
	if got, want := mod.InputRequired, []string{"EXAMPLE_TOKEN"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("input required = %v, want %v", got, want)
	}
	if got, want := mod.Required, []string{"EXAMPLE_MODE", "EXAMPLE_ENDPOINT"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("legacy pre-hook required = %v, want %v", got, want)
	}
	if got, want := mod.MustResolve, []string{"EXAMPLE_ADDRESS", "EXAMPLE_TOKEN"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("post-hook must-resolve = %v, want %v", got, want)
	}
	if !contains(mod.Parameters, "address") {
		t.Fatalf("must_resolve-only parameter missing from inventory: %v", mod.Parameters)
	}
}

func TestLegacyBareEnvRequirementRemainsRuntimeOnly(t *testing.T) {
	dir := writeModuleConfigManifest(t, "  required: [EXTERNAL_TOKEN]\n")
	mod, err := loadModuleManifest(dir, "example")
	if err != nil {
		t.Fatal(err)
	}
	if got, want := mod.Required, []string{"EXTERNAL_TOKEN"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("legacy runtime requirement = %v, want %v", got, want)
	}
	if contains(mod.Parameters, "external_token") {
		t.Fatalf("bare runtime requirement was fabricated as Module parameter: %v", mod.Parameters)
	}

	newApp := func(env map[string]string) *app {
		seedCalculateGlobalRequirements(env)
		return &app{
			base: t.TempDir(), reg: map[string]Module{"example": mod}, order: []string{"example"},
			env: env, envOwner: map[string]string{},
			secrets: &secretStore{values: map[string]string{}, metadata: map[string]secretMetadata{}},
		}
	}
	if err := newApp(map[string]string{}).calculate(); err == nil || !strings.Contains(err.Error(), "EXTERNAL_TOKEN") {
		t.Fatalf("missing bare legacy requirement error = %v", err)
	}
	if err := newApp(map[string]string{"EXTERNAL_TOKEN": "provided"}).calculate(); err != nil {
		t.Fatalf("bare legacy requirement supplied through its runtime key was rejected: %v", err)
	}
}

func TestManifestRejectsMisspelledInputRequired(t *testing.T) {
	dir := writeModuleConfigManifest(t, `  input_requiredd: [token]
  types: {token: string}
`)
	if _, err := loadModuleManifest(dir, "example"); err == nil || !strings.Contains(err.Error(), "input_requiredd") {
		t.Fatalf("error = %v, want strict unknown-field rejection", err)
	}
}

func TestRuntimeManifestRejectsContradictoryInputRequiredDefaults(t *testing.T) {
	for _, configBlock := range []string{
		`  input_required: [token]
  defaults: {token: fallback}
  types: {token: string}
`,
		`  input_required: [token]
  types:
    token: {kind: string, default_source: generated}
`,
		`  input_required: [EXAMPLE_TOKEN]
  defaults: {token: fallback}
  types: {token: string}
`,
		`  input_required: [EXAMPLE_TOKEN]
  types:
    token: {kind: string, default_source: generated}
`,
	} {
		dir := writeModuleConfigManifest(t, configBlock)
		if _, err := loadModuleManifest(dir, "example"); err == nil || !strings.Contains(err.Error(), "config.input_required") {
			t.Errorf("contradictory runtime manifest error = %v", err)
		}
	}
}

func TestManifestRejectsMalformedRequirementLists(t *testing.T) {
	for _, field := range []string{"input_required", "required", "must_resolve"} {
		for _, test := range []struct {
			name, values, want string
		}{
			{name: "empty", values: `"", token`, want: "empty parameter"},
			{name: "normalized duplicate", values: `Token, " token "`, want: "more than once after normalization"},
		} {
			t.Run(field+"/"+test.name, func(t *testing.T) {
				dir := writeModuleConfigManifest(t, "  "+field+": ["+test.values+"]\n  types: {token: string}\n")
				if _, err := loadModuleManifest(dir, "example"); err == nil || !strings.Contains(err.Error(), "config."+field) || !strings.Contains(err.Error(), test.want) {
					t.Fatalf("error = %v, want config.%s %s", err, field, test.want)
				}
			})
		}
	}
}

func TestManifestRejectsMalformedDefaultAndChangeKeys(t *testing.T) {
	for _, test := range []struct {
		name, config, field, want string
	}{
		{name: "empty default", config: "  defaults:\n    \" \": value\n", field: "config.defaults", want: "empty parameter name"},
		{name: "duplicate default", config: "  defaults:\n    Mode: safe\n    \" mode \": fast\n  types: {mode: string}\n", field: "config.defaults", want: "more than once after normalization"},
		{name: "empty change", config: "  changes:\n    \" \": {effect: container_recreate}\n", field: "config.changes", want: "empty parameter name"},
		{name: "duplicate change", config: "  changes:\n    Mode: {effect: container_recreate}\n    \" mode \": {effect: hot_reload}\n  types: {mode: string}\n", field: "config.changes", want: "more than once after normalization"},
	} {
		t.Run(test.name, func(t *testing.T) {
			dir := writeModuleConfigManifest(t, test.config)
			if _, err := loadModuleManifest(dir, "example"); err == nil || !strings.Contains(err.Error(), test.field) || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want %s %s", err, test.field, test.want)
			}
		})
	}
}

func TestManifestRequiresCredentialRotationParametersToBeSensitive(t *testing.T) {
	dir := writeModuleConfigManifest(t, `  changes:
    token: {effect: credential_rotate}
  types: {token: string}
`)
	if _, err := loadModuleManifest(dir, "example"); err == nil || !strings.Contains(err.Error(), "credential_rotate") || !strings.Contains(err.Error(), "sensitive") {
		t.Fatalf("unsafe credential policy error = %v", err)
	}

	dir = writeModuleConfigManifest(t, `  changes:
    token: {effect: credential_rotate, sensitive: true}
  types: {token: string}
`)
	if _, err := loadModuleManifest(dir, "example"); err != nil {
		t.Fatalf("sensitive credential policy was rejected: %v", err)
	}
}

func TestManifestRejectsDefaultOutsideDeclaredTypeButAllowsLegacyUndeclaredDefault(t *testing.T) {
	invalid := writeModuleConfigManifest(t, `  defaults:
    enabled: maybe
  types:
    enabled: bool
`)
	if _, err := loadModuleManifest(invalid, "example"); err == nil || !strings.Contains(err.Error(), "accepts true or false") {
		t.Fatalf("invalid typed default error = %v", err)
	}
	for _, value := range []string{"null", `""`} {
		empty := writeModuleConfigManifest(t, `  defaults:
    enabled: `+value+`
  types:
    enabled: bool
`)
		if _, err := loadModuleManifest(empty, "example"); err == nil || !strings.Contains(err.Error(), "non-empty bool") {
			t.Errorf("typed empty default %s error = %v", value, err)
		}
	}

	emptyString := writeModuleConfigManifest(t, `  defaults:
    label: ""
  types:
    label: string
`)
	if mod, err := loadModuleManifest(emptyString, "example"); err != nil || mod.Defaults["EXAMPLE_LABEL"] != "" {
		t.Fatalf("explicit empty string default = %+v, %v", mod, err)
	}

	legacy := writeModuleConfigManifest(t, `  defaults:
    legacy_value: anything
`)
	mod, err := loadModuleManifest(legacy, "example")
	if err != nil {
		t.Fatalf("legacy undeclared default was rejected: %v", err)
	}
	if mod.Types["legacy_value"].Declared() {
		t.Fatalf("legacy type = %+v, want undeclared", mod.Types["legacy_value"])
	}

	canonical := writeModuleConfigManifest(t, `  defaults:
    enabled: " TRUE "
    mode: FAST
  types:
    enabled: bool
    mode: {enum: [safe, fast]}
`)
	mod, err = loadModuleManifest(canonical, "example")
	if err != nil {
		t.Fatalf("canonicalizable typed defaults were rejected: %v", err)
	}
	if got := mod.Defaults["EXAMPLE_ENABLED"]; got != "true" {
		t.Errorf("bool default = %q, want true", got)
	}
	if got := mod.Defaults["EXAMPLE_MODE"]; got != "fast" {
		t.Errorf("enum default = %q, want fast", got)
	}
}

func TestModuleLifecyclePlanSummary(t *testing.T) {
	a := &app{
		order: []string{"stable", "vpn", "old"},
		reg: map[string]Module{
			"stable": {Name: "stable", Lifecycle: "release"},
			"vpn":    {Name: "vpn", Lifecycle: "developing"},
			"old":    {Name: "old", Lifecycle: "deprecated"},
		},
	}
	got := a.moduleLifecyclePlanSummary()
	for _, want := range []string{
		"module lifecycle: vpn=developing (not release quality)",
		"module lifecycle: old=deprecated (do not use for new deployments)",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("summary = %q, want %q", got, want)
		}
	}
	if strings.Contains(got, "stable") {
		t.Fatalf("release modules should not create lifecycle warnings: %q", got)
	}
}

func TestManifestRejectsRemovedBeforeDependencyField(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "example")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	manifest := `api_version: anas.module/v1
kind: Module
name: example
version: 1.0.0
revision: 1
status: release
abi:
  supports: [anas.module-hook/v1]
runtime:
  type: builtin
dependencies:
  before: [core]
`
	if err := os.WriteFile(filepath.Join(dir, "module.yml"), []byte(manifest), 0600); err != nil {
		t.Fatal(err)
	}
	_, err := loadModuleManifest(dir, "example")
	if err == nil || !strings.Contains(err.Error(), "field before not found") {
		t.Fatalf("error = %v, want strict rejection of dependencies.before", err)
	}
}

func looksLikeEnvParam(key string) bool {
	if strings.HasPrefix(key, "global.") {
		key = strings.TrimPrefix(key, "global.")
	}
	if key == "IPv4" || key == "IPv6" {
		return true
	}
	return strings.ToUpper(key) == key && strings.Contains(key, "_")
}

func writeModuleConfigManifest(t *testing.T, configBlock string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "example")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	manifest := `api_version: anas.module/v1
kind: Module
name: example
version: 1.0.0
revision: 1
status: release
abi:
  supports: [anas.module-hook/v1]
runtime:
  type: builtin
config:
` + configBlock
	if err := os.WriteFile(filepath.Join(dir, "module.yml"), []byte(manifest), 0600); err != nil {
		t.Fatal(err)
	}
	return dir
}
