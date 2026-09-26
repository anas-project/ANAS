//go:build unix

package main

import (
	"io"
	"os"
	"path/filepath"
	"syscall"
)

// The only production caller supplies the fixed read-only Module mount and
// root UID. The owner argument permits unprivileged temporary-file regression
// tests; no CLI/config/request can select a path or owner.
func readRunnerTrustFile(path string, owner int) ([]byte, error) {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return nil, errRunnerTrust
	}
	fd, err := syscall.Open(path, syscall.O_RDONLY|syscall.O_CLOEXEC|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	file := os.NewFile(uintptr(fd), path)
	defer file.Close()
	before, err := file.Stat()
	if err != nil {
		return nil, errRunnerTrust
	}
	st, ok := before.Sys().(*syscall.Stat_t)
	if !ok || !before.Mode().IsRegular() || before.Mode().Perm()&0022 != 0 || st.Uid != uint32(owner) ||
		st.Nlink != 1 || before.Size() < 1 || before.Size() > maxRunnerTrustBytes {
		return nil, errRunnerTrust
	}
	body, err := io.ReadAll(io.LimitReader(file, maxRunnerTrustBytes+1))
	after, statErr := file.Stat()
	current, pathErr := os.Lstat(path)
	if err != nil || statErr != nil || pathErr != nil || !os.SameFile(before, current) ||
		!os.SameFile(before, after) || current.Mode() != before.Mode() || after.Mode() != before.Mode() ||
		current.Size() != before.Size() || after.Size() != before.Size() || int64(len(body)) != before.Size() ||
		!current.ModTime().Equal(before.ModTime()) || !after.ModTime().Equal(before.ModTime()) {
		return nil, errRunnerTrust
	}
	for _, info := range []os.FileInfo{after, current} {
		st, ok := info.Sys().(*syscall.Stat_t)
		if !ok || st.Uid != uint32(owner) || st.Nlink != 1 {
			return nil, errRunnerTrust
		}
	}
	return body, nil
}
