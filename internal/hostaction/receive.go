package hostaction

import (
	"context"
	"net"
	"time"

	"github.com/anas-project/ANAS/internal/actionabi"
)

// Receive accepts an ALREADY accepted local connection. It does not create a
// listening socket or perform activation. A future trusted launcher must own
// the root socket, its permissions, installation policy and activated fd.
// Closing the request write half ends one ABI request, not the action job.
func Receive(ctx context.Context, connection *net.UnixConn, policy PeerPolicy) (*Invocation, error) {
	return receive(ctx, connection, policy, nil)
}

// ReceiveObserved is the admission path for an installed launcher. Malformed
// or denied input is audited without echoing payloads or self-reported ids.
// The original Receive remains an internal transport primitive, not an entrypoint.
func ReceiveObserved(ctx context.Context, connection *net.UnixConn, policy PeerPolicy, journal AuditJournal) (*Invocation, error) {
	if journal == nil {
		return nil, ErrAudit
	}
	return receive(ctx, connection, policy, journal)
}

func receive(ctx context.Context, connection *net.UnixConn, policy PeerPolicy, journal AuditJournal) (*Invocation, error) {
	if ctx == nil || connection == nil {
		return nil, ErrUnavailable
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	peer, err := authenticatePeer(ctx, connection, policy)
	if err != nil {
		return nil, rejectAdmission(ctx, journal, peer, "peer_denied", err)
	}
	deadline := time.Now().Add(3 * time.Second)
	if bound, ok := ctx.Deadline(); ok && bound.Before(deadline) {
		deadline = bound
	}
	if connection.SetReadDeadline(deadline) != nil {
		return nil, rejectAdmission(ctx, journal, peer, "request_unavailable", ErrUnavailable)
	}
	stop := context.AfterFunc(ctx, func() { _ = connection.SetReadDeadline(time.Now()) })
	defer stop()
	request, err := actionabi.ReadRequest(connection)
	if ctx.Err() != nil {
		return nil, rejectAdmission(ctx, journal, peer, "request_cancelled", ctx.Err())
	}
	if err != nil {
		return nil, rejectAdmission(ctx, journal, peer, "invalid_request", ErrRequest)
	}
	call, err := prepare(request, peer)
	if err != nil {
		return nil, rejectAdmission(ctx, journal, peer, "invalid_request", err)
	}
	return call, nil
}
