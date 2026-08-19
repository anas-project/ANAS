package runner

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/anas-project/ANAS/internal/config"
)

const moduleValidationHookHelperMarker = "module-validation-hook-helper"

func TestModuleValidationLegacyHookDoesNotRunValidate(t *testing.T) {
	called := filepath.Join(t.TempDir(), "called")
	mod := Module{
		Name:      "legacy",
		EnvPrefix: "LEGACY",
		SourceDir: t.TempDir(),
		Hook: HookConfig{Command: moduleValidationHookHelperCommand(
			"capture", called,
		)},
	}
	a := moduleValidationTestApp([]string{mod.Name}, map[string]Module{mod.Name: mod})

	if err := a.validateModules(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(called); !os.IsNotExist(err) {
		t.Fatalf("legacy Hook unexpectedly received validate: %v", err)
	}
}

func TestModuleValidationRunsOptedInHooksInEffectiveOrder(t *testing.T) {
	orderFile := filepath.Join(t.TempDir(), "order")
	reg := map[string]Module{}
	for _, name := range []string{"provider", "consumer"} {
		reg[name] = Module{
			Name: name, EnvPrefix: strings.ToUpper(name), SourceDir: t.TempDir(),
			Hook: HookConfig{
				Command: moduleValidationHookHelperCommand("append-order", orderFile),
				Phases:  []string{"validate"},
			},
		}
	}
	a := moduleValidationTestApp([]string{"provider", "consumer"}, reg)
	a.deps = map[string][]string{"consumer": {"provider"}}

	if err := a.validateModules(); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(orderFile)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := strings.Fields(string(body)), []string{"provider", "consumer"}; fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("validation order = %v, want %v", got, want)
	}
}

func TestModuleValidationOmitsSecretsAndSensitiveEnvironment(t *testing.T) {
	requestFile := filepath.Join(t.TempDir(), "request.json")
	mod := Module{
		Name:       "demo",
		EnvPrefix:  "DEMO",
		SourceDir:  t.TempDir(),
		Parameters: []string{"password"},
		Changes: map[string]ChangePolicy{
			"password": {Sensitive: true},
		},
		Consumes: []string{"CONFIG_SECRET", "STORE_SECRET"},
		Hook: HookConfig{
			Command: moduleValidationHookHelperCommand("capture", requestFile),
			Phases:  []string{"validate"},
		},
	}
	a := moduleValidationTestApp([]string{mod.Name}, map[string]Module{mod.Name: mod})
	a.cfg = &config.File{Secrets: map[string]any{"CONFIG_SECRET": "config-secret"}}
	a.env = map[string]string{
		"GLOBAL_VISIBLE":    "global-visible",
		"DEMO_VISIBLE":      "module-visible",
		"DEMO_PASSWORD":     "manifest-secret",
		"CONFIG_SECRET":     "config-secret",
		"DEMO_CONFIG_ALIAS": "config-secret",
		"STORE_SECRET":      "store-secret",
		"DEMO_STORE_ALIAS":  "store-secret",
	}
	a.envOwner = map[string]string{
		"GLOBAL_VISIBLE":    globalScope,
		"DEMO_VISIBLE":      mod.Name,
		"DEMO_PASSWORD":     mod.Name,
		"CONFIG_SECRET":     config.OwnerUserSecret,
		"DEMO_CONFIG_ALIAS": mod.Name,
		"STORE_SECRET":      config.OwnerUserSecret,
		"DEMO_STORE_ALIAS":  mod.Name,
	}
	a.secrets = &secretStore{
		values: map[string]string{"STORE_SECRET": "  store-secret\n"},
		metadata: map[string]secretMetadata{
			"STORE_SECRET": {Owner: mod.Name, Kind: "lifecycle_managed", Provenance: "test"},
		},
	}

	if err := a.validateModules(); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(requestFile)
	if err != nil {
		t.Fatal(err)
	}
	var req hookRequest
	if err := json.Unmarshal(body, &req); err != nil {
		t.Fatal(err)
	}
	if req.Phase != "validate" || req.Module != mod.Name {
		t.Fatalf("validation request identity = phase %q module %q", req.Phase, req.Module)
	}
	if len(req.Secrets) != 0 {
		t.Fatalf("validation request received Secrets: %#v", req.Secrets)
	}
	for _, key := range []string{
		"DEMO_PASSWORD", "CONFIG_SECRET", "DEMO_CONFIG_ALIAS",
		"STORE_SECRET", "DEMO_STORE_ALIAS",
	} {
		if _, leaked := req.Env[key]; leaked {
			t.Errorf("validation request leaked sensitive env %s", key)
		}
	}
	for key, want := range map[string]string{
		"GLOBAL_VISIBLE": "global-visible",
		"DEMO_VISIBLE":   "module-visible",
	} {
		if got := req.Env[key]; got != want {
			t.Errorf("validation request env %s = %q, want %q", key, got, want)
		}
	}
}

func TestModuleValidationRecomputesSensitiveAliasesAfterNormalization(t *testing.T) {
	requestFile := filepath.Join(t.TempDir(), "request.json")
	mod := Module{
		Name: "demo", EnvPrefix: "DEMO", SourceDir: t.TempDir(),
		Parameters: []string{"password"},
		Types: map[string]ParamType{
			"password": {Kind: "enum", Enum: []string{"classified"}},
		},
		Changes: map[string]ChangePolicy{"password": {Sensitive: true}},
		Hook: HookConfig{
			Command: moduleValidationHookHelperCommand("capture", requestFile),
			Phases:  []string{"validate"},
		},
	}
	a := moduleValidationTestApp([]string{mod.Name}, map[string]Module{mod.Name: mod})
	a.env = map[string]string{
		"DEMO_PASSWORD": " Classified ",
		"DEMO_ALIAS":    "classified",
		"DEMO_VISIBLE":  "visible",
	}
	a.envOwner = map[string]string{
		"DEMO_PASSWORD": mod.Name,
		"DEMO_ALIAS":    mod.Name,
		"DEMO_VISIBLE":  mod.Name,
	}

	if err := a.validateModules(); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(requestFile)
	if err != nil {
		t.Fatal(err)
	}
	var req hookRequest
	if err := json.Unmarshal(body, &req); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"DEMO_PASSWORD", "DEMO_ALIAS"} {
		if _, leaked := req.Env[key]; leaked {
			t.Errorf("normalized sensitive alias %s leaked to validation Hook", key)
		}
	}
	if req.Env["DEMO_VISIBLE"] != "visible" {
		t.Fatalf("ordinary module env missing after sensitive filtering: %#v", req.Env)
	}
}

func TestModuleValidationRejectsMutationResponses(t *testing.T) {
	for _, test := range []struct {
		name, response, field string
	}{
		{name: "env", response: `{"env":{"DEMO_VALUE":"changed"}}`, field: "env"},
		{name: "secrets", response: `{"secrets":{"DEMO_SECRET":"created"}}`, field: "secrets"},
		{name: "files", response: `{"files":{"config.yml":"changed"}}`, field: "files"},
		{name: "runtime files", response: `{"runtime_files":{"state.yml":"changed"}}`, field: "runtime_files"},
		{name: "services", response: `{"disable_services":["web"]}`, field: "disable_services"},
		{name: "docker copies", response: `{"docker_copies":[{"source":"a","container":"b","destination":"c"}]}`, field: "docker_copies"},
		{name: "internal env", response: `{"internal_env":["DEMO_PRIVATE"]}`, field: "internal_env"},
	} {
		t.Run(test.name, func(t *testing.T) {
			mod := Module{
				Name: "demo", EnvPrefix: "DEMO", SourceDir: t.TempDir(),
				Hook: HookConfig{
					Command: moduleValidationHookHelperCommand("respond", test.response),
					Phases:  []string{"validate"},
				},
			}
			a := moduleValidationTestApp([]string{mod.Name}, map[string]Module{mod.Name: mod})

			err := a.validateModules()
			if err == nil || !strings.Contains(err.Error(), "forbidden mutation fields") ||
				!strings.Contains(err.Error(), test.field) {
				t.Fatalf("mutation response error = %v, want field %s", err, test.field)
			}
			if _, mutated := a.env["DEMO_VALUE"]; mutated {
				t.Fatal("rejected validation response mutated deployment env")
			}
		})
	}
}

func TestValidationHookBuildEnvironmentIsIsolated(t *testing.T) {
	t.Setenv("PARENT_PROCESS_TOKEN", "must-not-leak")
	base := t.TempDir()
	env := validationHookBuildEnv(base, filepath.Join(base, "cache"), "https://proxy.example")
	joined := strings.Join(env, "\n")
	if strings.Contains(joined, "PARENT_PROCESS_TOKEN") || strings.Contains(joined, "must-not-leak") {
		t.Fatalf("validation build inherited parent secret: %s", joined)
	}
	for _, want := range []string{
		"GOCACHE=" + filepath.Join(base, "cache"),
		"HOME=" + filepath.Join(base, "home"),
		"GOMODCACHE=" + filepath.Join(base, "go-module-cache"),
		"GOPATH=" + filepath.Join(base, "go-path"),
		"GOENV=off",
		"GOPROXY=https://proxy.example",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("validation build env missing %q: %s", want, joined)
		}
	}
}

func TestValidationHookRuntimeSupportsVersionManagerShimWithoutLeakingTokens(t *testing.T) {
	binDir := t.TempDir()
	hostHome := t.TempDir()
	shim := filepath.Join(binDir, "demo-validation-hook")
	if err := os.WriteFile(shim, []byte("#!/bin/sh\n[ \"$HOME\" = \""+hostHome+"\" ] || exit 42\nprintf '{}\\n'\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir)
	t.Setenv("HOME", hostHome)
	t.Setenv("PARENT_PROCESS_TOKEN", "must-not-leak")
	mod := Module{
		Name: "demo", SourceDir: t.TempDir(),
		Hook: HookConfig{Command: []string{"demo-validation-hook"}, Phases: []string{"validate"}},
	}
	a := moduleValidationTestApp([]string{mod.Name}, map[string]Module{mod.Name: mod})
	if err := a.validateModules(); err != nil {
		t.Fatal(err)
	}
	env := strings.Join(validationHookProcessEnv(filepath.Join(t.TempDir(), "cache")), "\n")
	if strings.Contains(env, "PARENT_PROCESS_TOKEN") || strings.Contains(env, "must-not-leak") {
		t.Fatalf("validation Hook runtime inherited parent token: %s", env)
	}
}

func TestValidationHookBuildUsesConcreteToolchainWithPrivateHome(t *testing.T) {
	source := t.TempDir()
	if err := os.MkdirAll(filepath.Join(source, "hook"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "go.mod"), []byte("module validation-hook-test\n\ngo 1.22\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "hook", "main.go"), []byte("package main\nfunc main() {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	// A deliberately unusable HOME proves the build does not depend on the
	// operator's asdf/pyenv state or user Go configuration.
	t.Setenv("HOME", filepath.Join(t.TempDir(), "unusable-parent-home"))
	a := &app{base: t.TempDir(), env: map[string]string{}, validationBuild: true}
	mod := Module{Name: "demo", SourceDir: source}
	bin, err := a.ensureHookBinary(mod, "./hook")
	if err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(bin); err != nil || info.Mode()&0o111 == 0 {
		t.Fatalf("validation Hook binary was not built at %s: info=%v err=%v", bin, info, err)
	}
}

func TestValidationGoToolchainDiscoveryDoesNotDependOnBuildGOROOT(t *testing.T) {
	root := t.TempDir()
	binDir := filepath.Join(root, "bin")
	if err := os.MkdirAll(binDir, 0o700); err != nil {
		t.Fatal(err)
	}
	goBinary := filepath.Join(binDir, "go")
	if err := os.WriteFile(goBinary, []byte("#!/bin/sh\nprintf '%s\\n' '"+root+"'\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir)
	got, err := resolveValidationGoBinary(filepath.Join(t.TempDir(), "missing-build-goroot"))
	if err != nil {
		t.Fatal(err)
	}
	if got != goBinary {
		t.Fatalf("resolved validation Go binary = %q, want %q", got, goBinary)
	}
}

func TestNormalizeHookConfigRejectsUnknownAndDuplicatePhases(t *testing.T) {
	for _, test := range []struct {
		name   string
		phases []string
		want   string
	}{
		{name: "unknown", phases: []string{"validate", "future"}, want: "unknown hook phase"},
		{name: "normalized duplicate", phases: []string{"validate", " Validate "}, want: "more than once"},
		{name: "explicit empty", phases: []string{}, want: "empty hook phase list"},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := normalizeHookConfig("demo", HookConfig{Command: []string{"hook"}, Phases: test.phases})
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("normalizeHookConfig error = %v, want %q", err, test.want)
			}
		})
	}
}

func moduleValidationTestApp(order []string, reg map[string]Module) *app {
	return &app{
		reg: reg, order: order,
		env: map[string]string{}, envOwner: map[string]string{},
		deps: map[string][]string{}, resolvedBindings: map[string]map[string]string{},
	}
}

func moduleValidationHookHelperCommand(args ...string) []string {
	command := []string{
		os.Args[0], "-test.run=^TestModuleValidationHookHelper$", "--",
		moduleValidationHookHelperMarker,
	}
	return append(command, args...)
}

// TestModuleValidationHookHelper runs only in a subprocess launched as a Hook.
// It exits directly so the testing package cannot append PASS output after the
// Hook's single JSON response.
func TestModuleValidationHookHelper(t *testing.T) {
	marker := -1
	for i, arg := range os.Args {
		if arg == moduleValidationHookHelperMarker {
			marker = i
			break
		}
	}
	if marker < 0 {
		return
	}
	args := os.Args[marker+1:]
	if len(args) == 0 {
		moduleValidationHookHelperFail("missing helper action")
	}
	body, err := io.ReadAll(os.Stdin)
	if err != nil {
		moduleValidationHookHelperFail("read request: %v", err)
	}
	var req hookRequest
	if err := json.Unmarshal(body, &req); err != nil {
		moduleValidationHookHelperFail("decode request: %v", err)
	}
	if req.Phase != "validate" {
		moduleValidationHookHelperFail("phase = %q, want validate", req.Phase)
	}

	switch args[0] {
	case "capture":
		if len(args) != 2 {
			moduleValidationHookHelperFail("capture requires a path")
		}
		if err := os.WriteFile(args[1], body, 0600); err != nil {
			moduleValidationHookHelperFail("capture request: %v", err)
		}
		fmt.Print(`{}`)
	case "append-order":
		if len(args) != 2 {
			moduleValidationHookHelperFail("append-order requires a path")
		}
		file, err := os.OpenFile(args[1], os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
		if err != nil {
			moduleValidationHookHelperFail("open order file: %v", err)
		}
		if _, err := fmt.Fprintln(file, req.Module); err != nil {
			_ = file.Close()
			moduleValidationHookHelperFail("append order: %v", err)
		}
		if err := file.Close(); err != nil {
			moduleValidationHookHelperFail("close order file: %v", err)
		}
		fmt.Print(`{}`)
	case "respond":
		if len(args) != 2 {
			moduleValidationHookHelperFail("respond requires JSON")
		}
		fmt.Print(args[1])
	case "reject-env":
		if len(args) != 3 {
			moduleValidationHookHelperFail("reject-env requires a key and value")
		}
		if req.Env[args[1]] == args[2] {
			moduleValidationHookHelperFail("%s is not allowed", args[1])
		}
		fmt.Print(`{}`)
	default:
		moduleValidationHookHelperFail("unknown helper action %q", args[0])
	}
	os.Exit(0)
}

func moduleValidationHookHelperFail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(2)
}
