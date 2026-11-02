package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
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
	ctx, cancel := context.WithTimeout(parent, containerHelperBudget(name, args))
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stdin = bytes.NewReader(payload)
	var output helperOutput
	cmd.Stdout, cmd.Stderr = &output, &output
	cmd.WaitDelay = 2 * time.Second
	if err := cmd.Run(); err != nil {
		failure := errContainerHelper
		if isOIDCContainerHelper(name, args) {
			failure = oidcHelperFailure(output.Bytes())
		}
		clear(output.Bytes())
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, failure
	}
	if ctx.Err() != nil {
		clear(output.Bytes())
		return nil, ctx.Err()
	}
	return output.Bytes(), nil
}

func containerHelperBudget(name string, args []string) time.Duration {
	// One fixed, independently ID-bound graceful stop needs the production
	// controller's two-minute cleanup window. No arbitrary Docker command or
	// target can select this longer allowance; shorter parent deadlines win.
	if name == "docker" && len(args) == 4 && args[0] == "stop" && args[1] == "--time" && args[2] == "125" && containerID.MatchString(args[3]) {
		return 135 * time.Second
	}
	// OIDC reconciliation initializes the upstream CLI three times (list,
	// add/update, verify). Full-stack startup on a constrained host exceeded
	// the ordinary observation budget. Only this fixed helper invocation gets
	// the larger allowance; caller deadlines still take precedence.
	if isOIDCContainerHelper(name, args) {
		return 180 * time.Second
	}
	return 45 * time.Second
}

func isOIDCContainerHelper(name string, args []string) bool {
	return name == "docker" && len(args) == 7 && args[0] == "exec" && args[1] == "-i" &&
		args[2] == "--user" && args[3] == "1000:1000" &&
		strings.HasSuffix(args[4], "forgejo") && containerPrefix.MatchString(strings.TrimSuffix(args[4], "forgejo")) &&
		args[5] == "/usr/local/bin/anas-forgejo-entrypoint" && args[6] == "oidc"
}

// Only exact, fixed entrypoint failures can cross this boundary. Native CLI,
// Docker and discovery response text may contain credentials and stays private.
func oidcHelperFailure(body []byte) error {
	for _, message := range []string{
		"list Forgejo authentication sources failed",
		"reconcile Forgejo OIDC source failed",
		"verify Forgejo OIDC source failed",
		"Forgejo OIDC source is missing, disabled, or has the wrong type after reconciliation",
	} {
		if string(body) == "anas-forgejo: "+message+"\n" {
			return fmt.Errorf("%w: %s", errContainerHelper, message)
		}
	}
	return errContainerHelper
}
