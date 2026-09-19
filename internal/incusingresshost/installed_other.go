//go:build !linux

package incusingresshost

import (
	"context"
	"fmt"
)

func NewLocalInstalledBackend(context.Context, string, Resolver) (*Backend, error) {
	return nil, fmt.Errorf("production Incus ingress host backend is Linux-only")
}
