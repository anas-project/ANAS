package jobexecutor

import (
	"context"
	"net"

	"github.com/anas-project/ANAS/internal/hostaction"
)

// Kept private for fault-injected tests. Production sessions can only be made
// by hostaction's kernel-authenticated accept path.
type hostBrokerSession interface {
	Serve(context.Context, hostaction.ReleaseIdentity, hostaction.JobBinding, hostaction.AuditJournal) error
	WaitExecutor(context.Context) error
	GrantPossible() bool
	Close() error
}

func (b *HostJobBinding) attachBroker(session hostBrokerSession) error {
	if b == nil || session == nil {
		return hostaction.ErrUnavailable
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed || b.used || b.remote != nil || b.unretain == nil {
		return hostaction.ErrDenied
	}
	b.remote = session
	return nil
}

// ServeBroker belongs to the execution owner's private accept path,
// not an HTTP/CLI subscriber. The binding must already refer to a running job
// in the shared store. Root never receives its path or opens that store.
// Failure after a possible grant keeps the remote process handle and lease;
// Close refuses until that exact process has exited. No implicit retry exists.
func (b *HostJobBinding) ServeBroker(owner context.Context, connection *net.UnixConn, journal hostaction.AuditJournal) error {
	if b == nil || owner == nil || connection == nil || journal == nil {
		return hostaction.ErrUnavailable
	}
	session, err := hostaction.AcceptJobBrokerObserved(owner, connection, journal)
	if err != nil {
		_ = connection.Close()
		return err
	}
	if err := b.attachBroker(session); err != nil {
		_ = session.Close()
		return err
	}
	err = session.Serve(owner, b.release, b, journal)
	b.brokerFinished(err)
	return err
}

func (b *HostJobBinding) brokerFinished(err error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if !b.exchangeFinished && b.exchangeDone != nil {
		b.exchangeFinished, b.exchangeErr = true, err
		close(b.exchangeDone)
	}
}

// WaitBrokerExecutor observes the authenticated root process, never a PID
// supplied in JSON. Exit observation alone is NOT sufficient to commit success:
// the common recorder still needs actual exit status and complete output.
func (b *HostJobBinding) WaitBrokerExecutor(ctx context.Context) error {
	if b == nil || ctx == nil {
		return hostaction.ErrUnavailable
	}
	b.mu.Lock()
	session := b.remote
	b.mu.Unlock()
	if session == nil {
		return hostaction.ErrUnavailable
	}
	return session.WaitExecutor(ctx)
}
