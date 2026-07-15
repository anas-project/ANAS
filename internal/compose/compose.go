package compose

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

type CLI struct {
	Bin []string
}

func Detect() (CLI, error) {
	if err := exec.Command("docker", "compose", "version").Run(); err == nil {
		return CLI{Bin: []string{"docker", "compose"}}, nil
	}
	if err := exec.Command("docker-compose", "-v").Run(); err == nil {
		return CLI{Bin: []string{"docker-compose"}}, nil
	}
	return CLI{}, fmt.Errorf("docker compose is not installed")
}

func (c CLI) Run(dir, project string, env map[string]string, args ...string) error {
	return c.RunFile(dir, project, "", env, args...)
}

func (c CLI) RunFile(dir, project, composeFile string, env map[string]string, args ...string) error {
	full := append([]string{}, c.Bin...)
	full = append(full, "--project-name", project, "--env-file", ".env")
	if composeFile != "" {
		full = append(full, "--file", composeFile)
	}
	full = append(full, args...)
	cmd := exec.Command(full[0], full[1:]...)
	cmd.Dir = dir
	cmd.Env = os.Environ()
	for k, v := range env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func (c CLI) Output(dir, project string, env map[string]string, args ...string) (string, error) {
	return c.OutputFile(dir, project, "", env, args...)
}

func (c CLI) OutputFile(dir, project, composeFile string, env map[string]string, args ...string) (string, error) {
	full := append([]string{}, c.Bin...)
	full = append(full, "--project-name", project, "--env-file", ".env")
	if composeFile != "" {
		full = append(full, "--file", composeFile)
	}
	full = append(full, args...)
	cmd := exec.Command(full[0], full[1:]...)
	cmd.Dir = dir
	cmd.Env = os.Environ()
	for k, v := range env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	err := cmd.Run()
	if err != nil {
		return out.String(), fmt.Errorf("%w: %s", err, strings.TrimSpace(errb.String()))
	}
	return out.String(), nil
}
