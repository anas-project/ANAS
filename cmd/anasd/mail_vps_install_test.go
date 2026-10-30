// TEST_CASES: MAILMOD-T-072
package main

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/anas-project/ANAS/modules/mail/cloudflare/installer"
)

func TestMailVPSOperationDirectories(t *testing.T) {
	t.Run("first-and-retry", func(t *testing.T) {
		dir := t.TempDir()
		if _, _, err := vpsInstallDirectories(dir, false); !errors.Is(err, installer.ErrUnknown) {
			t.Fatal("prepare claimed empty operation", err)
		}
		provision, relay, err := vpsInstallDirectories(dir, true)
		if err != nil {
			t.Fatal(err)
		}
		if workerOperationAllowed(dir) {
			t.Fatal("VPS operation accepted as Worker")
		}
		if _, err = relayOperationDirectory(dir); err == nil {
			t.Fatal("VPS operation accepted as manual relay")
		}
		p, r, err := vpsInstallDirectories(dir, true)
		if err != nil || p != provision || r != relay {
			t.Fatal("retry changed binding", err)
		}
		if err = os.Remove(relay); err != nil {
			t.Fatal(err)
		}
		if _, _, err = vpsInstallDirectories(dir, true); !errors.Is(err, installer.ErrUnknown) {
			t.Fatal("missing journal recreated", err)
		}
		if _, err = os.Lstat(relay); !errors.Is(err, os.ErrNotExist) {
			t.Fatal("missing journal was recreated", err)
		}
	})
	t.Run("other-operation", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.Mkdir(filepath.Join(dir, "relay"), 0700); err != nil {
			t.Fatal(err)
		}
		if _, _, err := vpsInstallDirectories(dir, true); !errors.Is(err, installer.ErrConflict) {
			t.Fatal("other type accepted", err)
		}
	})
	t.Run("symlink", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.Symlink(t.TempDir(), filepath.Join(dir, "vps-ssh")); err != nil {
			t.Fatal(err)
		}
		if _, _, err := vpsInstallDirectories(dir, true); err == nil {
			t.Fatal("symlink accepted")
		}
	})
}
