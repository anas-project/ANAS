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
	"strings"
	"testing"
	"time"
)

const lifecycleFixtureRoot = "/run/anas-incus-lifecycle"
const lifecycleEntrypoint = "/usr/local/bin/anas-fixture"

func lifecycleLeases(t *testing.T) []Lease {
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
		if l.Validate() != nil || l.Interface != InterfaceContainer || l.Endpoint != "https://127.0.0.1:8443" || l.Sandbox != []string{"anas-lifecycle-a", "anas-lifecycle-b"}[i] || l.InstancePrefix != "anas-native-" || l.MaxInstances != 1 || l.DiskGiB != 4 {
			t.Fatal("unexpected fixture lease")
		}
	}
	return leases
}

// This uses the production Provider's actual projects/profiles/certificates,
// real btrfs root volumes, and the shared client's entire lifecycle. The tiny
// fixture image is not a published runner, and no Forgejo API is substituted.
func TestNativeIncusContainerLeaseLifecycle(t *testing.T) {
	leases := lifecycleLeases(t)
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
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
			t.Fatal("real create failed", err)
		}
		if err := client.Start(ctx, id); err != nil {
			t.Fatal("real start failed", err)
		}
		if err := client.WaitForGuest(ctx, id, 100*time.Millisecond); err != nil {
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
		}
		for name, args := range cases {
			t.Run(name, func(t *testing.T) {
				if _, err := c.run.Run(ctx, nil, args...); err == nil {
					t.Fatal("daemon accepted direct forbidden mutation")
				}
				after, err := c.run.Run(ctx, nil, "config", "show", remoteName+":"+id)
				if err != nil || !bytes.Equal(before, after) {
					t.Fatal("rejected mutation changed instance configuration", err)
				}
			})
		}
	})
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
	t.Run("btrfs-root-disk-quota", func(t *testing.T) {
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
}
