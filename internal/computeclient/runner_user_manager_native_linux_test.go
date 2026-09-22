//go:build linux

package computeclient

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// Controlled replacement in one disposable guest, not immutable-image proof.
// The wrapper reuses the complete gate, including effective OCI limits.
func TestNativeRunnerUserManagerControl(t *testing.T) {
	if os.Getenv("ANAS_REQUIRE_INCUS_USER_MANAGER_CONTROL") != "1" {
		t.Skip("requires explicit disposable user-manager control")
	}
	t.Setenv("ANAS_REQUIRE_INCUS_RUNNER_IMAGE_NATIVE", "1")
	TestNativeBakedForgejoRunnerImage(t)
	t.Log("user_manager_control=true; immutable_image_admission=false")
}

func applyNativeUserManagerControl(t *testing.T, ctx context.Context, c *Client, id string, lease Lease) {
	t.Helper()
	identity, err := os.ReadFile("/var/lib/cloud/data/instance-id")
	if err != nil || strings.TrimSpace(string(identity)) != "anas-runner-bake-uefkek" || len(lease.ImageAllowlist) != 1 || lease.ImageAllowlist[0] != "bc94b374754c6163212ac41590ef74730cfb9bb102d70422a0cff5b9d0535b79" {
		t.Fatal("user-manager control requires its exact VM and original candidate")
	}
	ready, cancel := context.WithTimeout(ctx, 35*time.Second)
	defer cancel()
	if _, err := waitForRunnerEngine(ready, func(call context.Context) ([]byte, error) {
		return c.run.Run(call, nil, "exec", remoteName+":"+id, "--", "/usr/sbin/runuser", "-u", "runner-agent", "--",
			"/usr/bin/env", "-i", "PATH=/usr/bin:/bin", "HOME=/home/runner-agent", "/usr/bin/podman", "--remote",
			"--url=unix:///run/anas-podman/podman.sock", "info", "--format=json")
	}, time.Second); err != nil {
		t.Fatal("original engine did not finish boot before controlled replacement", err)
	}
	stage := 0
	command := func(input []byte, args ...string) {
		t.Helper()
		stage++
		t.Logf("user_manager_control_stage=%d operation=%s", stage, strings.Join(args[:min(2, len(args))], " "))
		// Diagnostic-only root CLI in the already verified empty test VM. These
		// fixed commands install public unit files before any token is issued.
		cmd := exec.CommandContext(ctx, "incus", append([]string{"--force-local", "--project", lease.Sandbox, "exec", id, "--"}, args...)...)
		cmd.Env = []string{"PATH=/usr/sbin:/usr/bin:/sbin:/bin", "HOME=/run/anas-incus-runner-image/admin"}
		cmd.Stdin = bytes.NewReader(input)
		var output nativeUserManagerOutput
		cmd.Stdout, cmd.Stderr = &output, &output
		if err := cmd.Run(); err != nil {
			t.Logf("public_unit_control_failure=%q", output.String())
			t.Fatalf("controlled guest user-manager stage %d failed: %v", stage, err)
		}
	}
	command(nil, "/usr/bin/systemctl", "disable", "--now", "anas-podman.service", "anas-podman.socket")
	command(nil, "/usr/bin/systemctl", "stop", "user@1002.service")
	command(nil, "/usr/bin/install", "-d", "-m", "0755", "/usr/lib/systemd/user", "/etc/systemd/system/user@1002.service.d")
	for name, dest := range map[string]string{
		"anas-podman.service":   "/usr/lib/systemd/user/anas-podman.service",
		"anas-podman.socket":    "/usr/lib/systemd/user/anas-podman.socket",
		"anas-engine-user.conf": "/etc/systemd/system/user@1002.service.d/anas-engine.conf",
	} {
		path := "/run/anas-incus-runner-image/user-manager-control/" + name
		info, err := os.Lstat(path)
		if err != nil || !credentialOwned(info, false) || info.Size() > 64<<10 {
			t.Fatal("private operator-owned control asset required")
		}
		body, err := os.ReadFile(path)
		if err != nil || len(body) == 0 {
			t.Fatal("missing control asset")
		}
		command(body, "/usr/bin/tee", dest)
		command(nil, "/usr/bin/chmod", "0644", dest)
	}
	const enabled = "/home/runner-engine/.config/systemd/user/sockets.target.wants"
	command(nil, "/usr/bin/install", "-d", "-o", "runner-engine", "-g", "actions-engine", "-m", "0700", "/home/runner-engine/.config", "/home/runner-engine/.config/systemd", "/home/runner-engine/.config/systemd/user", enabled)
	command(nil, "/usr/bin/ln", "-s", "/usr/lib/systemd/user/anas-podman.socket", enabled+"/anas-podman.socket")
	command(nil, "/usr/bin/chown", "-h", "runner-engine:actions-engine", enabled+"/anas-podman.socket")
	command(nil, "/usr/bin/systemctl", "daemon-reload")
	command(nil, "/usr/bin/systemctl", "start", "user@1002.service")
	t.Log("user_manager_control_applied; no Runner token issued")
}

type nativeUserManagerOutput struct{ bytes.Buffer }

func (out *nativeUserManagerOutput) Write(body []byte) (int, error) {
	_, _ = out.Buffer.Write(body[:min(len(body), 4096-out.Len())])
	return len(body), nil
}
