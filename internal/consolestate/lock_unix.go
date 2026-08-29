//go:build unix

package consolestate

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

func acquireStateLock(ctx context.Context, directory string) (func(), bool, error) {
	if ctx == nil {
		return nil, false, errors.New("context is nil")
	}
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	if err := verifyPrivateDirectory(directory); err != nil {
		return nil, false, err
	}
	path := filepath.Join(directory, LockFileName)
	file, created, err := openStateLock(path, directory)
	if err != nil {
		return nil, false, err
	}
	for {
		if err := ctx.Err(); err != nil {
			_ = file.Close()
			return nil, created, err
		}
		err = syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			if err := verifyOpenedLock(file, path); err != nil {
				_ = syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
				_ = file.Close()
				return nil, created, err
			}
			return func() {
				_ = syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
				_ = file.Close()
			}, created, nil
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) && !errors.Is(err, syscall.EAGAIN) {
			_ = file.Close()
			return nil, created, fmt.Errorf("lock console capability state: %w", err)
		}
		timer := time.NewTimer(10 * time.Millisecond)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			_ = file.Close()
			return nil, created, ctx.Err()
		case <-timer.C:
		}
	}
}

func openStateLock(path, directory string) (*os.File, bool, error) {
	for {
		entryInfo, err := os.Lstat(path)
		if errors.Is(err, os.ErrNotExist) {
			file, createErr := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0o600)
			if errors.Is(createErr, os.ErrExist) {
				continue
			}
			if createErr != nil {
				return nil, false, fmt.Errorf("create %s: %w", LockFileName, createErr)
			}
			if err := file.Chmod(0o600); err != nil {
				_ = file.Close()
				return nil, false, fmt.Errorf("set %s permissions: %w", LockFileName, err)
			}
			if err := file.Sync(); err != nil {
				_ = file.Close()
				return nil, false, fmt.Errorf("sync %s: %w", LockFileName, err)
			}
			if err := syncDirectory(directory); err != nil {
				_ = file.Close()
				return nil, false, fmt.Errorf("sync new %s entry: %w", LockFileName, err)
			}
			if err := verifyOpenedLock(file, path); err != nil {
				_ = file.Close()
				return nil, false, err
			}
			return file, true, nil
		}
		if err != nil {
			return nil, false, fmt.Errorf("inspect %s: %w", LockFileName, err)
		}
		if err := validatePrivateFileInfo(entryInfo, LockFileName); err != nil {
			return nil, false, err
		}
		file, err := os.OpenFile(path, os.O_RDWR, 0)
		if err != nil {
			return nil, false, fmt.Errorf("open %s: %w", LockFileName, err)
		}
		if err := verifyOpenedLockAgainst(file, path, entryInfo); err != nil {
			_ = file.Close()
			return nil, false, err
		}
		return file, false, nil
	}
}

func verifyOpenedLock(file *os.File, path string) error {
	entryInfo, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("reinspect %s: %w", LockFileName, err)
	}
	return verifyOpenedLockAgainst(file, path, entryInfo)
}

func verifyOpenedLockAgainst(file *os.File, path string, entryInfo os.FileInfo) error {
	if err := validatePrivateFileInfo(entryInfo, LockFileName); err != nil {
		return err
	}
	openedInfo, err := file.Stat()
	if err != nil {
		return fmt.Errorf("inspect opened %s: %w", LockFileName, err)
	}
	if !os.SameFile(entryInfo, openedInfo) {
		return fmt.Errorf("%s changed while opening", LockFileName)
	}
	return validatePrivateFileInfo(openedInfo, LockFileName)
}
