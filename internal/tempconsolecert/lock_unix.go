//go:build linux || darwin || freebsd || netbsd || openbsd || dragonfly

package tempconsolecert

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

func openSecureLockFile(path string) (*os.File, error) {
	for {
		info, err := os.Lstat(path)
		created := false
		switch {
		case errors.Is(err, os.ErrNotExist):
			created = true
		case err != nil:
			return nil, err
		default:
			if err := validateLockInfo(info); err != nil {
				return nil, err
			}
		}

		flags := os.O_RDWR
		if created {
			flags |= os.O_CREATE | os.O_EXCL
		}
		file, err := os.OpenFile(path, flags, 0o600)
		if created && errors.Is(err, os.ErrExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		closeWithError := func(err error) (*os.File, error) {
			_ = file.Close()
			return nil, err
		}
		if created {
			if err := file.Chmod(0o600); err != nil {
				return closeWithError(err)
			}
		}
		openedInfo, err := file.Stat()
		if err != nil {
			return closeWithError(err)
		}
		if err := validateLockInfo(openedInfo); err != nil {
			return closeWithError(err)
		}
		pathInfo, err := os.Lstat(path)
		if err != nil {
			return closeWithError(err)
		}
		if err := validateLockInfo(pathInfo); err != nil {
			return closeWithError(err)
		}
		if !os.SameFile(openedInfo, pathInfo) {
			return closeWithError(fmt.Errorf("%s changed while it was opened", filepath.Base(path)))
		}
		return file, nil
	}
}

func validateLockInfo(info os.FileInfo) error {
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return fmt.Errorf("%s must be a non-symlink regular file", LockFilename)
	}
	if info.Mode().Perm() != 0o600 {
		return fmt.Errorf("%s mode is %04o, want 0600", LockFilename, info.Mode().Perm())
	}
	if err := validateCurrentOwner(info); err != nil {
		return fmt.Errorf("%s owner: %w", LockFilename, err)
	}
	return nil
}

func lockExclusive(file *os.File) error {
	for {
		err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX)
		if !errors.Is(err, syscall.EINTR) {
			return err
		}
	}
}

func unlockFile(file *os.File) {
	for {
		err := syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
		if !errors.Is(err, syscall.EINTR) {
			return
		}
	}
}
