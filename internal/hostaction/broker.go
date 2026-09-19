package hostaction

import (
	"context"
	"errors"
	"net"
	"sync"

	"github.com/anas-project/ANAS/internal/actionabi"
)

// ErrBrokerExecutorRunning forbids releasing execution ownership merely
// because the binding socket closed. The session retains a kernel process
// handle until exit is observed. It does not report an exit code or outcome.
var ErrBrokerExecutorRunning = errors.New("host executor exit is unconfirmed; retain execution ownership")

// AcceptJobBrokerObserved adds the same fail-closed rejection audit used by
// activated requests. A malformed or denied peer never supplies an audit actor
// or job identifier; only kernel credentials may be included.
func AcceptJobBrokerObserved(ctx context.Context, connection *net.UnixConn, journal AuditJournal) (*BrokerSession, error) {
	if ctx == nil || connection == nil || journal == nil {
		return nil, ErrUnavailable
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	session, err := AcceptJobBroker(connection)
	if err == nil {
		return session, nil
	}
	peer, _ := authenticatePeer(ctx, connection, PeerPolicy{})
	return nil, rejectAdmission(ctx, journal, peer, "broker_peer_denied", err)
}

// A production instance is a socket-bound pidfd, never a numeric PID lookup.
// Fixtures can implement this private interface without weakening admission.
type brokerProcess interface {
	alive() error
	waitExited(context.Context) error
	exited() (bool, error)
	close() error
}

// BrokerSession belongs to the installed execution owner. AcceptJobBroker checks
// a connected root peer before any bytes are read. Serve binds to an existing
// running job. No listener, store, callback registry or recovered job is made.
// Once a grant may have been observed, Close refuses until the exact root peer
// exits. Retain this object and the shared execution lease on uncertainty.
type BrokerSession struct {
	mu             sync.Mutex
	conn           *net.UnixConn
	process        brokerProcess
	self           PeerIdentity
	peer           Peer
	used           bool
	granted        bool
	closed         bool
	requireSystemd bool
	exitWatch      *systemdExitWatch
	exitConfirmed  bool
	closeErr       error
}

func (s *BrokerSession) Serve(ctx context.Context, release ReleaseIdentity, binding JobBinding, journal AuditJournal) (result error) {
	if s == nil || ctx == nil || release.Validate() != nil || binding == nil || journal == nil {
		return ErrUnavailable
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.used || s.conn == nil || s.process == nil || s.process.alive() != nil {
		return ErrUnavailable
	}
	s.used = true
	defer func() {
		if s.conn.Close() != nil {
			result = ErrUnavailable
		}
		s.conn = nil
	}()
	deny := func(reason string) error { return rejectAdmission(ctx, journal, s.peer, reason, ErrDenied) }
	stop, err := brokerDeadline(ctx, s.conn, brokerIOTimeout)
	if err != nil {
		return deny("broker_input_unavailable")
	}
	var claim brokerClaim
	err = readBrokerFrame(s.conn, &claim)
	stop()
	if err != nil || !validBrokerClaim(claim) || claim.Release != release || claim.Peer.peer() != s.self {
		return deny("broker_binding_mismatch")
	}
	ctx, cancel := context.WithTimeout(ctx, brokerExecutionTimeout(claim.Request.Action))
	defer cancel()
	if s.requireSystemd {
		// Pin the manager record BEFORE a grant can reach the executor. Holding
		// Unit.Ref prevents rapid exit/GC from discarding its actual exit status.
		s.exitWatch, err = captureSystemdExit(ctx, s.peer, s.process)
		if err != nil {
			return deny("broker_exit_observer_unavailable")
		}
	}
	invoked, finished, duplicate := false, false, false
	var callbackGate sync.Mutex
	err = binding.WithHostInvocation(ctx, claim.Request, claim.Release, s.self, func(owner context.Context) error {
		callbackGate.Lock()
		defer callbackGate.Unlock()
		if invoked && !finished {
			duplicate = true
		}
		if finished || invoked || owner == nil || owner.Err() != nil || s.process.alive() != nil {
			return ErrDenied
		}
		invoked = true
		// Set BEFORE write: partial/uncertain writes may already authorize the
		// executor, so neither failure nor EOF is permission to drop the pidfd.
		s.granted = true
		if writeBrokerStep(owner, s.conn, claim.Nonce, "bound") != nil || readBrokerFinished(owner, s.conn, claim.Nonce) != nil ||
			brokerEOF(owner, s.conn) != nil || s.process.alive() != nil {
			return ErrUnavailable
		}
		return nil
	})
	callbackGate.Lock()
	finished = true
	callbackGate.Unlock()
	if err != nil || !invoked || duplicate || s.process.alive() != nil {
		return deny("broker_authorization_unconfirmed")
	}
	// HostJobBinding rechecks current authority after its callback. The root
	// executor cannot emit a success candidate before this acknowledgement.
	if writeBrokerStep(ctx, s.conn, claim.Nonce, "validated") != nil || s.conn.CloseWrite() != nil {
		return ErrUnavailable
	}
	return nil
}

// WaitExecutor only observes termination of the socket-bound process. It does
// NOT establish a zero exit code, successful audit, empty cgroup or job result.
// Current compiled preflight spawns no children. Future child-spawning actions
// need additional cgroup/manager evidence and are NOT enabled by this method.
func (s *BrokerSession) WaitExecutor(ctx context.Context) error {
	if s == nil || ctx == nil {
		return ErrUnavailable
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.process == nil {
		return ErrUnavailable
	}
	return s.process.waitExited(ctx)
}

// GrantPossible reports whether authorization bytes may have reached the
// executor. It is not a completion receipt. Owners use it only to stop further
// admission after an uncertain exchange; process pins still require Close.
func (s *BrokerSession) GrantPossible() bool {
	if s == nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.granted
}

func (s *BrokerSession) Close() error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return s.closeErr
	}
	if s.granted {
		exited, err := s.process.exited()
		if err != nil || !exited || (s.requireSystemd && !s.exitConfirmed) {
			return ErrBrokerExecutorRunning
		}
	}
	s.closed = true
	failed := false
	if s.conn != nil {
		failed = s.conn.Close() != nil
		s.conn = nil
	}
	if s.process != nil {
		failed = s.process.close() != nil || failed
		s.process = nil
	}
	if s.exitWatch != nil {
		failed = s.exitWatch.source.close() != nil || failed
		s.exitWatch = nil
	}
	if failed {
		s.closeErr = ErrUnavailable
	}
	return s.closeErr
}

// ObserveSystemdExit reads independent PID 1 evidence for the exact invocation
// captured before grant. It neither trusts executor JSON nor invents a wait
// status from EOF/pidfd readiness. Non-systemd fixtures cannot call it as proof.
func (s *BrokerSession) ObserveSystemdExit(ctx context.Context) (actionabi.ExitState, error) {
	if s == nil || ctx == nil {
		return actionabi.ExitState{}, ErrUnavailable
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || !s.granted || !s.requireSystemd || s.exitWatch == nil || s.process == nil {
		return actionabi.ExitState{}, ErrUnavailable
	}
	exit, err := s.exitWatch.wait(ctx, s.process)
	if err == nil {
		s.exitConfirmed = true
	}
	return exit, err
}
