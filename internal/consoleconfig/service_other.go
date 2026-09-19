//go:build !linux

package consoleconfig

// Non-root production service loading is supported only on Linux.
func LoadService(path string) (Config, error) { return Load(path, RootOwnedFilePolicy()) }
