//go:build unix

package hostconfirmation

import (
	"fmt"
	"os"
)

func requireProductionRoot() error {
	if os.Geteuid() != 0 {
		return fmt.Errorf("%w: production confirmation store requires root", ErrUnavailable)
	}
	return nil
}
