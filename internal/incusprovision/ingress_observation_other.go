//go:build !linux

package incusprovision

import (
	"context"
	"github.com/anas-project/ANAS/internal/incusingresshost"
)

func openInstalledIngressObservation(context.Context, incusingresshost.ProjectionRequest) (*ingressObservationSession, error) {
	return nil, ErrBlocked
}
