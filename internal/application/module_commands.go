package application

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"

	"github.com/anas-project/ANAS/internal/configschema"
	"github.com/anas-project/ANAS/internal/deployment"
	"github.com/anas-project/ANAS/internal/dotenv"
	"gopkg.in/yaml.v3"
)

var (
	applicationModuleNamePattern        = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)
	applicationModuleCommandIDPattern   = regexp.MustCompile(`^[a-z][a-z0-9-]{0,62}$`)
	applicationModuleCommandNamePattern = regexp.MustCompile(`^[a-z][a-z0-9_]{0,62}$`)
	applicationModuleCommandEnvPattern  = regexp.MustCompile(`^[A-Z][A-Z0-9_]*$`)
)

func (s *Service) ListModuleCommands(ctx context.Context, req ListModuleCommandsRequest) (ListModuleCommandsResult, error) {
	if err := contextError(ctx); err != nil {
		return ListModuleCommandsResult{}, err
	}
	moduleFilter := strings.TrimSpace(req.Module)
	if moduleFilter != "" && !applicationModuleNamePattern.MatchString(moduleFilter) {
		return ListModuleCommandsResult{}, newError(ErrorKindInvalidArgument, "invalid_module", "module name is invalid", nil)
	}
	active, err := s.reader.Active(ctx)
	if err != nil {
		return ListModuleCommandsResult{}, moduleCommandReadError(ctx, "read active deployment state", err)
	}
	result := ListModuleCommandsResult{Commands: []EffectiveModuleCommand{}}
	if active.ActiveDeployment == "" {
		return result, nil
	}
	result.ActiveDeployment = nullableString(active.ActiveDeployment)
	manifest, _, err := s.reader.Manifest(ctx, active.ActiveDeployment)
	if err != nil {
		return ListModuleCommandsResult{}, moduleCommandReadError(ctx, "read active deployment manifest", err)
	}
	if moduleFilter != "" {
		if _, ok := manifest.Modules[moduleFilter]; !ok {
			return ListModuleCommandsResult{}, newError(ErrorKindNotFound, "module_not_active", "module is not present in the active deployment", nil)
		}
	}

	moduleNames := moduleCommandModuleOrder(manifest)
	for _, moduleName := range moduleNames {
		if moduleFilter != "" && moduleName != moduleFilter {
			continue
		}
		module := manifest.Modules[moduleName]
		commands := append([]deployment.ModuleCommand{}, module.Commands...)
		sort.Slice(commands, func(i, j int) bool { return commands[i].ID < commands[j].ID })
		for _, command := range commands {
			view := EffectiveModuleCommand{
				Module: moduleName, Release: fmt.Sprintf("%s-r%d", module.Version, module.Revision),
				DeploymentID: manifest.ID, Command: publicModuleCommand(command),
			}
			view.Available, view.UnavailableReason = s.moduleCommandAvailability(active, manifest, moduleName, module, command)
			result.Commands = append(result.Commands, view)
		}
	}
	return result, contextError(ctx)
}

func (s *Service) GetModuleCommand(ctx context.Context, req GetModuleCommandRequest) (EffectiveModuleCommand, error) {
	if err := contextError(ctx); err != nil {
		return EffectiveModuleCommand{}, err
	}
	moduleName, commandID, err := validateModuleCommandTarget(req.Module, req.Command)
	if err != nil {
		return EffectiveModuleCommand{}, err
	}
	result, err := s.ListModuleCommands(ctx, ListModuleCommandsRequest{Module: moduleName})
	if err != nil {
		return EffectiveModuleCommand{}, err
	}
	for _, command := range result.Commands {
		if command.Command.ID == commandID {
			return command, nil
		}
	}
	return EffectiveModuleCommand{}, newError(ErrorKindNotFound, "module_command_not_found", "module command was not found", nil)
}

func moduleCommandReadError(ctx context.Context, action string, err error) error {
	if contextError(ctx) != nil {
		return contextError(ctx)
	}
	kind := ErrorKindFailedPrecondition
	if os.IsNotExist(err) {
		kind = ErrorKindNotFound
	}
	return newError(kind, "module_commands_unavailable", fmt.Sprintf("%s: %v", action, err), err)
}

func moduleCommandModuleOrder(manifest *deployment.Manifest) []string {
	out := []string{}
	seen := map[string]bool{}
	for _, name := range manifest.ModuleOrder {
		if _, ok := manifest.Modules[name]; ok && !seen[name] {
			seen[name] = true
			out = append(out, name)
		}
	}
	extra := []string{}
	for name := range manifest.Modules {
		if !seen[name] {
			extra = append(extra, name)
		}
	}
	sort.Strings(extra)
	return append(out, extra...)
}

func publicModuleCommand(command deployment.ModuleCommand) ModuleCommandDescriptor {
	parameters := make([]ModuleCommandParameter, 0, len(command.Parameters))
	for _, parameter := range command.Parameters {
		parameters = append(parameters, ModuleCommandParameter{
			Name: parameter.Name, Title: parameter.Title, Description: parameter.Description,
			Type: parameter.Type, Required: parameter.Required, Default: parameter.Default,
		})
	}
	return ModuleCommandDescriptor{
		ID: command.ID, Title: command.Title, Description: command.Description,
		Mode: command.Mode, Risk: command.Risk, RuntimeState: command.RuntimeState, Lock: command.Lock,
		TimeoutSeconds: command.TimeoutSeconds, Cancellable: command.Cancellable,
		Parameters: parameters, Digest: command.Digest,
	}
}

func (s *Service) moduleCommandAvailability(active deployment.ActiveState, manifest *deployment.Manifest, moduleName string, module deployment.Module, command deployment.ModuleCommand) (bool, string) {
	if !applicationModuleNamePattern.MatchString(moduleName) || module.Name != moduleName {
		return false, "executor_missing"
	}
	if !validFrozenModuleCommand(moduleName, module, command) {
		return false, "descriptor_invalid"
	}
	digest, err := deployment.CommandDigest(command)
	if err != nil || digest != command.Digest {
		return false, "descriptor_digest_mismatch"
	}
	if command.RuntimeState != "any" {
		state := "stopped"
		if active.RuntimeStatus == "running" {
			state = "running"
		}
		if state != command.RuntimeState {
			return false, "runtime_state_mismatch"
		}
	}
	if len(module.CommandExecutor.Command) != 1 || module.CommandExecutor.Digest == "" {
		return false, "executor_missing"
	}
	rel := filepath.Clean(module.CommandExecutor.Command[0])
	if filepath.IsAbs(rel) || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return false, "executor_missing"
	}
	artifactDeployment := module.ArtifactDeployment
	if artifactDeployment == "" {
		artifactDeployment = manifest.ID
	}
	if deployment.ValidateID(artifactDeployment) != nil {
		return false, "executor_missing"
	}
	moduleRoot := filepath.Join(s.workspace, ".anas", "deployments", artifactDeployment, "modules", moduleName)
	path := filepath.Join(moduleRoot, rel)
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0111 == 0 {
		return false, "executor_missing"
	}
	digest, err = applicationFileDigest(path)
	if err != nil || digest != module.CommandExecutor.Digest {
		return false, "executor_digest_mismatch"
	}
	if len(command.Env) > 0 {
		env, err := dotenv.ParseFile(filepath.Join(moduleRoot, ".env"))
		if err != nil {
			return false, "missing_env"
		}
		for _, key := range command.Env {
			if env[key] == "" {
				return false, "missing_env"
			}
		}
	}
	if len(command.Secrets) > 0 {
		secrets, err := readModuleCommandSecrets(filepath.Join(s.workspace, ".anas", "secrets.yml"))
		if err != nil {
			return false, "missing_secret"
		}
		for _, key := range command.Secrets {
			if secrets[key] == "" {
				return false, "missing_secret"
			}
		}
	}
	return true, ""
}

func validFrozenModuleCommand(moduleName string, module deployment.Module, command deployment.ModuleCommand) bool {
	if !applicationModuleCommandIDPattern.MatchString(command.ID) || command.Title == "" || command.Description == "" ||
		!applicationModuleCommandNamePattern.MatchString(command.Handler) ||
		!oneOfModuleCommandValue(command.Mode, "query", "change") ||
		!oneOfModuleCommandValue(command.Risk, "normal", "destructive") ||
		!oneOfModuleCommandValue(command.RuntimeState, "running", "stopped", "any") ||
		!oneOfModuleCommandValue(command.Lock, "module_read", "module_write", "workspace_write") ||
		!oneOfModuleCommandValue(command.Cancellable, "false", "true", "safe_points") ||
		command.TimeoutSeconds < 1 || command.TimeoutSeconds > 3600 {
		return false
	}
	if command.Risk == "destructive" && command.Mode != "change" ||
		command.Mode == "query" && command.Lock != "module_read" ||
		command.Mode == "change" && command.Lock == "module_read" {
		return false
	}
	matchingIDs := 0
	for _, candidate := range module.Commands {
		if candidate.ID == command.ID {
			matchingIDs++
		}
	}
	if matchingIDs != 1 {
		return false
	}
	parameterNames := map[string]bool{}
	for _, parameter := range command.Parameters {
		if parameterNames[parameter.Name] || !validFrozenModuleCommandParameter(parameter) {
			return false
		}
		parameterNames[parameter.Name] = true
	}
	envInputs := map[string]bool{}
	for _, key := range command.Env {
		if envInputs[key] || !validFrozenModuleCommandInput(moduleName, module, key) {
			return false
		}
		envInputs[key] = true
	}
	secretInputs := map[string]bool{}
	for _, key := range command.Secrets {
		if envInputs[key] || secretInputs[key] || !validFrozenModuleCommandInput(moduleName, module, key) {
			return false
		}
		secretInputs[key] = true
	}
	return true
}

func validFrozenModuleCommandParameter(parameter deployment.ModuleCommandParameter) bool {
	if !applicationModuleCommandNamePattern.MatchString(parameter.Name) || parameter.Title == "" || parameter.Description == "" {
		return false
	}
	normalized, err := configschema.NormalizeDefinition(parameter.Type)
	if err != nil || !normalized.Declared() || normalized.DefaultSource != "" || !reflect.DeepEqual(normalized, parameter.Type) {
		return false
	}
	if parameter.Required && parameter.Default != nil {
		return false
	}
	if parameter.Default != nil {
		value, err := normalizeModuleCommandParameterValue(parameter.Type, parameter.Default)
		if err != nil || !reflect.DeepEqual(value, parameter.Default) {
			return false
		}
	}
	return true
}

func validFrozenModuleCommandInput(moduleName string, module deployment.Module, key string) bool {
	if !applicationModuleCommandEnvPattern.MatchString(key) {
		return false
	}
	prefix := strings.TrimSuffix(strings.TrimSpace(module.EnvPrefix), "_")
	if prefix != "" && strings.HasPrefix(key, prefix+"_") {
		return true
	}
	for _, pattern := range module.Consumes {
		if !validFrozenModuleCommandConsumePattern(pattern) {
			continue
		}
		if pattern == key || strings.HasSuffix(pattern, "*") && strings.HasPrefix(key, strings.TrimSuffix(pattern, "*")) {
			return true
		}
	}
	// Command-bearing manifests always freeze env_prefix. This fallback is only
	// for hand-authored legacy fixtures and does not broaden a differently named
	// explicit prefix.
	return prefix == "" && strings.HasPrefix(key, strings.ToUpper(moduleName)+"_")
}

func validFrozenModuleCommandConsumePattern(pattern string) bool {
	if applicationModuleCommandEnvPattern.MatchString(pattern) {
		return true
	}
	if !strings.HasSuffix(pattern, "*") || len(pattern) < 2 {
		return false
	}
	prefix := strings.TrimSuffix(pattern, "*")
	return applicationModuleCommandEnvPattern.MatchString(strings.TrimSuffix(prefix, "_"))
}

func oneOfModuleCommandValue(value string, allowed ...string) bool {
	for _, candidate := range allowed {
		if value == candidate {
			return true
		}
	}
	return false
}

func readModuleCommandSecrets(path string) (map[string]string, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var document struct {
		APIVersion string `yaml:"api_version"`
		Secrets    map[string]struct {
			Value string `yaml:"value"`
		} `yaml:"secrets"`
	}
	if err := yaml.Unmarshal(body, &document); err != nil {
		return nil, err
	}
	if document.APIVersion != "anas.secrets/v2" {
		return nil, fmt.Errorf("unsupported Secret Store version")
	}
	values := make(map[string]string, len(document.Secrets))
	for key, record := range document.Secrets {
		values[key] = record.Value
	}
	return values, nil
}

func applicationFileDigest(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}
	return fmt.Sprintf("sha256:%x", hash.Sum(nil)), nil
}
