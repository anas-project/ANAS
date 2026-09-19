package incusingresshost

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

type commandRunner struct {
	config backendConfig
}

func newCommandRunner(config backendConfig) (commandRunner, error) {
	for label, path := range map[string]string{"ip": config.Binaries.IP, "nft": config.Binaries.NFT, "conntrack": config.Binaries.Conntrack} {
		if err := validateBinary(path, config.fixture); err != nil {
			return commandRunner{}, fmt.Errorf("invalid trusted %s binary: %w", label, err)
		}
	}
	if err := validateDirectory(config.ReceiptDir, config.fixture); err != nil {
		return commandRunner{}, fmt.Errorf("invalid ingress receipt directory: %w", err)
	}
	return commandRunner{config: config}, nil
}

func (r commandRunner) run(ctx context.Context, binary string, argv []string, stdin []byte) error {
	_, err := r.execute(ctx, binary, argv, stdin)
	return err
}

func (r commandRunner) output(ctx context.Context, binary string, argv []string) ([]byte, error) {
	return r.execute(ctx, binary, argv, nil)
}

func (r commandRunner) execute(ctx context.Context, binary string, argv []string, stdin []byte) (out []byte, result error) {
	if ctx == nil {
		return nil, fmt.Errorf("host command requires a context")
	}
	ctx, cancel := context.WithTimeout(ctx, r.config.CommandTimeout)
	defer cancel()
	execPath, executable, err := prepareCommandPath(binary, r.config.fixture)
	if err != nil {
		return nil, err
	}
	if executable != nil {
		defer func() { result = errors.Join(result, executable.Close()) }()
	}
	invoke := func(arguments []string) error {
		cmd := exec.CommandContext(ctx, execPath, arguments...)
		if executable != nil {
			// The executable is explicitly mapped to fd 3 in the child. Never
			// depend on the parent's incidental descriptor numbering surviving
			// the subprocess fd shuffle.
			cmd.ExtraFiles = []*os.File{executable}
		}
		cmd.Env = []string{"PATH=/usr/sbin:/usr/bin:/sbin:/bin", "LC_ALL=C", "LANG=C"}
		cmd.Stdin = bytes.NewReader(stdin)
		cmd.WaitDelay = 250 * time.Millisecond
		stdout := &boundedWriter{limit: 1 << 20}
		stderr := &boundedWriter{limit: 4096}
		cmd.Stdout, cmd.Stderr = stdout, stderr
		if err := cmd.Run(); err != nil {
			if ctx.Err() != nil {
				return fmt.Errorf("host command timed out or was canceled: %w", ctx.Err())
			}
			return fmt.Errorf("host command failed")
		}
		if stdout.overflow || stderr.overflow {
			return fmt.Errorf("host command output exceeded limit")
		}
		out = stdout.buffer.Bytes()
		return nil
	}
	if !r.config.fixture && binary == r.config.Binaries.IP {
		arguments, namespaced, err := installedNamespaceArguments(argv, r.config.RouteNetNS)
		if err != nil {
			return nil, err
		}
		if namespaced {
			err = withInstalledNamespace(ctx, r.config, func() error { return invoke(arguments) })
			return out, err
		}
	}
	err = invoke(argv)
	return out, err
}

// Existing typed route builders mark namespace operations with -n. In
// production it is only a selector: strip it, then enter the independently
// verified opened kernel namespace. No namespace name/path reaches ip.
func installedNamespaceArguments(argv []string, installed string) ([]string, bool, error) {
	arguments := make([]string, 0, len(argv))
	found := false
	for i := 0; i < len(argv); i++ {
		if argv[i] == "-n" {
			if found || i+1 >= len(argv) || argv[i+1] != installed || installed == "" {
				return nil, false, fmt.Errorf("invalid installed namespace selector")
			}
			found = true
			i++
			continue
		}
		arguments = append(arguments, argv[i])
	}
	return arguments, found, nil
}

type boundedWriter struct {
	buffer   bytes.Buffer
	limit    int
	overflow bool
}

func (w *boundedWriter) Write(p []byte) (int, error) {
	remaining := w.limit - w.buffer.Len()
	if remaining > 0 {
		if len(p) < remaining {
			remaining = len(p)
		}
		_, _ = w.buffer.Write(p[:remaining])
	}
	if len(p) > remaining {
		w.overflow = true
	}
	return len(p), nil
}

func validateBinary(path string, fixture bool) error {
	if !filepath.IsAbs(path) || path == "/" {
		return fmt.Errorf("path must be absolute")
	}
	info, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("inspect binary: %w", err)
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0022 != 0 || info.Mode().Perm()&0111 == 0 {
		return fmt.Errorf("binary must be a non-writable regular executable")
	}
	if !fixture {
		return validateTrustedExecutable(path, info)
	}
	return nil
}

func validateDirectory(path string, fixture bool) error {
	if !filepath.IsAbs(path) || path == "/" {
		return fmt.Errorf("path must be absolute")
	}
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode().Perm()&0022 != 0 {
		return fmt.Errorf("directory must be private and not group/world writable")
	}
	if !fixture {
		return validateTrustedDirectory(path, info)
	}
	return nil
}

func sanitize(value string) string {
	if len(value) > 512 {
		value = value[:512]
	}
	out := make([]rune, 0, len(value))
	for _, c := range value {
		if c == '\n' || c == '\t' || c == '\r' || c >= 0x20 && c < 0x7f {
			out = append(out, c)
		}
	}
	if len(out) == 0 {
		return "no diagnostic"
	}
	return string(out)
}
