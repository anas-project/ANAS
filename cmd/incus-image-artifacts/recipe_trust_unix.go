//go:build unix

package main

import (
	"context"
	"os"
	"path/filepath"
	"syscall"

	"golang.org/x/sys/unix"
)

// The production caller fixes the directory and owner; these parameters only
// let unprivileged tests inspect their own temporary files. No CLI flag or
// recipe field can substitute a keyring, an owner or an executable.
func checkBootstrapKeyringAt(ctx context.Context, directory string, owner int) error {
	if ctx == nil || owner < 0 || !filepath.IsAbs(directory) || filepath.Clean(directory) != directory {
		return errDebianBootstrapTrust
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	dirFD, err := unix.Open(directory, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_DIRECTORY|unix.O_NOFOLLOW, 0)
	if err != nil {
		return errDebianBootstrapTrust
	}
	dir := os.NewFile(uintptr(dirFD), "bootstrap-keyrings")
	defer dir.Close()
	dirInfo, err := dir.Stat()
	if err != nil || !trustedBootstrapInfo(dirInfo, owner, true) {
		return errDebianBootstrapTrust
	}
	const name = "debian-archive-keyring.gpg"
	selected := name
	var alias os.FileInfo
	entry, err := os.Lstat(filepath.Join(directory, name))
	if err != nil {
		return errDebianBootstrapTrust
	}
	if entry.Mode()&os.ModeSymlink != 0 {
		// Ubuntu's official package uses this one relative sibling alias.
		// Do not use EvalSymlinks or follow arbitrary/absolute/parent paths.
		if !trustedBootstrapAlias(entry, owner) || !fixedBootstrapAliasTarget(dirFD, name) {
			return errDebianBootstrapTrust
		}
		alias = entry
		selected = "debian-archive-keyring.pgp"
	}
	fd, err := unix.Openat(dirFD, selected, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
	if err != nil {
		return errDebianBootstrapTrust
	}
	file := os.NewFile(uintptr(fd), "debian-bootstrap-keyring")
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !trustedBootstrapInfo(info, owner, false) {
		return errDebianBootstrapTrust
	}
	// The fixed source must still name the inspected object. Presence and
	// ownership are prerequisites, not a claim that a Release was verified:
	// the actual debootstrap/gpgv operation still has to succeed afterwards.
	current, err := os.Lstat(filepath.Join(directory, selected))
	if err != nil || !trustedBootstrapInfo(current, owner, false) || !os.SameFile(info, current) ||
		info.Size() != current.Size() || !info.ModTime().Equal(current.ModTime()) {
		return errDebianBootstrapTrust
	}
	if alias != nil {
		currentAlias, err := os.Lstat(filepath.Join(directory, name))
		if err != nil || !trustedBootstrapAlias(currentAlias, owner) || !os.SameFile(alias, currentAlias) ||
			!alias.ModTime().Equal(currentAlias.ModTime()) || !fixedBootstrapAliasTarget(dirFD, name) {
			return errDebianBootstrapTrust
		}
	}
	currentDir, err := os.Lstat(directory)
	if err != nil || !trustedBootstrapInfo(currentDir, owner, true) || !os.SameFile(dirInfo, currentDir) {
		return errDebianBootstrapTrust
	}
	return ctx.Err()
}

func trustedBootstrapAlias(info os.FileInfo, owner int) bool {
	st, ok := info.Sys().(*syscall.Stat_t)
	// Symlinks normally report 0777; write authority is controlled by their
	// already-verified containing directory, not those synthetic mode bits.
	return ok && info.Mode()&os.ModeSymlink != 0 && st.Uid == uint32(owner) && st.Nlink == 1
}

func fixedBootstrapAliasTarget(directory int, name string) bool {
	var target [128]byte
	n, err := unix.Readlinkat(directory, name, target[:])
	return err == nil && n < len(target) && string(target[:n]) == "debian-archive-keyring.pgp"
}

func trustedBootstrapInfo(info os.FileInfo, owner int, directory bool) bool {
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok || st.Uid != uint32(owner) || info.Mode().Perm()&0022 != 0 || info.Mode()&(os.ModeSymlink|os.ModeSetuid|os.ModeSetgid) != 0 {
		return false
	}
	if directory {
		return info.IsDir()
	}
	return info.Mode().IsRegular() && st.Nlink == 1 && info.Size() >= 1024 && info.Size() <= 4<<20
}
