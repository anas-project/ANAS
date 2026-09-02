package compose

import (
	"bytes"
	"context"
	"fmt"
	"io"
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

// DetectContext is the service-facing detector: cancellation reaches the
// probe and the caller supplies the complete environment instead of inheriting
// the daemon process.
func DetectContext(ctx context.Context, environment []string) (CLI, error) {
	for _, candidate := range []struct {
		bin  string
		args []string
		cli  CLI
	}{
		{bin: "docker", args: []string{"compose", "version"}, cli: CLI{Bin: []string{"docker", "compose"}}},
		{bin: "docker-compose", args: []string{"-v"}, cli: CLI{Bin: []string{"docker-compose"}}},
	} {
		cmd := exec.CommandContext(ctx, candidate.bin, candidate.args...)
		cmd.Env = append([]string(nil), environment...)
		if err := cmd.Run(); err == nil {
			return candidate.cli, nil
		}
		if err := ctx.Err(); err != nil {
			return CLI{}, err
		}
	}
	return CLI{}, fmt.Errorf("docker compose is not installed")
}

func (c CLI) Run(dir, project string, env map[string]string, args ...string) error {
	return c.RunFile(dir, project, "", env, args...)
}

func (c CLI) RunFile(dir, project, composeFile string, env map[string]string, args ...string) error {
	cmd := c.fileCommand(dir, project, composeFile, env, args...)
	// Compose's chatter is a log, not this command's result. Sending it to
	// stdout put container-pull lines in front of the JSON document that
	// docs/contracts/README.md promises is the only thing on stdout, so a
	// caller could not parse the output of any command that starts containers.
	// stderr is where the contract already puts progress and logs.
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// RunFileQuiet is used while candidate credential values exist in a Compose
// environment. Compose output is not trusted to keep those values redacted;
// callers receive only the process exit status.
func (c CLI) RunFileQuiet(dir, project, composeFile string, env map[string]string, args ...string) error {
	cmd := c.fileCommand(dir, project, composeFile, env, args...)
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	return cmd.Run()
}

func (c CLI) RunFileContext(ctx context.Context, dir, project, composeFile string, environment []string, stdout, stderr io.Writer, args ...string) error {
	cmd := c.fileCommandContext(ctx, dir, project, composeFile, environment, args...)
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	return cmd.Run()
}

func (c CLI) fileCommand(dir, project, composeFile string, env map[string]string, args ...string) *exec.Cmd {
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
	return cmd
}

func (c CLI) fileCommandContext(ctx context.Context, dir, project, composeFile string, environment []string, args ...string) *exec.Cmd {
	full := append([]string{}, c.Bin...)
	full = append(full, "--project-name", project, "--env-file", ".env")
	if composeFile != "" {
		full = append(full, "--file", composeFile)
	}
	full = append(full, args...)
	cmd := exec.CommandContext(ctx, full[0], full[1:]...)
	cmd.Dir = dir
	cmd.Env = append([]string(nil), environment...)
	return cmd
}

func (c CLI) Output(dir, project string, env map[string]string, args ...string) (string, error) {
	return c.OutputFile(dir, project, "", env, args...)
}

func (c CLI) OutputFile(dir, project, composeFile string, env map[string]string, args ...string) (string, error) {
	return c.outputFile(dir, project, composeFile, env, false, args...)
}

// OutputFileQuiet retains machine-readable stdout (for example `config
// --services`) while discarding untrusted stderr during credential handling.
func (c CLI) OutputFileQuiet(dir, project, composeFile string, env map[string]string, args ...string) (string, error) {
	return c.outputFile(dir, project, composeFile, env, true, args...)
}

func (c CLI) OutputFileContext(ctx context.Context, dir, project, composeFile string, environment []string, quiet bool, args ...string) (string, error) {
	cmd := c.fileCommandContext(ctx, dir, project, composeFile, environment, args...)
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	if quiet {
		cmd.Stderr = io.Discard
	} else {
		cmd.Stderr = &errb
	}
	err := cmd.Run()
	if err != nil {
		if contextErr := ctx.Err(); contextErr != nil {
			return out.String(), contextErr
		}
		if quiet {
			return out.String(), err
		}
		return out.String(), fmt.Errorf("%w: %s", err, strings.TrimSpace(errb.String()))
	}
	return out.String(), nil
}

func (c CLI) outputFile(dir, project, composeFile string, env map[string]string, quiet bool, args ...string) (string, error) {
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
	if quiet {
		cmd.Stderr = io.Discard
	} else {
		cmd.Stderr = &errb
	}
	err := cmd.Run()
	if err != nil {
		if quiet {
			return out.String(), err
		}
		return out.String(), fmt.Errorf("%w: %s", err, strings.TrimSpace(errb.String()))
	}
	return out.String(), nil
}
