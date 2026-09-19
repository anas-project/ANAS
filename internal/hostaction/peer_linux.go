//go:build linux

package hostaction

import (
	"context"
	"golang.org/x/sys/unix"
	"net"
)

func authenticatePeer(ctx context.Context, connection *net.UnixConn, policy PeerPolicy) (Peer, error) {
	raw, err := connection.SyscallConn()
	if err != nil {
		return Peer{}, ErrDenied
	}
	var peer Peer
	var socketErr error
	err = raw.Control(func(fd uintptr) {
		var kind int
		kind, socketErr = unix.GetsockoptInt(int(fd), unix.SOL_SOCKET, unix.SO_TYPE)
		if socketErr != nil || kind != unix.SOCK_STREAM {
			return
		}
		var credentials *unix.Ucred
		credentials, socketErr = unix.GetsockoptUcred(int(fd), unix.SOL_SOCKET, unix.SO_PEERCRED)
		if socketErr != nil || credentials == nil {
			return
		}
		peer = Peer{pid: credentials.Pid, uid: credentials.Uid, gid: credentials.Gid}
	})
	if err != nil || socketErr != nil {
		return Peer{}, ErrDenied
	}
	if !policy.authorizes(ctx, peer) {
		// Preserve ONLY the kernel credentials for rejected-connection audit.
		// verified remains false, so this value cannot prepare an invocation.
		return peer, ErrDenied
	}
	peer.verified = true
	return peer, nil
}
