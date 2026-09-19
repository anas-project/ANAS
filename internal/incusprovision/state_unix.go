//go:build linux || darwin

package incusprovision

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

type unixStateLock struct {
	file *os.File
}

func (s *fileStateStore) Lock(ctx context.Context) (stateLock, error) {
	if ctx == nil {
		return nil, ErrInvalid
	}
	lockPath := s.statePath + ".lock"
	if err := ensureTrustedRootDirectory(filepathDir(lockPath), 0700); err != nil {
		return nil, ErrUnsafeState
	}
	if err := trustedAncestors(lockPath); err != nil {
		return nil, ErrUnsafeState
	}
	parent, err := os.Lstat(filepathDir(lockPath))
	if err != nil || !rootOwnedPrivateDir(parent) {
		return nil, ErrUnsafeState
	}
	file, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR|syscall.O_CLOEXEC|syscall.O_NOFOLLOW, 0600)
	if err != nil {
		return nil, ErrUnsafeState
	}
	for {
		if err := ctx.Err(); err != nil {
			_ = file.Close()
			return nil, err
		}
		if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err == nil {
			if info, err := file.Stat(); err != nil || !rootOwnedPrivateFile(info, 0600) {
				_ = syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
				_ = file.Close()
				return nil, ErrUnsafeState
			}
			return &unixStateLock{file: file}, nil
		} else if !errors.Is(err, syscall.EWOULDBLOCK) && !errors.Is(err, syscall.EAGAIN) {
			_ = file.Close()
			return nil, ErrUnsafeState
		}
		timer := time.NewTimer(50 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			_ = file.Close()
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
}

// Anchor creation at a verified directory descriptor. Neither a symlink in
// the chain nor a concurrently replaced last component may redirect root
// writes outside the fixed installation tree.
func ensureTrustedRootDirectory(path string, mode os.FileMode) (result error) {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path || mode.Perm()&0022 != 0 || os.Geteuid() != 0 {
		return ErrUnsafeState
	}
	fd, err := unix.Open("/", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return ErrUnsafeState
	}
	defer func() {
		if unix.Close(fd) != nil {
			result = ErrUnsafeState
		}
	}()
	for _, part := range strings.Split(strings.TrimPrefix(path, "/"), "/") {
		if part == "" {
			continue
		}
		next, err := unix.Openat(fd, part, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
		if errors.Is(err, unix.ENOENT) {
			if err := unix.Mkdirat(fd, part, uint32(mode.Perm())); err != nil && !errors.Is(err, unix.EEXIST) {
				return ErrUnsafeState
			}
			if unix.Fsync(fd) != nil {
				return ErrUnsafeState
			}
			next, err = unix.Openat(fd, part, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
		}
		if err != nil {
			return ErrUnsafeState
		}
		var st unix.Stat_t
		if unix.Fstat(next, &st) != nil || st.Uid != 0 || st.Mode&0022 != 0 || st.Mode&unix.S_IFMT != unix.S_IFDIR {
			_ = unix.Close(next)
			return ErrUnsafeState
		}
		if unix.Close(fd) != nil {
			_ = unix.Close(next)
			return ErrUnsafeState
		}
		fd = next
	}
	return nil
}

func openRootFileNoFollow(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
}

func (l *unixStateLock) Unlock() error {
	if l == nil || l.file == nil {
		return nil
	}
	err := syscall.Flock(int(l.file.Fd()), syscall.LOCK_UN)
	closeErr := l.file.Close()
	l.file = nil
	return errors.Join(err, closeErr)
}

func rootOwnedPrivateDir(info os.FileInfo) bool {
	if info == nil || !info.IsDir() || info.Mode().Perm()&0022 != 0 {
		return false
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && stat.Uid == 0
}

func rootOwnedPrivateFile(info os.FileInfo, mode os.FileMode) bool {
	if info == nil || !info.Mode().IsRegular() || info.Mode().Perm() != mode {
		return false
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && stat.Uid == 0 && stat.Nlink == 1
}

func rootOwnedExecutable(info os.FileInfo) bool {
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && stat.Uid == 0
}

func sameFileIdentity(a, b os.FileInfo) bool {
	as, aok := a.Sys().(*syscall.Stat_t)
	bs, bok := b.Sys().(*syscall.Stat_t)
	return aok && bok && as.Dev == bs.Dev && as.Ino == bs.Ino && as.Mode == bs.Mode && as.Uid == bs.Uid && as.Gid == bs.Gid && as.Nlink == bs.Nlink && as.Size == bs.Size && a.ModTime().Equal(b.ModTime())
}

func filepathDir(path string) string {
	for i := len(path) - 1; i >= 0; i-- {
		if path[i] == '/' {
			if i == 0 {
				return "/"
			}
			return path[:i]
		}
	}
	return "."
}
