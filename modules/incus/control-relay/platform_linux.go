//go:build linux

package main

import (
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"golang.org/x/sys/unix"
)

// Configuration and every ancestor must be installed by root, without group
// or other write access. Each component is opened relative to a held directory
// descriptor, with NOFOLLOW. No credentials or executable paths are loaded.
func readRelayConfiguration(path string) ([]byte, error) {
	if os.Geteuid() == 0 {
		return nil, errIdentity
	}
	if !filepath.IsAbs(path) || filepath.Clean(path) != path || path == "/" {
		return nil, errConfiguration
	}
	fd, err := unix.Open("/", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return nil, errConfiguration
	}
	directory := os.NewFile(uintptr(fd), "relay-configuration-root")
	defer func() { _ = directory.Close() }()
	parts := strings.Split(strings.TrimPrefix(path, "/"), "/")
	for i, part := range parts {
		var parent unix.Stat_t
		if unix.Fstat(int(directory.Fd()), &parent) != nil || parent.Uid != 0 || parent.Mode&0022 != 0 || parent.Mode&unix.S_IFMT != unix.S_IFDIR {
			return nil, errConfiguration
		}
		flags := unix.O_RDONLY | unix.O_CLOEXEC | unix.O_NOFOLLOW | unix.O_NONBLOCK
		if i < len(parts)-1 {
			flags |= unix.O_DIRECTORY
		}
		nextFD, err := unix.Openat(int(directory.Fd()), part, flags, 0)
		if err != nil {
			return nil, errConfiguration
		}
		next := os.NewFile(uintptr(nextFD), "relay-installation-configuration")
		if i < len(parts)-1 {
			_ = directory.Close()
			directory = next
			continue
		}
		defer next.Close()
		var before unix.Stat_t
		if unix.Fstat(nextFD, &before) != nil || before.Uid != 0 || before.Mode&0022 != 0 ||
			before.Mode&unix.S_IFMT != unix.S_IFREG || before.Nlink != 1 || before.Size < 1 || before.Size > maximumConfigurationBytes {
			return nil, errConfiguration
		}
		body, err := io.ReadAll(io.LimitReader(next, maximumConfigurationBytes+1))
		var after unix.Stat_t
		if err != nil || int64(len(body)) != before.Size || unix.Fstat(nextFD, &after) != nil ||
			before.Dev != after.Dev || before.Ino != after.Ino || before.Mode != after.Mode || before.Uid != after.Uid ||
			before.Gid != after.Gid || before.Nlink != after.Nlink || before.Size != after.Size ||
			before.Mtim != after.Mtim || before.Ctim != after.Ctim {
			return nil, errConfiguration
		}
		return body, nil
	}
	return nil, errConfiguration
}

func checkRelayIdentity(settings relaySettings) error {
	if os.Geteuid() == 0 || os.Getuid() != os.Geteuid() || os.Getgid() != os.Getegid() ||
		uint32(os.Geteuid()) != settings.RunUID || uint32(os.Getegid()) != settings.RunGID {
		return errIdentity
	}
	groups, err := os.Getgroups()
	if err != nil {
		return errIdentity
	}
	for _, group := range groups {
		if uint32(group) != settings.RunGID {
			return errIdentity
		}
	}
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	header := unix.CapUserHeader{Version: unix.LINUX_CAPABILITY_VERSION_3}
	var capabilities [2]unix.CapUserData
	if unix.Capget(&header, &capabilities[0]) != nil {
		return errIdentity
	}
	for _, capability := range capabilities {
		if capability.Effective != 0 || capability.Permitted != 0 || capability.Inheritable != 0 {
			return errIdentity
		}
	}
	return nil
}
