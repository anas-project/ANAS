//go:build unix

package consolejobs

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestOpenRejectsLinkedOrphanCompactionFiles(t *testing.T) {
	for _, fixture := range []struct {
		name      string
		create    func(string, string) error
		wantError string
	}{
		{
			name: "symlink",
			create: func(target, path string) error {
				return os.Symlink(target, path)
			},
			wantError: "non-symlink regular file",
		},
		{
			name: "hardlink",
			create: func(target, path string) error {
				return os.Link(target, path)
			},
			wantError: "link count",
		},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			directory := filepath.Join(t.TempDir(), "jobs")
			store, err := Open(directory, Options{})
			if err != nil {
				t.Fatal(err)
			}
			if err := store.Close(); err != nil {
				t.Fatal(err)
			}

			target := filepath.Join(t.TempDir(), "target")
			const targetBody = "must remain untouched"
			if err := os.WriteFile(target, []byte(targetBody), 0o600); err != nil {
				t.Fatal(err)
			}
			orphanPath := filepath.Join(directory, JournalCompactionFilename)
			if err := fixture.create(target, orphanPath); err != nil {
				t.Fatal(err)
			}

			_, err = Open(directory, Options{})
			if !errors.Is(err, ErrUnavailable) || !strings.Contains(err.Error(), fixture.wantError) {
				t.Fatalf("Open with %s compaction file error = %v, want %q rejection", fixture.name, err, fixture.wantError)
			}
			body, readErr := os.ReadFile(target)
			if readErr != nil {
				t.Fatal(readErr)
			}
			if string(body) != targetBody {
				t.Fatalf("linked target after rejected cleanup = %q, want %q", body, targetBody)
			}
			if _, err := os.Lstat(orphanPath); err != nil {
				t.Fatalf("rejected %s compaction path was removed: %v", fixture.name, err)
			}
		})
	}
}
