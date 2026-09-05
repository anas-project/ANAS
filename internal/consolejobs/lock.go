package consolejobs

import (
	"context"
	"os"

	"github.com/anas-project/ANAS/internal/securefs"
)

// crossProcessLockSupported gates the store: without real advisory locking the
// execution lease cannot exclude a second daemon, so Open fails closed.
const crossProcessLockSupported = securefs.CrossProcessLockSupported

func lockFileContext(ctx context.Context, done <-chan struct{}, file *os.File) error {
	return securefs.Lock(ctx, done, file, errStoreClosed)
}

func unlockFile(file *os.File) error { return securefs.Unlock(file) }

func tryLockFile(file *os.File) (bool, error) { return securefs.TryLock(file) }
