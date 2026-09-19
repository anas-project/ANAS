//go:build linux

package incusingresshost

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

// The real nft binary runs only in a newly-created, otherwise unused namespace.
// Routes/Incus allocations stay fixtures: this verifies kernel syntax/readback
// and owned lifecycle, not Docker coexistence, guest traffic or allocator safety.
func TestNativeNFTScriptAndReadback(t *testing.T) {
	nft, err := filepath.EvalSymlinks("/usr/sbin/nft")
	if err != nil {
		if os.Getenv("ANAS_REQUIRE_INGRESS_NATIVE") == "1" {
			t.Fatal("native nft binary is required")
		}
		t.Skip("requires nft; the native gate makes this a failure")
	}
	type created struct {
		file   *os.File
		cookie uint64
		err    error
	}
	ready := make(chan created, 1)
	go func() {
		runtime.LockOSThread()
		if err := unix.Unshare(unix.CLONE_NEWNET); err != nil {
			runtime.UnlockOSThread()
			ready <- created{err: err}
			return
		}
		file, err := openKernelNetworkNamespace("thread-self")
		if err != nil {
			ready <- created{err: err}
			return
		}
		cookie, err := networkNamespaceCookie()
		ready <- created{file: file, cookie: cookie, err: err}
		// Never return a newly-namespaced thread to the runtime's thread pool.
	}()
	namespace := <-ready
	if namespace.file != nil {
		defer namespace.file.Close()
	}
	if namespace.err != nil {
		if os.Getenv("ANAS_REQUIRE_INGRESS_NATIVE") == "1" || (!errors.Is(namespace.err, unix.EPERM) && !errors.Is(namespace.err, unix.EACCES)) {
			t.Fatal(namespace.err)
		}
		t.Skip("requires CAP_SYS_ADMIN in disposable Linux; native gate forbids skip")
	}
	b, target, _ := testBackend(t)
	if err := os.Remove(filepath.Join(b.config.ReceiptDir, baselineReceiptName)); err != nil {
		t.Fatal(err)
	}
	b.config.Binaries.NFT = nft
	b.runner.config = b.config
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	err = inOpenedNetworkNamespace(ctx, namespace.file, namespace.cookie, func() error {
		steps := []struct {
			name string
			run  func() error
		}{
			{"install", func() error { return b.InstallBaseline(ctx) }},
			{"repeat install", func() error { return b.InstallBaseline(ctx) }},
			{"hold fixture", func() error { return b.HoldAddress(ctx, target) }},
			{"route fixture", func() error { return b.EnsureGuestRoute(ctx, target) }},
			{"real nft permit", func() error { return b.EnsureHTTPPermit(ctx, target) }},
			{"renew real nft permit", func() error { return b.EnsureHTTPPermit(ctx, target) }},
			{"read back real nft", func() error { return b.CheckHTTPArtifacts(ctx, []Target{target}) }},
			{"revoke real nft permit", func() error { return b.RemoveHTTPPermit(ctx, target) }},
			{"remove fixture route", func() error { return b.RemoveGuestRoute(ctx, target) }},
			{"release fixture", func() error { return b.ReleaseAddress(ctx, target) }},
			{"remove real nft baseline", func() error { return b.RemoveBaseline(ctx) }},
			{"repeat remove", func() error { return b.RemoveBaseline(ctx) }},
		}
		for _, step := range steps {
			if err := step.run(); err != nil {
				return fmt.Errorf("%s: %w", step.name, err)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
