//go:build linux

package incusprovision

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// Only the explicit isolated-daemon harness installs this tmpfs fixture. This
// test does not install packages, initialize a host socket, or start a daemon.
func TestNativeIncusUnixStorageLifecycle(t *testing.T) {
	if os.Getenv("ANAS_REQUIRE_INCUS_DAEMON_NATIVE") != "1" {
		t.Skip("requires the isolated daemon harness; native gate forbids skip")
	}
	const root = "/run/anas-incus-native"
	if os.Geteuid() != 0 {
		t.Fatal("native daemon fixture requires its isolated root owner")
	}
	var marker struct {
		Schema  string `json:"schema"`
		Network uint64 `json:"parent_netns"`
		Mount   uint64 `json:"parent_mntns"`
		PID     uint64 `json:"parent_pidns"`
	}
	path := filepath.Join(root, "isolation.json")
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0600 {
		t.Fatal("missing protected native isolation evidence")
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Uid != 0 || stat.Nlink != 1 {
		t.Fatal("native isolation evidence is not owned by the harness")
	}
	body, err := os.ReadFile(path)
	if err != nil || len(body) > 4096 || json.Unmarshal(body, &marker) != nil || marker.Schema != "anas.incus-native-isolation/v1" {
		t.Fatal("invalid native isolation evidence")
	}
	for name, parent := range map[string]uint64{"net": marker.Network, "mnt": marker.Mount, "pid": marker.PID} {
		current, err := os.Stat("/proc/self/ns/" + name)
		if err != nil || parent == 0 {
			t.Fatal("native namespace identity unavailable")
		}
		st, ok := current.Sys().(*syscall.Stat_t)
		if !ok || st.Ino == parent {
			t.Fatal("test would access the parent host namespace")
		}
	}
	c := &incusUnixClient{socket: root + "/state/unix.socket"}
	if err := verifyRootOwnedUnixSocket(c.socket); err != nil {
		t.Fatal("native socket fixture failed production ownership checks", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	const name = "anas-native-unix-dir"
	if _, err := c.getStoragePool(ctx, name); !errors.Is(err, errIncusNotFound) {
		t.Fatal("native test pool already exists or cannot be inventoried", err)
	}
	// Even an uncertain POST must be read back and cleaned, not blindly retried.
	t.Cleanup(func() {
		clean, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		_, err := c.getStoragePool(clean, name)
		if err == nil {
			if err = c.deleteStoragePool(clean, name); err != nil {
				t.Error("native pool cleanup failed", err)
			}
		} else if !errors.Is(err, errIncusNotFound) {
			t.Error("native cleanup inventory failed", err)
		}
		if _, err = c.getStoragePool(clean, name); !errors.Is(err, errIncusNotFound) {
			t.Error("native pool removal unconfirmed")
		}
	})
	if err := c.createStoragePool(ctx, name, "dir", map[string]string{}); err != nil {
		t.Fatal("real synchronous pool creation was misclassified", err)
	}
	pool, err := c.getStoragePool(ctx, name)
	if err != nil || pool.Name != name || pool.Driver != "dir" || pool.Status != "Created" {
		t.Fatal("created pool lacks complete readback", err)
	}
	if err := c.deleteStoragePool(ctx, name); err != nil {
		t.Fatal("delete native pool", err)
	}
	if _, err := c.getStoragePool(ctx, name); !errors.Is(err, errIncusNotFound) {
		t.Fatal("deleted pool remains visible")
	}
}
