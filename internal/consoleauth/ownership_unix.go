//go:build unix

package consoleauth

import (
	"errors"
	"io/fs"
	"os"
	"syscall"
)

func effectiveUID() uint32 {
	return uint32(os.Geteuid())
}

func ownerUID(info fs.FileInfo) (uint32, error) {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, errors.New("Unix owner metadata is unavailable")
	}
	return stat.Uid, nil
}
