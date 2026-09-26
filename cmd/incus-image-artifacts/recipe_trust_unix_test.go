//go:build unix

package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestBootstrapKeyringHasTrustedFileIdentity(t *testing.T) {
	for _, fault := range []string{"valid", "absent", "empty", "writable", "hardlink", "symlink", "directory-symlink", "directory-writable", "wrong-owner"} {
		t.Run(fault, func(t *testing.T) {
			root := filepath.Join(t.TempDir(), "keyrings")
			if err := os.Mkdir(root, 0755); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(root, "debian-archive-keyring.gpg")
			if err := os.WriteFile(path, make([]byte, 2048), 0644); err != nil {
				t.Fatal(err)
			}
			owner := os.Geteuid()
			var err error
			switch fault {
			case "absent":
				err = os.Remove(path)
			case "empty":
				err = os.Truncate(path, 0)
			case "writable":
				err = os.Chmod(path, 0666)
			case "hardlink":
				err = os.Link(path, path+".other")
			case "symlink":
				if err = os.Rename(path, path+".original"); err == nil {
					err = os.Symlink(path+".original", path)
				}
			case "directory-symlink":
				if err = os.Rename(root, root+".original"); err == nil {
					err = os.Symlink(root+".original", root)
				}
			case "directory-writable":
				err = os.Chmod(root, 0777)
			case "wrong-owner":
				owner++
			}
			if err != nil {
				t.Fatal(err)
			}
			// Synthetic bytes test filesystem preconditions only. Actual release
			// signatures still have to be verified by debootstrap/gpgv.
			err = checkBootstrapKeyringAt(context.Background(), root, owner)
			if (err == nil) != (fault == "valid") {
				t.Fatalf("keyring identity fault %s: %v", fault, err)
			}
		})
	}
}
