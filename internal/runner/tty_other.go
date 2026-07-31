//go:build !linux && !darwin

package runner

// Without a way to ask, the safe answer is "not a terminal": that makes every
// confirmation demand -y rather than blocking on input that may never come.
func isTerminal(fd uintptr) bool { return false }
