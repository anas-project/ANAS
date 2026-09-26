package main

import (
	"bytes"
	"context"
	"errors"
	"os/exec"
	"time"
)

var errContainerHelper = errors.New("Forgejo container reconciliation could not be confirmed")

type helperOutput struct{ bytes.Buffer }

func (output *helperOutput) Write(body []byte) (int, error) {
	if len(body) > (64<<10)-output.Len() {
		return 0, errContainerHelper
	}
	return output.Buffer.Write(body)
}

// Hook-selected commands only: this is not an API for user-supplied argv.
// A shorter observation deadline propagates to the actual Docker client,
// rather than merely limiting the delay between unbounded inspect calls.
func boundedContainerHelper(parent context.Context, payload []byte, name string, args ...string) ([]byte, error) {
	if parent == nil {
		return nil, errContainerHelper
	}
	if err := parent.Err(); err != nil {
		return nil, err
	}
	budget := 45 * time.Second
	// One fixed, independently ID-bound graceful stop needs the production
	// controller's two-minute cleanup window. No arbitrary Docker command or
	// target can select this longer allowance; shorter parent deadlines win.
	if name == "docker" && len(args) == 4 && args[0] == "stop" && args[1] == "--time" && args[2] == "125" && containerID.MatchString(args[3]) {
		budget = 135 * time.Second
	}
	ctx, cancel := context.WithTimeout(parent, budget)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stdin = bytes.NewReader(payload)
	var output helperOutput
	cmd.Stdout, cmd.Stderr = &output, &output
	cmd.WaitDelay = 2 * time.Second
	if err := cmd.Run(); err != nil {
		clear(output.Bytes())
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, errContainerHelper
	}
	if ctx.Err() != nil {
		clear(output.Bytes())
		return nil, ctx.Err()
	}
	return output.Bytes(), nil
}
