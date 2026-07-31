//go:build linux

package runner

import "syscall"

// btrfsSuperMagic identifies a Btrfs filesystem in statfs(2). Detecting it
// through the syscall rather than by shelling out to `btrfs` keeps the check
// working on hosts where the userspace tools are absent, which is exactly the
// case `anas init` needs to report on.
const btrfsSuperMagic = 0x9123683E

func filesystemIsBtrfs(path string) (bool, error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return false, err
	}
	return int64(st.Type) == btrfsSuperMagic, nil
}

// filesystemFree reports the bytes an unprivileged writer can still use, which
// is f_bavail rather than f_bfree: the difference is the reserve only root may
// dip into, and a backup that fills the destination to the point where only
// root can write is a failed backup with a confusing error.
func filesystemFree(path string) (int64, bool) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return 0, false
	}
	return int64(st.Bavail) * st.Bsize, true
}
