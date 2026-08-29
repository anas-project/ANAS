//go:build unix

package consoleconfig

import (
	"fmt"
	"io/fs"
	"os"
	"syscall"
)

type unixOwnerFilePolicy struct {
	uid uint32
}

// RootOwnedFilePolicy is the production anasd policy: a regular, non-symlink
// file owned by root with no permission bits outside 0600.
func RootOwnedFilePolicy() FileSecurityPolicy {
	return unixOwnerFilePolicy{uid: 0}
}

// CurrentUIDFilePolicy applies the production shape while allowing the current
// Unix user to own the file. It exists for non-root development and tests; the
// production daemon must use RootOwnedFilePolicy.
func CurrentUIDFilePolicy() FileSecurityPolicy {
	return unixOwnerFilePolicy{uid: uint32(os.Geteuid())}
}

func (policy unixOwnerFilePolicy) Validate(path string, info fs.FileInfo) error {
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("%s must not be a symbolic link", path)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("%s must be a regular file", path)
	}
	permissions := info.Mode().Perm()
	if permissions&^fs.FileMode(0o600) != 0 {
		return fmt.Errorf("%s permissions %04o are wider than 0600", path, permissions)
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return fmt.Errorf("%s owner is unavailable", path)
	}
	if stat.Uid != policy.uid {
		return fmt.Errorf("%s owner UID is %d, want %d", path, stat.Uid, policy.uid)
	}
	return nil
}
