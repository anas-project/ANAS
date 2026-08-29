//go:build !unix

package consoleauth

import (
	"context"
	"errors"
)

func acquireStoreLock(context.Context, string) (func(), error) {
	return nil, errors.New("cross-process authentication locking requires Unix flock")
}
