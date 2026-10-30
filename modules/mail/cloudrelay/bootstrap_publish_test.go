// TEST_CASES: MAILMOD-T-075
package cloudrelay

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func bootstrapPublicationFixture(t *testing.T) (string, string, []byte) {
	t.Helper()
	root := t.TempDir()
	etc, lib := filepath.Join(root, "etc"), filepath.Join(root, "libexec")
	for _, path := range []string{etc, lib} {
		if err := os.Mkdir(path, 0700); err != nil {
			t.Fatal(err)
		}
	}
	return etc, lib, []byte("synthetic bootstrap executable")
}
func TestBootstrapPublishStableAndRefusesChangedOrMissingFiles(t *testing.T) {
	etc, lib, binary := bootstrapPublicationFixture(t)
	name := "anas-mail-relay-bootstrap"
	journal := filepath.Join(etc, name)
	target := filepath.Join(lib, name, "cloudrelay")
	if err := publishBootstrapBinary(etc, lib, binary); err != nil {
		t.Fatal(err)
	}
	if err := publishBootstrapBinary(etc, lib, binary); err != nil {
		t.Fatal("retry", err)
	}
	data, err := os.ReadFile(target)
	if err != nil || !bytes.Equal(data, binary) {
		t.Fatal("wrong binary", err)
	}
	for _, p := range []string{journal, filepath.Join(lib, name)} {
		st, e := os.Stat(p)
		if e != nil || st.Mode().Perm() != map[string]os.FileMode{journal: 0700, filepath.Join(lib, name): 0755}[p] {
			t.Fatal("unsafe directory", e)
		}
	}
	st, err := os.Stat(target)
	if err != nil || st.Mode().Perm() != 0755 {
		t.Fatal("unsafe binary", err)
	}
	if err = publishBootstrapBinary(etc, lib, []byte("different executable")); !errors.Is(err, ErrPublishConflict) {
		t.Fatal("binary changed", err)
	}
	if err = os.Remove(target); err != nil {
		t.Fatal(err)
	}
	if err = publishBootstrapBinary(etc, lib, binary); !errors.Is(err, ErrPublishConflict) {
		t.Fatal("removed binary recreated", err)
	}
	if _, err = os.Lstat(target); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("missing binary restored", err)
	}
}
func TestBootstrapPublishResumeAndUnknownStates(t *testing.T) {
	for _, mode := range []string{"valid-stage", "missing-stage", "missing-directory", "unfinished-directory", "partial-lock", "foreign-binary", "symlink-directory", "unsafe-directory"} {
		t.Run(mode, func(t *testing.T) {
			etc, lib, binary := bootstrapPublicationFixture(t)
			name := "anas-mail-relay-bootstrap"
			journal := filepath.Join(etc, name)
			dir := filepath.Join(lib, name)
			target := filepath.Join(dir, "cloudrelay")
			switch mode {
			case "foreign-binary":
				if err := os.Mkdir(dir, 0755); err != nil {
					t.Fatal(err)
				}
			case "symlink-directory":
				if err := os.Symlink(t.TempDir(), dir); err != nil {
					t.Fatal(err)
				}
			case "partial-lock":
				if err := os.Mkdir(journal, 0700); err != nil {
					t.Fatal(err)
				}
			default:
				if err := publishBootstrapBinary(etc, lib, binary); err != nil {
					t.Fatal(err)
				}
				switch mode {
				case "valid-stage", "missing-stage":
					if err := os.Remove(filepath.Join(journal, "publish-binary.done")); err != nil {
						t.Fatal(err)
					}
					if mode == "valid-stage" {
						if err := os.Rename(target, filepath.Join(dir, ".cloudrelay.anas-stage")); err != nil {
							t.Fatal(err)
						}
					} else if err := os.Remove(target); err != nil {
						t.Fatal(err)
					}
				case "missing-directory":
					if err := os.Remove(target); err != nil {
						t.Fatal(err)
					}
					if err := os.Remove(dir); err != nil {
						t.Fatal(err)
					}
				case "unfinished-directory":
					if err := os.Remove(filepath.Join(journal, "directory.done")); err != nil {
						t.Fatal(err)
					}
				case "unsafe-directory":
					if err := os.Chmod(dir, 0777); err != nil {
						t.Fatal(err)
					}
				}
			}
			err := publishBootstrapBinary(etc, lib, binary)
			if mode == "valid-stage" {
				if err != nil {
					t.Fatal("complete stage not resumed", err)
				}
				data, e := os.ReadFile(target)
				if e != nil || !bytes.Equal(data, binary) {
					t.Fatal("wrong resumed binary", e)
				}
				return
			}
			want := ErrPublishConflict
			if mode == "missing-stage" || mode == "partial-lock" || mode == "unfinished-directory" {
				want = ErrPublishUnknown
			}
			if !errors.Is(err, want) {
				t.Fatalf("%s: wanted %v, got %v", mode, want, err)
			}
		})
	}
}
