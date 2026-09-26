//go:build linux

package computeclient

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"
)

// This gate needs the explicitly authorized disposable-QEMU image harness.
// It boots actual baked bytes through the product client, never an installed
// host image or a fixture pretending to implement the Runner executable.
func TestNativeBakedForgejoRunnerImage(t *testing.T) {
	if os.Getenv("ANAS_REQUIRE_INCUS_RUNNER_IMAGE_NATIVE") != "1" {
		t.Skip("requires explicit disposable QEMU baked-image harness")
	}
	const root = "/run/anas-incus-runner-image"
	read := func(path string) []byte {
		t.Helper()
		info, err := os.Lstat(path)
		if err != nil || os.Geteuid() != 0 || !credentialOwned(info, false) || info.Size() > 64<<10 {
			t.Fatal("protected baked-image input required")
		}
		body, err := os.ReadFile(path)
		if err != nil {
			t.Fatal("baked-image input unavailable")
		}
		return body
	}
	identity := strings.TrimSpace(string(read(root + "/identity")))
	actual, err := os.ReadFile("/var/lib/cloud/data/instance-id")
	vendor, ve := os.ReadFile("/sys/class/dmi/id/sys_vendor")
	if err != nil || ve != nil || !strings.HasPrefix(identity, "anas-runner-bake-") || string(actual) != identity+"\n" || strings.TrimSpace(string(vendor)) != "QEMU" {
		t.Fatal("baked-image test requires its exact disposable VM")
	}
	for _, path := range []string{"/var/run/docker.sock", "/var/lib/docker"} {
		if _, err := os.Lstat(path); !os.IsNotExist(err) {
			t.Fatal("Docker must not be present in the image test VM")
		}
	}
	var lease Lease
	if json.Unmarshal(read(filepath.Join(root, "lease.json")), &lease) != nil || lease.Validate() != nil || lease.Endpoint != "https://127.0.0.1:8443" || lease.Sandbox != "anas-runner-image" || lease.Interface != InterfaceContainer || len(lease.ImageAllowlist) != 1 {
		t.Fatal("invalid baked-image lease")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	c, err := NewWithContext(ctx, lease, []string{"/usr/local/libexec/anas-forgejo-runner-start"}, root+"/client")
	if err != nil {
		t.Fatal(err)
	}
	const id = "anas-bake-job"
	t.Cleanup(func() {
		clean, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		if err := c.Delete(clean, id); err != nil {
			t.Error("baked-image cleanup unconfirmed", err)
		}
	})
	if err := c.Create(ctx, InstanceSpec{ID: id, Image: lease.ImageAllowlist[0], WorkloadID: "baked-image-smoke", CPU: 1, MemoryMiB: 768, DiskGiB: 8}); err != nil {
		t.Fatal("create baked image", err)
	}
	if err := c.Start(ctx, id); err != nil {
		t.Fatal("start baked image", err)
	}
	if err := c.WaitForGuest(ctx, id, time.Second); err != nil {
		t.Fatal("baked image has no executable guest entrypoint", err)
	}
	if os.Getenv("ANAS_REQUIRE_INCUS_USER_MANAGER_CONTROL") == "1" {
		applyNativeUserManagerControl(t, ctx, c, id, lease)
	}
	if os.Getenv("ANAS_REQUIRE_INCUS_USER_CONFIG_CONTROL") == "1" {
		applyNativeUserConfigControl(t, ctx, c, id, lease)
	}
	t.Run("provider-namespace-fence", func(t *testing.T) {
		for key, want := range map[string]string{"restricted.containers.nesting": "allow", "restricted.containers.privilege": "unprivileged", "restricted.containers.lowlevel": "block", "restricted.devices.disk": "block", "restricted.devices.unix-char": "block"} {
			body, err := c.run.Run(ctx, nil, "project", "get", remoteName+":"+lease.Sandbox, key)
			if err != nil || strings.TrimSpace(string(body)) != want {
				t.Fatalf("project fence %s mismatch: %v", key, err)
			}
		}
		body, err := c.run.Run(ctx, nil, "profile", "get", remoteName+":"+lease.Profile, "security.nesting")
		if err != nil || strings.TrimSpace(string(body)) != "true" {
			t.Fatal("provider profile does not own namespace nesting", err)
		}
		for _, args := range [][]string{
			{"config", "set", remoteName + ":" + id, "security.privileged=true"},
			{"config", "set", remoteName + ":" + id, "raw.lxc=lxc.log.level=ERROR"},
			{"config", "device", "add", remoteName + ":" + id, "forbidden-host-disk", "disk", "source=/usr/share/doc", "path=/mnt/host-doc", "readonly=true"},
			{"config", "device", "add", remoteName + ":" + id, "forbidden-host-char", "unix-char", "source=/dev/null", "path=/dev/anas-control"},
		} {
			if _, err := c.run.Run(ctx, nil, args...); err == nil {
				t.Fatal("daemon accepted a forbidden privileged/raw/device override")
			}
		}
	})
	// These are fixed read-only test diagnostics, not new consumer entrypoints.
	guest := func(args ...string) ([]byte, error) {
		return c.run.Run(ctx, nil, append([]string{"exec", remoteName + ":" + id, "--"}, args...)...)
	}
	t.Run("real-runner-binary", func(t *testing.T) {
		body, err := guest("/usr/local/bin/forgejo-runner", "--version")
		if err != nil || !strings.Contains(string(body), "forgejo-runner version") {
			t.Fatal("baked Runner binary did not execute", err)
		}
		t.Log(strings.TrimSpace(string(body)))
	})
	t.Run("one-job-interface", func(t *testing.T) {
		body, err := guest("/usr/local/bin/forgejo-runner", "one-job", "--help")
		if err != nil {
			t.Fatal("one-job interface unavailable", err)
		}
		for _, flag := range []string{"--handle", "--token-url", "--wait"} {
			if !strings.Contains(string(body), flag) {
				t.Error("one-job option missing", flag)
			}
		}
	})
	t.Run("runner-config-readable", func(t *testing.T) {
		if _, err := guest("/usr/sbin/runuser", "-u", "runner-agent", "--", "/usr/bin/test", "-r", "/etc/forgejo-runner/config.yml"); err != nil {
			t.Fatal("actual Runner account cannot read its public configuration", err)
		}
	})
	if !t.Run("engine-config-owned", func(t *testing.T) {
		for _, dir := range []string{"/home/runner-engine/.config", "/home/runner-engine/.config/systemd", "/home/runner-engine/.config/systemd/user", "/home/runner-engine/.config/systemd/user/sockets.target.wants"} {
			body, err := guest("/usr/bin/stat", "-c", "%u:%g:%a", dir)
			if err != nil || strings.TrimSpace(string(body)) != "1002:1003:700" {
				t.Fatal("engine private configuration parent has incorrect ownership or mode", err)
			}
		}
		if _, err := guest("/usr/sbin/runuser", "-u", "runner-engine", "--", "/usr/bin/test", "-w", "/home/runner-engine/.config"); err != nil {
			t.Fatal("engine cannot initialize its private configuration", err)
		}
	}) {
		return
	}
	if !t.Run("guest-podman-namespace-policy", func(t *testing.T) {
		const path = "/usr/share/anas/forgejo-runner/podman.apparmor"
		body, err := guest("/usr/bin/stat", "-c", "%u:%g:%a:%h", path)
		if err != nil || strings.TrimSpace(string(body)) != "0:0:644:1" {
			t.Fatal("guest namespace permission is not a fixed root-owned public asset", err)
		}
		body, err = guest("/usr/bin/cat", path)
		if err != nil || !strings.Contains(string(body), "profile anas-forgejo-podman /usr/bin/podman flags=(unconfined) {") ||
			!strings.Contains(string(body), "\n  userns,\n") {
			t.Fatal("expected executable-scoped guest namespace policy is missing", err)
		}
		// Older kernels without this mediation retain their existing behavior;
		// the policy is not installed through the global AppArmor autoloader.
		// On a mediating host, the actual boot loader must already have run
		// before the engine user manager. The test does not load it itself.
		mediation, readErr := os.ReadFile("/proc/sys/kernel/apparmor_restrict_unprivileged_userns")
		if readErr != nil && !os.IsNotExist(readErr) {
			t.Fatal("host namespace mediation is unknown", readErr)
		}
		if readErr == nil && strings.TrimSpace(string(mediation)) == "1" {
			for _, unit := range []string{"apparmor.service", "anas-forgejo-podman-policy.service"} {
				body, err = guest("/usr/bin/systemctl", "show", unit,
					"--property=ActiveState,SubState,Result,ExecMainStatus,ConditionResult")
				if err != nil {
					t.Fatal("read actual guest policy loader result", unit, err)
				}
				for _, line := range []string{"ActiveState=active", "SubState=exited", "Result=success", "ExecMainStatus=0", "ConditionResult=yes"} {
					if !strings.Contains("\n"+string(body), "\n"+line+"\n") {
						t.Fatal("guest policy loader did not complete before engine admission", unit, line)
					}
				}
			}
			body, err = guest("/usr/bin/systemctl", "show", "apparmor.service", "--property=ExecStart,ExecReload")
			if err != nil || strings.Contains(string(body), "apparmor.systemd") ||
				strings.Count(string(body), "argv[]=/usr/sbin/apparmor_parser --replace --skip-cache "+path) != 2 {
				t.Fatal("package boot/reload loader may introduce unrelated namespace grants", err)
			}
			// Preserve the product executor's rule that failing guest output is
			// never returned. A fixed native-only probe validates the exact errno
			// diagnostic inside the guest and emits one closed success marker.
			const denyProbe = `result=$(/usr/bin/unshare --user --map-root-user /usr/bin/true 2>&1); code=$?; [ "$code" -eq 1 ] && [ "$result" = 'unshare: unshare failed: Permission denied' ] && printf 'userns-denied\n'`
			body, err = guest("/usr/sbin/runuser", "-u", "runner-agent", "--", "/usr/bin/env", "-i", "PATH=/usr/bin:/bin", "LC_ALL=C", "/bin/sh", "-c", denyProbe)
			if err != nil || string(body) != "userns-denied\n" {
				t.Fatal("generic guest namespace refusal was not confirmed")
			}
			after, err := os.ReadFile("/proc/sys/kernel/apparmor_restrict_unprivileged_userns")
			if err != nil || string(after) != string(mediation) {
				t.Fatal("guest setup changed the outer host restriction")
			}
		}
	}) {
		return
	}
	engineReady := t.Run("rootless-engine-api", func(t *testing.T) {
		ready, done := context.WithTimeout(ctx, 35*time.Second)
		defer done()
		attempts, err := waitForRunnerEngine(ready, func(call context.Context) ([]byte, error) {
			return c.run.Run(call, nil, "exec", remoteName+":"+id, "--", "/usr/sbin/runuser", "-u", "runner-agent", "--",
				"/usr/bin/env", "-i", "PATH=/usr/bin:/bin", "HOME=/home/runner-agent",
				"/usr/bin/podman", "--remote", "--url=unix:///run/anas-podman/podman.sock", "info", "--format=json")
		}, time.Second)
		t.Logf("engine_api_probe_attempts=%d", attempts)
		if err != nil {
			// Only fixed read-only commands in the freshly booted test image.
			// Summaries are observations, not a diagnosis or a passing result.
			diagnostic, finish := context.WithTimeout(context.Background(), 20*time.Second)
			defer finish()
			userBody, userErr := c.run.Run(diagnostic, nil, "exec", remoteName+":"+id, "--", "/usr/sbin/runuser", "-u", "runner-engine", "--",
				"/usr/bin/env", "-i", "PATH=/usr/bin:/bin", "HOME=/home/runner-engine", "XDG_RUNTIME_DIR=/run/user/1002",
				"DBUS_SESSION_BUS_ADDRESS=unix:path=/run/user/1002/bus", "/usr/bin/systemctl", "--user", "show",
				"--property=ActiveState,SubState,Result,ExecMainStatus,ExecMainCode,NRestarts", "anas-podman.service")
			if userErr == nil {
				t.Logf("engine_user_unit_observation=%v", runnerEngineUnitSummary(userBody))
			}
			userBody, userErr = c.run.Run(diagnostic, nil, "exec", remoteName+":"+id, "--", "/usr/bin/journalctl", "-b", "--no-pager",
				"--output=cat", "--lines=40", "_SYSTEMD_USER_UNIT=anas-podman.service")
			if userErr == nil {
				t.Logf("engine_user_journal_observation=%v", runnerEngineJournalSummary(userBody))
			}
			body, readErr := c.run.Run(diagnostic, nil, "exec", remoteName+":"+id, "--", "/usr/bin/systemctl", "show",
				"--property=ActiveState,SubState,Result,ExecMainStatus,ExecMainCode,NRestarts", "anas-podman.service")
			if readErr == nil {
				t.Logf("engine_unit_observation=%v", runnerEngineUnitSummary(body))
			}
			body, readErr = c.run.Run(diagnostic, nil, "exec", remoteName+":"+id, "--", "/usr/bin/journalctl", "-b", "--no-pager",
				"--output=cat", "--lines=40", "--unit=anas-podman.service")
			if readErr == nil {
				t.Logf("engine_journal_observation=%v", runnerEngineJournalSummary(body))
			}
			body, readErr = c.run.Run(diagnostic, nil, "exec", remoteName+":"+id, "--", "/usr/bin/systemctl", "list-jobs",
				"--no-legend", "--no-pager", "--plain")
			if readErr == nil {
				t.Logf("engine_boot_jobs=%v", runnerEngineBootJobs(body))
			}
			for _, unit := range []string{"systemd-networkd.service", "systemd-networkd-wait-online.service", "networking.service", "systemd-sysusers.service"} {
				body, readErr = c.run.Run(diagnostic, nil, "exec", remoteName+":"+id, "--", "/usr/bin/systemctl", "show",
					"--property=ActiveState,SubState,Result,ExecMainStatus,ExecMainCode,NRestarts", unit)
				if readErr == nil {
					t.Logf("boot_dependency=%s observation=%v", unit, runnerEngineUnitSummary(body))
				}
			}
			body, readErr = c.run.Run(diagnostic, nil, "exec", remoteName+":"+id, "--", "/usr/bin/ps", "--no-headers", "-eo", "pid,comm,state,wchan:40")
			if readErr == nil {
				t.Logf("guest_boot_processes=%v", runnerEngineProcesses(body))
			}
			t.Fatal("Runner user cannot access a usable guest engine", err)
		}
	})
	if !engineReady {
		// Preserve the failed gate and cleanup, rather than hanging a second
		// operation on an API already proven unavailable. Missing required
		// subtests cannot be interpreted as successful admission by the harness.
		return
	}
	t.Run("rootless-user-session", func(t *testing.T) {
		body, err := guest("/usr/sbin/runuser", "-u", "runner-engine", "--", "/usr/bin/env", "-i",
			"PATH=/usr/bin:/bin", "HOME=/home/runner-engine", "XDG_RUNTIME_DIR=/run/user/1002",
			"DBUS_SESSION_BUS_ADDRESS=unix:path=/run/user/1002/bus", "/usr/bin/systemctl", "--user", "is-active", "default.target")
		if err != nil || strings.TrimSpace(string(body)) != "active" {
			t.Fatal("rootless engine user manager/bus unavailable", err)
		}
	})
	t.Run("rootless-oci-exec-limits", func(t *testing.T) {
		// A successful info request is insufficient: OCI exec must join the
		// same delegated cgroup as create, with its limits still enforced.
		// This uses the immutable image without service or profile overrides.
		engine := func(call context.Context, args ...string) ([]byte, error) {
			argv := []string{"exec", remoteName + ":" + id, "--", "/usr/sbin/runuser", "-u", "runner-agent", "--",
				"/usr/bin/env", "-i", "PATH=/usr/bin:/bin", "HOME=/home/runner-agent", "/usr/bin/podman",
				"--remote", "--url=unix:///run/anas-podman/podman.sock"}
			return c.run.Run(call, nil, append(argv, args...)...)
		}
		const name = "anas-immutable-cgroup-check"
		const image = "public.ecr.aws/docker/library/busybox@sha256:7a3ebe5bfd1a4a19797d20b0c0bb39d44393e9a03fd852c0865b0f540d868df0"
		t.Cleanup(func() {
			clean, done := context.WithTimeout(context.Background(), 20*time.Second)
			defer done()
			if _, err := engine(clean, "rm", "--force", "--ignore", name); err != nil {
				t.Error("inner diagnostic container cleanup unconfirmed", err)
			}
		})
		if _, err := engine(ctx, "run", "--detach", "--name="+name, "--cpus=0.5", "--memory=128m", "--pids-limit=32",
			"--security-opt=no-new-privileges", image, "sleep", "120"); err != nil {
			t.Fatal("immutable image cannot start a limited OCI container", err)
		}
		const probe = `set -eu
# Rootless runtimes can retain the guest cgroup namespace. The mount root is
# then an ancestor, not the exec process's actual cgroup. Read membership.
cg=$(awk -F: '$1 == "0" && $2 == "" { print $3 }' /proc/self/cgroup)
case "$cg" in /*) ;; *) exit 65 ;; esac
case "$cg/" in */../*|*/./*) exit 65 ;; esac
base=/sys/fs/cgroup${cg%/}
# A runtime may place processes in a child of the limited scope. Walk to the
# visible namespace root and calculate the effective (most restrictive) caps.
while :; do
  printf 'level %s %s %s\n' "$(cat "$base/memory.max" 2>/dev/null || echo max)" "$(cat "$base/pids.max" 2>/dev/null || echo max)" "$(cat "$base/cpu.max" 2>/dev/null || echo 'max 100000')"
  test "$base" = /sys/fs/cgroup && break
  base=${base%/*}
done | awk '
BEGIN { m="max"; p="max"; q="max"; period=100000 }
NF!=5 || $1!="level" || $2!~/^(max|[0-9]+)$/ || $3!~/^(max|[0-9]+)$/ || $4!~/^(max|[0-9]+)$/ || $5!~/^[0-9]+$/ || $5<=0 { bad=1; next }
{ count++; if ($2!="max" && (m=="max" || $2+0<m+0)) m=$2
  if ($3!="max" && (p=="max" || $3+0<p+0)) p=$3
  if ($4!="max" && (q=="max" || $4*period<q*$5)) { q=$4; period=$5 } }
END { if (bad || !count) exit 65; printf "memory=%s\npids=%s\ncpu=%s %s\n",m,p,q,period }'
printf 'nnp=%s\n' "$(awk '/^NoNewPrivs:/ { print $2 }' /proc/self/status)"
if test -e /run/anas-actions-token/runner-token; then echo token_visible=true; else echo token_visible=false; fi
`
		body, err := engine(ctx, "exec", name, "/bin/sh", "-c", probe)
		observation := runnerOCILimitObservation(body)
		t.Logf("oci_exec_limit_observation=%v", observation)
		if err != nil || !runnerOCILimitsEnforced(observation) {
			// Read only this test container's selected numeric configuration and
			// kernel cgroup paths. No environment, argv, mounts or secrets are logged.
			info, readErr := engine(ctx, "inspect", name)
			var views []struct {
				State      struct{ Pid int }
				HostConfig struct{ Memory, PidsLimit, CPUQuota, CPUPeriod int64 }
			}
			if readErr == nil && json.Unmarshal(info, &views) == nil && len(views) == 1 {
				t.Logf("oci_created_limit_observation=%+v", views[0].HostConfig)
				pid := views[0].State.Pid
				if pid > 0 && pid < 1<<30 {
					membership, e := guest("/usr/bin/cat", "/proc/"+strconv.Itoa(pid)+"/cgroup")
					if e == nil && len(membership) < 1024 && regexp.MustCompile(`^[A-Za-z0-9/_.:@\n-]+$`).Match(membership) {
						t.Logf("oci_init_cgroup_observation=%q", strings.TrimSpace(string(membership)))
					}
				}
			}
			paths, e := engine(ctx, "exec", name, "/bin/sh", "-c", `cat /proc/self/cgroup; awk '$0 ~ / - cgroup2 / { print $4; print $5 }' /proc/self/mountinfo`)
			if e == nil && len(paths) < 2048 && regexp.MustCompile(`^[A-Za-z0-9/_.:@\n-]+$`).Match(paths) {
				t.Logf("oci_exec_cgroup_path_observation=%q", strings.TrimSpace(string(paths)))
			}
			t.Fatal("OCI exec or actual cgroup/no-new-privileges constraints failed", err)
		}
		t.Log("OCI create/exec confirmed; effective cpu=0.5 memory=128MiB pids=32 no-new-privileges=1")
	})
}
