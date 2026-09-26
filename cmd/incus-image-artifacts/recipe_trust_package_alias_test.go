//go:build unix

package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// Ubuntu's official debian-archive-keyring 2025.1ubuntu1 installs the fixed
// .gpg name as a relative link to its .pgp sibling. Accommodate that precise
// distribution layout, not general symlink traversal or caller-chosen trust.
func TestBootstrapKeyringSupportsOnlyTheOfficialPackageAlias(t *testing.T) {
	for _, fault := range []string{"package-alias", "absolute-target", "parent-target", "different-target", "dangling-target", "linked-target", "hardlinked-target", "writable-target", "hardlinked-alias"} {
		t.Run(fault, func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "keyrings")
			if err := os.Mkdir(dir, 0755); err != nil {
				t.Fatal(err)
			}
			const targetName = "debian-archive-keyring.pgp"
			target := filepath.Join(dir, targetName)
			if err := os.WriteFile(target, make([]byte, 2048), 0644); err != nil {
				t.Fatal(err)
			}
			linkTarget := targetName
			var err error
			switch fault {
			case "absolute-target":
				linkTarget = target
			case "parent-target":
				linkTarget = "../keyrings/" + targetName
			case "different-target":
				linkTarget = "debian-other-keyring.pgp"
				err = os.Rename(target, filepath.Join(dir, linkTarget))
			case "dangling-target":
				err = os.Remove(target)
			case "linked-target":
				if err = os.Rename(target, target+".original"); err == nil {
					err = os.Symlink(target+".original", target)
				}
			case "hardlinked-target":
				err = os.Link(target, target+".other")
			case "writable-target":
				err = os.Chmod(target, 0666)
			}
			if err != nil {
				t.Fatal(err)
			}
			alias := filepath.Join(dir, "debian-archive-keyring.gpg")
			if err := os.Symlink(linkTarget, alias); err != nil {
				t.Fatal(err)
			}
			if fault == "hardlinked-alias" {
				if err := os.Link(alias, alias+".other"); err != nil {
					t.Fatal(err)
				}
			}
			err = checkBootstrapKeyringAt(context.Background(), dir, os.Geteuid())
			if (err == nil) != (fault == "package-alias") {
				t.Fatalf("package keyring layout %s: %v", fault, err)
			}
		})
	}
}
