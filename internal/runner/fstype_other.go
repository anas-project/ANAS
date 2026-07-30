//go:build !linux

package runner

// Btrfs only exists on Linux. On any other host the snapshot features are
// unavailable, and reporting "not Btrfs" is the correct answer rather than an
// error: `anas init` still has to work on a developer's macOS machine, it just
// warns that rollback-with-data will not be available there.
func filesystemIsBtrfs(path string) (bool, error) {
	return false, nil
}
