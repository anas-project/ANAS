package hostaction

import (
	"context"
	"net"
	"sync"
	"time"

	"github.com/anas-project/ANAS/internal/actionabi"
	"github.com/anas-project/ANAS/internal/securefs"
)

type activationGuard interface {
	check() error
	close() error
}

// Activation owns one already-accepted stream. It never binds/listens, reads
// an arbitrary configuration path, creates a job journal, or installs a unit.
// The launcher supplies the fixed audit writer and hostd's invocation ledger.
type Activation struct {
	mu           sync.Mutex
	connection   *net.UnixConn
	guard        activationGuard
	policy       installationPolicy
	used, closed bool
}

func (a *Activation) Close() error {
	if a == nil {
		return nil
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.closeLocked()
}

func (a *Activation) closeLocked() error {
	if a.closed {
		return nil
	}
	a.closed = true
	failed := false
	if a.connection != nil && a.connection.Close() != nil {
		failed = true
	}
	if a.guard != nil && a.guard.close() != nil {
		failed = true
	}
	if failed {
		return ErrUnavailable
	}
	return nil
}

// Serve handles exactly one request then closes its descriptors. The owner
// context is NOT derived from a browser/CLI subscription. Client EOF terminates
// input only; client disconnect does not interrupt execution or final audit:
// the ledger keeps the terminal for a later ActionInvocationStatus query.
func (a *Activation) Serve(owner context.Context, journal AuditJournal, ledger InvocationLedger) (returnErr error) {
	if a == nil || owner == nil || journal == nil || ledger == nil {
		return ErrUnavailable
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.used || a.closed || a.connection == nil || a.guard == nil {
		return ErrUnavailable
	}
	a.used = true
	defer func() {
		// A valid result frame is still provisional. Do not let a launcher
		// report clean exit when its descriptor cleanup could not be confirmed.
		if a.closeLocked() != nil {
			returnErr = ErrUnavailable
		}
	}()
	if owner.Err() != nil {
		return owner.Err()
	}
	if a.guard.check() != nil {
		peer, _ := authenticatePeer(owner, a.connection, PeerPolicy{})
		return rejectAdmission(owner, journal, peer, "installation_changed", ErrUnavailable)
	}
	call, err := ReceiveObserved(owner, a.connection, a.policy.peers(), journal)
	if err != nil {
		return err
	}
	var event actionabi.Event
	if call.request.Action == ActionInvocationStatus {
		event, err = queryInvocation(owner, call, journal, ledger, a.guard.check)
	} else {
		event, err = executeRecorded(owner, call, journal, ledger, a.policy.Release, a.guard.check, func(progress actionabi.Event) error {
			frame, err := actionabi.EncodeExecutorEvent(progress)
			if err != nil || a.guard.check() != nil || a.connection.SetWriteDeadline(time.Now().Add(ioTimeout)) != nil {
				return ErrUnavailable
			}
			return securefs.WriteAll(a.connection, frame)
		})
	}
	if err != nil {
		return err
	}
	frame, err := actionabi.EncodeExecutorEvent(event)
	if err != nil || a.guard.check() != nil {
		return ErrUnavailable
	}
	// Do not let an abandoned reader hold a socket-activated process alive.
	if a.connection.SetWriteDeadline(time.Now().Add(ioTimeout)) != nil {
		return ErrUnavailable
	}
	if securefs.WriteAll(a.connection, frame) != nil {
		return ErrUnavailable
	}
	if a.connection.CloseWrite() != nil {
		return ErrUnavailable
	}
	// Failure terminals require a nonzero real process exit (action ABI).
	// Returning nil here would contradict the emitted failed result.
	if event.Error != nil {
		return ErrActionFailed
	}
	return nil
}

// ioTimeout bounds each socket read or write; ExecutionTimeout is the compiled
// handler budget plus its bounded final audit and the terminal exchange.
const ioTimeout = 3 * time.Second

func ExecutionTimeout(action string) time.Duration {
	spec, ok := LookupAction(action)
	if !ok || spec.Timeout <= 0 {
		return ioTimeout
	}
	return time.Duration(spec.Timeout)*time.Second + 5*time.Second + 2*ioTimeout
}
