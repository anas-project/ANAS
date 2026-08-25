//go:build !unix

package application

import (
	"context"
	"fmt"
)

func acquireModuleCommandLock(context.Context, string, string, string) (func(), error) {
	return nil, fmt.Errorf("Module Command locking is unsupported on this platform")
}
