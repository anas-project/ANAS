//go:build !linux

package hostaction

import "context"

func captureSystemdExit(context.Context, Peer, brokerProcess) (*systemdExitWatch, error) {
	return nil, ErrUnavailable
}
