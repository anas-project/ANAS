//go:build !unix

package securefs

import (
	"context"
	"errors"
	"os"
)

const CrossProcessLockSupported = false

func Lock(context.Context, <-chan struct{}, *os.File, error) error {
	return errors.New("cross-process locking is unavailable on this platform")
}

func Unlock(*os.File) error {
	return errors.New("cross-process unlocking is unavailable on this platform")
}

func TryLock(*os.File) (bool, error) {
	return false, errors.New("cross-process locking is unavailable on this platform")
}
