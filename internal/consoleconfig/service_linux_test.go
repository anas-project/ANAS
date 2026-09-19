//go:build linux

package consoleconfig

import (
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"
)

func TestServiceConfigRequiresFixedOwnershipAndPrivateGroup(t *testing.T) {
	for _, which := range []string{"valid", "world-readable", "group-writable", "wrong-group", "wrong-owner", "symlink", "parent-link", "shared-parent", "hardlink", "fifo"} {
		t.Run(which, func(t *testing.T) {
			root := t.TempDir()
			if err := os.Chmod(root, 0700); err != nil {
				t.Fatal(err)
			}
			dir := filepath.Join(root, "etc", "anas")
			if err := os.MkdirAll(dir, 0700); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(dir, "anasd.yml")
			body := []byte("api_version: anas.console-config/v1\nconsole_store: /var/lib/anas/console\n")
			if err := os.WriteFile(path, body, 0640); err != nil {
				t.Fatal(err)
			}
			if err := os.Chmod(path, 0640); err != nil {
				t.Fatal(err)
			}
			uid, gid := uint32(os.Geteuid()), uint32(os.Getegid())
			switch which {
			case "world-readable":
				_ = os.Chmod(path, 0644)
			case "group-writable":
				_ = os.Chmod(path, 0660)
			case "wrong-group":
				gid++
			case "wrong-owner":
				uid++
			case "symlink":
				_ = os.Rename(path, path+".old")
				_ = os.Symlink(path+".old", path)
			case "parent-link":
				_ = os.Rename(dir, dir+".old")
				_ = os.Symlink(dir+".old", dir)
			case "shared-parent":
				_ = os.Chmod(dir, 0770)
			case "hardlink":
				if err := os.Link(path, path+".link"); err != nil {
					t.Fatal(err)
				}
			case "fifo":
				_ = os.Remove(path)
				if err := unix.Mkfifo(path, 0640); err != nil {
					t.Fatal(err)
				}
			}
			f, err := os.Open(root)
			if err != nil {
				t.Fatal(err)
			}
			defer f.Close()
			_, err = loadServiceAt(int(f.Fd()), "/etc/anas/anasd.yml", uid, gid)
			if (err == nil) != (which == "valid") {
				t.Fatal(which, err)
			}
		})
	}
}
