package application

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/anas-project/ANAS/internal/configschema"
	"github.com/anas-project/ANAS/internal/deployment"
	"gopkg.in/yaml.v3"
)

type recordingModuleCommandExecutor struct {
	execution moduleCommandExecution
	result    moduleCommandExecutionResult
	err       error
}

func (executor *recordingModuleCommandExecutor) Execute(_ context.Context, execution moduleCommandExecution, _ EventSink) (moduleCommandExecutionResult, error) {
	executor.execution = execution
	return executor.result, executor.err
}

type recordingModuleCommandEvents struct {
	progress []ProgressEvent
	warnings []WarningEvent
}

func (events *recordingModuleCommandEvents) Progress(event ProgressEvent) {
	events.progress = append(events.progress, event)
}
func (events *recordingModuleCommandEvents) Warning(event WarningEvent) {
	events.warnings = append(events.warnings, event)
}
func (*recordingModuleCommandEvents) Log(LogEvent) {}

func TestInvokeModuleCommandNormalizesInputsAndRequiresDestructiveConfirmation(t *testing.T) {
	workspace, command := writeModuleCommandApplicationFixture(t, `#!/bin/sh
printf '%s\n' '{"type":"result","changed":false,"result":{}}'
`)
	executor := &recordingModuleCommandExecutor{result: moduleCommandExecutionResult{
		Changed: false, Result: map[string]any{"state": "ready"},
	}}
	service := NewService(workspace)
	service.executor = executor

	prepared, err := service.PrepareModuleCommand(context.Background(), PrepareModuleCommandRequest{
		Module: "demo", Command: "repair", Parameters: map[string]any{"enabled": "TRUE"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if prepared.Command.Digest != command.Digest || prepared.Parameters["enabled"] != true || prepared.Parameters["attempts"] != 3 {
		t.Fatalf("prepared = %#v", prepared)
	}

	_, err = service.InvokeModuleCommand(context.Background(), InvokeModuleCommandRequest{
		Module: "demo", Command: "repair", Parameters: prepared.Parameters, CommandDigest: prepared.Command.Digest,
	})
	appErr := requireApplicationError(t, err)
	if appErr.Code != "module_command_confirmation_required" {
		t.Fatalf("confirmation error = %#v", appErr)
	}

	result, err := service.InvokeModuleCommand(context.Background(), InvokeModuleCommandRequest{
		Module: "demo", Command: "repair", Parameters: prepared.Parameters,
		CommandDigest: prepared.Command.Digest, Confirmed: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Changed || result.Result["state"] != "ready" {
		t.Fatalf("result = %#v", result)
	}
	if !reflect.DeepEqual(executor.execution.Parameters, map[string]any{"attempts": 3, "enabled": true}) {
		t.Fatalf("parameters = %#v", executor.execution.Parameters)
	}
	if !reflect.DeepEqual(executor.execution.Env, map[string]string{"DEMO_ENDPOINT": "https://incus.test:8443"}) {
		t.Fatalf("env = %#v", executor.execution.Env)
	}
	if !reflect.DeepEqual(executor.execution.Secrets, map[string]string{"DEMO_MAINTENANCE_KEY": "fixture-secret"}) {
		t.Fatalf("secrets = %#v", executor.execution.Secrets)
	}

	_, err = service.PrepareModuleCommand(context.Background(), PrepareModuleCommandRequest{
		Module: "demo", Command: "repair", Parameters: map[string]any{"unknown": "value"},
	})
	if appErr = requireApplicationError(t, err); appErr.Code != "module_command_invalid_parameter" {
		t.Fatalf("unknown parameter error = %#v", appErr)
	}
	_, err = service.PrepareModuleCommand(context.Background(), PrepareModuleCommandRequest{
		Module: "demo", Command: "repair", Parameters: map[string]any{"enabled": "maybe"},
	})
	if appErr = requireApplicationError(t, err); appErr.Code != "module_command_invalid_parameter" {
		t.Fatalf("invalid parameter error = %#v", appErr)
	}
}

func TestDecodeModuleCommandOutputIsStrictAndBounded(t *testing.T) {
	events := &recordingModuleCommandEvents{}
	valid := strings.Join([]string{
		`{"type":"progress","phase":"drain","current":1,"total":2,"unit":"jobs"}`,
		`{"type":"warning","code":"retrying","message":"Retrying a public check."}`,
		`{"type":"result","changed":false,"result":{"count":2,"states":["ready",true,null]}}`,
	}, "\n") + "\n"
	result, err := decodeModuleCommandOutput(strings.NewReader(valid), events)
	if err != nil {
		t.Fatal(err)
	}
	if result.Changed || result.Result["count"] != int64(2) || len(events.progress) != 1 || len(events.warnings) != 1 {
		t.Fatalf("result/events = %#v / %#v / %#v", result, events.progress, events.warnings)
	}

	tests := []struct {
		name string
		body string
	}{
		{"missing result", `{"type":"warning","code":"notice","message":"No result."}` + "\n"},
		{"unknown record", `{"type":"log","message":"not allowed"}` + "\n"},
		{"unknown field", `{"type":"result","changed":false,"result":{},"extra":true}` + "\n"},
		{"after result", `{"type":"result","changed":false,"result":{}}` + "\n" + `{"type":"warning","code":"late","message":"Late."}` + "\n"},
		{"nested result", `{"type":"result","changed":false,"result":{"nested":{"unsafe":true}}}` + "\n"},
		{"fraction result", `{"type":"result","changed":false,"result":{"count":1.5}}` + "\n"},
		{"missing result object", `{"type":"result","changed":false}` + "\n"},
		{"oversized", strings.Repeat("x", moduleCommandMaxOutputBytes+1)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := decodeModuleCommandOutput(strings.NewReader(test.body), NopEventSink{}); err == nil {
				t.Fatal("invalid output succeeded")
			}
		})
	}
	secretEvents := &recordingModuleCommandEvents{}
	_, err = decodeModuleCommandOutput(strings.NewReader(
		`{"type":"warning","code":"leak","message":"fixture-secret"}`+"\n"+
			`{"type":"result","changed":false,"result":{}}`+"\n",
	), secretEvents, "fixture-secret")
	if err == nil || len(secretEvents.warnings) != 0 {
		t.Fatalf("sensitive protocol record was emitted: error=%v events=%#v", err, secretEvents.warnings)
	}
}

func TestFrozenModuleCommandValidationFailsClosedAfterDigestRecalculation(t *testing.T) {
	command := deployment.ModuleCommand{
		ID: "doctor", Title: "Inspect service", Description: "Read safe diagnostics.", Handler: "doctor",
		Mode: "query", Risk: "normal", RuntimeState: "any", Lock: "module_read", TimeoutSeconds: 10,
		Cancellable: "true", Env: []string{"DEMO_ENDPOINT"}, Secrets: []string{"DEMO_KEY"},
		Parameters: []deployment.ModuleCommandParameter{{
			Name: "verbose", Title: "Verbose", Description: "Include safe details.",
			Type: configschema.Parameter{Kind: "bool"}, Default: false,
		}},
	}
	module := deployment.Module{Name: "demo", EnvPrefix: "DEMO", Commands: []deployment.ModuleCommand{command}}
	if !validFrozenModuleCommand("demo", module, command) {
		t.Fatal("valid frozen descriptor was rejected")
	}

	tests := map[string]func(*deployment.Module, *deployment.ModuleCommand){
		"lock downgrade": func(_ *deployment.Module, command *deployment.ModuleCommand) { command.Lock = "module_write" },
		"unknown lock":   func(_ *deployment.Module, command *deployment.ModuleCommand) { command.Lock = "something_else" },
		"secret overlap": func(_ *deployment.Module, command *deployment.ModuleCommand) {
			command.Secrets = []string{"DEMO_ENDPOINT"}
		},
		"scope escape": func(module *deployment.Module, command *deployment.ModuleCommand) {
			module.Consumes = []string{"*"}
			command.Env = []string{"OTHER_ENDPOINT"}
		},
		"duplicate ID": func(module *deployment.Module, command *deployment.ModuleCommand) {
			module.Commands = append(module.Commands, *command)
		},
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			candidateModule := module
			candidateModule.Commands = append([]deployment.ModuleCommand{}, module.Commands...)
			candidate := command
			mutate(&candidateModule, &candidate)
			candidate.Digest, _ = deployment.CommandDigest(candidate)
			if len(candidateModule.Commands) == 1 {
				candidateModule.Commands[0] = candidate
			}
			if validFrozenModuleCommand("demo", candidateModule, candidate) {
				t.Fatal("tampered descriptor was accepted after digest recalculation")
			}
		})
	}
}

func TestProcessModuleCommandExecutorUsesEmptyEnvironmentAndDiscardsStderr(t *testing.T) {
	moduleRoot := t.TempDir()
	executable := filepath.Join(moduleRoot, "executor")
	body := `#!/bin/sh
printf '%s' 'fixture-secret' >&2
if [ -n "${DOCKER_HOST+x}" ]; then inherited=true; else inherited=false; fi
printf '{"type":"result","changed":false,"result":{"inherited":%s}}\n' "$inherited"
`
	if err := os.WriteFile(executable, []byte(body), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DOCKER_HOST", "unix:///sensitive.sock")
	result, err := (processModuleCommandExecutor{}).Execute(context.Background(), moduleCommandExecution{
		DeploymentID: "dep", ModuleRoot: moduleRoot,
		Module:     deployment.Module{Name: "demo", Version: "1.0.0", Revision: 1, CommandExecutor: deployment.CommandExecutor{Command: []string{"./executor"}}},
		Command:    deployment.ModuleCommand{ID: "doctor", Handler: "doctor"},
		Parameters: map[string]any{}, Env: map[string]string{}, Secrets: map[string]string{"DEMO_SECRET": "fixture-secret"},
	}, NopEventSink{})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if inherited, ok := result.Result["inherited"].(bool); !ok || inherited {
		t.Fatalf("result = %#v", result.Result)
	}
}

func TestInvokeModuleCommandMapsTimeoutAndCancellationWithoutSecretLeak(t *testing.T) {
	workspace, command := writeModuleCommandApplicationFixture(t, `#!/bin/sh
printf '%s' 'fixture-secret' >&2
sleep 5
`)
	service := NewService(workspace)
	_, err := service.InvokeModuleCommand(context.Background(), InvokeModuleCommandRequest{
		Module: "demo", Command: "repair", Parameters: map[string]any{"enabled": true},
		CommandDigest: command.Digest, Confirmed: true,
	})
	appErr := requireApplicationError(t, err)
	if appErr.Code != "module_command_timeout" || strings.Contains(appErr.Error(), "fixture-secret") {
		t.Fatalf("timeout error = %#v", appErr)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, err = service.InvokeModuleCommand(ctx, InvokeModuleCommandRequest{
		Module: "demo", Command: "repair", Parameters: map[string]any{"enabled": true},
		CommandDigest: command.Digest, Confirmed: true,
	})
	if !errors.Is(err, context.DeadlineExceeded) {
		if appErr, ok := ErrorOf(err); !ok || appErr.Code != "module_command_timeout" {
			t.Fatalf("canceled invocation error = %T %v", err, err)
		}
	}
}

func writeModuleCommandApplicationFixture(t *testing.T, executorBody string) (string, deployment.ModuleCommand) {
	t.Helper()
	workspace := t.TempDir()
	id := "dep-command-invoke"
	moduleRoot := filepath.Join(workspace, ".anas", "deployments", id, "modules", "demo")
	executable := filepath.Join(moduleRoot, "executor")
	writeApplicationFile(t, executable, []byte(executorBody))
	if err := os.Chmod(executable, 0700); err != nil {
		t.Fatal(err)
	}
	writeApplicationFile(t, filepath.Join(moduleRoot, ".env"), []byte("DEMO_ENDPOINT=https://incus.test:8443\nUNDECLARED_ENV=hidden\n"))
	writeApplicationFile(t, filepath.Join(workspace, ".anas", "secrets.yml"), []byte("api_version: anas.secrets/v2\nsecrets:\n  DEMO_MAINTENANCE_KEY:\n    value: fixture-secret\n  UNDECLARED_SECRET:\n    value: hidden\n"))
	executorDigest, err := applicationFileDigest(executable)
	if err != nil {
		t.Fatal(err)
	}
	command := deployment.ModuleCommand{
		ID: "repair", Title: "Repair service", Description: "Repair the managed service.", Handler: "repair",
		Mode: "change", Risk: "destructive", RuntimeState: "running", Lock: "module_write", TimeoutSeconds: 1,
		Cancellable: "true", Env: []string{"DEMO_ENDPOINT"}, Secrets: []string{"DEMO_MAINTENANCE_KEY"},
		Parameters: []deployment.ModuleCommandParameter{
			{Name: "enabled", Title: "Enabled", Description: "Enable the repair.", Type: configschema.Parameter{Kind: "bool"}, Required: true},
			{Name: "attempts", Title: "Attempts", Description: "Maximum attempts.", Type: configschema.Parameter{Kind: "int"}, Default: 3},
		},
	}
	command.Digest, err = deployment.CommandDigest(command)
	if err != nil {
		t.Fatal(err)
	}
	manifest := deployment.Manifest{
		APIVersion: deployment.ManifestAPIVersion, ID: id, ModuleOrder: []string{"demo"},
		Modules: map[string]deployment.Module{"demo": {
			Name: "demo", Version: "1.2.3", Revision: 4, ArtifactDeployment: id,
			CommandExecutor: deployment.CommandExecutor{Command: []string{"./executor"}, Digest: executorDigest},
			Commands:        []deployment.ModuleCommand{command},
		}},
	}
	manifestBody, err := yaml.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	writeApplicationFile(t, filepath.Join(workspace, ".anas", "deployments", id, "deployment.yml"), manifestBody)
	writeApplicationFile(t, filepath.Join(workspace, ".anas", "state", "active.yml"), []byte("api_version: anas.state/v2\nactive_deployment: "+id+"\nruntime_status: running\n"))
	return workspace, command
}

func TestModuleCommandRequestNeverContainsWorkspacePath(t *testing.T) {
	request := moduleCommandRequest{ABI: "anas.module-command/v1", InvocationID: "id", Module: moduleCommandModuleRef{Name: "demo"}}
	body, err := yaml.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(body, []byte("workspace")) {
		t.Fatalf("request exposes workspace field: %s", body)
	}
}
