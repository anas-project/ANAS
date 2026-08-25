package runner

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"

	"github.com/anas-project/ANAS/internal/configschema"
	"github.com/anas-project/ANAS/internal/deployment"
)

const commandBinaryName = ".command.bin"

var (
	moduleCommandIDPattern        = regexp.MustCompile(`^[a-z][a-z0-9-]{0,62}$`)
	moduleCommandParameterPattern = regexp.MustCompile(`^[a-z][a-z0-9_]{0,62}$`)
	moduleCommandHandlerPattern   = regexp.MustCompile(`^[a-z][a-z0-9_]{0,62}$`)
)

func normalizeModuleCommands(dir, module, envPrefix string, consumes, abis []string, management manifestManagement) (CommandExecutor, []ModuleCommand, error) {
	declared := management.Commands
	if len(declared) == 0 {
		if len(management.CommandExecutor.Command) != 0 {
			return CommandExecutor{}, nil, fmt.Errorf("module %q declares a command executor without commands", module)
		}
		return CommandExecutor{}, nil, nil
	}
	if !contains(abis, currentModuleCommandABI) {
		return CommandExecutor{}, nil, fmt.Errorf("module %q declares commands without supporting runner ABI %s", module, currentModuleCommandABI)
	}

	executor, err := normalizeModuleCommandExecutor(dir, module, management.CommandExecutor)
	if err != nil {
		return CommandExecutor{}, nil, err
	}
	commands := make([]ModuleCommand, 0, len(declared))
	seen := map[string]bool{}
	for _, raw := range declared {
		command, err := normalizeModuleCommand(module, envPrefix, consumes, raw)
		if err != nil {
			return CommandExecutor{}, nil, err
		}
		if seen[command.ID] {
			return CommandExecutor{}, nil, fmt.Errorf("module %q declares command %q more than once", module, command.ID)
		}
		seen[command.ID] = true
		command.Digest, err = deployment.CommandDigest(command)
		if err != nil {
			return CommandExecutor{}, nil, fmt.Errorf("module %q command %q digest: %w", module, command.ID, err)
		}
		commands = append(commands, command)
	}
	sort.Slice(commands, func(i, j int) bool { return commands[i].ID < commands[j].ID })
	return executor, commands, nil
}

func normalizeModuleCommandExecutor(dir, module string, raw manifestCommandExecutor) (CommandExecutor, error) {
	command := append([]string{}, raw.Command...)
	if len(command) == 3 && command[0] == "go" && command[1] == "run" && filepath.ToSlash(filepath.Clean(command[2])) == "command" {
		if info, err := os.Lstat(filepath.Join(dir, "command")); err != nil || !info.IsDir() {
			return CommandExecutor{}, fmt.Errorf("module %q command executor source ./command is missing or not a directory", module)
		}
		return CommandExecutor{Command: command}, nil
	}
	if len(command) != 1 {
		return CommandExecutor{}, fmt.Errorf("module %q command executor must be one fixed relative executable or go run ./command", module)
	}
	rel := strings.TrimSpace(command[0])
	clean := filepath.Clean(rel)
	if !strings.HasPrefix(filepath.ToSlash(rel), "./") || filepath.IsAbs(rel) || clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return CommandExecutor{}, fmt.Errorf("module %q command executor %q is not a safe package-relative path", module, rel)
	}
	info, err := os.Lstat(filepath.Join(dir, clean))
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0111 == 0 {
		return CommandExecutor{}, fmt.Errorf("module %q command executor %q is missing or not executable", module, rel)
	}
	return CommandExecutor{Command: []string{"./" + filepath.ToSlash(clean)}}, nil
}

func normalizeModuleCommand(module, envPrefix string, consumes []string, raw manifestModuleCommand) (ModuleCommand, error) {
	command := ModuleCommand{
		ID: strings.TrimSpace(raw.ID), Title: strings.TrimSpace(raw.Title), Description: strings.TrimSpace(raw.Description),
		Handler: strings.TrimSpace(raw.Handler), Mode: strings.ToLower(strings.TrimSpace(raw.Mode)),
		Risk: strings.ToLower(strings.TrimSpace(raw.Risk)), RuntimeState: strings.ToLower(strings.TrimSpace(raw.RuntimeState)),
		Lock: strings.ToLower(strings.TrimSpace(raw.Lock)), TimeoutSeconds: raw.TimeoutSeconds,
	}
	if !moduleCommandIDPattern.MatchString(command.ID) {
		return ModuleCommand{}, fmt.Errorf("module %q command id %q is invalid", module, raw.ID)
	}
	if command.Title == "" || command.Description == "" {
		return ModuleCommand{}, fmt.Errorf("module %q command %q requires non-empty title and description", module, command.ID)
	}
	if !moduleCommandHandlerPattern.MatchString(command.Handler) {
		return ModuleCommand{}, fmt.Errorf("module %q command %q handler %q is invalid", module, command.ID, raw.Handler)
	}
	if !contains([]string{"query", "change"}, command.Mode) {
		return ModuleCommand{}, fmt.Errorf("module %q command %q mode %q is invalid; use query or change", module, command.ID, raw.Mode)
	}
	if !contains([]string{"normal", "destructive"}, command.Risk) {
		return ModuleCommand{}, fmt.Errorf("module %q command %q risk %q is invalid; use normal or destructive", module, command.ID, raw.Risk)
	}
	if command.Risk == "destructive" && command.Mode != "change" {
		return ModuleCommand{}, fmt.Errorf("module %q command %q cannot be destructive in query mode", module, command.ID)
	}
	if !contains([]string{"running", "stopped", "any"}, command.RuntimeState) {
		return ModuleCommand{}, fmt.Errorf("module %q command %q runtime_state %q is invalid", module, command.ID, raw.RuntimeState)
	}
	if !contains([]string{"module_read", "module_write", "workspace_write"}, command.Lock) {
		return ModuleCommand{}, fmt.Errorf("module %q command %q lock %q is invalid", module, command.ID, raw.Lock)
	}
	if command.Mode == "query" && command.Lock != "module_read" {
		return ModuleCommand{}, fmt.Errorf("module %q query command %q must use module_read", module, command.ID)
	}
	if command.Mode == "change" && command.Lock == "module_read" {
		return ModuleCommand{}, fmt.Errorf("module %q change command %q must use module_write or workspace_write", module, command.ID)
	}
	if command.TimeoutSeconds < 1 || command.TimeoutSeconds > 3600 {
		return ModuleCommand{}, fmt.Errorf("module %q command %q timeout_seconds must be between 1 and 3600", module, command.ID)
	}
	cancellable, err := normalizeModuleCommandCancellable(raw.Cancellable)
	if err != nil {
		return ModuleCommand{}, fmt.Errorf("module %q command %q: %w", module, command.ID, err)
	}
	command.Cancellable = cancellable

	parameterNames := map[string]bool{}
	for _, rawParameter := range raw.Parameters {
		parameter, err := normalizeModuleCommandParameter(module, command.ID, rawParameter)
		if err != nil {
			return ModuleCommand{}, err
		}
		if parameterNames[parameter.Name] {
			return ModuleCommand{}, fmt.Errorf("module %q command %q declares parameter %q more than once", module, command.ID, parameter.Name)
		}
		parameterNames[parameter.Name] = true
		command.Parameters = append(command.Parameters, parameter)
	}
	sort.Slice(command.Parameters, func(i, j int) bool { return command.Parameters[i].Name < command.Parameters[j].Name })
	command.Env, err = normalizeModuleCommandInputs(module, command.ID, "env", envPrefix, consumes, raw.Env)
	if err != nil {
		return ModuleCommand{}, err
	}
	command.Secrets, err = normalizeModuleCommandInputs(module, command.ID, "secrets", envPrefix, consumes, raw.Secrets)
	if err != nil {
		return ModuleCommand{}, err
	}
	for _, secret := range command.Secrets {
		if contains(command.Env, secret) {
			return ModuleCommand{}, fmt.Errorf("module %q command %q input %s cannot be declared as both env and secret", module, command.ID, secret)
		}
	}
	return command, nil
}

func normalizeModuleCommandCancellable(raw any) (string, error) {
	switch value := raw.(type) {
	case nil:
		return "false", nil
	case bool:
		return strconv.FormatBool(value), nil
	case string:
		value = strings.ToLower(strings.TrimSpace(value))
		if contains([]string{"false", "true", "safe_points"}, value) {
			return value, nil
		}
	}
	return "", fmt.Errorf("cancellable must be false, true, or safe_points")
}

func normalizeModuleCommandParameter(module, command string, raw manifestCommandParameter) (ModuleCommandParameter, error) {
	parameter := ModuleCommandParameter{
		Name: strings.TrimSpace(raw.Name), Title: strings.TrimSpace(raw.Title), Description: strings.TrimSpace(raw.Description),
		Required: raw.Required,
	}
	if !moduleCommandParameterPattern.MatchString(parameter.Name) {
		return ModuleCommandParameter{}, fmt.Errorf("module %q command %q parameter name %q is invalid", module, command, raw.Name)
	}
	if parameter.Title == "" || parameter.Description == "" {
		return ModuleCommandParameter{}, fmt.Errorf("module %q command %q parameter %q requires non-empty title and description", module, command, parameter.Name)
	}
	var enum []string
	if raw.Type.Enum != nil {
		enum = append([]string{}, raw.Type.Enum...)
	}
	definition, err := configschema.NormalizeDefinition(configschema.Parameter{
		Kind: raw.Type.Kind, Enum: enum, Constraints: raw.Type.Constraints,
		DefaultSource: raw.Type.DefaultSource,
	})
	if err != nil || !definition.Declared() {
		if err == nil {
			err = fmt.Errorf("type is required")
		}
		return ModuleCommandParameter{}, fmt.Errorf("module %q command %q parameter %q: %w", module, command, parameter.Name, err)
	}
	if definition.DefaultSource != "" {
		return ModuleCommandParameter{}, fmt.Errorf("module %q command %q parameter %q cannot use default_source", module, command, parameter.Name)
	}
	parameter.Type = definition
	if raw.Default != nil {
		if parameter.Required {
			return ModuleCommandParameter{}, fmt.Errorf("module %q command %q required parameter %q cannot declare a default", module, command, parameter.Name)
		}
		parameter.Default, err = normalizeModuleCommandDefault(definition, raw.Default)
		if err != nil {
			return ModuleCommandParameter{}, fmt.Errorf("module %q command %q parameter %q default: %w", module, command, parameter.Name, err)
		}
	}
	return parameter, nil
}

func normalizeModuleCommandDefault(definition configschema.Parameter, raw any) (any, error) {
	var text string
	switch definition.Kind {
	case "string", "enum":
		value, ok := raw.(string)
		if !ok {
			return nil, fmt.Errorf("must be a string")
		}
		text = value
	case "bool":
		value, ok := raw.(bool)
		if !ok {
			return nil, fmt.Errorf("must be a boolean")
		}
		text = strconv.FormatBool(value)
	case "int":
		switch value := raw.(type) {
		case int:
			text = strconv.Itoa(value)
		case int64:
			text = strconv.FormatInt(value, 10)
		case uint64:
			text = strconv.FormatUint(value, 10)
		default:
			return nil, fmt.Errorf("must be an integer")
		}
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

func normalizeModuleCommandInputs(module, command, field, envPrefix string, consumes, raw []string) ([]string, error) {
	out := make([]string, 0, len(raw))
	seen := map[string]bool{}
	for _, value := range raw {
		key := strings.TrimSpace(value)
		if !envKeyPattern.MatchString(key) {
			return nil, fmt.Errorf("module %q command %q %s input %q is not an environment key", module, command, field, value)
		}
		allowed := strings.HasPrefix(key, envPrefix+"_") || matchEnvPattern(consumes, key)
		if !allowed {
			return nil, fmt.Errorf("module %q command %q %s input %s is outside the Module namespace and config.consumes", module, command, field, key)
		}
		if seen[key] {
			return nil, fmt.Errorf("module %q command %q repeats %s input %s", module, command, field, key)
		}
		seen[key] = true
		out = append(out, key)
	}
	sort.Strings(out)
	return out, nil
}

func (a *app) ensureModuleCommandBinary(mod Module) (string, error) {
	command := mod.CommandExecutor.Command
	if len(command) == 1 {
		return filepath.Abs(filepath.Join(mod.SourceDir, command[0]))
	}
	if len(command) != 3 || command[0] != "go" || command[1] != "run" {
		return "", fmt.Errorf("module %s has an unsupported command executor", mod.Name)
	}
	cacheKey := "command:" + mod.Name
	if bin, ok := a.hookBins[cacheKey]; ok {
		return bin, nil
	}
	dir, err := filepath.Abs(filepath.Join(a.base, "command-bin"))
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", err
	}
	bin := filepath.Join(dir, mod.Name)
	prebuilt := filepath.Join(mod.SourceDir, "command", "bin", runtime.GOOS+"-"+runtime.GOARCH, "anas-module-command")
	if exists(prebuilt) {
		if err := copyFileMode(prebuilt, bin, 0755); err != nil {
			return "", err
		}
	} else {
		cacheDir, err := filepath.Abs(filepath.Join(a.base, "go-build-cache"))
		if err != nil {
			return "", err
		}
		build := exec.Command("go", "build", "-o", bin, command[2])
		build.Dir = mod.SourceDir
		build.Env = append(os.Environ(), "GOCACHE="+cacheDir)
		if proxy := strings.TrimSpace(a.env["GOPROXY_URL"]); proxy != "" {
			build.Env = append(build.Env, "GOPROXY="+proxy)
		}
		var stderr bytes.Buffer
		build.Stderr = &stderr
		if err := build.Run(); err != nil {
			if stderr.Len() > 0 {
				return "", fmt.Errorf("%s command executor build: %w: %s", mod.Name, err, strings.TrimSpace(stderr.String()))
			}
			return "", fmt.Errorf("%s command executor build: %w", mod.Name, err)
		}
	}
	if a.hookBins == nil {
		a.hookBins = map[string]string{}
	}
	a.hookBins[cacheKey] = bin
	return bin, nil
}

func (a *app) freezeModuleCommandBinary(mod Module, dir string) error {
	if len(mod.Commands) == 0 || len(mod.CommandExecutor.Command) == 1 {
		return nil
	}
	bin, err := a.ensureModuleCommandBinary(mod)
	if err != nil {
		return err
	}
	if err := copyFileMode(bin, filepath.Join(dir, commandBinaryName), 0755); err != nil {
		return err
	}
	return os.RemoveAll(filepath.Join(dir, "command"))
}

func frozenModuleCommandExecutor(mod Module, renderedDir string) (CommandExecutor, error) {
	if len(mod.Commands) == 0 {
		return CommandExecutor{}, nil
	}
	command := append([]string{}, mod.CommandExecutor.Command...)
	if len(command) == 3 && command[0] == "go" && command[1] == "run" {
		command = []string{"./" + commandBinaryName}
	}
	if len(command) != 1 {
		return CommandExecutor{}, fmt.Errorf("module %s has no frozen command executor", mod.Name)
	}
	path := filepath.Join(renderedDir, filepath.Clean(command[0]))
	digest, err := fileDigest(path)
	if err != nil {
		return CommandExecutor{}, fmt.Errorf("digest module %s command executor: %w", mod.Name, err)
	}
	return CommandExecutor{Command: command, Digest: digest}, nil
}

func cloneModuleCommands(in []ModuleCommand) []ModuleCommand {
	out := make([]ModuleCommand, 0, len(in))
	for _, command := range in {
		command.Env = append([]string{}, command.Env...)
		command.Secrets = append([]string{}, command.Secrets...)
		parameters := make([]ModuleCommandParameter, 0, len(command.Parameters))
		for _, parameter := range command.Parameters {
			parameter.Type.Enum = append([]string{}, parameter.Type.Enum...)
			parameters = append(parameters, parameter)
		}
		command.Parameters = parameters
		out = append(out, command)
	}
	return out
}
