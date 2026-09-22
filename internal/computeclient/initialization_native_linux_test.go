//go:build linux

package computeclient

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"testing"
	"time"
)

const nativeClientRoot = "/run/anas-incus-native"

// The administrator harness owns the daemon, restricted test certificate and
// separate mount/network/PID namespaces. No system socket is ever selected.
func nativeClientLease(t *testing.T, flag string) Lease {
	t.Helper()
	if os.Getenv(flag) != "1" {
		t.Skip("requires explicit isolated native daemon harness")
	}
	if os.Geteuid() != 0 {
		t.Fatal("isolated root test owner required")
	}
	read := func(name string) []byte {
		path := filepath.Join(nativeClientRoot, name)
		info, err := os.Lstat(path)
		if err != nil || !credentialOwned(info, false) || info.Size() > 64<<10 {
			t.Fatal("protected native test input required")
		}
		body, err := os.ReadFile(path)
		if err != nil {
			t.Fatal("native test input unavailable")
		}
		return body
	}
	var marker struct {
		Schema  string `json:"schema"`
		Network uint64 `json:"parent_netns"`
		Mount   uint64 `json:"parent_mntns"`
		PID     uint64 `json:"parent_pidns"`
	}
	if json.Unmarshal(read("isolation.json"), &marker) != nil || marker.Schema != "anas.incus-native-isolation/v1" {
		t.Fatal("invalid native namespace marker")
	}
	for name, parent := range map[string]uint64{"net": marker.Network, "mnt": marker.Mount, "pid": marker.PID} {
		info, err := os.Stat("/proc/self/ns/" + name)
		if err != nil || parent == 0 {
			t.Fatal("namespace evidence missing")
		}
		value, ok := info.Sys().(*syscall.Stat_t)
		if !ok || value.Ino == parent {
			t.Fatal("native client test cannot run in parent host namespace")
		}
	}
	var lease Lease
	if json.Unmarshal(read("client-lease.json"), &lease) != nil || lease.Endpoint != "https://127.0.0.1:8443" || lease.Sandbox != "anas-native-client" {
		t.Fatal("invalid isolated client fixture")
	}
	if err := lease.Validate(); err != nil {
		t.Fatal("invalid native lease declaration")
	}
	t.Setenv("PATH", "/usr/sbin:/usr/bin:/sbin:/bin")
	return lease
}

func TestNativeIncusClientInitialization(t *testing.T) {
	l := nativeClientLease(t, "ANAS_REQUIRE_INCUS_CLIENT_NATIVE")
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	dir := nativeClientRoot + "/client-restart"
	if _, err := os.Lstat(dir); !os.IsNotExist(err) {
		t.Fatal("native initialization requires a fresh private directory")
	}
	newClient := func(lease Lease, path string) (*Client, error) {
		return NewWithContext(ctx, lease, []string{"/usr/local/bin/fixture-entry"}, path)
	}
	client, err := newClient(l, dir)
	if err != nil {
		t.Fatal("real CLI did not accept frozen TLS/project configuration", err)
	}
	if instances, err := client.ListManaged(ctx); err != nil || len(instances) != 0 {
		t.Fatal("restricted empty-project listing failed", err)
	}
	before, err := os.Stat(filepath.Join(dir, "config.yml"))
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if _, err := newClient(l, dir); err != nil {
			t.Fatal("real CLI restart failed", err)
		}
	}
	var wg sync.WaitGroup
	errors := make(chan error, 8)
	for range 8 {
		wg.Go(func() { _, err := newClient(l, dir); errors <- err })
	}
	wg.Wait()
	close(errors)
	for err := range errors {
		if err != nil {
			t.Error("concurrent real CLI initialization failed", err)
		}
	}
	after, err := os.Stat(filepath.Join(dir, "config.yml"))
	if err != nil || !os.SameFile(before, after) || !before.ModTime().Equal(after.ModTime()) {
		t.Fatal("CLI rewrote immutable configuration")
	}
	other := l
	other.Sandbox = "anas-native-other"
	if _, err := newClient(other, nativeClientRoot+"/client-other"); err == nil {
		t.Fatal("restricted certificate accessed another existing project")
	}
	wrong := credentialBoundaryLease(t)
	other = l
	other.ServerCertB64, other.ServerCertFingerprint = wrong.ServerCertB64, wrong.ServerCertFingerprint
	if _, err := newClient(other, nativeClientRoot+"/client-wrong-pin"); err == nil {
		t.Fatal("actual TLS connection accepted a different pinned server certificate")
	}
}

func TestNativeIncusClientRevokedCertificate(t *testing.T) {
	l := nativeClientLease(t, "ANAS_REQUIRE_INCUS_CLIENT_REVOKED_NATIVE")
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if _, err := NewWithContext(ctx, l, []string{"/usr/local/bin/fixture-entry"}, nativeClientRoot+"/client-restart"); err == nil {
		t.Fatal("client restart accepted revoked credentials")
	}
}
