//go:build !linux

package incusingresshost

import (
	"context"
	"fmt"
)

func withInstalledNamespace(context.Context, backendConfig, func() error) error {
	return fmt.Errorf("opened network namespace execution requires Linux")
}
