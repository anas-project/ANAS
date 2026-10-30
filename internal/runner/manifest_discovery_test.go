package runner

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRegistryDiscoversManifestsBesideApplicationDrafts(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"published", "draft"} {
		if err := os.Mkdir(filepath.Join(root, name), 0700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "draft", "README.md"), []byte("Unfinished application"), 0600); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "published", "module.yml")
	const body = "api_version: anas.module/v1\nkind: Module\nname: published\nversion: 1.0.0\nrevision: 1\nstatus: release\nabi:\n  supports: [anas.module-hook/v1]\nruntime:\n  type: builtin\n"
	if err := os.WriteFile(path, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	reg, err := loadRegistryDir(root)
	if err != nil || len(reg) != 1 || reg["published"].Name != "published" {
		t.Fatalf("manifest discovery: %v, %v", reg, err)
	}
	if err := os.WriteFile(path, []byte("invalid: ["), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadRegistryDir(root); err == nil {
		t.Fatal("present but invalid manifest was ignored")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("missing-manifest.yml", path); err != nil {
		t.Fatal(err)
	}
	if _, err := loadRegistryDir(root); err == nil {
		t.Fatal("present but broken manifest link was ignored")
	}
}
