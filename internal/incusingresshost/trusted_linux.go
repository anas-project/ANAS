//go:build linux

package incusingresshost

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"

	"golang.org/x/sys/unix"
)

func validateTrustedExecutable(path string, info os.FileInfo) error {
	if err := validateRootOwnedImmutable(path, info, true); err != nil {
		return err
	}
	return validateAncestors(path)
}

func validateTrustedDirectory(path string, info os.FileInfo) error {
	if err := validateRootOwnedImmutable(path, info, false); err != nil {
		return err
	}
	return validateAncestors(path)
}

func validateRootOwnedImmutable(path string, info os.FileInfo, executable bool) error {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Uid != 0 || stat.Gid != 0 || stat.Nlink != 1 && executable {
		return fmt.Errorf("%s must be root-owned with stable metadata", filepath.Base(path))
	}
	if executable && !info.Mode().IsRegular() || !executable && !(info.Mode().IsRegular() || info.IsDir()) {
		return fmt.Errorf("%s has an unexpected file type", filepath.Base(path))
	}
	if info.Mode().Perm()&0022 != 0 || executable && info.Mode().Perm()&0111 == 0 {
		return fmt.Errorf("%s permissions are not trusted", filepath.Base(path))
	}
	return nil
}

func validateAncestors(path string) error {
	dir := filepath.Clean(filepath.Dir(path))
	for {
		info, err := os.Lstat(dir)
		if err != nil {
			return fmt.Errorf("inspect trusted path ancestor")
		}
		if !info.IsDir() || info.Mode().Perm()&0022 != 0 {
			return fmt.Errorf("trusted path ancestor is writable or not a directory")
		}
		stat, ok := info.Sys().(*syscall.Stat_t)
		if !ok || stat.Uid != 0 || stat.Gid != 0 {
			return fmt.Errorf("trusted path ancestor must be root-owned")
		}
		if dir == "/" {
			return nil
		}
		next := filepath.Dir(dir)
		if next == dir {
			return nil
		}
		dir = next
	}
}

func prepareCommandPath(path string, fixture bool) (string, *os.File, error) {
	if fixture {
		return path, nil, nil
	}
	before, err := os.Lstat(path)
	if err != nil {
		return "", nil, fmt.Errorf("inspect trusted binary")
	}
	if err := validateTrustedExecutable(path, before); err != nil {
		return "", nil, err
	}
	fd, err := unix.Open(path, unix.O_PATH|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return "", nil, fmt.Errorf("open trusted binary")
	}
	file := os.NewFile(uintptr(fd), filepath.Base(path))
	cleanup := func() { _ = file.Close() }
	opened, err := file.Stat()
	if err != nil || !os.SameFile(before, opened) {
		cleanup()
		return "", nil, fmt.Errorf("trusted binary changed while opening")
	}
	after, err := os.Lstat(path)
	if err != nil || !os.SameFile(before, after) {
		cleanup()
		return "", nil, fmt.Errorf("trusted binary changed before execution")
	}
	return "/proc/self/fd/3", file, nil
}
