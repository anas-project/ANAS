//go:build unix

package consolestate

import (
	"fmt"
	"io/fs"
	"os"
	"syscall"
)

func validateCurrentOwner(info fs.FileInfo, name string) error {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return fmt.Errorf("%s owner metadata is unavailable", name)
	}
	want := uint64(os.Geteuid())
	if got := uint64(stat.Uid); got != want {
		return fmt.Errorf("%s owner UID is %d, want current effective UID %d", name, got, want)
	}
	return nil
}

func validateSingleLink(info fs.FileInfo, name string) error {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return fmt.Errorf("%s link metadata is unavailable", name)
	}
	if links := uint64(stat.Nlink); links != 1 {
		return fmt.Errorf("%s link count is %d, want 1", name, links)
	}
	return nil
}
