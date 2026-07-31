//go:build !linux

package runner

// Btrfs only exists on Linux. On any other host the snapshot features are
// unavailable, and reporting "not Btrfs" is the correct answer rather than an
// error: `anas init` still has to work on a developer's macOS machine, it just
// warns that rollback-with-data will not be available there.
func filesystemIsBtrfs(path string) (bool, error) {
	return false, nil
}

// filesystemFree reports "unknown" rather than a number off a differently
// shaped statfs. Every caller already has to handle not knowing, because a
// destination may be a filesystem that does not report free space at all, and
// an unknown is safer than a plausible wrong figure that would let a backup
// start with nowhere to land.
func filesystemFree(path string) (int64, bool) {
	return 0, false
}
