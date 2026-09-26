//go:build unix

package main

import (
	"errors"
	"io"
	"os"
	"path/filepath"

	"github.com/anas-project/ANAS/internal/securefs"
	"golang.org/x/sys/unix"
)

var errUnsafeControllerState = errors.New("controller state identity is unverified")

func readControllerState(path string) ([]byte, error) {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return nil, errUnsafeControllerState
	}
	dir := filepath.Dir(path)
	info, err := os.Lstat(dir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil || securefs.ValidateDirectoryInfo(info, "Actions state directory") != nil {
		return nil, errUnsafeControllerState
	}
	dfd, err := unix.Open(dir, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, errUnsafeControllerState
	}
	directory := os.NewFile(uintptr(dfd), dir)
	defer directory.Close()
	if securefs.VerifyOpenDirectory(directory, dir, "Actions state directory") != nil {
		return nil, errUnsafeControllerState
	}
	fd, err := unix.Openat(dfd, filepath.Base(path), unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if errors.Is(err, unix.ENOENT) {
		// ENOENT from a followed dangling link is not absence: NOFOLLOW above
		// makes that ELOOP. Recheck the pinned parent and the current entry.
		if _, currentErr := os.Lstat(path); !os.IsNotExist(currentErr) ||
			securefs.VerifyOpenDirectory(directory, dir, "Actions state directory") != nil {
			return nil, errUnsafeControllerState
		}
		return nil, nil
	}
	if err != nil {
		return nil, errUnsafeControllerState
	}
	file := os.NewFile(uintptr(fd), path)
	defer file.Close()
	before, err := file.Stat()
	if err != nil || securefs.ValidateFileInfo(before, "Actions state") != nil || before.Size() < 1 || before.Size() > maxControllerStateBytes {
		return nil, errUnsafeControllerState
	}
	body, err := io.ReadAll(io.LimitReader(file, maxControllerStateBytes+1))
	after, statErr := file.Stat()
	if err != nil || statErr != nil || int64(len(body)) != before.Size() || after.Size() != before.Size() ||
		!after.ModTime().Equal(before.ModTime()) || securefs.VerifyOpenNamedFile(file, path, "Actions state") != nil ||
		securefs.VerifyOpenDirectory(directory, dir, "Actions state directory") != nil {
		return nil, errUnsafeControllerState
	}
	return body, nil
}
