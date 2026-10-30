//go:build linux

package cloudrelay

import "golang.org/x/sys/unix"

func renameDirectoryExclusive(parent int, from, to string) error {
	return unix.Renameat2(parent, from, parent, to, unix.RENAME_NOREPLACE)
}
