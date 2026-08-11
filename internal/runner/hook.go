package runner

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

type HookConfig struct {
	Command []string `yaml:"command" json:"command"`
}

type hookRequest struct {
	ABI     string            `json:"abi"`
	Phase   string            `json:"phase"`
	Module  string            `json:"module"`
	Workdir string            `json:"workdir"`
	Env     map[string]string `json:"env"`
	Secrets map[string]string `json:"secrets"`
}

type hookResponse struct {
	Env             map[string]string `json:"env"`
	Secrets         map[string]string `json:"secrets"`
	Files           map[string]string `json:"files"`
	DisableServices []string          `json:"disable_services"`
	DockerCopies    []dockerCopy      `json:"docker_copies"`
	// InternalEnv lists env keys from this response that templates may use but
	// that must not be written into the rendered .env file.
	InternalEnv []string `json:"internal_env"`
}

type dockerCopy struct {
	Source      string `json:"source"`
	Container   string `json:"container"`
	Destination string `json:"destination"`
}

func (a *app) runHook(mod Module, phase, workdir string, env map[string]string) (hookResponse, error) {
	if len(mod.Hook.Command) == 0 {
		return hookResponse{}, nil
	}
	secrets := a.secrets.clone()
	if phase != "calculate" {
		// Only the calculate phase is a privileged derivation stage; the other
		// phases receive the cask-scoped view.
		secrets = a.scopedSecrets(mod.Name)
	}
	req := hookRequest{
		ABI:     currentCaskABI,
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
	cmd := exec.Command(command[0], command[1:]...)
	cmd.Dir = mod.SourceDir
	cacheDir, err := filepath.Abs(filepath.Join(a.base, "go-build-cache"))
	if err != nil {
		return hookResponse{}, err
	}
	cmd.Env = append(os.Environ(), "GOCACHE="+cacheDir)
	cmd.Stdin = bytes.NewReader(in)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if stderr.Len() > 0 {
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
	return resp, nil
}

// hookCommand resolves the command used to execute a cask hook. Hooks
// declared as `go run <pkg>` are compiled once per run and executed as a
// binary instead of re-compiling for every phase. Artifact starts prefer the
// binary frozen into the rendered cask so no Go toolchain is needed; render
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
	build := exec.Command("go", "build", "-o", bin, pkg)
	build.Dir = mod.SourceDir
	build.Env = append(os.Environ(), "GOCACHE="+cacheDir)
	if proxy := strings.TrimSpace(a.env["GOPROXY_URL"]); proxy != "" {
		build.Env = append(build.Env, "GOPROXY="+proxy)
	}
	var stderr bytes.Buffer
	build.Stderr = &stderr
	if err := build.Run(); err != nil {
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

// freezeHookBinary copies the compiled hook binary into the rendered cask so
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

func applyHookEnv(env map[string]string, patch map[string]string) {
	for k, v := range patch {
		env[k] = v
	}
}

func applyHookFiles(dir string, files map[string]string) error {
	for rel, content := range files {
		if err := writeFileUnder(dir, rel, content); err != nil {
			return err
		}
	}
	return nil
}

func runDockerCopies(copies []dockerCopy) error {
	for _, cp := range copies {
		if cp.Source == "" || cp.Container == "" || cp.Destination == "" {
			continue
		}
		target := cp.Container + ":" + cp.Destination
		if err := exec.Command("docker", "cp", cp.Source, target).Run(); err != nil {
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
