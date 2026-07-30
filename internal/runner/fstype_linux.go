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
