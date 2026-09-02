//go:build !unix

package consolejobs

import (
	"context"
	"errors"
	"os"
)

const crossProcessLockSupported = false

func lockFileContext(context.Context, <-chan struct{}, *os.File) error {
	return errors.New("cross-process job locking is unavailable on this platform")
}

func unlockFile(*os.File) error {
	return errors.New("cross-process job unlocking is unavailable on this platform")
}

func tryLockFile(*os.File) (bool, error) {
	return false, errors.New("cross-process job locking is unavailable on this platform")
}
