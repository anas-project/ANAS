//go:build !aix && !android && !darwin && !dragonfly && !freebsd && !illumos && !ios && !linux && !netbsd && !openbsd && !solaris

package consoletls

import (
	"fmt"
	"io/fs"
	"runtime"
)

// RootOwnedFileSecurityCheck fails closed on platforms without Unix UID file
// metadata. Callers on those platforms must supply an ACL-aware CheckFile
// implementation instead.
func RootOwnedFileSecurityCheck(path string, role FileRole, _ fs.FileInfo) error {
	return fmt.Errorf("%s %s: root ownership checks are unsupported on %s", role, path, runtime.GOOS)
}
