// TEST_CASES: MAILMOD-T-062
package cloudrelay

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestSystemdRuntimeRejectsInvalidStatePaths(t *testing.T) {
	state := t.TempDir()
	if err := os.Chmod(state, 0700); err != nil {
		t.Fatal(err)
	}
	cfg := runtimeFixture(t, filepath.Join(state, "data", "source"), spareListen(t))
	config := writeRuntimeConfig(t, cfg)
	credentials := filepath.Dir(config)
	for _, bad := range []string{"", "relative", state + "/../other", state + ":" + state} {
		if err := RunSystemdRuntime(context.Background(), credentials, bad, nil); !errors.Is(err, ErrRuntimeConfiguration) {
			t.Fatalf("bad state path accepted: %q: %v", bad, err)
		}
	}
	wrong := cfg
	wrong.Directory = filepath.Join(state, "other")
	wrongPath := writeRuntimeConfig(t, wrong)
	if err := RunSystemdRuntime(context.Background(), filepath.Dir(wrongPath), state, nil); !errors.Is(err, ErrRuntimeConfiguration) {
		t.Fatal("wrong source directory accepted", err)
	}
	if _, err := os.Lstat(filepath.Join(state, "data")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("unexpected state created")
	}
	target := t.TempDir()
	if err := os.Symlink(target, filepath.Join(state, "data")); err != nil {
		t.Fatal(err)
	}
	if err := RunSystemdRuntime(context.Background(), credentials, state, nil); !errors.Is(err, ErrRuntimeUnavailable) {
		t.Fatal("state symlink accepted", err)
	}
	entries, err := os.ReadDir(target)
	if err != nil || len(entries) != 0 {
		t.Fatal("changed symlink target", err)
	}
}
