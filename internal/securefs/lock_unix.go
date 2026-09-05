//go:build unix

package securefs

import (
	"context"
	"errors"
	"os"
	"syscall"
	"time"
)

// CrossProcessLockSupported reports whether Lock actually excludes other
// processes on this platform. Callers fail closed when it is false.
const CrossProcessLockSupported = true

const lockRetryInterval = 10 * time.Millisecond

// Lock takes an exclusive advisory lock without blocking in the kernel.
//
// A blocking flock cannot be cancelled and pins the calling OS thread, which is
// unacceptable in a daemon: one stuck holder would hold a request goroutine
// forever. Retrying LOCK_NB on a short timer keeps waiting cancellable through
// ctx and through the store's own close channel.
func Lock(ctx context.Context, closed <-chan struct{}, file *os.File, closedErr error) error {
	if file == nil {
		return errors.New("lock descriptor is unavailable")
	}
	for {
		err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			return nil
		}
		if errors.Is(err, syscall.EINTR) {
			continue
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) && !errors.Is(err, syscall.EAGAIN) {
			return err
		}
		timer := time.NewTimer(lockRetryInterval)
		select {
		case <-ctx.Done():
			stopAndDrainTimer(timer)
			return ctx.Err()
		case <-closed:
			stopAndDrainTimer(timer)
			return closedErr
		case <-timer.C:
		}
	}
}

func Unlock(file *os.File) error {
	if file == nil {
		return errors.New("lock descriptor is unavailable")
	}
	for {
		err := syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
		if errors.Is(err, syscall.EINTR) {
			continue
		}
		return err
	}
}

// TryLock reports whether the lock was free, without waiting. A false result is
// not an error: it is how a second daemon discovers the execution lease is held.
func TryLock(file *os.File) (bool, error) {
	if file == nil {
		return false, errors.New("lock descriptor is unavailable")
	}
	for {
		err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		switch {
		case err == nil:
			return true, nil
		case errors.Is(err, syscall.EINTR):
			continue
		case errors.Is(err, syscall.EWOULDBLOCK), errors.Is(err, syscall.EAGAIN):
			return false, nil
		default:
			return false, err
		}
	}
}

func stopAndDrainTimer(timer *time.Timer) {
	if timer.Stop() {
		return
	}
	select {
	case <-timer.C:
	default:
	}
}
