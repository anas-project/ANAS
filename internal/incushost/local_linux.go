//go:build linux

package incushost

import (
	"context"
	"errors"
	"io"
	"os"

	"golang.org/x/sys/unix"
)

func localFacts(ctx context.Context, facts *Facts) error {
	root, err := unix.Open("/", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return ErrObservation
	}
	defer unix.Close(root)
	body, err := readOSReleaseAt(ctx, root, 0)
	if err != nil {
		return err
	}
	facts.Release, err = ParseOSRelease(body)
	if err != nil {
		return ErrObservation
	}
	// These are explicitly presence observations, not proof of live systemd
	// or a usable KVM API. No device or administrative socket is opened.
	if info, err := os.Lstat("/run/systemd/system"); err == nil {
		facts.Systemd = info.IsDir() && info.Mode()&os.ModeSymlink == 0
	} else if !errors.Is(err, os.ErrNotExist) {
		return ErrObservation
	}
	if info, err := os.Lstat("/dev/kvm"); err == nil {
		facts.KVMDevice = info.Mode()&os.ModeCharDevice != 0
	} else if !errors.Is(err, os.ErrNotExist) {
		return ErrObservation
	}
	return nil
}

// uid is fixed to root by the production entry. The argument allows isolated
// unprivileged filesystem fixtures without granting callers a root override.
func readOSReleaseAt(ctx context.Context, root int, uid uint32) ([]byte, error) {
	if ctx == nil {
		return nil, ErrInvalid
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	etc, err := openOwnedDirectories(root, uid, "etc")
	if err != nil {
		return nil, ErrObservation
	}
	defer unix.Close(etc)
	var original unix.Stat_t
	err = unix.Fstatat(etc, "os-release", &original, unix.AT_SYMLINK_NOFOLLOW)
	missing := errors.Is(err, unix.ENOENT)
	if err != nil && !missing {
		return nil, ErrObservation
	}
	selected := etc
	if missing || original.Mode&unix.S_IFMT == unix.S_IFLNK {
		if !missing {
			link := make([]byte, 128)
			n, err := unix.Readlinkat(etc, "os-release", link)
			if err != nil || original.Uid != uid || (string(link[:n]) != "../usr/lib/os-release" && string(link[:n]) != "/usr/lib/os-release") {
				return nil, ErrObservation
			}
		}
		selected, err = openOwnedDirectories(root, uid, "usr", "lib")
		if err != nil {
			return nil, ErrObservation
		}
		defer unix.Close(selected)
	}
	fd, err := unix.Openat(selected, "os-release", unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
	if err != nil {
		return nil, ErrObservation
	}
	file := os.NewFile(uintptr(fd), "os-release")
	defer file.Close()
	var before unix.Stat_t
	if unix.Fstat(fd, &before) != nil || before.Uid != uid || before.Mode&0022 != 0 || before.Mode&unix.S_IFMT != unix.S_IFREG || before.Nlink != 1 || before.Size < 1 || before.Size > MaxOSReleaseBytes {
		return nil, ErrObservation
	}
	body, err := io.ReadAll(io.LimitReader(file, MaxOSReleaseBytes+1))
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	var after, named, current unix.Stat_t
	if err != nil || int64(len(body)) != before.Size || unix.Fstat(fd, &after) != nil || unix.Fstatat(selected, "os-release", &named, unix.AT_SYMLINK_NOFOLLOW) != nil || !sameReleaseStat(before, after) || !sameReleaseStat(before, named) {
		return nil, ErrObservation
	}
	err = unix.Fstatat(etc, "os-release", &current, unix.AT_SYMLINK_NOFOLLOW)
	if missing {
		if !errors.Is(err, unix.ENOENT) {
			return nil, ErrObservation
		}
	} else if err != nil || !sameReleaseStat(original, current) {
		return nil, ErrObservation
	}
	// Reopen the fixed ancestors to reject a concurrently replaced path.
	var parent unix.Stat_t
	reopened, err := openOwnedDirectories(root, uid, "etc")
	if err != nil {
		return nil, ErrObservation
	}
	defer unix.Close(reopened)
	var pinned unix.Stat_t
	if unix.Fstat(reopened, &parent) != nil || unix.Fstat(etc, &pinned) != nil || parent.Dev != pinned.Dev || parent.Ino != pinned.Ino {
		return nil, ErrObservation
	}
	if selected != etc {
		vendor, err := openOwnedDirectories(root, uid, "usr", "lib")
		if err != nil {
			return nil, ErrObservation
		}
		defer unix.Close(vendor)
		if unix.Fstat(vendor, &parent) != nil || unix.Fstat(selected, &pinned) != nil || parent.Dev != pinned.Dev || parent.Ino != pinned.Ino {
			return nil, ErrObservation
		}
	}
	return body, nil
}

func openOwnedDirectories(root int, uid uint32, parts ...string) (int, error) {
	fd, err := unix.Openat(root, ".", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return -1, ErrObservation
	}
	for i := 0; ; i++ {
		var info unix.Stat_t
		if unix.Fstat(fd, &info) != nil || info.Uid != uid || info.Mode&0022 != 0 || info.Mode&unix.S_IFMT != unix.S_IFDIR {
			unix.Close(fd)
			return -1, ErrObservation
		}
		if i == len(parts) {
			return fd, nil
		}
		next, err := unix.Openat(fd, parts[i], unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
		unix.Close(fd)
		if err != nil {
			return -1, ErrObservation
		}
		fd = next
	}
}

func sameReleaseStat(a, b unix.Stat_t) bool {
	return a.Dev == b.Dev && a.Ino == b.Ino && a.Mode == b.Mode && a.Uid == b.Uid && a.Gid == b.Gid && a.Nlink == b.Nlink && a.Size == b.Size && a.Mtim == b.Mtim && a.Ctim == b.Ctim
}
