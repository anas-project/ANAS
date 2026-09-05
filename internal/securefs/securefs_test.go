package securefs

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// These invariants are the reason the package exists. Both durable stores rely
// on them, so they are asserted here once rather than twice at a distance.
func TestFileValidationRejectsUnsafeModes(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "journal")
	if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateFileInfo(info, "journal"); err != nil {
		t.Fatalf("a 0600 regular file was rejected: %v", err)
	}

	if err := os.Chmod(path, 0o640); err != nil {
		t.Fatal(err)
	}
	info, _ = os.Lstat(path)
	if err := ValidateFileInfo(info, "journal"); err == nil || !strings.Contains(err.Error(), "want 0600") {
		t.Fatalf("group-readable file error = %v", err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatal(err)
	}

	// A second hard link is another name for the same credential-bearing
	// journal, which defeats atomic replacement of the canonical path.
	linked := filepath.Join(dir, "journal.link")
	if err := os.Link(path, linked); err != nil {
		t.Skipf("hard links unavailable: %v", err)
	}
	info, _ = os.Lstat(path)
	if err := ValidateFileInfo(info, "journal"); err == nil || !strings.Contains(err.Error(), "link count") {
		t.Fatalf("multi-link file error = %v", err)
	}
	if err := os.Remove(linked); err != nil {
		t.Fatal(err)
	}

	symlink := filepath.Join(dir, "journal.symlink")
	if err := os.Symlink(path, symlink); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	info, _ = os.Lstat(symlink)
	if err := ValidateFileInfo(info, "journal"); err == nil || !strings.Contains(err.Error(), "non-symlink") {
		t.Fatalf("symlink error = %v", err)
	}
}

func TestOpenDirectoryCreatesPrivateStoreAndReportsCreatedEntries(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "outer", "inner", "store")
	directory, created, err := OpenDirectory(target, "test store")
	if err != nil {
		t.Fatal(err)
	}
	defer directory.Close()
	info, err := os.Lstat(target)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o700 {
		t.Fatalf("store mode = %04o, want 0700", info.Mode().Perm())
	}
	// Entries are returned root first so the caller can fsync leaf first.
	want := []string{filepath.Join(root, "outer"), filepath.Join(root, "outer", "inner"), target}
	if len(created) != len(want) {
		t.Fatalf("created entries = %v, want %v", created, want)
	}
	for index := range want {
		if created[index] != want[index] {
			t.Fatalf("created entries = %v, want %v", created, want)
		}
	}
	if err := SyncCreatedDirectoryEntries(created); err != nil {
		t.Fatalf("sync created entries: %v", err)
	}
}

func TestOpenDirectoryRejectsAWorldReadableStore(t *testing.T) {
	target := filepath.Join(t.TempDir(), "store")
	if err := os.Mkdir(target, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, _, err := OpenDirectory(target, "test store"); err == nil || !strings.Contains(err.Error(), "want 0700") {
		t.Fatalf("open of a 0755 store = %v, want a mode refusal", err)
	}
}

func TestCreateExclusiveNamedFileRefusesAnExistingPath(t *testing.T) {
	path := filepath.Join(t.TempDir(), "compaction.tmp")
	file, err := CreateExclusiveNamedFile(path, "compaction temp")
	if err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := CreateExclusiveNamedFile(path, "compaction temp"); !errors.Is(err, os.ErrExist) {
		t.Fatalf("second exclusive create = %v, want os.ErrExist", err)
	}
}

func TestOpenExistingNamedFileDoesNotCreate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "absent")
	if _, err := OpenExistingNamedFile(path, "journal"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("open of an absent journal = %v, want os.ErrNotExist", err)
	}
	if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("a failed open created the journal")
	}
}

// A short write with a nil error would truncate a journal record, so WriteAll
// must keep going rather than trust one Write call.
func TestWriteAllCompletesShortWrites(t *testing.T) {
	writer := &shortWriter{limit: 3}
	body := []byte("0123456789")
	if err := WriteAll(writer, body); err != nil {
		t.Fatal(err)
	}
	if writer.written.String() != string(body) {
		t.Fatalf("written = %q, want %q", writer.written.String(), body)
	}
	if writer.calls < 4 {
		t.Fatalf("calls = %d, want the write to have been resumed", writer.calls)
	}
	// A writer that reports progress it did not make must surface an error
	// rather than spin forever.
	if err := WriteAll(&shortWriter{limit: 0}, body); !errors.Is(err, io.ErrShortWrite) {
		t.Fatalf("a zero-length write = %v, want io.ErrShortWrite", err)
	}
}

type shortWriter struct {
	limit   int
	calls   int
	written strings.Builder
}

func (w *shortWriter) Write(body []byte) (int, error) {
	w.calls++
	if w.limit == 0 {
		return 0, nil
	}
	if len(body) > w.limit {
		body = body[:w.limit]
	}
	w.written.Write(body)
	return len(body), nil
}
