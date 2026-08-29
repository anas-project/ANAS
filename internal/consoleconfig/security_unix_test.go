//go:build unix

package consoleconfig

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCurrentUIDFilePolicyAcceptsPrivateRegularFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "anasd.yml")
	if err := os.WriteFile(path, []byte(validSource()), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path, CurrentUIDFilePolicy()); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o400); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path, CurrentUIDFilePolicy()); err != nil {
		t.Fatalf("0400 should not be wider than 0600: %v", err)
	}
}

func TestCurrentUIDFilePolicyRejectsWidePermissions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "anasd.yml")
	if err := os.WriteFile(path, []byte(validSource()), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o640); err != nil {
		t.Fatal(err)
	}
	_, err := Load(path, CurrentUIDFilePolicy())
	if err == nil || !strings.Contains(err.Error(), "wider than 0600") {
		t.Fatalf("wide permissions error = %v", err)
	}
}

func TestCurrentUIDFilePolicyRejectsSymlinkAndNonRegularFile(t *testing.T) {
	directory := t.TempDir()
	target := filepath.Join(directory, "target.yml")
	if err := os.WriteFile(target, []byte(validSource()), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(directory, "anasd.yml")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(link, CurrentUIDFilePolicy()); err == nil || !strings.Contains(err.Error(), "symbolic link") {
		t.Fatalf("symlink error = %v", err)
	}
	if _, err := Load(directory, CurrentUIDFilePolicy()); err == nil || !strings.Contains(err.Error(), "regular file") {
		t.Fatalf("directory error = %v", err)
	}
}

func TestRootOwnedFilePolicyRequiresUIDZero(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("test process already owns fixture as root")
	}
	path := filepath.Join(t.TempDir(), "anasd.yml")
	if err := os.WriteFile(path, []byte(validSource()), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := Load(path, RootOwnedFilePolicy())
	if err == nil || !strings.Contains(err.Error(), "want 0") {
		t.Fatalf("root owner error = %v", err)
	}
}
