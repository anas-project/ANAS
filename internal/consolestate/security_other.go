//go:build !unix

package consolestate

import (
	"fmt"
	"io/fs"
)

func validateCurrentOwner(fs.FileInfo, string) error {
	return fmt.Errorf("console state ownership validation requires Unix")
}

func validateSingleLink(fs.FileInfo, string) error {
	return fmt.Errorf("console state link-count validation requires Unix")
}
