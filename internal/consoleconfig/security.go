package consoleconfig

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
)

const maximumConfigBytes = 1 << 20

// FileSecurityPolicy validates service configuration file metadata. Production
// callers use RootOwnedFilePolicy; tests and non-root development can use
// CurrentUIDFilePolicy. Load rejects a nil policy rather than silently falling
// back to an unsafe mode.
type FileSecurityPolicy interface {
	Validate(path string, info fs.FileInfo) error
}

// FileSecurityPolicyFunc adapts a function for focused tests or an explicitly
// defined platform policy.
type FileSecurityPolicyFunc func(path string, info fs.FileInfo) error

func (function FileSecurityPolicyFunc) Validate(path string, info fs.FileInfo) error {
	return function(path, info)
}

// Load securely opens an absolute service configuration path, validates both
// the directory entry and opened file against policy, detects replacement
// between those operations, and parses its contents. It does not consult the
// environment.
func Load(path string, policy FileSecurityPolicy) (Config, error) {
	if !filepath.IsAbs(path) {
		return Config{}, errors.New("anasd service configuration path must be absolute")
	}
	if policy == nil {
		return Config{}, errors.New("anasd service configuration file security policy is required")
	}

	entryInfo, err := os.Lstat(path)
	if err != nil {
		return Config{}, fmt.Errorf("inspect anasd service configuration: %w", err)
	}
	if err := policy.Validate(path, entryInfo); err != nil {
		return Config{}, fmt.Errorf("validate anasd service configuration: %w", err)
	}

	file, err := os.Open(path)
	if err != nil {
		return Config{}, fmt.Errorf("open anasd service configuration: %w", err)
	}
	defer file.Close()

	openedInfo, err := file.Stat()
	if err != nil {
		return Config{}, fmt.Errorf("inspect opened anasd service configuration: %w", err)
	}
	if !os.SameFile(entryInfo, openedInfo) {
		return Config{}, errors.New("anasd service configuration changed while it was being opened")
	}
	if err := policy.Validate(path, openedInfo); err != nil {
		return Config{}, fmt.Errorf("validate opened anasd service configuration: %w", err)
	}

	source, err := io.ReadAll(io.LimitReader(file, maximumConfigBytes+1))
	if err != nil {
		return Config{}, fmt.Errorf("read anasd service configuration: %w", err)
	}
	if len(source) > maximumConfigBytes {
		return Config{}, fmt.Errorf("read anasd service configuration: file exceeds %d bytes", maximumConfigBytes)
	}
	afterReadInfo, err := file.Stat()
	if err != nil {
		return Config{}, fmt.Errorf("reinspect opened anasd service configuration: %w", err)
	}
	if err := policy.Validate(path, afterReadInfo); err != nil {
		return Config{}, fmt.Errorf("revalidate opened anasd service configuration: %w", err)
	}
	currentInfo, err := os.Lstat(path)
	if err != nil {
		return Config{}, fmt.Errorf("reinspect anasd service configuration path: %w", err)
	}
	if err := policy.Validate(path, currentInfo); err != nil {
		return Config{}, fmt.Errorf("revalidate anasd service configuration path: %w", err)
	}
	if !sameFileVersion(entryInfo, openedInfo) || !sameFileVersion(openedInfo, afterReadInfo) || !sameFileVersion(afterReadInfo, currentInfo) {
		return Config{}, errors.New("anasd service configuration changed while it was being read")
	}
	config, err := Parse(source)
	if err != nil {
		return Config{}, err
	}
	if err := validateResolvedStorageBoundary(config); err != nil {
		return Config{}, err
	}
	return config, nil
}

func sameFileVersion(left, right fs.FileInfo) bool {
	return left != nil && right != nil && os.SameFile(left, right) &&
		left.Mode() == right.Mode() && left.Size() == right.Size() && left.ModTime().Equal(right.ModTime())
}
