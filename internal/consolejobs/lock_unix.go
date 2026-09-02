//go:build unix

package consolejobs

import (
	"context"
	"errors"
	"os"
	"syscall"
	"time"
)

const (
	crossProcessLockSupported = true
	lockRetryInterval         = 10 * time.Millisecond
)

func lockFileContext(ctx context.Context, done <-chan struct{}, file *os.File) error {
	if file == nil {
		return errors.New("job lock descriptor is unavailable")
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
		case <-done:
			stopAndDrainTimer(timer)
			return errStoreClosed
		case <-timer.C:
		}
	}
}

func unlockFile(file *os.File) error {
	if file == nil {
		return errors.New("job lock descriptor is unavailable")
	}
	for {
		err := syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
		if errors.Is(err, syscall.EINTR) {
			continue
		}
		return err
	}
}

func tryLockFile(file *os.File) (bool, error) {
	if file == nil {
		return false, errors.New("job lock descriptor is unavailable")
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
