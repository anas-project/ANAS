//go:build !unix

package tempconsolecert

import (
	"fmt"
	"io/fs"
)

func validateCurrentOwner(fs.FileInfo) error {
	return fmt.Errorf("file ownership validation is unsupported on this platform")
}
