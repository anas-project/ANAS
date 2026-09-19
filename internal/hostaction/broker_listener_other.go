//go:build !linux

package hostaction

import "context"

func OpenJobBrokerListener(context.Context) (*JobBrokerListener, error) { return nil, ErrUnavailable }
