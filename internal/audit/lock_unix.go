//go:build unix

package audit

import (
	"context"
	"errors"
	"os"
	"syscall"
	"time"
)

const (
	crossProcessAuditLockSupported = true
	auditLockRetryInterval         = 10 * time.Millisecond
)

func lockAuditFile(ctx context.Context, done <-chan struct{}, file *os.File) error {
	if file == nil {
		return errors.New("audit lock descriptor is unavailable")
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

		timer := time.NewTimer(auditLockRetryInterval)
		select {
		case <-ctx.Done():
			stopAndDrainAuditTimer(timer)
			return ctx.Err()
		case <-done:
			stopAndDrainAuditTimer(timer)
			return errWriterClosed
		case <-timer.C:
		}
	}
}

func stopAndDrainAuditTimer(timer *time.Timer) {
	if timer.Stop() {
		return
	}
	select {
	case <-timer.C:
	default:
	}
}

func unlockAuditFile(file *os.File) error {
	if file == nil {
		return errors.New("audit lock descriptor is unavailable")
	}
	for {
		err := syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
		if errors.Is(err, syscall.EINTR) {
			continue
		}
		return err
	}
}

func tryAuditLock(file *os.File) (bool, error) {
	if file == nil {
		return false, errors.New("audit lock descriptor is unavailable")
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
