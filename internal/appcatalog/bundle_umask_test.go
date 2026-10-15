//go:build linux || darwin

// TEST_CASES: PORTAL-T-001
package appcatalog

import (
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"
)

func TestBundleReadableByConsumerUnderRestrictiveUmask(t *testing.T) {
	// Umask is process-wide, so this test must not run in parallel.
	previous := unix.Umask(0077)
	defer unix.Umask(previous)
	root := t.TempDir()
	catalog, inputs, err := Compile(Config{}, []Input{sampleInput("cloud")}, root, "en")
	if err != nil {
		t.Fatal(err)
	}
	catalog, assets, files, err := BuildAssets(catalog, inputs)
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(root, "bundle")
	if _, err := WriteBundle(dir, catalog, assets, files); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{dir, filepath.Join(dir, "assets")} {
		info, err := os.Stat(name)
		if err != nil || info.Mode().Perm() != 0755 {
			t.Fatalf("consumer cannot traverse bundle directory %s: %v", name, err)
		}
	}
	paths := []string{"catalog.json", "assets.json"}
	for name := range files {
		paths = append(paths, name)
	}
	for _, name := range paths {
		info, err := os.Stat(filepath.Join(dir, name))
		if err != nil || info.Mode().Perm() != 0644 {
			t.Fatalf("consumer cannot read bundle file %s: %v", name, err)
		}
	}
	if _, err := Read(filepath.Join(dir, "catalog.json")); err != nil {
		t.Fatal(err)
	}
}
