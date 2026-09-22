//go:build linux

package computeclient

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"
)

func TestNativeRunnerUserConfigControl(t *testing.T) {
	if os.Getenv("ANAS_REQUIRE_INCUS_USER_CONFIG_CONTROL") != "1" {
		t.Skip("requires explicit cold-boot config control")
	}
	t.Setenv("ANAS_REQUIRE_INCUS_RUNNER_IMAGE_NATIVE", "1")
	TestNativeBakedForgejoRunnerImage(t)
	t.Log("user_config_control=true; immutable_image_admission=false")
}

func applyNativeUserConfigControl(t *testing.T, ctx context.Context, c *Client, id string, lease Lease) {
	t.Helper()
	identity, err := os.ReadFile("/var/lib/cloud/data/instance-id")
	if err != nil || strings.TrimSpace(string(identity)) != "anas-runner-bake-uefkek" || len(lease.ImageAllowlist) != 1 || lease.ImageAllowlist[0] != "ce16caf4476cac89ff66fc654af26efdc512ad707a20acb0d53d91204abf52b8" {
		t.Fatal("exact cold-boot candidate required")
	}
	guest := func(args ...string) ([]byte, error) {
		return c.run.Run(ctx, nil, append([]string{"exec", remoteName + ":" + id, "--"}, args...)...)
	}
	userctl := func(args ...string) ([]byte, error) {
		return guest(append([]string{"/usr/sbin/runuser", "-u", "runner-engine", "--", "/usr/bin/env", "-i", "PATH=/usr/bin:/bin", "HOME=/home/runner-engine", "XDG_RUNTIME_DIR=/run/user/1002", "DBUS_SESSION_BUS_ADDRESS=unix:path=/run/user/1002/bus", "/usr/bin/systemctl", "--user"}, args...)...)
	}
	for i := 0; i < 30; i++ {
		b, e := userctl("is-active", "default.target")
		if e == nil && strings.TrimSpace(string(b)) == "active" {
			break
		}
		if i == 29 {
			t.Fatal("cold boot user manager unavailable")
		}
		time.Sleep(time.Second)
	}
	const homeConfig = "/home/runner-engine/.config"
	b, err := guest("/usr/bin/stat", "-c", "%u:%g:%a", homeConfig)
	if err != nil || strings.TrimSpace(string(b)) != "0:0:755" {
		t.Fatal("original root-owned config not reproduced", err)
	}
	if _, err := guest("/usr/sbin/runuser", "-u", "runner-engine", "--", "/usr/bin/test", "-w", homeConfig); err == nil {
		t.Fatal("original engine config unexpectedly writable")
	}
	journal, e := guest("/usr/bin/journalctl", "-b", "--no-pager", "--output=cat", "--lines=30", "_SYSTEMD_USER_UNIT=anas-podman.service")
	if e == nil {
		t.Logf("cold_boot_user_journal_observation=%v", runnerEngineJournalSummary(journal))
	}
	if _, err := userctl("stop", "anas-podman.service", "anas-podman.socket"); err != nil {
		t.Fatal("stop control user units", err)
	}
	for _, dir := range []string{homeConfig, homeConfig + "/systemd", homeConfig + "/systemd/user", homeConfig + "/systemd/user/sockets.target.wants"} {
		if _, err := guest("/usr/bin/chown", "runner-engine:actions-engine", dir); err != nil {
			t.Fatal(err)
		}
		if _, err := guest("/usr/bin/chmod", "0700", dir); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := userctl("start", "anas-podman.socket"); err != nil {
		t.Fatal("start control user socket", err)
	}
	t.Log("cold_boot_config_control_applied; no unit, image or namespace policy modified")
}
