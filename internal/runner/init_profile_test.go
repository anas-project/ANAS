package runner

import (
	"os"
	"path/filepath"
	"testing"
)

// A shell profile is a file anas did not create and the user cannot afford to
// lose. These cover the three ways the rename could be worse than the truncating
// write it replaces: losing the file's mode, replacing a managed symlink with a
// regular file, and failing outright when the file is not there yet.

func TestAtomicWriteProfileCreatesMissingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".bashrc")

	if err := atomicWriteProfile(path, []byte("export A=1\n")); err != nil {
		t.Fatalf("atomicWriteProfile: %v", err)
	}

	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if string(body) != "export A=1\n" {
		t.Fatalf("contents = %q", body)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if info.Mode().Perm() != 0644 {
		t.Fatalf("mode = %v, want 0644 for a file anas created", info.Mode().Perm())
	}
}

func TestAtomicWriteProfilePreservesMode(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".zshrc")
	if err := os.WriteFile(path, []byte("old\n"), 0600); err != nil {
		t.Fatal(err)
	}

	if err := atomicWriteProfile(path, []byte("new\n")); err != nil {
		t.Fatalf("atomicWriteProfile: %v", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	// os.WriteFile left an existing file's mode alone; the rename must not
	// silently widen a profile the user deliberately kept private.
	if info.Mode().Perm() != 0600 {
		t.Fatalf("mode = %v, want the original 0600", info.Mode().Perm())
	}
	body, _ := os.ReadFile(path)
	if string(body) != "new\n" {
		t.Fatalf("contents = %q", body)
	}
}

func TestAtomicWriteProfileWritesThroughSymlink(t *testing.T) {
	root := t.TempDir()
	dotfiles := filepath.Join(root, "dotfiles")
	home := filepath.Join(root, "home")
	for _, dir := range []string{dotfiles, home} {
		if err := os.Mkdir(dir, 0755); err != nil {
			t.Fatal(err)
		}
	}

	real := filepath.Join(dotfiles, "bashrc")
	if err := os.WriteFile(real, []byte("old\n"), 0640); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(home, ".bashrc")
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	if err := atomicWriteProfile(link, []byte("new\n")); err != nil {
		t.Fatalf("atomicWriteProfile: %v", err)
	}

	// The link must survive: replacing it with a regular file detaches the
	// profile from the repository that manages it, and nothing would report it.
	info, err := os.Lstat(link)
	if err != nil {
		t.Fatalf("lstat: %v", err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Fatal("the symlink was replaced by a regular file")
	}
	body, err := os.ReadFile(real)
	if err != nil {
		t.Fatalf("read link target: %v", err)
	}
	if string(body) != "new\n" {
		t.Fatalf("link target contents = %q, want the new body", body)
	}
	target, err := os.Stat(real)
	if err != nil {
		t.Fatal(err)
	}
	if target.Mode().Perm() != 0640 {
		t.Fatalf("link target mode = %v, want the original 0640", target.Mode().Perm())
	}
}

func TestAtomicWriteProfileWritesThroughDanglingSymlink(t *testing.T) {
	root := t.TempDir()
	real := filepath.Join(root, "bashrc")
	link := filepath.Join(root, ".bashrc")
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	if err := atomicWriteProfile(link, []byte("new\n")); err != nil {
		t.Fatalf("atomicWriteProfile: %v", err)
	}

	info, err := os.Lstat(link)
	if err != nil {
		t.Fatalf("lstat: %v", err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Fatal("a dangling symlink was replaced by a regular file")
	}
	body, err := os.ReadFile(real)
	if err != nil {
		t.Fatalf("read link target: %v", err)
	}
	if string(body) != "new\n" {
		t.Fatalf("link target contents = %q", body)
	}
}

// The temporary file must land in the destination's directory, not in TMPDIR:
// a home directory on its own mount would make the rename fail.
func TestAtomicWriteProfileLeavesNoTemporaryFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".bashrc")

	if err := atomicWriteProfile(path, []byte("body\n")); err != nil {
		t.Fatalf("atomicWriteProfile: %v", err)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != ".bashrc" {
		names := make([]string, 0, len(entries))
		for _, entry := range entries {
			names = append(names, entry.Name())
		}
		t.Fatalf("directory holds %v, want only .bashrc", names)
	}
}
