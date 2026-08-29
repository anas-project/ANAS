//go:build unix

package consoleauth

import (
	"context"
	"errors"
	"fmt"
	"os"
	"syscall"
	"time"
)

func acquireStoreLock(ctx context.Context, path string) (func(), error) {
	file, err := openLockFile(path)
	if err != nil {
		return nil, err
	}
	for {
		err = syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			return func() {
				_ = syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
				_ = file.Close()
			}, nil
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) && !errors.Is(err, syscall.EAGAIN) {
			_ = file.Close()
			return nil, fmt.Errorf("lock console authentication state: %w", err)
		}
		timer := time.NewTimer(10 * time.Millisecond)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			_ = file.Close()
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
}

func openLockFile(path string) (*os.File, error) {
	for {
		entryInfo, err := os.Lstat(path)
		if errors.Is(err, os.ErrNotExist) {
			file, createErr := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0o600)
			if errors.Is(createErr, os.ErrExist) {
				continue
			}
			if createErr != nil {
				return nil, fmt.Errorf("create console authentication lock: %w", createErr)
			}
			if err := file.Chmod(0o600); err != nil {
				_ = file.Close()
				return nil, fmt.Errorf("set console authentication lock permissions: %w", err)
			}
			entryInfo, err = os.Lstat(path)
			if err != nil {
				_ = file.Close()
				return nil, fmt.Errorf("inspect console authentication lock: %w", err)
			}
			if err := validateOpenedLock(path, file, entryInfo); err != nil {
				_ = file.Close()
				return nil, err
			}
			return file, nil
		}
		if err != nil {
			return nil, fmt.Errorf("inspect console authentication lock: %w", err)
		}
		if err := validatePrivateInfo(path, entryInfo, false); err != nil {
			return nil, err
		}
		file, err := os.OpenFile(path, os.O_RDWR, 0)
		if err != nil {
			return nil, fmt.Errorf("open console authentication lock: %w", err)
		}
		if err := validateOpenedLock(path, file, entryInfo); err != nil {
			_ = file.Close()
			return nil, err
		}
		return file, nil
	}
}

func validateOpenedLock(path string, file *os.File, entryInfo os.FileInfo) error {
	openedInfo, err := file.Stat()
	if err != nil {
		return fmt.Errorf("inspect opened console authentication lock: %w", err)
	}
	if !os.SameFile(entryInfo, openedInfo) {
		return errors.New("console authentication lock changed while opening")
	}
	return validatePrivateInfo(path, openedInfo, false)
}
