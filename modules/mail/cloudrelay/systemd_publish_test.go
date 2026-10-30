// TEST_CASES: MAILMOD-T-063
package cloudrelay

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/unix"
)

func publishFixture(t *testing.T) (systemdLayout, string, RuntimeConfig, []byte, []byte) {
	t.Helper()
	root := t.TempDir()
	l := systemdLayout{etc: filepath.Join(root, "etc"), libexec: filepath.Join(root, "libexec"), units: filepath.Join(root, "units"), state: filepath.Join(root, "state")}
	for _, p := range []string{l.etc, l.libexec, l.units, l.state} {
		if err := os.Mkdir(p, 0755); err != nil {
			t.Fatal(err)
		}
	}
	cfg := runtimeFixture(t, filepath.Join(l.state, "anas-mail-relay", "data", "source"), spareListen(t))
	cfg.SMTPListen = spareListen(t)
	cfg.SMTPHostname = "relay.example.test"
	return l, writeRuntimeConfig(t, cfg), cfg, []byte("fixed executable"), []byte("fixed systemd unit")
}

func TestPublishSystemdHonorsPrivateUmask(t *testing.T) {
	if os.Getenv("ANAS_TEST_PUBLISH_UMASK") == "1" {
		unix.Umask(0077)
		l, path, _, binary, unit := publishFixture(t)
		if err := publishSystemdFiles(l, path, binary, unit); err != nil {
			t.Fatal(err)
		}
		for _, item := range []string{filepath.Join(l.libexec, "anas-mail-relay"), filepath.Join(l.libexec, "anas-mail-relay", "cloudrelay")} {
			st, err := os.Stat(item)
			if err != nil || st.Mode().Perm() != 0755 {
				t.Fatalf("mode: %v %v", st, err)
			}
		}
		return
	}
	child := exec.Command(os.Args[0], "-test.run=^TestPublishSystemdHonorsPrivateUmask$")
	child.Env = append(os.Environ(), "ANAS_TEST_PUBLISH_UMASK=1")
	if output, err := child.CombinedOutput(); err != nil {
		t.Fatalf("isolated umask test: %v: %s", err, output)
	}
}

func TestPublishSystemdFilesRetryAndResume(t *testing.T) {
	l, path, cfg, binary, unit := publishFixture(t)
	if err := publishSystemdFiles(l, path, binary, unit); err != nil {
		t.Fatal(err)
	}
	if err := publishSystemdFiles(l, path, binary, unit); err != nil {
		t.Fatalf("retry: %v", err)
	}
	configPath := filepath.Join(l.etc, "anas-mail-relay", "runtime.json")
	stage := filepath.Join(l.etc, "anas-mail-relay", ".runtime.json.anas-stage")
	// Simulate interruption after the stage was synced but before completion.
	if err := os.Remove(filepath.Join(l.etc, "anas-mail-relay", "publish-config.done")); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(configPath, stage); err != nil {
		t.Fatal(err)
	}
	if err := publishSystemdFiles(l, path, binary, unit); err != nil {
		t.Fatalf("complete stage recovery: %v", err)
	}
	binding, err := os.ReadFile(filepath.Join(l.etc, "anas-mail-relay", "install.lock"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(binding), cfg.Reader) || strings.Contains(string(binding), cfg.Control) || strings.Contains(string(binding), cfg.TLSPrivateKey) {
		t.Fatal("binding exposes credentials")
	}
	for p, mode := range map[string]os.FileMode{configPath: 0600, filepath.Join(l.libexec, "anas-mail-relay", "cloudrelay"): 0755, filepath.Join(l.units, "anas-mail-relay.service"): 0644} {
		st, err := os.Stat(p)
		if err != nil || st.Mode().Perm() != mode {
			t.Fatalf("mode %s: %v %v", p, st, err)
		}
	}
	if err := publishSystemdFiles(l, path, []byte("different executable"), unit); !errors.Is(err, ErrPublishConflict) {
		t.Fatalf("changed input: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(l.libexec, "anas-mail-relay", "cloudrelay"))
	if err != nil || string(got) != string(binary) {
		t.Fatal("changed original executable")
	}
}
func TestPublishSystemdFilesRejectsConflicts(t *testing.T) {
	t.Run("other unit lookup", func(t *testing.T) {
		l, path, _, binary, unit := publishFixture(t)
		other := filepath.Join(filepath.Dir(l.units), "runtime-units")
		if err := os.Mkdir(other, 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(other, "anas-mail-relay.service"), []byte("keep"), 0644); err != nil {
			t.Fatal(err)
		}
		l.otherUnits = []string{other}
		if err := publishSystemdFiles(l, path, binary, unit); !errors.Is(err, ErrPublishConflict) {
			t.Fatal(err)
		}
		if _, err := os.Lstat(filepath.Join(l.etc, "anas-mail-relay")); !errors.Is(err, os.ErrNotExist) {
			t.Fatal("created configuration directory")
		}
	})
	t.Run("foreign unit", func(t *testing.T) {
		l, path, _, binary, unit := publishFixture(t)
		foreign := filepath.Join(l.units, "anas-mail-relay.service")
		if err := os.WriteFile(foreign, []byte("keep"), 0644); err != nil {
			t.Fatal(err)
		}
		if err := publishSystemdFiles(l, path, binary, unit); !errors.Is(err, ErrPublishConflict) {
			t.Fatal(err)
		}
		raw, _ := os.ReadFile(foreign)
		if string(raw) != "keep" {
			t.Fatal("overwrote unit")
		}
		if _, err := os.Lstat(filepath.Join(l.etc, "anas-mail-relay")); !errors.Is(err, os.ErrNotExist) {
			t.Fatal("created configuration directory")
		}
	})
	t.Run("foreign directory", func(t *testing.T) {
		l, path, _, binary, unit := publishFixture(t)
		d := filepath.Join(l.etc, "anas-mail-relay")
		if err := os.Mkdir(d, 0700); err != nil {
			t.Fatal(err)
		}
		if err := publishSystemdFiles(l, path, binary, unit); !errors.Is(err, ErrPublishUnknown) {
			t.Fatal(err)
		}
		entries, err := os.ReadDir(d)
		if err != nil || len(entries) != 0 {
			t.Fatal("modified foreign directory", err)
		}
	})
	t.Run("incomplete stage", func(t *testing.T) {
		l, path, _, binary, unit := publishFixture(t)
		if err := publishSystemdFiles(l, path, binary, unit); err != nil {
			t.Fatal(err)
		}
		dest := filepath.Join(l.etc, "anas-mail-relay", "runtime.json")
		if err := os.Remove(filepath.Join(l.etc, "anas-mail-relay", "publish-config.done")); err != nil {
			t.Fatal(err)
		}
		if err := os.Remove(dest); err != nil {
			t.Fatal(err)
		}
		stage := filepath.Join(l.etc, "anas-mail-relay", ".runtime.json.anas-stage")
		if err := os.WriteFile(stage, []byte("partial"), 0600); err != nil {
			t.Fatal(err)
		}
		if err := publishSystemdFiles(l, path, binary, unit); !errors.Is(err, ErrPublishUnknown) {
			t.Fatal(err)
		}
		raw, _ := os.ReadFile(stage)
		if string(raw) != "partial" {
			t.Fatal("altered incomplete stage")
		}
	})
	for _, item := range []string{"config", "binary", "unit", "binary-directory"} {
		t.Run("removed "+item, func(t *testing.T) {
			l, path, _, binary, unit := publishFixture(t)
			if err := publishSystemdFiles(l, path, binary, unit); err != nil {
				t.Fatal(err)
			}
			var target string
			switch item {
			case "config":
				target = filepath.Join(l.etc, "anas-mail-relay", "runtime.json")
			case "binary", "binary-directory":
				target = filepath.Join(l.libexec, "anas-mail-relay", "cloudrelay")
			case "unit":
				target = filepath.Join(l.units, "anas-mail-relay.service")
			}
			if err := os.Remove(target); err != nil {
				t.Fatal(err)
			}
			if item == "binary-directory" {
				target = filepath.Dir(target)
				if err := os.Remove(target); err != nil {
					t.Fatal(err)
				}
			}
			if err := publishSystemdFiles(l, path, binary, unit); !errors.Is(err, ErrPublishConflict) {
				t.Fatalf("removed target was recreated: %v", err)
			}
			if _, err := os.Lstat(target); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("removed target reappeared")
			}
		})
	}
	t.Run("symlink input", func(t *testing.T) {
		l, path, _, binary, unit := publishFixture(t)
		link := filepath.Join(t.TempDir(), "input")
		if err := os.Symlink(path, link); err != nil {
			t.Fatal(err)
		}
		if err := publishSystemdFiles(l, link, binary, unit); !errors.Is(err, ErrPublishConflict) {
			t.Fatal(err)
		}
	})
}
