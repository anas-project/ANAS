package application

import (
	"context"
	"errors"
	"fmt"
	"math"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/anas-project/ANAS/internal/configschema"
	"github.com/anas-project/ANAS/internal/deployment"
	"github.com/anas-project/ANAS/internal/dotenv"
)

var errModuleCommandProtocol = errors.New("module command protocol error")

type resolvedModuleCommand struct {
	active          deployment.ActiveState
	manifest        *deployment.Manifest
	moduleName      string
	module          deployment.Module
	command         deployment.ModuleCommand
	moduleRoot      string
	secretStorePath string
}

// PrepareModuleCommand resolves and validates an invocation without acquiring
// a mutation lock or starting the executor. CLI confirmation uses this safe
// projection so the values displayed to the operator are the same normalized
// values InvokeModuleCommand will validate again under the lock.
func (s *Service) PrepareModuleCommand(ctx context.Context, req PrepareModuleCommandRequest) (PrepareModuleCommandResult, error) {
	if err := contextError(ctx); err != nil {
		return PrepareModuleCommandResult{}, err
	}
	moduleName, commandID, err := validateModuleCommandTarget(req.Module, req.Command)
	if err != nil {
		return PrepareModuleCommandResult{}, err
	}
	resolved, err := s.resolveModuleCommand(ctx, moduleName, commandID)
	if err != nil {
		return PrepareModuleCommandResult{}, err
	}
	if req.CommandDigest != "" && req.CommandDigest != resolved.command.Digest {
		return PrepareModuleCommandResult{}, newError(ErrorKindFailedPrecondition, "module_command_changed", "module command descriptor changed", nil)
	}
	parameters, err := normalizeModuleCommandParameters(resolved.command, req.Parameters)
	if err != nil {
		return PrepareModuleCommandResult{}, err
	}
	available, reason := s.moduleCommandAvailability(resolved.active, resolved.manifest, resolved.moduleName, resolved.module, resolved.command)
	if !available {
		return PrepareModuleCommandResult{}, newError(ErrorKindFailedPrecondition, "module_command_unavailable", "module command is unavailable: "+reason, nil)
	}
	return PrepareModuleCommandResult{
		DeploymentID: resolved.manifest.ID,
		Module:       moduleName,
		Release:      fmt.Sprintf("%s-r%d", resolved.module.Version, resolved.module.Revision),
		Command:      publicModuleCommand(resolved.command),
		Parameters:   parameters,
	}, nil
}

func (s *Service) InvokeModuleCommand(ctx context.Context, req InvokeModuleCommandRequest) (InvokeModuleCommandResult, error) {
	if err := contextError(ctx); err != nil {
		return InvokeModuleCommandResult{}, err
	}
	moduleName, commandID, err := validateModuleCommandTarget(req.Module, req.Command)
	if err != nil {
		return InvokeModuleCommandResult{}, err
	}
	req.Module, req.Command = moduleName, commandID
	initial, err := s.resolveModuleCommand(ctx, moduleName, commandID)
	if err != nil {
		return InvokeModuleCommandResult{}, err
	}
	if req.CommandDigest != "" && req.CommandDigest != initial.command.Digest {
		return InvokeModuleCommandResult{}, newError(ErrorKindFailedPrecondition, "module_command_changed", "module command descriptor changed", nil)
	}
	parameters, err := normalizeModuleCommandParameters(initial.command, req.Parameters)
	if err != nil {
		return InvokeModuleCommandResult{}, err
	}
	if initial.command.Risk == "destructive" && !req.Confirmed {
		return InvokeModuleCommandResult{}, newError(ErrorKindFailedPrecondition, "module_command_confirmation_required", "destructive module command requires confirmation", nil)
	}

	unlock, err := acquireModuleCommandLock(ctx, s.workspace, req.Module, initial.command.Lock)
	if err != nil {
		if contextError(ctx) != nil {
			return InvokeModuleCommandResult{}, contextError(ctx)
		}
		if errors.Is(err, errModuleCommandConfigRecoveryRequired) {
			return InvokeModuleCommandResult{}, newError(
				ErrorKindInternal, "config_recovery_required", "workspace configuration recovery is required", err,
			)
		}
		return InvokeModuleCommandResult{}, newError(ErrorKindFailedPrecondition, "module_command_busy", "module command could not acquire the runtime lock", err)
	}
	defer unlock()

	resolved, err := s.resolveModuleCommand(ctx, req.Module, req.Command)
	if err != nil {
		return InvokeModuleCommandResult{}, err
	}
	if resolved.manifest.ID != initial.manifest.ID || resolved.command.Digest != initial.command.Digest {
		return InvokeModuleCommandResult{}, newError(ErrorKindFailedPrecondition, "module_command_changed", "active deployment or module command changed while waiting for the lock", nil)
	}
	if req.CommandDigest != "" && req.CommandDigest != resolved.command.Digest {
		return InvokeModuleCommandResult{}, newError(ErrorKindFailedPrecondition, "module_command_changed", "module command descriptor changed", nil)
	}
	available, reason := s.moduleCommandAvailability(resolved.active, resolved.manifest, resolved.moduleName, resolved.module, resolved.command)
	if !available {
		return InvokeModuleCommandResult{}, newError(ErrorKindFailedPrecondition, "module_command_unavailable", "module command is unavailable: "+reason, nil)
	}
	env, secrets, err := moduleCommandInputs(resolved)
	if err != nil {
		return InvokeModuleCommandResult{}, err
	}
	execution := moduleCommandExecution{
		DeploymentID: resolved.manifest.ID, Module: resolved.module, Command: resolved.command,
		ModuleRoot: resolved.moduleRoot, Parameters: parameters, Env: env, Secrets: secrets,
	}
	timeout := time.Duration(resolved.command.TimeoutSeconds) * time.Second
	executionContext, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	executor := s.executor
	if executor == nil {
		executor = processModuleCommandExecutor{}
	}
	events := s.events
	if events == nil {
		events = NopEventSink{}
	}
	result, err := executor.Execute(executionContext, execution, events)
	if err != nil {
		if errors.Is(executionContext.Err(), context.DeadlineExceeded) {
			return InvokeModuleCommandResult{}, newError(ErrorKindInternal, "module_command_timeout", "module command timed out", context.DeadlineExceeded)
		}
		if errors.Is(executionContext.Err(), context.Canceled) {
			return InvokeModuleCommandResult{}, context.Canceled
		}
		if errors.Is(err, errModuleCommandProtocol) {
			return InvokeModuleCommandResult{}, newError(ErrorKindInternal, "module_command_protocol_error", "module command returned invalid output", err)
		}
		return InvokeModuleCommandResult{}, newError(ErrorKindInternal, "module_command_failed", "module command failed", err)
	}
	if result.Result == nil {
		result.Result = map[string]any{}
	}
	return InvokeModuleCommandResult{
		DeploymentID: resolved.manifest.ID, Module: req.Module, Command: req.Command,
		Changed: result.Changed, Result: result.Result,
	}, nil
}

func validateModuleCommandTarget(moduleName, commandID string) (string, string, error) {
	moduleName = strings.TrimSpace(moduleName)
	commandID = strings.TrimSpace(commandID)
	if !applicationModuleNamePattern.MatchString(moduleName) {
		return "", "", newError(ErrorKindInvalidArgument, "invalid_module", "module name is invalid", nil)
	}
	if !applicationModuleCommandIDPattern.MatchString(commandID) {
		return "", "", newError(ErrorKindInvalidArgument, "invalid_module_command", "module command ID is invalid", nil)
	}
	return moduleName, commandID, nil
}

func (s *Service) resolveModuleCommand(ctx context.Context, moduleName, commandID string) (resolvedModuleCommand, error) {
	active, err := s.reader.Active(ctx)
	if err != nil {
		return resolvedModuleCommand{}, moduleCommandReadError(ctx, "read active deployment state", err)
	}
	if active.ActiveDeployment == "" {
		return resolvedModuleCommand{}, newError(ErrorKindFailedPrecondition, "no_active_deployment", "workspace has no active deployment", nil)
	}
	manifest, _, err := s.reader.Manifest(ctx, active.ActiveDeployment)
	if err != nil {
		return resolvedModuleCommand{}, moduleCommandReadError(ctx, "read active deployment manifest", err)
	}
	module, ok := manifest.Modules[moduleName]
	if !ok || module.Name != moduleName {
		return resolvedModuleCommand{}, newError(ErrorKindNotFound, "module_not_active", "module is not present in the active deployment", nil)
	}
	var command deployment.ModuleCommand
	found := false
	for _, candidate := range module.Commands {
		if candidate.ID == commandID {
			command = candidate
			found = true
			break
		}
	}
	if !found {
		return resolvedModuleCommand{}, newError(ErrorKindNotFound, "module_command_not_found", "module command was not found", nil)
	}
	artifactDeployment := module.ArtifactDeployment
	if artifactDeployment == "" {
		artifactDeployment = manifest.ID
	}
	if deployment.ValidateID(artifactDeployment) != nil {
		return resolvedModuleCommand{}, newError(ErrorKindFailedPrecondition, "module_command_unavailable", "module command artifact identity is invalid", nil)
	}
	return resolvedModuleCommand{
		active: active, manifest: manifest, moduleName: moduleName, module: module, command: command,
		moduleRoot:      filepath.Join(s.workspace, ".anas", "deployments", artifactDeployment, "modules", moduleName),
		secretStorePath: filepath.Join(s.workspace, ".anas", "secrets.yml"),
	}, nil
}

func normalizeModuleCommandParameters(command deployment.ModuleCommand, provided map[string]any) (map[string]any, error) {
	definitions := make(map[string]deployment.ModuleCommandParameter, len(command.Parameters))
	for _, parameter := range command.Parameters {
		definitions[parameter.Name] = parameter
	}
	for name := range provided {
		if _, ok := definitions[name]; !ok {
			return nil, newError(ErrorKindInvalidArgument, "module_command_invalid_parameter", fmt.Sprintf("unknown module command parameter %q", name), nil)
		}
	}
	result := make(map[string]any, len(command.Parameters))
	for _, parameter := range command.Parameters {
		raw, exists := provided[parameter.Name]
		if !exists {
			if parameter.Default != nil {
				result[parameter.Name] = parameter.Default
				continue
			}
			if parameter.Required {
				return nil, newError(ErrorKindInvalidArgument, "module_command_invalid_parameter", fmt.Sprintf("module command parameter %q is required", parameter.Name), nil)
			}
			continue
		}
		normalized, err := normalizeModuleCommandParameterValue(parameter.Type, raw)
		if err != nil {
			return nil, newError(ErrorKindInvalidArgument, "module_command_invalid_parameter", fmt.Sprintf("module command parameter %q: %v", parameter.Name, err), err)
		}
		result[parameter.Name] = normalized
	}
	return result, nil
}

func normalizeModuleCommandParameterValue(definition configschema.Parameter, raw any) (any, error) {
	var text string
	switch definition.Kind {
	case "string", "enum":
		value, ok := raw.(string)
		if !ok {
			return nil, fmt.Errorf("must be a string")
		}
		text = value
	case "bool":
		switch value := raw.(type) {
		case bool:
			text = strconv.FormatBool(value)
		case string:
			text = value
		default:
			return nil, fmt.Errorf("must be a boolean")
		}
	case "int":
		switch value := raw.(type) {
		case int:
			text = strconv.Itoa(value)
		case int64:
			text = strconv.FormatInt(value, 10)
		case float64:
			if math.Trunc(value) != value || value > math.MaxInt64 || value < math.MinInt64 {
				return nil, fmt.Errorf("must be an integer")
			}
			text = strconv.FormatInt(int64(value), 10)
		case string:
			text = value
		default:
			return nil, fmt.Errorf("must be an integer")
		}
	default:
		return nil, fmt.Errorf("has unsupported type %q", definition.Kind)
	}
	normalized, err := definition.Normalize(text)
	if err != nil {
		return nil, err
	}
	switch definition.Kind {
	case "bool":
		return strconv.ParseBool(normalized)
	case "int":
		return strconv.Atoi(normalized)
	default:
		return normalized, nil
	}
}

func moduleCommandInputs(resolved resolvedModuleCommand) (map[string]string, map[string]string, error) {
	envValues := map[string]string{}
	if len(resolved.command.Env) > 0 {
		rendered, err := dotenv.ParseFile(filepath.Join(resolved.moduleRoot, ".env"))
		if err != nil {
			return nil, nil, newError(ErrorKindFailedPrecondition, "module_command_unavailable", "module command environment is unavailable", err)
		}
		for _, key := range resolved.command.Env {
			if rendered[key] == "" {
				return nil, nil, newError(ErrorKindFailedPrecondition, "module_command_unavailable", "module command environment is incomplete", nil)
			}
			envValues[key] = rendered[key]
		}
	}
	secretValues := map[string]string{}
	if len(resolved.command.Secrets) > 0 {
		stored, err := readModuleCommandSecrets(resolved.secretStorePath)
		if err != nil {
			return nil, nil, newError(ErrorKindFailedPrecondition, "module_command_unavailable", "module command secrets are unavailable", err)
		}
		for _, key := range resolved.command.Secrets {
			if stored[key] == "" {
				return nil, nil, newError(ErrorKindFailedPrecondition, "module_command_unavailable", "module command secrets are incomplete", nil)
			}
			secretValues[key] = stored[key]
		}
	}
	return envValues, secretValues, nil
}
