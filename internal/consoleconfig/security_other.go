//go:build !unix

package consoleconfig

import (
	"errors"
	"io/fs"
)

type unsupportedOwnerFilePolicy struct{}

func RootOwnedFilePolicy() FileSecurityPolicy  { return unsupportedOwnerFilePolicy{} }
func CurrentUIDFilePolicy() FileSecurityPolicy { return unsupportedOwnerFilePolicy{} }

func (unsupportedOwnerFilePolicy) Validate(string, fs.FileInfo) error {
	return errors.New("Unix file ownership validation is unavailable on this platform")
}
