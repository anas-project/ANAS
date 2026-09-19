//go:build !linux

package hostaction

import (
	"context"
	"net"
)

func AcceptJobBroker(*net.UnixConn) (*BrokerSession, error)              { return nil, ErrUnavailable }
func pinBrokerOrigin(*net.UnixConn, PeerIdentity) (brokerProcess, error) { return nil, ErrUnavailable }
func dialJobBroker(context.Context, installationPolicy, PeerIdentity) (*remoteJobBinding, error) {
	return nil, ErrUnavailable
}
