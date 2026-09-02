package runner

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"github.com/anas-project/ANAS/internal/deployment"
)

type HookConfig = deployment.HookConfig

func (a *app) subprocessContext() context.Context {
	if a != nil && a.commandContext != nil {
		return a.commandContext
	}
	return context.Background()
}

func hookSupportsPhase(hook HookConfig, phase string) bool {
	if len(hook.Command) == 0 {
		return false
	}
	if len(hook.Phases) == 0 {
		// Legacy v1 Hooks keep the lifecycle they were published with. validate
		// and credential lifecycle phases are opt-in because an old default/no-op
		// branch is not proof that validation or a credential transition ran.
		return phase != "validate" && !strings.HasPrefix(phase, "credential_")
	}
	return contains(hook.Phases, phase)
}

type hookRequest struct {
	ABI          string                   `json:"abi"`
	Phase        string                   `json:"phase"`
	Module       string                   `json:"module"`
	Workdir      string                   `json:"workdir"`
	Env          map[string]string        `json:"env"`
	Secrets      map[string]string        `json:"secrets"`
	LocalAccount *localAccountOperation   `json:"local_account,omitempty"`
	Credential   *credentialHookOperation `json:"credential,omitempty"`
}

// runCredentialHook is deliberately stricter and quieter than the general
// Hook path. Credential lifecycle stderr and arbitrary response fields are not
// propagated because either could contain a rejected candidate value.
func (a *app) runCredentialHook(mod Module, phase, workdir string, env map[string]string, credential deploymentCredential) (credentialHookResult, error) {
	handler := ""
	switch phase {
	case "credential_probe":
		handler = credential.Lifecycle.Probe
	case "credential_reconcile":
		handler = credential.Lifecycle.Reconcile
	case "credential_verify":
		handler = credential.Lifecycle.Verify
	default:
		return credentialHookResult{}, fmt.Errorf("credential %s requested unsupported hook phase %s", credential.ID, phase)
	}
	if handler == "" || !hookSupportsPhase(mod.Hook, phase) {
		return credentialHookResult{}, fmt.Errorf("module %s has no declared %s handler for credential %s", mod.Name, phase, credential.ID)
	}
	desired := env[credential.SecretKey]
	if desired == "" {
		return credentialHookResult{}, fmt.Errorf("credential %s desired projection is missing", credential.ID)
	}
	requestEnv := cloneMap(env)
	delete(requestEnv, credential.SecretKey)
	secrets := a.scopedSecrets(mod.Name)
	delete(secrets, credential.SecretKey)
	secrets[credentialDesiredSecretKey] = desired
	req := hookRequest{
		ABI: currentModuleABI, Phase: phase, Module: mod.Name, Workdir: workdir,
		Env: requestEnv, Secrets: secrets,
		Credential: &credentialHookOperation{
			Handler: handler, CredentialID: credential.ID, SecretKey: credential.SecretKey,
			DesiredSecretKey: credentialDesiredSecretKey, Authority: credential.Authority,
			Generation: credential.Generation,
		},
	}
	in, err := json.Marshal(req)
	if err != nil {
		return credentialHookResult{}, err
	}
	command, err := a.hookCommand(mod, workdir)
	if err != nil {
		return credentialHookResult{}, err
	}
	cmd := externalCommandContext(a.subprocessContext(), command[0], command[1:]...)
	cmd.Dir = mod.SourceDir
	cacheDir, err := filepath.Abs(filepath.Join(a.base, "go-build-cache"))
	if err != nil {
		return credentialHookResult{}, err
	}
	cmd.Env = a.commandEnvironment(map[string]string{"GOCACHE": cacheDir})
	cmd.Stdin = bytes.NewReader(in)
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = io.Discard
	if err := cmd.Run(); err != nil {
		return credentialHookResult{}, fmt.Errorf("%s hook %s failed for credential %s", mod.Name, phase, credential.ID)
	}
	decoder := json.NewDecoder(bytes.NewReader(stdout.Bytes()))
	decoder.DisallowUnknownFields()
	var resp hookResponse
	if err := decoder.Decode(&resp); err != nil {
		return credentialHookResult{}, fmt.Errorf("%s hook %s returned invalid credential response", mod.Name, phase)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return credentialHookResult{}, fmt.Errorf("%s hook %s returned more than one credential response", mod.Name, phase)
	}
	if fields := credentialHookMutationFields(resp); len(fields) > 0 {
		return credentialHookResult{}, fmt.Errorf("%s hook %s returned forbidden fields: %s", mod.Name, phase, strings.Join(fields, ", "))
	}
	if resp.Credential == nil || resp.Credential.CredentialID != credential.ID {
		return credentialHookResult{}, fmt.Errorf("%s hook %s did not identify credential %s", mod.Name, phase, credential.ID)
	}
	resp.Credential.Status = strings.ToLower(strings.TrimSpace(resp.Credential.Status))
	if !contains([]string{"match", "missing", "mismatch", "unavailable", "unsupported", "reconciled"}, resp.Credential.Status) {
		return credentialHookResult{}, fmt.Errorf("%s hook %s returned an invalid status for credential %s", mod.Name, phase, credential.ID)
	}
	return *resp.Credential, nil
}

func (a *app) runLocalAccountHook(mod Module, phase, workdir string, env map[string]string, operation localAccountOperation, secrets map[string]string) (hookResponse, error) {
	if len(mod.Hook.Command) == 0 {
		return hookResponse{}, fmt.Errorf("module %s has no hook for %s", mod.Name, operation.Handler)
	}
	if !hookSupportsPhase(mod.Hook, phase) {
		return hookResponse{}, fmt.Errorf("module %s hook does not declare phase %s for %s", mod.Name, phase, operation.Handler)
	}
	req := hookRequest{ABI: currentModuleABI, Phase: phase, Module: mod.Name, Workdir: workdir, Env: env, Secrets: secrets, LocalAccount: &operation}
	in, err := json.Marshal(req)
	if err != nil {
		return hookResponse{}, err
	}
	command, err := a.hookCommand(mod, workdir)
	if err != nil {
		return hookResponse{}, err
	}
	cmd := externalCommandContext(a.subprocessContext(), command[0], command[1:]...)
	cmd.Dir = mod.SourceDir
	cacheDir, err := filepath.Abs(filepath.Join(a.base, "go-build-cache"))
	if err != nil {
		return hookResponse{}, err
	}
	cmd.Env = a.commandEnvironment(map[string]string{"GOCACHE": cacheDir})
	cmd.Stdin = bytes.NewReader(in)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	if a.suppressSensitiveOutput {
		cmd.Stderr = io.Discard
	} else {
		cmd.Stderr = &stderr
	}
	if err := cmd.Run(); err != nil {
		if !a.suppressSensitiveOutput && stderr.Len() > 0 {
			return hookResponse{}, fmt.Errorf("%s hook %s: %w: %s", mod.Name, phase, err, strings.TrimSpace(stderr.String()))
		}
		return hookResponse{}, fmt.Errorf("%s hook %s: %w", mod.Name, phase, err)
	}
	if stdout.Len() == 0 {
		return hookResponse{}, nil
	}
	var resp hookResponse
	if err := json.Unmarshal(stdout.Bytes(), &resp); err != nil {
		return hookResponse{}, fmt.Errorf("%s hook %s returned invalid JSON: %w", mod.Name, phase, err)
	}
	return resp, nil
}

type hookResponse struct {
	Env     map[string]string `json:"env"`
	Secrets map[string]string `json:"secrets"`
	Files   map[string]string `json:"files"`
	// Plan is non-sensitive, read-only metadata a validate Hook wants Core to
	// expose in plan output and freeze into the deployment manifest. It is the
	// only validate response field besides Warnings that may be non-empty.
	Plan map[string]string `json:"plan"`
	// RuntimeFiles are mutable, reconstructable files that must never enter the
	// sealed deployment artifact. The runner writes them below the module's
	// deployment-scoped runtime-state directory and rebuilds them before start.
	RuntimeFiles    map[string]string `json:"runtime_files"`
	DisableServices []string          `json:"disable_services"`
	DockerCopies    []dockerCopy      `json:"docker_copies"`
	// InternalEnv lists env keys from this response that templates may use but
	// that must not be written into the rendered .env file.
	InternalEnv []string `json:"internal_env"`
	// Warnings report recoverable adaptation problems. They never make the hook
	// fail; the runner renders them according to the command's output mode.
	Warnings   []string              `json:"warnings"`
	Credential *credentialHookResult `json:"credential,omitempty"`
}

func credentialHookMutationFields(resp hookResponse) []string {
	fields := []string{}
	for _, field := range validationMutationFields(resp) {
		if field != "credential" {
			fields = append(fields, field)
		}
	}
	if len(resp.Plan) > 0 {
		fields = append(fields, "plan")
	}
	if len(resp.Warnings) > 0 {
		fields = append(fields, "warnings")
	}
	sort.Strings(fields)
	return fields
}

type dockerCopy struct {
	Source      string `json:"source"`
	Container   string `json:"container"`
	Destination string `json:"destination"`
}

func (a *app) runHook(mod Module, phase, workdir string, env map[string]string) (hookResponse, error) {
	if !hookSupportsPhase(mod.Hook, phase) {
		return hookResponse{}, nil
	}
	secrets := a.secrets.clone()
	if phase != "calculate" {
		// Only the calculate phase is a privileged derivation stage; the other
		// phases receive the module-scoped view.
		secrets = a.scopedSecrets(mod.Name)
	}
	req := hookRequest{
		ABI:     currentModuleABI,
		Phase:   phase,
		Module:  mod.Name,
		Workdir: workdir,
		Env:     env,
		Secrets: secrets,
	}
	in, err := json.Marshal(req)
	if err != nil {
		return hookResponse{}, err
	}
	command, err := a.hookCommand(mod, workdir)
	if err != nil {
		return hookResponse{}, err
	}
	cmd := externalCommandContext(a.subprocessContext(), command[0], command[1:]...)
	cmd.Dir = mod.SourceDir
	cacheDir, err := filepath.Abs(filepath.Join(a.base, "go-build-cache"))
	if err != nil {
		return hookResponse{}, err
	}
	cmd.Env = a.commandEnvironment(map[string]string{"GOCACHE": cacheDir})
	cmd.Stdin = bytes.NewReader(in)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	if a.suppressSensitiveOutput {
		cmd.Stderr = io.Discard
	} else {
		cmd.Stderr = &stderr
	}
	if err := cmd.Run(); err != nil {
		if !a.suppressSensitiveOutput && stderr.Len() > 0 {
			return hookResponse{}, fmt.Errorf("%s hook %s: %w: %s", mod.Name, phase, err, stderr.String())
		}
		return hookResponse{}, fmt.Errorf("%s hook %s: %w", mod.Name, phase, err)
	}
	if stdout.Len() == 0 {
		return hookResponse{}, nil
	}
	var resp hookResponse
	if err := json.Unmarshal(stdout.Bytes(), &resp); err != nil {
		return hookResponse{}, fmt.Errorf("%s hook %s returned invalid JSON: %w", mod.Name, phase, err)
	}
	if !a.suppressSensitiveOutput {
		for _, warning := range resp.Warnings {
			a.warning("module_localization_fallback", "%s hook %s: %s", mod.Name, phase, warning)
		}
	}
	return resp, nil
}

// runValidationHook is deliberately separate from runHook. Validation gets no
// Secret Store view, runs with a minimal process environment, and rejects
// response fields unknown to this runner so a newer Hook cannot accidentally
// smuggle a mutation through an older read-only boundary.
func (a *app) runValidationHook(mod Module, env map[string]string) (hookResponse, error) {
	if !hookSupportsPhase(mod.Hook, "validate") {
		return hookResponse{}, nil
	}
	req := hookRequest{
		ABI: currentModuleABI, Phase: "validate", Module: mod.Name,
		Env: env, Secrets: map[string]string{},
	}
	in, err := json.Marshal(req)
	if err != nil {
		return hookResponse{}, err
	}
	command, err := a.hookCommand(mod, "")
	if err != nil {
		return hookResponse{}, err
	}
	commandContext := a.subprocessContext()
	cmd := externalCommandContext(commandContext, command[0], command[1:]...)
	cmd.Dir = mod.SourceDir
	cacheDir, err := filepath.Abs(filepath.Join(a.base, "go-build-cache"))
	if err != nil {
		return hookResponse{}, err
	}
	cmd.Env = validationHookProcessEnv(cacheDir)
	cmd.Stdin = bytes.NewReader(in)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		if contextErr := commandContext.Err(); contextErr != nil {
			return hookResponse{}, contextErr
		}
		if stderr.Len() > 0 {
			return hookResponse{}, fmt.Errorf("%s hook validate: %w: %s", mod.Name, err, strings.TrimSpace(stderr.String()))
		}
		return hookResponse{}, fmt.Errorf("%s hook validate: %w", mod.Name, err)
	}
	if stdout.Len() == 0 {
		return hookResponse{}, nil
	}
	var resp hookResponse
	decoder := json.NewDecoder(bytes.NewReader(stdout.Bytes()))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&resp); err != nil {
		return hookResponse{}, fmt.Errorf("%s hook validate returned invalid JSON: %w", mod.Name, err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return hookResponse{}, fmt.Errorf("%s hook validate returned more than one JSON value", mod.Name)
		}
		return hookResponse{}, fmt.Errorf("%s hook validate returned invalid trailing JSON: %w", mod.Name, err)
	}
	return resp, nil
}

func validationHookProcessEnv(cacheDir string) []string {
	out := []string{"GOCACHE=" + cacheDir}
	for _, key := range []string{
		"PATH", "HOME", "TMPDIR", "TMP", "TEMP", "LANG", "LC_ALL",
		"ASDF_DATA_DIR", "ASDF_CONFIG_FILE", "MISE_DATA_DIR", "MISE_CONFIG_DIR", "PYENV_ROOT",
	} {
		if value := os.Getenv(key); value != "" {
			out = append(out, key+"="+value)
		}
	}
	return out
}

func validationHookBuildEnv(base, cacheDir, proxy string) []string {
	out := []string{"GOCACHE=" + cacheDir}
	for _, key := range []string{"PATH", "TMPDIR", "TMP", "TEMP", "LANG", "LC_ALL"} {
		if value := os.Getenv(key); value != "" {
			out = append(out, key+"="+value)
		}
	}
	out = append(out,
		"HOME="+filepath.Join(base, "home"),
		"GOMODCACHE="+filepath.Join(base, "go-module-cache"),
		"GOPATH="+filepath.Join(base, "go-path"),
		"GOENV=off",
	)
	if proxy = strings.TrimSpace(proxy); proxy != "" {
		out = append(out, "GOPROXY="+proxy)
	}
	return out
}

func validationToolchainDiscoveryEnv() []string {
	out := []string{"GOENV=off"}
	// Toolchain managers need their own location metadata to resolve a shim.
	// This discovery command runs no Module code; keep arbitrary tokens and
	// application environment out while allowing common asdf/mise/pyenv setups.
	for _, key := range []string{
		"PATH", "HOME", "ASDF_DATA_DIR", "ASDF_CONFIG_FILE",
		"MISE_DATA_DIR", "MISE_CONFIG_DIR", "PYENV_ROOT",
	} {
		if value := os.Getenv(key); value != "" {
			out = append(out, key+"="+value)
		}
	}
	return out
}

func resolveValidationGoBinary(buildGOROOT string) (string, error) {
	return resolveValidationGoBinaryContext(context.Background(), buildGOROOT)
}

func resolveValidationGoBinaryContext(ctx context.Context, buildGOROOT string) (string, error) {
	var discoveryErr error
	if goOnPath, err := exec.LookPath("go"); err == nil {
		cmd := externalCommandContext(ctx, goOnPath, "env", "GOROOT")
		cmd.Env = validationToolchainDiscoveryEnv()
		if output, err := cmd.Output(); err == nil {
			candidate := filepath.Join(strings.TrimSpace(string(output)), "bin", "go")
			if exists(candidate) {
				return candidate, nil
			}
			discoveryErr = fmt.Errorf("go env GOROOT returned unusable path %q", strings.TrimSpace(string(output)))
		} else if contextErr := ctx.Err(); contextErr != nil {
			return "", contextErr
		} else {
			discoveryErr = fmt.Errorf("discover host Go toolchain: %w", err)
		}
	} else {
		discoveryErr = fmt.Errorf("find host Go toolchain: %w", err)
	}
	// Source-tree development commonly has the build toolchain in the runtime
	// GOROOT. Treat it as a fallback only: packaged ANAS binaries may retain a
	// CI-machine GOROOT that does not exist on the target host.
	candidate := filepath.Join(buildGOROOT, "bin", "go")
	if exists(candidate) {
		return candidate, nil
	}
	return "", fmt.Errorf("validation Hook build has no usable Go toolchain (%v; build GOROOT candidate %s is unavailable)", discoveryErr, candidate)
}

// hookCommand resolves the command used to execute a module hook. Hooks
// declared as `go run <pkg>` are compiled once per run and executed as a
// binary instead of re-compiling for every phase. Artifact starts prefer the
// binary frozen into the rendered module so no Go toolchain is needed; render
// runs always compile from the current source so a stale frozen binary can
// never leak into a new release.
func (a *app) hookCommand(mod Module, workdir string) ([]string, error) {
	command := mod.Hook.Command
	if len(command) < 3 || command[0] != "go" || command[1] != "run" {
		return command, nil
	}
	if a.useFrozenHooks {
		if bin, err := filepath.Abs(filepath.Join(workdir, hookBinaryName)); err == nil && exists(bin) {
			return append([]string{bin}, command[3:]...), nil
		}
	}
	bin, err := a.ensureHookBinary(mod, command[2])
	if err != nil {
		return nil, err
	}
	return append([]string{bin}, command[3:]...), nil
}

const hookBinaryName = ".hook.bin"

func (a *app) ensureHookBinary(mod Module, pkg string) (string, error) {
	if bin, ok := a.hookBins[mod.Name]; ok {
		return bin, nil
	}
	dir, err := filepath.Abs(filepath.Join(a.base, "hook-bin"))
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", err
	}
	bin := filepath.Join(dir, mod.Name)
	prebuilt := filepath.Join(mod.SourceDir, "hook", "bin", runtime.GOOS+"-"+runtime.GOARCH, "anas-hook")
	if exists(prebuilt) {
		if err := copyFileMode(prebuilt, bin, 0755); err != nil {
			return "", err
		}
		if a.hookBins == nil {
			a.hookBins = map[string]string{}
		}
		a.hookBins[mod.Name] = bin
		return bin, nil
	}
	cacheDir, err := filepath.Abs(filepath.Join(a.base, "go-build-cache"))
	if err != nil {
		return "", err
	}
	goBinary := "go"
	if a.validationBuild {
		// Resolve shims before clearing HOME, then invoke the concrete compiler
		// with the private validation environment below.
		goBinary, err = resolveValidationGoBinaryContext(a.subprocessContext(), runtime.GOROOT())
		if err != nil {
			return "", fmt.Errorf("%s hook validation build: %w", mod.Name, err)
		}
		if err := os.MkdirAll(filepath.Join(a.base, "home"), 0700); err != nil {
			return "", err
		}
	}
	buildContext := a.subprocessContext()
	build := externalCommandContext(buildContext, goBinary, "build", "-o", bin, pkg)
	build.Dir = mod.SourceDir
	if a.validationBuild {
		build.Env = validationHookBuildEnv(a.base, cacheDir, a.env["GOPROXY_URL"])
	} else {
		buildEnv := map[string]string{"GOCACHE": cacheDir}
		if proxy := strings.TrimSpace(a.env["GOPROXY_URL"]); proxy != "" {
			buildEnv["GOPROXY"] = proxy
		}
		build.Env = a.commandEnvironment(buildEnv)
	}
	var stderr bytes.Buffer
	build.Stderr = &stderr
	if err := build.Run(); err != nil {
		if contextErr := buildContext.Err(); contextErr != nil {
			return "", contextErr
		}
		if stderr.Len() > 0 {
			return "", fmt.Errorf("%s hook build: %w: %s", mod.Name, err, stderr.String())
		}
		return "", fmt.Errorf("%s hook build: %w", mod.Name, err)
	}
	if a.hookBins == nil {
		a.hookBins = map[string]string{}
	}
	a.hookBins[mod.Name] = bin
	return bin, nil
}

// freezeHookBinary copies the compiled hook binary into the rendered module so
// the release stays runnable without a Go toolchain.
func (a *app) freezeHookBinary(mod Module, dir string) error {
	command := mod.Hook.Command
	if len(command) < 3 || command[0] != "go" || command[1] != "run" {
		return nil
	}
	bin, err := a.ensureHookBinary(mod, command[2])
	if err != nil {
		return err
	}
	if err := copyFileMode(bin, filepath.Join(dir, hookBinaryName), 0755); err != nil {
		return err
	}
	// The deployment carries the executable hook, not its Go source or the
	// platform bundle used during materialization.
	return os.RemoveAll(filepath.Join(dir, "hook"))
}

func invalidHookEnvKeys(patch map[string]string) []string {
	keys := make([]string, 0, len(patch))
	for key := range patch {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	invalid := []string{}
	for _, key := range keys {
		if !envKeyPattern.MatchString(key) {
			invalid = append(invalid, fmt.Sprintf("%q", key))
		}
	}
	return invalid
}

func applyHookEnv(env map[string]string, patch map[string]string) error {
	if invalid := invalidHookEnvKeys(patch); len(invalid) > 0 {
		return fmt.Errorf("hook returned invalid env keys: %s", strings.Join(invalid, ", "))
	}
	for key, value := range patch {
		env[key] = value
	}
	return nil
}

func applyHookFiles(dir string, files map[string]string) error {
	for rel, content := range files {
		if err := writeFileUnder(dir, rel, content); err != nil {
			return err
		}
	}
	return nil
}

func applyHookRuntimeFiles(dir string, files map[string]string) error {
	for rel, content := range files {
		if err := writeRuntimeFileUnder(dir, rel, content); err != nil {
			return err
		}
	}
	return nil
}

func (a *app) runDockerCopies(copies []dockerCopy) error {
	for _, cp := range copies {
		if cp.Source == "" || cp.Container == "" || cp.Destination == "" {
			continue
		}
		target := cp.Container + ":" + cp.Destination
		cmd := externalCommandContext(a.subprocessContext(), "docker", "cp", cp.Source, target)
		cmd.Env = a.commandEnvironment(nil)
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("docker cp %s %s: %w", cp.Source, target, err)
		}
	}
	return nil
}

func remove(in []string, names ...string) []string {
	skip := map[string]bool{}
	for _, n := range names {
		skip[n] = true
	}
	out := []string{}
	for _, n := range in {
		if !skip[n] {
			out = append(out, n)
		}
	}
	return out
}

func writeFileUnder(root, rel, content string) error {
	if filepath.IsAbs(rel) || strings.Contains(rel, "..") {
		return fmt.Errorf("invalid hook file path %q", rel)
	}
	path := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	// Hook files are configuration assets mounted into service containers (for
	// example PostgreSQL init scripts), so they must be readable by the in-image
	// service user. Generated secrets use the separate secret store, not this
	// path.
	return os.WriteFile(path, []byte(content), 0644)
}

func writeRuntimeFileUnder(root, rel, content string) error {
	if filepath.IsAbs(rel) || strings.Contains(rel, "..") {
		return fmt.Errorf("invalid hook runtime file path %q", rel)
	}
	path := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(content), 0600)
}
