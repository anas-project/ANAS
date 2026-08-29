//go:build !unix

package audit

import (
	"context"
	"errors"
	"os"
)

const crossProcessAuditLockSupported = false

func lockAuditFile(context.Context, <-chan struct{}, *os.File) error {
	return errors.New("cross-process audit locking is unavailable on this platform")
}

func unlockAuditFile(*os.File) error {
	return errors.New("cross-process audit unlocking is unavailable on this platform")
}
