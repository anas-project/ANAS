//go:build linux

package computeclient

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"strings"
	"testing"
	"time"
)

// This controlled repair of a disposable instance is a diagnostic, never an
// acceptance of the immutable image. No Runner credential is issued here.
func TestNativeRunnerCgroupManagerControl(t *testing.T) {
	if os.Getenv("ANAS_REQUIRE_INCUS_CGROUP_CONTROL") != "1" {
		t.Skip("requires the explicit disposable cgroup control")
	}
	const root = "/run/anas-incus-runner-image"
	identity, err := os.ReadFile("/var/lib/cloud/data/instance-id")
	vendor, ve := os.ReadFile("/sys/class/dmi/id/sys_vendor")
	if os.Geteuid() != 0 || err != nil || ve != nil || strings.TrimSpace(string(identity)) != "anas-runner-bake-uefkek" || strings.TrimSpace(string(vendor)) != "QEMU" {
		t.Fatal("exact isolated diagnostic VM required")
	}
	for _, p := range []string{"/var/lib/docker", "/var/run/docker.sock"} {
		if _, e := os.Lstat(p); !os.IsNotExist(e) {
			t.Fatal("Docker must be absent")
		}
	}
	info, err := os.Lstat(root + "/lease.json")
	if err != nil || !credentialOwned(info, false) || info.Size() > 64<<10 {
		t.Fatal("private diagnostic lease required")
	}
	body, err := os.ReadFile(root + "/lease.json")
	var lease Lease
	if err != nil || json.Unmarshal(body, &lease) != nil || lease.Validate() != nil || lease.Sandbox != "anas-runner-image" || lease.Endpoint != "https://127.0.0.1:8443" || lease.Interface != InterfaceContainer || len(lease.ImageAllowlist) != 1 || lease.ImageAllowlist[0] != "43bcbf3d19c3e369580e316c48e615776cb662d35288c18e44b2160be402075c" {
		t.Fatal("wrong diagnostic image or lease")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	c, err := NewWithContext(ctx, lease, []string{"/usr/local/libexec/anas-forgejo-runner-start"}, root+"/cgroup-control-client")
	if err != nil {
		t.Fatal(err)
	}
	const id = "anas-bake-job"
	t.Cleanup(func() {
		clean, done := context.WithTimeout(context.Background(), time.Minute)
		defer done()
		if err := c.Delete(clean, id); err != nil {
			t.Error(err)
		}
	})
	if err := c.Create(ctx, InstanceSpec{ID: id, Image: lease.ImageAllowlist[0], WorkloadID: "cgroup-diagnostic", CPU: 1, MemoryMiB: 768, DiskGiB: 8}); err != nil {
		t.Fatal(err)
	}
	if err := c.Start(ctx, id); err != nil {
		t.Fatal(err)
	}
	if err := c.WaitForGuest(ctx, id, time.Second); err != nil {
		t.Fatal(err)
	}
	guest := func(input io.Reader, args ...string) ([]byte, error) {
		return c.run.Run(ctx, input, append([]string{"exec", remoteName + ":" + id, "--"}, args...)...)
	}
	engine := func(args ...string) ([]byte, error) {
		return guest(nil, append([]string{"/usr/sbin/runuser", "-u", "runner-agent", "--", "/usr/bin/env", "-i", "PATH=/usr/bin:/bin", "HOME=/home/runner-agent", "/usr/bin/podman", "--remote", "--url=unix:///run/anas-podman/podman.sock"}, args...)...)
	}
	for i := 0; i < 30; i++ {
		b, e := engine("info", "--format=json")
		if e == nil && runnerEngineReady(b) {
			break
		}
		if i == 29 {
			t.Fatal("engine not ready")
		}
		time.Sleep(time.Second)
	}
	const image = "public.ecr.aws/docker/library/busybox@sha256:7a3ebe5bfd1a4a19797d20b0c0bb39d44393e9a03fd852c0865b0f540d868df0"
	start := func() {
		t.Helper()
		if _, err := engine("run", "--detach", "--name=anas-cgroup-control", "--cpus=1", "--memory=128m", "--pids-limit=32", "--security-opt=no-new-privileges", image, "sleep", "120"); err != nil {
			t.Fatal("diagnostic container start", err)
		}
	}
	start()
	if _, err := engine("exec", "anas-cgroup-control", "/bin/sh", "-c", "printf cgroup-exec-ok"); err == nil {
		t.Fatal("original cgroup exec failure not reproduced")
	}
	if _, err := engine("rm", "--force", "anas-cgroup-control"); err != nil {
		t.Fatal(err)
	}
	if _, err := guest(nil, "/usr/bin/systemctl", "stop", "anas-podman.service", "anas-podman.socket"); err != nil {
		t.Fatal(err)
	}
	if _, err := guest(nil, "/usr/bin/install", "-d", "-m", "0755", "/run/systemd/system/anas-podman.service.d"); err != nil {
		t.Fatal(err)
	}
	const override = "[Service]\nExecStart=\nExecStart=/usr/bin/podman --cgroup-manager=cgroupfs system service --time=0\n"
	if _, err := guest(strings.NewReader(override), "/usr/bin/tee", "/run/systemd/system/anas-podman.service.d/cgroup-control.conf"); err != nil {
		t.Fatal(err)
	}
	if _, err := guest(nil, "/usr/bin/systemctl", "daemon-reload"); err != nil {
		t.Fatal(err)
	}
	if _, err := guest(nil, "/usr/bin/systemctl", "start", "anas-podman.socket", "anas-podman.service"); err != nil {
		t.Fatal(err)
	}
	start()
	b, err := engine("exec", "anas-cgroup-control", "/bin/sh", "-c", "printf cgroup-exec-ok")
	if err != nil || string(b) != "cgroup-exec-ok" {
		t.Fatal("aligned cgroup exec still failed", err)
	}
	if _, err := engine("rm", "--force", "anas-cgroup-control"); err != nil {
		t.Fatal(err)
	}
	t.Log("diagnostic_cgroupfs_exec_passed; immutable_image_admission=false")
}
