//go:build !linux && !darwin

package consolejobs

import "os"

func journalChangeTime(os.FileInfo) (int64, int64, bool) {
	return 0, 0, false
}
