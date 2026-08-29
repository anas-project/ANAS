//go:build !unix

package audit

import (
	"fmt"
	"os"
)

func validateCurrentOwner(os.FileInfo, string) error {
	return fmt.Errorf("audit file ownership validation is unavailable on this platform")
}

func validateSingleLink(os.FileInfo, string) error {
	return fmt.Errorf("audit file link-count validation is unavailable on this platform")
}
