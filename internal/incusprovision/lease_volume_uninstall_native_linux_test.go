//go:build linux

package incusprovision

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// TestNativeUninstallKeepsPoolWithLeaseVolume runs the uninstall pool check
// against a real daemon in the disposable lifecycle lab VM (INCUS-R-171): a
// custom volume in a lease project, not the default project, must block
// deleting the provisioned pool, and removing it must unblock the check.
func TestNativeUninstallKeepsPoolWithLeaseVolume(t *testing.T) {
	if os.Getenv("ANAS_REQUIRE_INCUS_LIFECYCLE_NATIVE") != "1" {
		t.Skip("requires the disposable lifecycle lab VM")
	}
	identity, err := os.ReadFile("/var/lib/cloud/data/instance-id")
	if os.Geteuid() != 0 || err != nil || !strings.HasPrefix(strings.TrimSpace(string(identity)), "anas-incus-lifecycle-") {
		t.Fatal("not the authorized disposable VM")
	}
	const (
		socket  = "/var/lib/incus/unix.socket"
		project = "anas-lifecycle-uninstall"
		volume  = "anas-native-data"
		owner   = "lifecycle-uninstall-check"
	)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	admin := func(args ...string) error {
		command := exec.CommandContext(ctx, "/usr/bin/incus", append([]string{"--force-local"}, args...)...)
		command.Env = []string{"PATH=/usr/sbin:/usr/bin:/sbin:/bin", "HOME=/root", "INCUS_DIR=/var/lib/incus", "INCUS_SOCKET=" + socket}
		return command.Run()
	}
	if admin("storage", "show", StoragePoolName) == nil {
		t.Fatal("the lab daemon already has the provisioned pool name")
	}
	t.Cleanup(func() {
		_ = admin("storage", "volume", "delete", StoragePoolName, volume, "--project", project)
		_ = admin("project", "delete", project)
		_ = admin("storage", "delete", StoragePoolName)
	})
	for _, args := range [][]string{
		{"storage", "create", StoragePoolName, "btrfs", "size=2GiB", "user.anas.owner=incus-host-provision", "user.anas.owner_id=" + owner},
		{"project", "create", project, "-c", "features.storage.volumes=true"},
		{"storage", "volume", "create", StoragePoolName, volume, "size=1GiB", "--project", project},
	} {
		if err := admin(args...); err != nil {
			t.Fatal("lab fixture setup failed", args[0], args[1])
		}
	}
	runtime := &localRuntime{incus: &incusUnixClient{socket: socket}}
	ownership := Ownership{ID: owner, StoragePool: StoragePoolName}
	if _, err := runtime.checkStoragePoolUnused(ctx, ownership); !errors.Is(err, ErrBlocked) {
		t.Fatalf("a lease-project volume did not block removing the pool: %v", err)
	}
	if err := admin("storage", "volume", "delete", StoragePoolName, volume, "--project", project); err != nil {
		t.Fatal("volume cleanup failed")
	}
	if owned, err := runtime.checkStoragePoolUnused(ctx, ownership); err != nil || !owned {
		t.Fatalf("the emptied pool still reads as in use: %v %v", owned, err)
	}
	t.Log("uninstall_pool_check lease_volume=blocked emptied_pool=unblocked")
}
