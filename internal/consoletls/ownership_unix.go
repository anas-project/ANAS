//go:build aix || android || darwin || dragonfly || freebsd || illumos || ios || linux || netbsd || openbsd || solaris

package consoletls

import (
	"fmt"
	"io/fs"
	"syscall"
)

// RootOwnedFileSecurityCheck rejects artifacts whose Unix owner is not UID 0.
// Pass it as Options.CheckFile to combine ownership validation with the
// package's unconditional file-type and mode checks.
func RootOwnedFileSecurityCheck(path string, role FileRole, info fs.FileInfo) error {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return fmt.Errorf("%s %s: cannot determine Unix owner", role, path)
	}
	if stat.Uid != 0 {
		return fmt.Errorf("%s %s must be owned by root (uid 0, got %d)", role, path, stat.Uid)
	}
	return nil
}
