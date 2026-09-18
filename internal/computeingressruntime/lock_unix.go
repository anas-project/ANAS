//go:build linux || darwin

package computeingressruntime

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

var ErrExecutorBusy = errors.New("HTTP executor state is already locked")

// WithExclusive never unlinks the lock file: deleting it would allow another
// process to lock a different inode while this executor is still running.
func (s FileStateStore) WithExclusive(ctx context.Context, run func(Journal) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if run == nil || !filepath.IsAbs(s.Directory) || filepath.Clean(s.Directory) != s.Directory {
		return fmt.Errorf("HTTP executor requires a callback and absolute clean state directory")
	}
	before, err := os.Lstat(s.Directory)
	if err != nil || !privateOwned(before, true) {
		return fmt.Errorf("HTTP executor state directory must be private and owned by this user, not a symlink")
	}
	root, err := os.OpenRoot(s.Directory)
	if err != nil {
		return err
	}
	defer root.Close()
	opened, err := root.Stat(".")
	if err != nil || !os.SameFile(before, opened) {
		return fmt.Errorf("HTTP executor directory changed while opening")
	}
	const name = "http-executor.lock"
	if info, err := root.Lstat(name); err == nil {
		if !privateOwned(info, false) {
			return fmt.Errorf("invalid HTTP executor lock file")
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	lock, err := root.OpenFile(name, os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0600)
	if err != nil {
		return fmt.Errorf("open HTTP executor lock: %w", err)
	}
	defer lock.Close()
	lockInfo, err := lock.Stat()
	if err != nil || !privateOwned(lockInfo, false) {
		return fmt.Errorf("HTTP executor lock must be a private single-link regular file")
	}
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		if errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EAGAIN) {
			return ErrExecutorBusy
		}
		return fmt.Errorf("lock HTTP executor: %w", err)
	}
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
	active := true
	defer func() { active = false }()
	check := func(ctx context.Context) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if !active {
			return fmt.Errorf("HTTP executor journal session has closed")
		}
		linked, err := os.Lstat(s.Directory)
		if err != nil || !privateOwned(linked, true) || !os.SameFile(opened, linked) {
			return fmt.Errorf("HTTP executor directory identity changed")
		}
		linkedLock, err := root.Lstat(name)
		if err != nil || !privateOwned(linkedLock, false) || !os.SameFile(lockInfo, linkedLock) {
			return fmt.Errorf("HTTP executor lock identity changed")
		}
		return nil
	}
	if err := check(ctx); err != nil {
		return err
	}
	if err := lock.Sync(); err != nil {
		return err
	}
	if err := syncRoot(root); err != nil {
		return err
	}
	return run(&fileJournal{root: root, check: check})
}

func privateOwned(info os.FileInfo, directory bool) bool {
	if info == nil || info.Mode().Perm()&0077 != 0 {
		return false
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Uid != uint32(os.Geteuid()) {
		return false
	}
	if directory {
		return info.IsDir()
	}
	return info.Mode().IsRegular() && stat.Nlink == 1
}

func trustedRouteOwner(info os.FileInfo) bool {
	if info == nil {
		return false
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && (stat.Uid == 0 || stat.Uid == uint32(os.Geteuid()))
}

func openRouteArtifact(root *os.Root, name string) (*os.File, error) {
	return root.OpenFile(name, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
}
