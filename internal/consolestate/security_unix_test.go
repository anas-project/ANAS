//go:build unix

package consolestate

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestOpenRejectsUnsafeConsoleStore(t *testing.T) {
	tests := []struct {
		name  string
		setup func(*testing.T, string)
		want  string
	}{
		{name: "wide permissions", setup: func(t *testing.T, path string) {
			if err := os.Mkdir(path, 0o755); err != nil {
				t.Fatal(err)
			}
		}, want: "mode is 0755"},
		{name: "symlink", setup: func(t *testing.T, path string) {
			target := filepath.Join(filepath.Dir(path), "target")
			if err := os.Mkdir(target, 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(target, path); err != nil {
				t.Fatal(err)
			}
		}, want: "non-symlink directory"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "console")
			test.setup(t, path)
			_, err := Open(context.Background(), path, &recordingAudit{})
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Open error = %v", err)
			}
		})
	}
}

func TestStateAndLockRejectSymlinkHardlinkWideModeAndNonRegular(t *testing.T) {
	tests := []struct {
		name   string
		target string
		mutate func(*testing.T, string)
		want   string
	}{
		{name: "state wide mode", target: StateFileName, mutate: func(t *testing.T, path string) {
			if err := os.Chmod(path, 0o644); err != nil {
				t.Fatal(err)
			}
		}, want: "mode is 0644"},
		{name: "state symlink", target: StateFileName, mutate: replaceWithSymlink, want: "non-symlink regular"},
		{name: "state hardlink", target: StateFileName, mutate: addHardlink, want: "link count is 2"},
		{name: "state directory", target: StateFileName, mutate: replaceWithDirectory, want: "non-symlink regular"},
		{name: "lock wide mode", target: LockFileName, mutate: func(t *testing.T, path string) {
			if err := os.Chmod(path, 0o640); err != nil {
				t.Fatal(err)
			}
		}, want: "mode is 0640"},
		{name: "lock symlink", target: LockFileName, mutate: replaceWithSymlink, want: "non-symlink regular"},
		{name: "lock hardlink", target: LockFileName, mutate: addHardlink, want: "link count is 2"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			directory := filepath.Join(t.TempDir(), "console")
			store := openTestStore(t, directory, &recordingAudit{})
			path := filepath.Join(directory, test.target)
			test.mutate(t, path)
			_, err := store.Current(context.Background())
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Current error = %v", err)
			}
		})
	}
}

func TestOwnerAndLinkMetadataAreMandatory(t *testing.T) {
	current := uint32(os.Geteuid())
	wrongOwner := &fakeFileInfo{mode: 0o600, sys: &syscall.Stat_t{Uid: current + 1, Nlink: 1}}
	if err := validatePrivateFileInfo(wrongOwner, "state"); err == nil || !strings.Contains(err.Error(), "owner UID") {
		t.Fatalf("wrong-owner error = %v", err)
	}
	missingMetadata := &fakeFileInfo{mode: 0o600, sys: struct{}{}}
	if err := validatePrivateFileInfo(missingMetadata, "state"); err == nil || !strings.Contains(err.Error(), "metadata is unavailable") {
		t.Fatalf("missing-metadata error = %v", err)
	}
	multipleLinks := &fakeFileInfo{mode: 0o600, sys: &syscall.Stat_t{Uid: current, Nlink: 2}}
	if err := validatePrivateFileInfo(multipleLinks, "state"); err == nil || !strings.Contains(err.Error(), "link count") {
		t.Fatalf("multiple-link error = %v", err)
	}
}

func replaceWithSymlink(t *testing.T, path string) {
	t.Helper()
	target := path + ".target"
	if err := os.Rename(path, target); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, path); err != nil {
		t.Fatal(err)
	}
}

func addHardlink(t *testing.T, path string) {
	t.Helper()
	if err := os.Link(path, path+".hardlink"); err != nil {
		t.Fatal(err)
	}
}

func replaceWithDirectory(t *testing.T, path string) {
	t.Helper()
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatal(err)
	}
}

type fakeFileInfo struct {
	mode fs.FileMode
	sys  any
}

func (info *fakeFileInfo) Name() string       { return "state" }
func (info *fakeFileInfo) Size() int64        { return 0 }
func (info *fakeFileInfo) Mode() fs.FileMode  { return info.mode }
func (info *fakeFileInfo) ModTime() time.Time { return time.Time{} }
func (info *fakeFileInfo) IsDir() bool        { return false }
func (info *fakeFileInfo) Sys() any           { return info.sys }

var _ fs.FileInfo = (*fakeFileInfo)(nil)
