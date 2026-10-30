//go:build darwin

package cloudrelay

import "golang.org/x/sys/unix"

func renameDirectoryExclusive(parent int, from, to string) error {
	return unix.RenameatxNp(parent, from, parent, to, unix.RENAME_EXCL)
}
