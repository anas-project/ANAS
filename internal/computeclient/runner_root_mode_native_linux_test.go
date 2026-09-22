//go:build linux

package computeclient

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"
)

// Explicit A/B diagnostic on the known faulty lab-r5 candidate only. It repairs
// root traversal and the engine account's primary group in one disposable guest,
// never the image artifact, host root,
// project policy or a production admission gate. A passing control is not a
// passing immutable-image smoke test; the corrected recipe still needs a bake.
func TestNativeRunnerRootAndGroupControl(t *testing.T) {
	if os.Getenv("ANAS_REQUIRE_INCUS_ROOT_MODE_CONTROL") != "1" {
		t.Skip("requires an explicit disposable guest root-mode control")
	}
	const root = "/run/anas-incus-runner-image"
	const broken = "75ec84539232bd9e1d24ac55cd8b6f7a68a65ce1a4a9ead283afe8bf509d58c5"
	identity, err := os.ReadFile("/var/lib/cloud/data/instance-id")
	vendor, ve := os.ReadFile("/sys/class/dmi/id/sys_vendor")
	if err != nil || ve != nil || os.Geteuid() != 0 || strings.TrimSpace(string(identity)) != "anas-runner-bake-uefkek" || strings.TrimSpace(string(vendor)) != "QEMU" {
		t.Fatal("exact root-mode diagnostic VM required")
	}
	for _, path := range []string{"/var/run/docker.sock", "/var/lib/docker"} {
		if _, err := os.Lstat(path); !os.IsNotExist(err) {
			t.Fatal("business Docker must be absent")
		}
	}
	info, err := os.Lstat(root + "/lease.json")
	if err != nil || !credentialOwned(info, false) || info.Size() > 64<<10 {
		t.Fatal("protected diagnostic lease required")
	}
	body, err := os.ReadFile(root + "/lease.json")
	var lease Lease
	if err != nil || json.Unmarshal(body, &lease) != nil || lease.Validate() != nil || lease.Sandbox != "anas-runner-image" || lease.Endpoint != "https://127.0.0.1:8443" || lease.Interface != InterfaceContainer || len(lease.ImageAllowlist) != 1 || lease.ImageAllowlist[0] != broken {
		t.Fatal("control requires the exact known faulty candidate")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	c, err := NewWithContext(ctx, lease, []string{"/usr/local/libexec/anas-forgejo-runner-start"}, root+"/root-control-client")
	if err != nil {
		t.Fatal(err)
	}
	const id = "anas-bake-job"
	t.Cleanup(func() {
		clean, stop := context.WithTimeout(context.Background(), time.Minute)
		defer stop()
		if err := c.Delete(clean, id); err != nil {
			t.Error("diagnostic guest cleanup failed", err)
		}
	})
	if err := c.Create(ctx, InstanceSpec{ID: id, Image: broken, WorkloadID: "root-mode-control", CPU: 1, MemoryMiB: 768, DiskGiB: 8}); err != nil {
		t.Fatal(err)
	}
	if err := c.Start(ctx, id); err != nil {
		t.Fatal(err)
	}
	if err := c.WaitForGuest(ctx, id, time.Second); err != nil {
		t.Fatal(err)
	}
	guest := func(args ...string) ([]byte, error) {
		return c.run.Run(ctx, nil, append([]string{"exec", remoteName + ":" + id, "--"}, args...)...)
	}
	mode, err := guest("/usr/bin/stat", "-c", "%a", "/")
	if err != nil || strings.TrimSpace(string(mode)) != "700" {
		t.Fatal("known root-mode defect was not reproduced")
	}
	if _, err := guest("/usr/sbin/runuser", "-u", "runner-engine", "--", "/usr/bin/test", "-x", "/"); err == nil {
		t.Fatal("unprivileged root traversal unexpectedly succeeded before repair")
	}
	if _, err := guest("/bin/chmod", "0755", "/"); err != nil {
		t.Fatal("disposable guest root-mode control failed", err)
	}
	if _, err := guest("/usr/sbin/runuser", "-u", "runner-engine", "--", "/usr/bin/test", "-x", "/"); err != nil {
		t.Fatal("root traversal was not repaired", err)
	}
	gid, err := guest("/usr/bin/id", "-g", "runner-engine")
	if err != nil || strings.TrimSpace(string(gid)) != "1002" {
		t.Fatal("known passwd/service group mismatch was not reproduced")
	}
	if _, err := guest("/usr/bin/systemctl", "stop", "anas-podman.service"); err != nil {
		t.Fatal("diagnostic engine stop failed", err)
	}
	if _, err := guest("/usr/sbin/usermod", "--gid", "actions-engine", "runner-engine"); err != nil {
		t.Fatal("disposable guest primary group control failed", err)
	}
	gid, err = guest("/usr/bin/id", "-g", "runner-engine")
	if err != nil || strings.TrimSpace(string(gid)) != "1003" {
		t.Fatal("engine primary group was not aligned")
	}
	if _, err := guest("/usr/bin/systemctl", "reset-failed", "systemd-networkd.service", "systemd-resolved.service", "anas-podman.service"); err != nil {
		t.Fatal(err)
	}
	if _, err := guest("/usr/bin/systemctl", "start", "--no-block", "systemd-networkd.service", "systemd-resolved.service", "anas-podman.service"); err != nil {
		t.Fatal(err)
	}
	ready, stop := context.WithTimeout(ctx, 35*time.Second)
	defer stop()
	_, err = waitForRunnerEngine(ready, func(call context.Context) ([]byte, error) {
		return c.run.Run(call, nil, "exec", remoteName+":"+id, "--", "/usr/sbin/runuser", "-u", "runner-agent", "--",
			"/usr/bin/env", "-i", "PATH=/usr/bin:/bin", "HOME=/home/runner-agent", "/usr/bin/podman", "--remote", "--url=unix:///run/anas-podman/podman.sock", "info", "--format=json")
	}, time.Second)
	if err != nil {
		journal, e := guest("/usr/bin/journalctl", "-b", "--no-pager", "--output=cat", "--lines=30", "--unit=anas-podman.service")
		if e == nil {
			t.Logf("post_root_repair_engine_observation=%v", runnerEngineJournalSummary(journal))
		}
		t.Fatal("root and group repaired but engine still unavailable", err)
	}
	t.Log("root/group control reached a real rootless engine; immutable image admission is separate")
}
