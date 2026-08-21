package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPrepareFilesCreatesTreeAndDoesNotFollowSymlinks(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "files")
	outside := filepath.Join(base, "outside")
	if err := os.WriteFile(outside, []byte("outside"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "attachments"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "attachments", "task.txt"), []byte("task"), 0o640); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "outside-link")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}

	if err := prepareFiles(root, os.Getuid(), os.Getgid()); err != nil {
		t.Fatal(err)
	}
	info, err := os.Lstat(link)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Fatal("attachment-tree symlink was followed or replaced")
	}
	body, err := os.ReadFile(outside)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "outside" {
		t.Fatalf("symlink target content = %q, want unchanged", body)
	}
}
