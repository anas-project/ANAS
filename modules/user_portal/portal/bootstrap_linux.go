//go:build linux

package main

import (
	"errors"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

func prepareStorage(path string) error {
	if os.Geteuid() != 0 {
		return nil
	}
	if path != "/var/lib/anas-user-portal/audit" {
		return errors.New("root bootstrap requires the fixed private audit mount")
	}
	parent := filepath.Dir(path)
	fd, err := unix.Open(parent, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW, 0)
	if err != nil {
		return errors.New("audit mount unavailable")
	}
	defer unix.Close(fd)
	if err := unix.Mkdirat(fd, "audit", 0700); err != nil && err != unix.EEXIST {
		return err
	}
	audit, err := unix.Openat(fd, "audit", unix.O_PATH|unix.O_DIRECTORY|unix.O_NOFOLLOW, 0)
	if err != nil {
		return err
	}
	defer unix.Close(audit)
	var stat unix.Stat_t
	if err := unix.Fstat(audit, &stat); err != nil {
		return err
	}
	if stat.Uid == 0 {
		writable, err := unix.Openat(fd, "audit", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW, 0)
		if err != nil {
			return err
		}
		defer unix.Close(writable)
		var opened unix.Stat_t
		if err := unix.Fstat(writable, &opened); err != nil || opened.Ino != stat.Ino || opened.Dev != stat.Dev {
			return errors.New("audit directory changed during bootstrap")
		}
		if err := unix.Fchmod(writable, 0700); err != nil {
			return err
		}
		if err := unix.Fchown(writable, 65532, 65532); err != nil {
			return err
		}
		if err := unix.Fsync(writable); err != nil {
			return err
		}
	} else if stat.Uid != 65532 || stat.Mode&0777 != 0700 {
		return errors.New("unsafe audit directory owner or mode")
	}
	if err := unix.Fsync(fd); err != nil {
		return err
	}
	if err := unix.Setgroups([]int{}); err != nil {
		return err
	}
	if err := unix.Setgid(65532); err != nil {
		return err
	}
	return unix.Setuid(65532)
}
