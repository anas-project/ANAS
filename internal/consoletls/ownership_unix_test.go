//go:build aix || android || darwin || dragonfly || freebsd || illumos || ios || linux || netbsd || openbsd || solaris

package consoletls

import (
	"io/fs"
	"os"
	"strings"
	"syscall"
	"testing"
)

type fileInfoWithSystem struct {
	fs.FileInfo
	system any
}

func (i fileInfoWithSystem) Sys() any {
	return i.system
}

func TestRootOwnedFileSecurityCheck(t *testing.T) {
	path := filepathForOwnershipTest(t)
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		t.Fatalf("file metadata type = %T", info.Sys())
	}

	root := *stat
	root.Uid = 0
	if err := RootOwnedFileSecurityCheck(path, FileRoleCertificate, fileInfoWithSystem{FileInfo: info, system: &root}); err != nil {
		t.Fatalf("root-owned artifact rejected: %v", err)
	}

	nonRoot := *stat
	nonRoot.Uid = 1234
	err = RootOwnedFileSecurityCheck(path, FileRolePrivateKey, fileInfoWithSystem{FileInfo: info, system: &nonRoot})
	if err == nil || !strings.Contains(err.Error(), "uid 0") {
		t.Fatalf("non-root-owned artifact error = %v", err)
	}

	err = RootOwnedFileSecurityCheck(path, FileRoleCertificate, fileInfoWithSystem{FileInfo: info})
	if err == nil || !strings.Contains(err.Error(), "cannot determine") {
		t.Fatalf("missing owner metadata error = %v", err)
	}
}

func filepathForOwnershipTest(t *testing.T) string {
	t.Helper()
	path := t.TempDir() + string(os.PathSeparator) + "artifact"
	if err := os.WriteFile(path, []byte("test"), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}
