//go:build !linux

package hostaction

import (
	"context"
	"net"
)

func authenticatePeer(context.Context, *net.UnixConn, PeerPolicy) (Peer, error) {
	return Peer{}, ErrUnavailable
}

func verifySystemdPeerUnit(context.Context, Peer, string) error { return ErrUnavailable }
