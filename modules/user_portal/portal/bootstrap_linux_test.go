//go:build linux

package main

import (
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"
)

func TestPrivateStorageRootBootstrap(t *testing.T) {
	if os.Getenv("PORTAL_BOOTSTRAP_NATIVE_TEST") != "1" || os.Geteuid() != 0 {
		t.Skip("explicit isolated root container required; no native acceptance")
	}
	for _, test := range []struct {
		name string
		uid  int
		mode uint32
		want bool
	}{
		{"core_private_mount", 0, 0700, true},
		{"empty_docker_mount", 0, 0755, true},
		{"restart_existing_owner", 65532, 0700, true},
		{"foreign_owner", 1000, 0700, false},
		{"shared_writable_mount", 0, 0777, false},
		{"exposed_existing_store", 65532, 0755, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "data")
			if err := os.Mkdir(path, 0700); err != nil {
				t.Fatal(err)
			}
			if os.Chmod(path, os.FileMode(test.mode)) != nil || os.Chown(path, test.uid, test.uid) != nil {
				t.Fatal("native fixture permissions unavailable")
			}
			fd, err := unix.Open(path, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW, 0)
			if err != nil {
				t.Fatal(err)
			}
			defer unix.Close(fd)
			if err := privateStorageRoot(fd); (err == nil) != test.want {
				t.Fatal("mount ownership result", err)
			}
			if test.want {
				if err := privateStorageRoot(fd); err != nil {
					t.Fatal("bootstrap replay failed", err)
				}
				var actual unix.Stat_t
				if unix.Fstat(fd, &actual) != nil || actual.Uid != 65532 || actual.Gid != 65532 || actual.Mode&0777 != 0700 {
					t.Fatal("mount remains inaccessible after dropping privileges")
				}
			}
		})
	}
}
