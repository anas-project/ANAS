//go:build linux

package consolejobs

import (
	"os"
	"syscall"
)

func journalChangeTime(info os.FileInfo) (int64, int64, bool) {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, 0, false
	}
	return stat.Ctim.Sec, stat.Ctim.Nsec, true
}
