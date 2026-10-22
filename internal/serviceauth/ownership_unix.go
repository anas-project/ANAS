//go:build unix

package serviceauth

import (
	"errors"
	"io/fs"
	"syscall"
)

func fileOwner(info fs.FileInfo) (int, int, error) {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, 0, errors.New("service credential ownership metadata unavailable")
	}
	return int(stat.Uid), int(stat.Gid), nil
}
