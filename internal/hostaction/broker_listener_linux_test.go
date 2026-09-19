//go:build linux

package hostaction

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func brokerListenerFixture(t *testing.T) (string, *os.File, string) {
	t.Helper()
	dir, err := os.MkdirTemp("", "anas-listen-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	private := filepath.Join(dir, "run/anas-job-broker")
	if err := os.MkdirAll(private, 0700); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{dir, filepath.Join(dir, "run"), private} {
		if err := os.Chmod(path, 0700); err != nil {
			t.Fatal(err)
		}
	}
	root, err := os.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = root.Close() })
	return dir, root, filepath.Join(private, "socket")
}

func openFixtureBrokerListener(t *testing.T, root *os.File, path string) (*JobBrokerListener, error) {
	t.Helper()
	uid, gid := uint32(os.Geteuid()), uint32(os.Getegid())
	return openJobBrokerListenerAt(context.Background(), int(root.Fd()), uid, uid, gid, path)
}

func TestBrokerListenerOwnsExclusiveSocketAndPreservesDirectory(t *testing.T) {
	_, root, path := brokerListenerFixture(t)
	l, err := openFixtureBrokerListener(t, root, path)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	info, err := os.Lstat(path)
	if err != nil || info.Mode()&os.ModeSocket == 0 || info.Mode().Perm() != 0600 {
		t.Fatal("socket mode/type did not match the private endpoint", err)
	}
	if other, err := openFixtureBrokerListener(t, root, path); err == nil {
		_ = other.Close()
		t.Fatal("a second listener took ownership")
	}
	if l.Check() != nil {
		t.Fatal("failed duplicate open disturbed the active socket")
	}
	client, err := net.DialUnix("unix", nil, &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	accepted, err := l.Accept(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer accepted.Close()
	// The dial side must see THIS process, not an inherited listener creator.
	raw, err := client.SyscallConn()
	if err != nil {
		t.Fatal(err)
	}
	var peer *unix.Ucred
	var peerErr error
	if err := raw.Control(func(fd uintptr) { peer, peerErr = unix.GetsockoptUcred(int(fd), unix.SOL_SOCKET, unix.SO_PEERCRED) }); err != nil || peerErr != nil || peer == nil || peer.Pid != int32(os.Getpid()) {
		t.Fatal("listener identity does not name the execution owner", err, peerErr)
	}
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("owned socket was not removed", err)
	}
	if _, err := os.Stat(filepath.Dir(path)); err != nil {
		t.Fatal("installed directory was removed", err)
	}
	if err := l.Close(); err != nil {
		t.Fatal("successful close is not idempotent", err)
	}
	reopened, err := openFixtureBrokerListener(t, root, path)
	if err != nil {
		t.Fatal("clean shutdown left a lock/socket behind", err)
	}
	if err := reopened.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestBrokerListenerNeverAdoptsOrDeletesExistingEntries(t *testing.T) {
	for _, kind := range []string{"file", "symlink", "stale socket", "live socket", "directory mode"} {
		t.Run(kind, func(t *testing.T) {
			_, root, path := brokerListenerFixture(t)
			if kind == "directory mode" {
				if err := os.Chmod(filepath.Dir(path), 0755); err != nil {
					t.Fatal(err)
				}
			} else if kind == "file" {
				if err := os.WriteFile(path, []byte("preserve"), 0600); err != nil {
					t.Fatal(err)
				}
			} else if kind == "symlink" {
				if err := os.Symlink("missing", path); err != nil {
					t.Fatal(err)
				}
			} else {
				old, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
				if err != nil {
					t.Fatal(err)
				}
				old.SetUnlinkOnClose(false)
				defer old.Close()
				if kind == "stale socket" {
					_ = old.Close()
				}
			}
			before, _ := os.Lstat(path)
			l, err := openFixtureBrokerListener(t, root, path)
			if err == nil {
				_ = l.Close()
				t.Fatal("existing or insecure endpoint accepted")
			}
			after, _ := os.Lstat(path)
			if before != nil && (after == nil || !os.SameFile(before, after) || before.Mode() != after.Mode()) {
				t.Fatal("failed open modified an existing entry")
			}
		})
	}
}

func TestBrokerListenerRefusesReplacementCleanupAndDetectsIdleDrift(t *testing.T) {
	for _, kind := range []string{"replacement", "ancestor", "mode"} {
		t.Run(kind, func(t *testing.T) {
			dir, root, path := brokerListenerFixture(t)
			l, err := openFixtureBrokerListener(t, root, path)
			if err != nil {
				t.Fatal(err)
			}
			defer l.Close()
			preserved := path
			switch kind {
			case "replacement":
				if err := os.Rename(path, path+".original"); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte("unowned"), 0600); err != nil {
					t.Fatal(err)
				}
			case "ancestor":
				if err := os.Rename(filepath.Join(dir, "run"), filepath.Join(dir, "run-old")); err != nil {
					t.Fatal(err)
				}
				preserved = filepath.Join(dir, "run-old/anas-job-broker/socket")
			case "mode":
				if err := os.Chmod(path, 0666); err != nil {
					t.Fatal(err)
				}
			}
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			if c, err := l.Accept(ctx); err == nil {
				_ = c.Close()
				t.Fatal("idle accept ignored drift")
			}
			if err := l.Close(); err == nil || l.Close() == nil {
				t.Fatal("uncertain cleanup became success on retry")
			}
			if _, err := os.Lstat(preserved); err != nil {
				t.Fatal("changed or displaced entry was removed", err)
			}
		})
	}
}

func TestBrokerListenerIdleCancellation(t *testing.T) {
	_, root, path := brokerListenerFixture(t)
	l, err := openFixtureBrokerListener(t, root, path)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if c, err := l.Accept(ctx); err == nil || ctx.Err() == nil {
		if c != nil {
			c.Close()
		}
		t.Fatal("idle listener did not honor cancellation", err)
	}
}
