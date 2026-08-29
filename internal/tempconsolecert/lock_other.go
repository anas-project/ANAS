//go:build !linux && !darwin && !freebsd && !netbsd && !openbsd && !dragonfly

package tempconsolecert

import (
	"fmt"
	"os"
)

func openSecureLockFile(string) (*os.File, error) {
	return nil, fmt.Errorf("temporary console certificate locking is unsupported on this platform")
}

func lockExclusive(*os.File) error { return fmt.Errorf("flock is unsupported on this platform") }
func unlockFile(*os.File)          {}
