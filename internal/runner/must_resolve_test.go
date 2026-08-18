package runner

import (
	"strings"
	"testing"

	"github.com/anas-project/ANAS/internal/configschema"
)

func TestCalculateChecksMustResolveAfterHookPatch(t *testing.T) {
	for _, test := range []struct {
		name       string
		hookOutput string
		wantError  bool
	}{
		{
			name:       "hook resolves value",
			hookOutput: `{"env":{"DEMO_GENERATED":"ready"}}`,
		},
		{
			name:       "hook leaves value missing",
			hookOutput: `{}`,
			wantError:  true,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			moduleDir := t.TempDir()
			mod := Module{
				Name: "demo", EnvPrefix: "DEMO", SourceDir: moduleDir,
				InputRequired: []string{"DEMO_INPUT"},
				MustResolve:   []string{"DEMO_GENERATED"},
				Hook: HookConfig{Command: []string{
					"sh", "-c", `printf '%s' "$1"`, "anas-test-hook", test.hookOutput,
				}},
			}
			a := &app{
				base: t.TempDir(), reg: map[string]Module{"demo": mod}, order: []string{"demo"},
				env: map[string]string{"DEMO_INPUT": "present"}, envOwner: map[string]string{},
				secrets: &secretStore{values: map[string]string{}, metadata: map[string]secretMetadata{}},
			}
			seedCalculateGlobalRequirements(a.env)

			err := a.calculate()
			if !test.wantError {
				if err != nil {
					t.Fatal(err)
				}
				if got := a.env["DEMO_GENERATED"]; got != "ready" {
					t.Fatalf("generated value = %q", got)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), "DEMO_GENERATED") {
				t.Fatalf("error = %v, want missing must_resolve value", err)
			}
		})
	}
}

func TestCalculateChecksInputRequiredBeforeHook(t *testing.T) {
	moduleDir := t.TempDir()
	mod := Module{
		Name: "demo", EnvPrefix: "DEMO", SourceDir: moduleDir,
		InputRequired: []string{"DEMO_INPUT"},
		Hook: HookConfig{Command: []string{
			"sh", "-c", `printf '%s' '{"env":{"DEMO_INPUT":"too-late"}}'`,
		}},
	}
	a := &app{
		base: t.TempDir(), reg: map[string]Module{"demo": mod}, order: []string{"demo"},
		env: map[string]string{}, envOwner: map[string]string{},
		secrets: &secretStore{values: map[string]string{}, metadata: map[string]secretMetadata{}},
	}
	seedCalculateGlobalRequirements(a.env)
	if err := a.calculate(); err == nil || !strings.Contains(err.Error(), "DEMO_INPUT") {
		t.Fatalf("error = %v, want pre-hook input-required rejection", err)
	}
}

func TestCalculateKeepsLegacyRequiredBeforeHook(t *testing.T) {
	moduleDir := t.TempDir()
	mod := Module{
		Name: "demo", EnvPrefix: "DEMO", SourceDir: moduleDir,
		Required: []string{"DEMO_LEGACY"},
		Hook: HookConfig{Command: []string{
			"sh", "-c", `printf '%s' '{"env":{"DEMO_LEGACY":"too-late"}}'`,
		}},
	}
	a := &app{
		base: t.TempDir(), reg: map[string]Module{"demo": mod}, order: []string{"demo"},
		env: map[string]string{}, envOwner: map[string]string{},
		secrets: &secretStore{values: map[string]string{}, metadata: map[string]secretMetadata{}},
	}
	seedCalculateGlobalRequirements(a.env)
	if err := a.calculate(); err == nil || !strings.Contains(err.Error(), "DEMO_LEGACY") {
		t.Fatalf("error = %v, want legacy required rejection before Hook", err)
	}
}

func TestCalculateNormalizesAndValidatesRuntimeSources(t *testing.T) {
	minimum := 1
	for _, test := range []struct {
		name, value, wantValue, wantError string
	}{
		{name: "canonicalizes enum", value: " FAST ", wantValue: "fast"},
		{name: "rejects constraint", value: "0", wantError: "at least 1"},
	} {
		t.Run(test.name, func(t *testing.T) {
			types := map[string]ParamType{}
			parameter := "mode"
			if test.wantError == "" {
				types[parameter] = ParamType{Kind: "enum", Enum: []string{"safe", "fast"}, DefaultSource: configschema.DefaultSourceRuntime}
			} else {
				parameter = "port"
				types[parameter] = ParamType{Kind: "int", Constraints: configschema.Constraints{Minimum: &minimum}, DefaultSource: configschema.DefaultSourceRuntime}
			}
			key := "DEMO_" + strings.ToUpper(parameter)
			mod := Module{
				Name: "demo", EnvPrefix: "DEMO", Parameters: []string{parameter},
				Required: []string{key}, Types: types,
			}
			a := &app{
				base: t.TempDir(), reg: map[string]Module{"demo": mod}, order: []string{"demo"},
				env: map[string]string{key: test.value}, envOwner: map[string]string{},
				secrets: &secretStore{values: map[string]string{}, metadata: map[string]secretMetadata{}},
			}
			seedCalculateGlobalRequirements(a.env)
			err := a.calculate()
			if test.wantError != "" {
				if err == nil || !strings.Contains(err.Error(), test.wantError) {
					t.Fatalf("error = %v, want %q", err, test.wantError)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got := a.env[key]; got != test.wantValue {
				t.Fatalf("runtime source = %q, want canonical %q", got, test.wantValue)
			}
		})
	}
}

func TestCalculateNormalizesHookPatch(t *testing.T) {
	mod := Module{
		Name: "demo", EnvPrefix: "DEMO", Parameters: []string{"mode"},
		MustResolve: []string{"DEMO_MODE"},
		Types: map[string]ParamType{
			"mode": {Kind: "enum", Enum: []string{"safe", "fast"}, DefaultSource: configschema.DefaultSourceGenerated},
		},
		Hook: HookConfig{Command: []string{
			"sh", "-c", `printf '%s' '{"env":{"DEMO_MODE":" FAST "}}'`,
		}},
	}
	a := &app{
		base: t.TempDir(), reg: map[string]Module{"demo": mod}, order: []string{"demo"},
		env: map[string]string{}, envOwner: map[string]string{},
		secrets: &secretStore{values: map[string]string{}, metadata: map[string]secretMetadata{}},
	}
	seedCalculateGlobalRequirements(a.env)
	if err := a.calculate(); err != nil {
		t.Fatal(err)
	}
	if got := a.env["DEMO_MODE"]; got != "fast" {
		t.Fatalf("Hook patch = %q, want canonical fast", got)
	}
}

func TestCalculateRedactsSensitiveHookConstraintError(t *testing.T) {
	minimumLength := 30
	secret := "do-not-print-this-secret"
	mod := Module{
		Name: "demo", EnvPrefix: "DEMO", Parameters: []string{"secret"},
		Types: map[string]ParamType{
			"secret": {Kind: "string", Constraints: configschema.Constraints{MinLength: &minimumLength}, DefaultSource: configschema.DefaultSourceGenerated},
		},
		Changes: map[string]ChangePolicy{"secret": {Sensitive: true}},
		Hook: HookConfig{Command: []string{
			"sh", "-c", `printf '%s' "$1"`, "anas-test-hook", `{"env":{"DEMO_SECRET":"` + secret + `"}}`,
		}},
	}
	a := &app{
		base: t.TempDir(), reg: map[string]Module{"demo": mod}, order: []string{"demo"},
		env: map[string]string{}, envOwner: map[string]string{},
		secrets: &secretStore{values: map[string]string{}, metadata: map[string]secretMetadata{}},
	}
	seedCalculateGlobalRequirements(a.env)
	err := a.calculate()
	if err == nil || !strings.Contains(err.Error(), "does not satisfy its declared type or constraints") {
		t.Fatalf("error = %v, want redacted constraint rejection", err)
	}
	if strings.Contains(err.Error(), secret) {
		t.Fatalf("sensitive value leaked in error: %v", err)
	}
}

func seedCalculateGlobalRequirements(values map[string]string) {
	for key, value := range map[string]string{
		"BASE_DOMAIN": "example.test", "EMAIL": "admin@example.test", "TZ": "UTC",
		"DEFAULT_LANGUAGE": "en", "DEFAULT_LOCALE": "en-US", "HOST_IP": "192.0.2.10",
	} {
		values[key] = value
	}
}
