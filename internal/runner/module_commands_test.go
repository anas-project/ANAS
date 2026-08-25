package runner

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/anas-project/ANAS/internal/configschema"
	"github.com/anas-project/ANAS/internal/deployment"
)

func TestModuleCommandManifestNormalizesAndRejectsUnsafeDeclarations(t *testing.T) {
	valid := `api_version: anas.module/v1
kind: Module
name: example
version: 1.0.0
revision: 1
status: developing
abi:
  supports: [anas.module-hook/v1, anas.module-command/v1]
runtime:
  type: builtin
management:
  command_executor:
    command: [go, run, ./command]
  commands:
    - id: doctor
      title: Inspect service
      description: Inspect the configured service without changing it.
      handler: doctor
      mode: query
      risk: normal
      runtime_state: any
      lock: module_read
      timeout_seconds: 20
      cancellable: true
      parameters:
        - name: verbose
          title: Verbose
          description: Include additional safe diagnostics.
          type: bool
          default: false
      env: [EXAMPLE_ENDPOINT]
      secrets: [EXAMPLE_MAINTENANCE_KEY]
`
	load := func(t *testing.T, manifest string) (Module, error) {
		t.Helper()
		dir := filepath.Join(t.TempDir(), "example")
		if err := os.MkdirAll(filepath.Join(dir, "command"), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "command", "main.go"), []byte("package main\nfunc main() {}\n"), 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "module.yml"), []byte(manifest), 0600); err != nil {
			t.Fatal(err)
		}
		return loadModuleManifest(dir, "example")
	}

	mod, err := load(t, valid)
	if err != nil {
		t.Fatal(err)
	}
	if len(mod.Commands) != 1 || mod.Commands[0].ID != "doctor" || mod.Commands[0].Digest == "" {
		t.Fatalf("commands = %#v", mod.Commands)
	}
	parameter := mod.Commands[0].Parameters[0]
	if parameter.Type.Kind != "bool" || parameter.Default != false || mod.Commands[0].Cancellable != "true" {
		t.Fatalf("normalized parameter/command = %#v / %#v", parameter, mod.Commands[0])
	}
	if digest, err := deployment.CommandDigest(mod.Commands[0]); err != nil || digest != mod.Commands[0].Digest {
		t.Fatalf("command digest = %q, %v", digest, err)
	}

	tests := []struct {
		name, old, replacement, want string
	}{
		{"abi", "[anas.module-hook/v1, anas.module-command/v1]", "[anas.module-hook/v1]", "without supporting runner ABI"},
		{"duplicate id", "      secrets: [EXAMPLE_MAINTENANCE_KEY]\n", "      secrets: [EXAMPLE_MAINTENANCE_KEY]\n    - id: doctor\n      title: Duplicate\n      description: Duplicate command.\n      handler: duplicate\n      mode: query\n      risk: normal\n      runtime_state: any\n      lock: module_read\n      timeout_seconds: 10\n      cancellable: false\n", "more than once"},
		{"change read lock", "      mode: query\n", "      mode: change\n", "must use module_write or workspace_write"},
		{"required default", "          default: false\n", "          required: true\n          default: false\n", "cannot declare a default"},
		{"outside input", "      env: [EXAMPLE_ENDPOINT]\n", "      env: [OTHER_ENDPOINT]\n", "outside the Module namespace"},
		{"env secret overlap", "      secrets: [EXAMPLE_MAINTENANCE_KEY]\n", "      secrets: [EXAMPLE_ENDPOINT]\n", "both env and secret"},
		{"invalid timeout", "      timeout_seconds: 20\n", "      timeout_seconds: 0\n", "between 1 and 3600"},
		{"arbitrary executor", "    command: [go, run, ./command]\n", "    command: [/bin/sh, -c, whoami]\n", "one fixed relative executable"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := load(t, strings.Replace(valid, test.old, test.replacement, 1))
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestModuleCommandsCLIDiscoversOnlyPublicFrozenMetadata(t *testing.T) {
	workspace := newWorkspace(t)
	base := stateDir(workspace)
	id := "dep-commands"
	moduleDir := filepath.Join(base, "deployments", id, "modules", "demo")
	if err := os.MkdirAll(moduleDir, 0700); err != nil {
		t.Fatal(err)
	}
	executor := filepath.Join(moduleDir, commandBinaryName)
	if err := os.WriteFile(executor, []byte("fixture executor"), 0555); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(moduleDir, ".env"), []byte("DEMO_ENDPOINT=https://incus.test:8443\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(base, "secrets.yml"), []byte("api_version: anas.secrets/v2\nsecrets:\n  DEMO_MAINTENANCE_KEY:\n    value: fixture-secret\n"), 0600); err != nil {
		t.Fatal(err)
	}
	executorDigest, err := fileDigest(executor)
	if err != nil {
		t.Fatal(err)
	}
	command := deployment.ModuleCommand{
		ID: "doctor", Title: "Inspect service", Description: "Read safe service diagnostics.", Handler: "internal_doctor",
		Mode: "query", Risk: "normal", RuntimeState: "running", Lock: "module_read", TimeoutSeconds: 10,
		Cancellable: "true", Env: []string{"DEMO_ENDPOINT"}, Secrets: []string{"DEMO_MAINTENANCE_KEY"},
	}
	command.Digest, err = deployment.CommandDigest(command)
	if err != nil {
		t.Fatal(err)
	}
	manifest := deployment.Manifest{
		APIVersion: deployment.ManifestAPIVersion, ID: id, ModuleOrder: []string{"demo"},
		Modules: map[string]deployment.Module{
			"demo": {
				Name: "demo", Version: "1.2.3", Revision: 4, ArtifactDeployment: id,
				CommandExecutor: deployment.CommandExecutor{Command: []string{"./" + commandBinaryName}, Digest: executorDigest},
				Commands:        []deployment.ModuleCommand{command},
			},
		},
	}
	if err := writeYAMLAtomic(filepath.Join(base, "deployments", id, "deployment.yml"), manifest, 0600); err != nil {
		t.Fatal(err)
	}
	if err := saveActiveState(base, &activeDeploymentState{ActiveDeployment: id, RuntimeStatus: "running"}); err != nil {
		t.Fatal(err)
	}

	stdout, _, exit := capture(t, "module", "commands", "demo", "-w", workspace, "--json")
	if exit != 0 {
		t.Fatalf("module commands exit %d: %s", exit, stdout)
	}
	document := requireSingleDocument(t, "module commands", stdout)
	commands, ok := document["commands"].([]any)
	if !ok || len(commands) != 1 || !strings.Contains(stdout, `"available": true`) {
		t.Fatalf("module commands output = %s", stdout)
	}
	for _, private := range []string{"internal_doctor", "DEMO_ENDPOINT", "DEMO_MAINTENANCE_KEY", commandBinaryName, workspace} {
		if strings.Contains(stdout, private) {
			t.Fatalf("module commands exposed %q: %s", private, stdout)
		}
	}
}

func TestModuleInvokeCLIUsesTypedApplicationServiceAndKeepsProtocolPrivate(t *testing.T) {
	workspace := newWorkspace(t)
	base := stateDir(workspace)
	id := "dep-command-invoke"
	moduleDir := filepath.Join(base, "deployments", id, "modules", "demo")
	if err := os.MkdirAll(moduleDir, 0700); err != nil {
		t.Fatal(err)
	}
	executor := filepath.Join(moduleDir, commandBinaryName)
	executorBody := `#!/bin/sh
read request
case "$request" in
  *'"verbose":true'*'"DEMO_ENDPOINT":"https://incus.test:8443"'*'"DEMO_MAINTENANCE_KEY":"fixture-secret"'*) ;;
  *) printf '%s\n' '{"type":"unknown-request"}'; exit 0 ;;
esac
printf '%s\n' '{"type":"progress","phase":"inspect","current":1,"total":1,"unit":"checks"}'
printf '%s\n' '{"type":"warning","code":"fixture_warning","message":"A public warning."}'
printf '%s\n' '{"type":"result","changed":false,"result":{"checks":2}}'
printf '%s' 'fixture-secret' >&2
`
	if err := os.WriteFile(executor, []byte(executorBody), 0555); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(moduleDir, ".env"), []byte("DEMO_ENDPOINT=https://incus.test:8443\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(base, "secrets.yml"), []byte("api_version: anas.secrets/v2\nsecrets:\n  DEMO_MAINTENANCE_KEY:\n    value: fixture-secret\n"), 0600); err != nil {
		t.Fatal(err)
	}
	executorDigest, err := fileDigest(executor)
	if err != nil {
		t.Fatal(err)
	}
	query := deployment.ModuleCommand{
		ID: "doctor", Title: "Inspect service", Description: "Read safe service diagnostics.", Handler: "internal_doctor",
		Mode: "query", Risk: "normal", RuntimeState: "running", Lock: "module_read", TimeoutSeconds: 10,
		Cancellable: "true", Env: []string{"DEMO_ENDPOINT"}, Secrets: []string{"DEMO_MAINTENANCE_KEY"},
		Parameters: []deployment.ModuleCommandParameter{{
			Name: "verbose", Title: "Verbose", Description: "Include safe details.",
			Type: configschema.Parameter{Kind: "bool"}, Required: true,
		}},
	}
	query.Digest, err = deployment.CommandDigest(query)
	if err != nil {
		t.Fatal(err)
	}
	destructive := deployment.ModuleCommand{
		ID: "repair", Title: "Repair service", Description: "Repair the managed service.", Handler: "repair",
		Mode: "change", Risk: "destructive", RuntimeState: "running", Lock: "module_write", TimeoutSeconds: 10,
		Cancellable: "false", Env: []string{"DEMO_ENDPOINT"}, Secrets: []string{"DEMO_MAINTENANCE_KEY"},
		Parameters: query.Parameters,
	}
	destructive.Digest, err = deployment.CommandDigest(destructive)
	if err != nil {
		t.Fatal(err)
	}
	manifest := deployment.Manifest{
		APIVersion: deployment.ManifestAPIVersion, ID: id, ModuleOrder: []string{"demo"},
		Modules: map[string]deployment.Module{"demo": {
			Name: "demo", Version: "1.2.3", Revision: 4, ArtifactDeployment: id,
			CommandExecutor: deployment.CommandExecutor{Command: []string{"./" + commandBinaryName}, Digest: executorDigest},
			Commands:        []deployment.ModuleCommand{query, destructive},
		}},
	}
	if err := writeYAMLAtomic(filepath.Join(base, "deployments", id, "deployment.yml"), manifest, 0600); err != nil {
		t.Fatal(err)
	}
	if err := saveActiveState(base, &activeDeploymentState{ActiveDeployment: id, RuntimeStatus: "running"}); err != nil {
		t.Fatal(err)
	}

	stdout, stderr, exit := capture(t, "module", "invoke", "demo", "doctor", "-w", workspace, "--param", "verbose=TRUE", "--json")
	if exit != 0 {
		t.Fatalf("module invoke exit %d: stdout=%s stderr=%s", exit, stdout, stderr)
	}
	document := requireSingleDocument(t, "module invoke", stdout)
	if document["changed"] != false || document["module"] != "demo" || document["command"] != "doctor" {
		t.Fatalf("module invoke document = %#v", document)
	}
	if strings.Contains(stdout+stderr, "fixture-secret") || strings.Contains(stdout, "internal_doctor") || strings.Contains(stdout, commandBinaryName) {
		t.Fatalf("module invoke leaked private executor data: stdout=%s stderr=%s", stdout, stderr)
	}
	for _, record := range strings.Split(strings.TrimSpace(stderr), "\n") {
		if !strings.HasPrefix(record, "{") {
			t.Fatalf("stderr contains raw executor output: %q", stderr)
		}
	}
	if !strings.Contains(stderr, `"type":"progress"`) || !strings.Contains(stderr, `"type":"warning"`) {
		t.Fatalf("validated events missing from stderr: %s", stderr)
	}

	stdout, _, exit = capture(t, "module", "invoke", "demo", "doctor", "-w", workspace,
		"--param", "verbose=true", "--param", "verbose=false", "--json")
	if exit != exitUsage {
		t.Fatalf("duplicate parameter exit=%d stdout=%s", exit, stdout)
	}

	stdout, _, exit = capture(t, "module", "invoke", "demo", "repair", "-w", workspace,
		"--param", "verbose=true", "--json")
	if exit != exitConfirmation {
		t.Fatalf("destructive command exit=%d stdout=%s", exit, stdout)
	}
	failure := requireFailureDocument(t, "module invoke confirmation", stdout)["error"].(map[string]any)
	if failure["code"] != "confirmation_required" {
		t.Fatalf("confirmation error = %#v", failure)
	}
}
