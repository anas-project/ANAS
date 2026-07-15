package runner

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

type HookConfig struct {
	Command []string `yaml:"command"`
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
	req := hookRequest{
		ABI:     currentCaskABI,
		Phase:   phase,
		Module:  mod.Name,
		Workdir: workdir,
		Env:     env,
		Secrets: a.secrets.clone(),
	}
	in, err := json.Marshal(req)
	if err != nil {
		return hookResponse{}, err
	}
	cmd := exec.Command(mod.Hook.Command[0], mod.Hook.Command[1:]...)
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
