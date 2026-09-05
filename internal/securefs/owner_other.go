//go:build !unix

package securefs

import (
	"fmt"
	"os"
)

func ValidateCurrentOwner(_ os.FileInfo, name string) error {
	return fmt.Errorf("%s ownership validation is unavailable on this platform", name)
}

func ValidateSingleLink(_ os.FileInfo, name string) error {
	return fmt.Errorf("%s link-count validation is unavailable on this platform", name)
}
