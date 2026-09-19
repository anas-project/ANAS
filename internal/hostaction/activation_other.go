//go:build !linux

package hostaction

import "context"

func OpenSystemdActivation(context.Context) (*Activation, error) { return nil, ErrUnavailable }
