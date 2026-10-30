// TEST_CASES: MAILMOD-T-063, MAILMOD-T-075
package cloudrelay

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestStandardLibexecCreatesOrVerifiesWithoutAdoptingUnsafeTargets(t *testing.T) {
	t.Run("create-and-repeat", func(t *testing.T) {
		parent := t.TempDir()
		if err := os.Chmod(parent, 0700); err != nil {
			t.Fatal(err)
		}
		if err := ensureStandardLibexec(parent); err != nil {
			t.Fatal(err)
		}
		target := filepath.Join(parent, "libexec")
		info, err := os.Stat(target)
		if err != nil || info.Mode().Perm() != 0755 {
			t.Fatal("wrong directory mode", err)
		}
		marker := filepath.Join(target, "other")
		if err := os.WriteFile(marker, []byte("preserve"), 0600); err != nil {
			t.Fatal(err)
		}
		if err := ensureStandardLibexec(parent); err != nil {
			t.Fatal(err)
		}
		data, err := os.ReadFile(marker)
		if err != nil || string(data) != "preserve" {
			t.Fatal("existing directory changed", err)
		}
	})
	for _, mode := range []string{"symlink", "file", "writable", "not-traversable", "unsafe-parent"} {
		t.Run(mode, func(t *testing.T) {
			parent := t.TempDir()
			if err := os.Chmod(parent, 0700); err != nil {
				t.Fatal(err)
			}
			target := filepath.Join(parent, "libexec")
			switch mode {
			case "symlink":
				if err := os.Symlink(t.TempDir(), target); err != nil {
					t.Fatal(err)
				}
			case "file":
				if err := os.WriteFile(target, []byte("external"), 0600); err != nil {
					t.Fatal(err)
				}
			case "writable":
				if err := os.Mkdir(target, 0777); err != nil {
					t.Fatal(err)
				}
				if err := os.Chmod(target, 0777); err != nil {
					t.Fatal(err)
				}
			case "not-traversable":
				if err := os.Mkdir(target, 0700); err != nil {
					t.Fatal(err)
				}
			case "unsafe-parent":
				if err := os.Chmod(parent, 0777); err != nil {
					t.Fatal(err)
				}
			}
			if err := ensureStandardLibexec(parent); !errors.Is(err, ErrPublishConflict) {
				t.Fatal("unsafe directory accepted", err)
			}
		})
	}
}
