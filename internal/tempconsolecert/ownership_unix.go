//go:build unix

package tempconsolecert

import (
	"fmt"
	"io/fs"
	"os"
	"syscall"
)

func validateCurrentOwner(info fs.FileInfo) error {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return fmt.Errorf("Unix owner metadata is unavailable")
	}
	want := uint32(os.Geteuid())
	if stat.Uid != want {
		return fmt.Errorf("UID is %d, want %d", stat.Uid, want)
	}
	return nil
}
