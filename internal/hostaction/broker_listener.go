package hostaction

import (
	"context"
	"net"
	"sync"
	"time"
)

type brokerListenerGuard interface {
	check() error
	retire() error
}

// JobBrokerListener belongs to the job owner, not the privileged
// activation service. OpenJobBrokerListener creates only the fixed private
// socket in an already installed directory. It cannot install/chown directories,
// adopt stale sockets, accept arbitrary paths, or start a root process.
type JobBrokerListener struct {
	mu       sync.Mutex
	listener *net.UnixListener
	guard    brokerListenerGuard
	closed   bool
	closeErr error
}

// Accept has one caller. It checks the pinned installation while idle as well
// as around accept; shutdown and directory/socket drift are not silent retries.
// Peer authentication is performed separately, before reading request bytes.
func (l *JobBrokerListener) Accept(ctx context.Context) (*net.UnixConn, error) {
	if l == nil || ctx == nil {
		return nil, ErrUnavailable
	}
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if err := l.Check(); err != nil {
			return nil, err
		}
		deadline := time.Now().Add(200 * time.Millisecond)
		if bound, ok := ctx.Deadline(); ok && bound.Before(deadline) {
			deadline = bound
		}
		if l.listener.SetDeadline(deadline) != nil {
			return nil, ErrUnavailable
		}
		connection, err := l.listener.AcceptUnix()
		if err != nil {
			if timeout, ok := err.(net.Error); ok && timeout.Timeout() {
				continue
			}
			return nil, ErrUnavailable
		}
		if err := l.Check(); err != nil || ctx.Err() != nil {
			_ = connection.Close()
			return nil, ErrUnavailable
		}
		return connection, nil
	}
}

func (l *JobBrokerListener) Check() error {
	if l == nil {
		return ErrUnavailable
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return net.ErrClosed
	}
	if l.listener == nil || l.guard == nil || l.guard.check() != nil {
		return ErrUnavailable
	}
	return nil
}

// Close stops admission and removes only the exact socket created by this
// listener. It never recursively removes the installed directory, takes over a
// stale entry or releases any job's execution lease. Cleanup errors stay sticky.
func (l *JobBrokerListener) Close() error {
	if l == nil {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return l.closeErr
	}
	l.closed = true
	if l.listener == nil || l.guard == nil {
		l.closeErr = ErrUnavailable
		return l.closeErr
	}
	if l.listener.Close() != nil {
		l.closeErr = ErrUnavailable
	}
	if l.guard.retire() != nil {
		l.closeErr = ErrUnavailable
	}
	return l.closeErr
}
