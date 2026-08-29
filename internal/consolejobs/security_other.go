//go:build !unix

package consolejobs

import (
	"fmt"
	"os"
)

func validateCurrentOwner(os.FileInfo, string) error {
	return fmt.Errorf("job store ownership validation is unavailable on this platform")
}

func validateSingleLink(os.FileInfo, string) error {
	return fmt.Errorf("job store link-count validation is unavailable on this platform")
}
