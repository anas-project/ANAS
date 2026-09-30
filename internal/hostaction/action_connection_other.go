//go:build !linux

package hostaction

import (
	"context"
	"github.com/anas-project/ANAS/internal/actionabi"
	"net"
)

func CheckHostActionClient() error { return ErrUnavailable }

func DialHostAction(context.Context, actionabi.Request) (*net.UnixConn, error) {
	return nil, ErrUnavailable
}

func QueryHostInvocation(context.Context, string, string) (InvocationStatus, error) {
	return InvocationStatus{}, ErrUnavailable
}
