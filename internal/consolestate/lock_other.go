//go:build !unix

package consolestate

import (
	"context"
	"errors"
)

func acquireStateLock(context.Context, string) (func(), bool, error) {
	return nil, false, errors.New("cross-process console state locking requires Unix flock")
}
