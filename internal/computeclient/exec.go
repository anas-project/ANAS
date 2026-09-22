package computeclient

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os/exec"
	"time"
)

// Resolve incus using the trusted consumer's PATH, but do not pass the
// parent's secrets, default Incus socket, proxies or loader overrides onward.
func (r execRunner) Run(ctx context.Context, stdin io.Reader, args ...string) ([]byte, error) {
	if ctx == nil {
		return nil, fmt.Errorf("compute command requires a context")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	child, cancel := context.WithCancel(ctx)
	defer cancel()
	cmd := exec.CommandContext(child, "incus", args...)
	cmd.Env = []string{
		"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin", "LC_ALL=C", "LANG=C",
		"HOME=" + r.configDir, "INCUS_CONF=" + r.configDir, "INCUS_PROJECT=" + r.project,
	}
	cmd.Stdin = stdin
	cmd.WaitDelay = time.Second
	stdout := &computeOutput{limit: 4 << 20, cancel: cancel}
	stderr := &computeOutput{limit: 64 << 10, cancel: cancel}
	cmd.Stdout, cmd.Stderr = stdout, stderr
	err := cmd.Run()
	if stdout.overflow || stderr.overflow {
		return nil, fmt.Errorf("compute command output exceeded its limit")
	}
	if ctx.Err() != nil {
		return nil, fmt.Errorf("compute command canceled: %w", ctx.Err())
	}
	if err != nil {
		// Never include argv, stderr, guest output or stdin in diagnostics.
		return nil, fmt.Errorf("incus %s failed: %w", firstArg(args), err)
	}
	return stdout.buffer.Bytes(), nil
}

// os/exec serializes writes to each stream and joins them before Run returns.
// Overflow cancels the child rather than buffering or discarding indefinitely.
type computeOutput struct {
	buffer   bytes.Buffer
	limit    int
	overflow bool
	cancel   context.CancelFunc
}

func (w *computeOutput) Write(p []byte) (int, error) {
	n := min(len(p), w.limit-w.buffer.Len())
	_, _ = w.buffer.Write(p[:n])
	if n != len(p) {
		w.overflow = true
		w.cancel()
	}
	return len(p), nil
}
