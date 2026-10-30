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
	if err := privateStorageRoot(fd); err != nil {
		return err
	}
	for _, name := range []string{"audit", "preferences", "themes"} {
		if err := privateDirectory(fd, name); err != nil {
			return err
		}
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

// Core creates the data mount as root:0700. Its owner must advance together
// with the child stores before dropping privileges; otherwise the portal
// cannot traverse the mount to reach its correctly owned audit directory.
func privateStorageRoot(fd int) error {
	var stat unix.Stat_t
	if err := unix.Fstat(fd, &stat); err != nil {
		return err
	}
	if stat.Mode&unix.S_IFMT != unix.S_IFDIR {
		return errors.New("portal data mount is not a directory")
	}
	if stat.Uid == 0 {
		if stat.Mode&0022 != 0 {
			return errors.New("portal data mount is writable by another user")
		}
		if err := unix.Fchmod(fd, 0700); err != nil {
			return err
		}
		return unix.Fchown(fd, 65532, 65532)
	}
	if stat.Uid != 65532 || stat.Mode&0777 != 0700 {
		return errors.New("unsafe portal data mount owner or mode")
	}
	return nil
}

// privateDirectory creates or verifies one 0700 directory owned by the
// unprivileged portal user inside the fixed data mount.
func privateDirectory(fd int, name string) error {
	if err := unix.Mkdirat(fd, name, 0700); err != nil && err != unix.EEXIST {
		return err
	}
	dir, err := unix.Openat(fd, name, unix.O_PATH|unix.O_DIRECTORY|unix.O_NOFOLLOW, 0)
	if err != nil {
		return err
	}
	defer unix.Close(dir)
	var stat unix.Stat_t
	if err := unix.Fstat(dir, &stat); err != nil {
		return err
	}
	if stat.Uid == 0 {
		writable, err := unix.Openat(fd, name, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW, 0)
		if err != nil {
			return err
		}
		defer unix.Close(writable)
		var opened unix.Stat_t
		if err := unix.Fstat(writable, &opened); err != nil || opened.Ino != stat.Ino || opened.Dev != stat.Dev {
			return errors.New(name + " directory changed during bootstrap")
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
		return errors.New("unsafe " + name + " directory owner or mode")
	}
	return nil
}
