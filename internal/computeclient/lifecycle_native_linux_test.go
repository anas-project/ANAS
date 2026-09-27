//go:build linux

package computeclient

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"
)

const lifecycleFixtureRoot = "/run/anas-incus-lifecycle"
const lifecycleProviderPath = "/var/lib/anas-incus-lifecycle/provider"
const lifecycleEntrypoint = "/usr/local/bin/anas-fixture"

// lifecycleLeases reads the two real Provider leases the harness prepared for
// exactly one isolation tier. A lab prepared for the other tier fails here
// rather than running the wrong matrix.
func lifecycleLeases(t *testing.T, tier string) []Lease {
	t.Helper()
	if os.Getenv("ANAS_REQUIRE_INCUS_LIFECYCLE_NATIVE") != "1" {
		t.Skip("requires disposable KVM lab with explicit lifecycle fixture")
	}
	identity, err := os.ReadFile("/var/lib/cloud/data/instance-id")
	if err != nil || !strings.HasPrefix(strings.TrimSpace(string(identity)), "anas-incus-lifecycle-") {
		t.Fatal("not the authorized disposable VM")
	}
	read := func(name string) []byte {
		info, err := os.Lstat(lifecycleFixtureRoot + "/" + name)
		if err != nil || !credentialOwned(info, false) || info.Size() > 128<<10 {
			t.Fatal("private fixture file required")
		}
		body, err := os.ReadFile(lifecycleFixtureRoot + "/" + name)
		if err != nil {
			t.Fatal("fixture read failed")
		}
		return body
	}
	if strings.TrimSpace(string(read("identity"))) != strings.TrimSpace(string(identity)) {
		t.Fatal("fixture VM identity mismatch")
	}
	var leases []Lease
	if json.Unmarshal(read("leases.json"), &leases) != nil || len(leases) != 2 {
		t.Fatal("two real provider leases required")
	}
	for i, l := range leases {
		if l.Validate() != nil || l.Interface != tier || l.Endpoint != "https://127.0.0.1:8443" || l.Sandbox != []string{"anas-lifecycle-a", "anas-lifecycle-b"}[i] || l.InstancePrefix != "anas-native-" || l.MaxInstances != 1 || l.DiskGiB != 4 {
			t.Fatal("unexpected fixture lease")
		}
	}
	return leases
}

// This uses the production Provider's actual projects/profiles/certificates,
// real btrfs root volumes, and the shared client's entire lifecycle. The tiny
// fixture image is not a published runner, and no Forgejo API is substituted.
func TestNativeIncusContainerLeaseLifecycle(t *testing.T) {
	runNativeLeaseLifecycle(t, InterfaceContainer, 8*time.Minute)
}

// The VM tier runs the same matrix against real KVM guests. Its fixture image
// is a measured lab image with the same fixture program, not a published
// runner; a guest kernel and the Incus agent replace the container init.
func TestNativeIncusVMLeaseLifecycle(t *testing.T) {
	runNativeLeaseLifecycle(t, InterfaceVM, 16*time.Minute)
}

// deviceOverrides are direct device requests a restricted lease certificate
// must not obtain on its tier. They bypass Client.Validate on purpose.
func deviceOverrides(tier, id string) map[string][]string {
	target := remoteName + ":" + id
	add := func(name, kind string, options ...string) []string {
		return append([]string{"config", "device", "add", target, name, kind}, options...)
	}
	cases := map[string][]string{
		"gpu": add("escape", "gpu", "gputype=physical"),
		"usb": add("escape", "usb", "vendorid=1d6b"),
	}
	if tier == InterfaceVM {
		cases["pci"] = add("escape", "pci", "address=0000:00:02.0")
		cases["raw-qemu"] = []string{"config", "set", target, "raw.qemu=-S"}
	} else {
		cases["unix-char"] = add("escape", "unix-char", "source=/dev/kmsg", "path=/dev/kmsg")
		cases["unix-block"] = add("escape", "unix-block", "source=/dev/loop0", "path=/dev/loop0")
		cases["privileged"] = []string{"config", "set", target, "security.privileged=true"}
		cases["raw-lxc"] = []string{"config", "set", target, "raw.lxc=lxc.apparmor.profile=unconfined"}
	}
	return cases
}

func runNativeLeaseLifecycle(t *testing.T, tier string, budget time.Duration) {
	leases := lifecycleLeases(t, tier)
	ctx, cancel := context.WithTimeout(context.Background(), budget)
	defer cancel()
	clients := make([]*Client, 2)
	const id = "anas-native-job"
	for i, l := range leases {
		client, err := NewWithContext(ctx, l, []string{lifecycleEntrypoint}, lifecycleFixtureRoot+"/client-"+string(rune('a'+i)))
		if err != nil {
			t.Fatal("initialize real lease", err)
		}
		clients[i] = client
		// Cleanup uses an independent budget even after test cancellation.
		t.Cleanup(func() {
			clean, done := context.WithTimeout(context.Background(), time.Minute)
			defer done()
			if err := client.Delete(clean, id); err != nil {
				t.Error("fixture cleanup unconfirmed", err)
			}
		})
	}
	for i, client := range clients {
		spec := InstanceSpec{ID: id, Image: leases[i].ImageAllowlist[0], WorkloadID: leases[i].Sandbox, CPU: 1, MemoryMiB: 512, DiskGiB: 4}
		start := time.Now()
		if err := client.Create(ctx, spec); err != nil {
			retainCreateDiagnostics(spec, leases[i])
			t.Fatal("real create failed", err)
		}
		if err := client.Start(ctx, id); err != nil {
			retainGuestDiagnostics(client, id, "start-"+leases[i].Sandbox, leases[i].Sandbox)
			t.Fatal("real start failed", err)
		}
		if err := client.WaitForGuest(ctx, id, 100*time.Millisecond); err != nil {
			retainGuestDiagnostics(client, id, "ready-"+leases[i].Sandbox, leases[i].Sandbox)
			t.Fatal("real guest readiness failed", err)
		}
		t.Logf("lease_%d_create_start_ready_ms=%d", i, time.Since(start).Milliseconds())
	}
	t.Run("same-name-project-isolation", func(t *testing.T) {
		for i, c := range clients {
			list, err := c.ListManaged(ctx)
			if err != nil || len(list) != 1 || list[0].ID != id || list[0].WorkloadID != leases[i].Sandbox {
				t.Fatal("cross-project visibility or wrong instance", err)
			}
		}
	})
	t.Run("daemon-instance-quota", func(t *testing.T) {
		c := clients[0]
		if err := c.Create(ctx, InstanceSpec{ID: "anas-native-overquota", Image: leases[0].ImageAllowlist[0], WorkloadID: "overquota", CPU: 1, MemoryMiB: 512, DiskGiB: 4}); err == nil {
			t.Fatal("daemon allowed a second instance")
		}
		if list, err := c.ListManaged(ctx); err != nil || len(list) != 1 {
			t.Fatal("quota negative control changed inventory", err)
		}
	})
	t.Run("daemon-rejects-direct-quota-and-device-overrides", func(t *testing.T) {
		c := clients[0]
		for _, args := range [][]string{
			{"config", "set", remoteName + ":" + id, "limits.cpu=1"},
			{"config", "set", remoteName + ":" + id, "limits.memory=512MiB"},
			{"config", "device", "set", remoteName + ":" + id, "root", "size=4GiB"},
		} {
			if _, err := c.run.Run(ctx, nil, args...); err != nil {
				t.Fatal("valid direct mutation syntax/control rejected", err)
			}
		}
		before, err := c.run.Run(ctx, nil, "config", "show", remoteName+":"+id)
		if err != nil {
			t.Fatal("positive config read failed", err)
		}
		// Deliberately bypass Client.Validate to prove daemon-side rejection,
		// not just the shared library's request validation. All targets remain
		// inside this disposable VM's two fixed fixture projects.
		cases := map[string][]string{
			"cpu":                   {"config", "set", remoteName + ":" + id, "limits.cpu=2"},
			"memory":                {"config", "set", remoteName + ":" + id, "limits.memory=513MiB"},
			"disk":                  {"config", "device", "set", remoteName + ":" + id, "root", "size=5GiB"},
			"host-disk":             {"config", "device", "add", remoteName + ":" + id, "escape", "disk", "source=/", "path=/escape"},
			"other-project-network": {"config", "device", "add", remoteName + ":" + id, "escape", "nic", "network=" + NetworkName(leases[1].Sandbox)},
			"proxy":                 {"config", "device", "add", remoteName + ":" + id, "escape", "proxy", "listen=tcp:127.0.0.1:18081", "connect=tcp:127.0.0.1:80"},
		}
		for name, args := range deviceOverrides(tier, id) {
			cases[name] = args
		}
		for name, args := range cases {
			t.Run(name, func(t *testing.T) {
				if _, err := c.run.Run(ctx, nil, args...); err == nil {
					t.Fatal("daemon accepted direct forbidden mutation")
				}
				after, err := c.run.Run(ctx, nil, "config", "show", remoteName+":"+id)
				// A running VM updates volatile runtime keys on its own; only
				// declared configuration and devices are the fence under test.
				if err != nil {
					t.Fatal("instance configuration unreadable after rejected mutation", err)
				}
				if changed := changedConfigLines(declaredConfig(before), declaredConfig(after)); len(changed) != 0 {
					t.Fatalf("rejected mutation changed instance configuration: %q", changed)
				}
			})
		}
	})
	if tier == InterfaceVM {
		// Nested virtualization is fenced only where the daemon offers
		// restricted.virtual-machines.nesting. Before that extension (Incus
		// 7.0 LTS) security.nesting is a container key a VM accepts inertly,
		// and the guest sees whatever virtualization the host CPU exposes.
		t.Run("nested-virtualization-fence", func(t *testing.T) {
			target := remoteName + ":" + id
			restricted := daemonAdvertises(ctx, "projects_restricted_virtual_machines_nesting")
			_, setErr := clients[0].run.Run(ctx, nil, "config", "set", target, "security.nesting=true")
			if restricted && setErr == nil {
				t.Fatal("daemon accepted nested virtualization despite the project restriction")
			}
			if setErr == nil {
				if _, err := clients[0].run.Run(ctx, nil, "config", "unset", target, "security.nesting"); err != nil {
					t.Fatal("inert nesting key could not be removed", err)
				}
			}
			flags, err := clients[0].run.Run(ctx, nil, "exec", target, "--", "sh", "-c", "grep -cwE 'vmx|svm' /proc/cpuinfo || true")
			if err != nil {
				t.Fatal("guest CPU flags unreadable", err)
			}
			t.Logf("nested_virtualization daemon_restriction=%t guest_virtualization_flags=%s", restricted, strings.TrimSpace(string(flags)))
		})
	}
	token := make([]byte, 32)
	if _, err := rand.Read(token); err != nil {
		t.Fatal(err)
	}
	defer clear(token)
	digest := sha256.Sum256(token)
	expected := hex.EncodeToString(digest[:])
	t.Run("stdin-secret", func(t *testing.T) {
		for _, c := range clients {
			if err := c.ExecStdin(ctx, id, []string{lifecycleEntrypoint, "probe", expected}, bytes.NewReader(token)); err != nil {
				t.Fatal("real stdin probe failed", err)
			}
		}
	})
	if !t.Run("management-certificate-rotation", func(t *testing.T) {
		verifyNativeManagementRotation(t, ctx, clients, leases, id, isolationFlag(tier))
	}) {
		t.FailNow()
	}
	t.Run("root-disk-quota", func(t *testing.T) {
		body, err := clients[0].run.Run(ctx, nil, "exec", remoteName+":"+id, "--", lifecycleEntrypoint, "quota")
		if err != nil {
			t.Fatal("real guest disk limit not demonstrated", err)
		}
		var proof struct {
			Enforced bool  `json:"write_limit_enforced"`
			Written  int64 `json:"bytes_written"`
			Limit    int64 `json:"disk_limit_bytes"`
		}
		if json.Unmarshal(body, &proof) != nil || !proof.Enforced || proof.Limit != 4<<30 || proof.Written < 16<<20 || proof.Written > proof.Limit {
			t.Fatal("invalid disk quota proof")
		}
		t.Logf("actual_guest_write_bytes=%d limit_bytes=%d", proof.Written, proof.Limit)
	})
	t.Run("exec-cancel-and-independent-reclaim", func(t *testing.T) {
		work, stop := context.WithCancel(ctx)
		defer stop()
		finished := make(chan error, 1)
		go func() {
			finished <- clients[0].ExecStdin(work, id, []string{lifecycleEntrypoint, "hold", expected}, bytes.NewReader(token))
		}()
		ready, done := context.WithTimeout(ctx, 15*time.Second)
		defer done()
		for {
			_, err := clients[0].run.Run(ready, nil, "exec", remoteName+":"+id, "--", lifecycleEntrypoint, "holding")
			if err == nil {
				break
			}
			if ready.Err() != nil {
				t.Fatal("guest hold never began")
			}
			time.Sleep(100 * time.Millisecond)
		}
		stop()
		select {
		case err := <-finished:
			if !errors.Is(err, context.Canceled) {
				t.Fatal("guest exec cancellation identity lost", err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("CLI did not stop after cancellation")
		}
		clean, done := context.WithTimeout(context.Background(), time.Minute)
		defer done()
		if err := clients[0].Delete(clean, id); err != nil {
			t.Fatal("canceled guest not reclaimed", err)
		}
		if view, err := clients[1].Inspect(clean, id); err != nil || view.State != "running" || view.WorkloadID != leases[1].Sandbox {
			t.Fatal("cleanup affected the other lease", err)
		}
	})
	t.Run("stop-delete-idempotent", func(t *testing.T) {
		if err := clients[1].Stop(ctx, id); err != nil {
			t.Fatal(err)
		}
		if view, err := clients[1].Inspect(ctx, id); err != nil || view.State != "stopped" {
			t.Fatal("stop not observed", err)
		}
		for range 2 {
			if err := clients[1].Delete(ctx, id); err != nil {
				t.Fatal(err)
			}
		}
	})
	// INCUS-R-034 baseline: one job-shaped run on an empty lease, measured the
	// same way on both tiers -- create, boot to a usable agent, one stdin exec,
	// then reclaim.
	t.Run("typical-job-wall-time", func(t *testing.T) {
		const timed = "anas-native-timed"
		spec := InstanceSpec{ID: timed, Image: leases[1].ImageAllowlist[0], WorkloadID: "timed", CPU: 1, MemoryMiB: 512, DiskGiB: 4}
		start := time.Now()
		if err := clients[1].Create(ctx, spec); err != nil {
			t.Fatal("timed create failed", err)
		}
		if err := clients[1].Start(ctx, timed); err != nil {
			t.Fatal("timed start failed", err)
		}
		if err := clients[1].WaitForGuest(ctx, timed, 100*time.Millisecond); err != nil {
			t.Fatal("timed guest never became ready", err)
		}
		ready := time.Now()
		if err := clients[1].ExecStdin(ctx, timed, []string{lifecycleEntrypoint, "probe", expected}, bytes.NewReader(token)); err != nil {
			t.Fatal("timed job exec failed", err)
		}
		executed := time.Now()
		clean, done := context.WithTimeout(context.Background(), 2*time.Minute)
		defer done()
		if err := clients[1].Delete(clean, timed); err != nil {
			t.Fatal("timed guest not reclaimed", err)
		}
		t.Logf("typical_job tier=%s ready_ms=%d exec_ms=%d reclaim_ms=%d wall_ms=%d", tier,
			ready.Sub(start).Milliseconds(), executed.Sub(ready).Milliseconds(), time.Since(executed).Milliseconds(), time.Since(start).Milliseconds())
	})
}

// retainGuestDiagnostics keeps why a real start failed before cleanup deletes
// the instance. The client never echoes CLI stderr, so the administrator CLI
// repeats the start with its error kept, then the instance log is read. The
// root-only file stays in the private lab report.
func retainGuestDiagnostics(c *Client, id, label, project string) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	var out bytes.Buffer
	for _, args := range [][]string{
		{"--force-local", "start", id, "--project", project},
		{"--force-local", "info", id, "--project", project, "--show-log"},
		{"--force-local", "config", "show", id, "--project", project, "--expanded"},
	} {
		command := exec.CommandContext(ctx, "/usr/bin/incus", args...)
		command.Env = []string{"PATH=/usr/sbin:/usr/bin:/sbin:/bin", "HOME=" + lifecycleFixtureRoot + "/admin",
			"INCUS_CONF=" + lifecycleFixtureRoot + "/admin", "INCUS_DIR=/var/lib/incus", "INCUS_SOCKET=/var/lib/incus/unix.socket"}
		body, err := command.CombinedOutput()
		out.WriteString("== incus " + strings.Join(args[1:3], " ") + "\n")
		out.Write(body[:min(len(body), 256<<10)])
		if err != nil {
			out.WriteString("\n(exit: " + err.Error() + ")\n")
		}
	}
	_ = os.WriteFile(lifecycleFixtureRoot+"/diagnostics-"+label+".log", out.Bytes(), 0600)
}

// retainCreateDiagnostics replays a refused create as the administrator in the
// same project with the same profile and limits, only to keep the daemon's
// reason in the private lab report. The replay is deleted if it succeeds.
func retainCreateDiagnostics(spec InstanceSpec, l Lease) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	id := spec.ID + "-diag"
	args := []string{"--force-local", "init", spec.Image, id, "--project", l.Sandbox, "--profile", l.Profile,
		"--config=limits.cpu=" + strconv.Itoa(spec.CPU), "--config=limits.memory=" + strconv.Itoa(spec.MemoryMiB) + "MiB",
		"--device=root,size=" + strconv.Itoa(spec.DiskGiB) + "GiB"}
	if l.Interface == InterfaceVM {
		args = append(args, "--vm", "--config=security.secureboot=true")
	}
	environment := []string{"PATH=/usr/sbin:/usr/bin:/sbin:/bin", "HOME=" + lifecycleFixtureRoot + "/admin",
		"INCUS_CONF=" + lifecycleFixtureRoot + "/admin", "INCUS_DIR=/var/lib/incus", "INCUS_SOCKET=/var/lib/incus/unix.socket"}
	command := exec.CommandContext(ctx, "/usr/bin/incus", args...)
	command.Env = environment
	body, err := command.CombinedOutput()
	record := append([]byte("== admin replay of refused create\n"), body[:min(len(body), 64<<10)]...)
	if err != nil {
		record = append(record, []byte("\n(exit: "+err.Error()+")\n")...)
	} else {
		cleanup := exec.CommandContext(ctx, "/usr/bin/incus", "--force-local", "delete", id, "--project", l.Sandbox, "--force")
		cleanup.Env = environment
		_ = cleanup.Run()
	}
	_ = os.WriteFile(lifecycleFixtureRoot+"/diagnostics-create-"+l.Sandbox+".log", record, 0600)
}

// daemonAdvertises asks the lab daemon, as the administrator, whether an API
// extension exists. Restricted lease certificates are not the right reader.
func daemonAdvertises(ctx context.Context, extension string) bool {
	command := exec.CommandContext(ctx, "/usr/bin/incus", "--force-local", "query", "/1.0")
	command.Env = []string{"PATH=/usr/sbin:/usr/bin:/sbin:/bin", "HOME=" + lifecycleFixtureRoot + "/admin",
		"INCUS_CONF=" + lifecycleFixtureRoot + "/admin", "INCUS_DIR=/var/lib/incus", "INCUS_SOCKET=/var/lib/incus/unix.socket"}
	body, err := command.Output()
	var server struct {
		APIExtensions []string `json:"api_extensions"`
	}
	if err != nil || json.Unmarshal(body, &server) != nil {
		return false
	}
	for _, name := range server.APIExtensions {
		if name == extension {
			return true
		}
	}
	return false
}

// declaredConfig drops daemon-maintained volatile.* lines from `config show`.
func declaredConfig(body []byte) []byte {
	var kept [][]byte
	for _, line := range bytes.Split(body, []byte("\n")) {
		if bytes.Contains(line, []byte("volatile.")) {
			continue
		}
		kept = append(kept, line)
	}
	return bytes.Join(kept, []byte("\n"))
}

// changedConfigLines names the declared lines present on only one side.
func changedConfigLines(before, after []byte) []string {
	count := map[string]int{}
	for _, line := range bytes.Split(before, []byte("\n")) {
		count[string(line)]++
	}
	for _, line := range bytes.Split(after, []byte("\n")) {
		count[string(line)]--
	}
	var changed []string
	for line, n := range count {
		if n != 0 {
			changed = append(changed, strings.TrimSpace(line))
		}
	}
	sort.Strings(changed)
	return changed[:min(len(changed), 8)]
}

func isolationFlag(tier string) string {
	if tier == InterfaceVM {
		return "vm"
	}
	return "container"
}
