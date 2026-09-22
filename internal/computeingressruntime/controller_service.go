package computeingressruntime

import (
	"context"
	"errors"
	"io"
	"sync"
)

var (
	ErrControllerNotRunning = errors.New("HTTP controller owner is not running")
	ErrControllerDrain      = errors.New("HTTP controller drain is unconfirmed; retained session requires recovery")
	ErrControllerRelease    = errors.New("HTTP controller resources could not be released")
)

type ControllerPhase string

const (
	ControllerNew         ControllerPhase = "new"
	ControllerStarting    ControllerPhase = "starting"
	ControllerRunning     ControllerPhase = "running"
	ControllerDraining    ControllerPhase = "draining"
	ControllerDrainFailed ControllerPhase = "drain_failed"
	ControllerStopped     ControllerPhase = "stopped"
	ControllerFailed      ControllerPhase = "failed"
)

type controllerDrainAttempt struct {
	done chan struct{}
	err  error // Immutable after done closes.
}

// ControllerService owns one controller lifetime and its retirement readers.
// Construction starts no goroutine or daemon and authorizes no publication.
// Run belongs to the trusted runtime owner, never an HTTP request. All adapters
// must honor their contexts and remain available until Run returns.
//
// Failed drain retains the SAME journal/flock and reader resources. Run waits
// for explicit RetryDrain even after its owner context is canceled. Do not hide
// Stop's error as normal cancellation, or independently close the readers or
// host-action service. Process death releases kernel locks, but the existing
// journal still requires recovery. No second persistent state store is added.
type ControllerService struct {
	mu               sync.Mutex
	controller       Controller
	resources        io.Closer
	releaseUnstarted io.Closer
	phase            ControllerPhase
	cancel           context.CancelFunc
	ready            chan struct{}
	done             chan struct{}
	retry            chan struct{}
	attempt          *controllerDrainAttempt
}

func NewControllerService(c Controller, resources io.Closer) (*ControllerService, error) {
	if _, err := c.configuredExecutor(); err != nil {
		return nil, err
	}
	if resources == nil {
		return nil, ErrControllerRelease
	}
	return &ControllerService{controller: c, resources: resources, phase: ControllerNew,
		ready: make(chan struct{}), done: make(chan struct{}), retry: make(chan struct{}, 1),
		attempt: &controllerDrainAttempt{done: make(chan struct{})}}, nil
}

// Ready closes after recovery and one complete successful reconciliation. It
// records startup, not permanent guest/network health. Failed startup or
// stopping before the first successful pass never signals readiness.
func (s *ControllerService) Ready() <-chan struct{} {
	if s == nil {
		return nil
	}
	return s.ready
}

// Done closes after Run leaves the held journal session. Check Stop's result
// too: failing to acquire a session is not proof of an empty installation.
func (s *ControllerService) Done() <-chan struct{} {
	if s == nil {
		return nil
	}
	return s.done
}

func (s *ControllerService) Phase() ControllerPhase {
	if s == nil {
		return ControllerFailed
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.phase
}

func (s *ControllerService) Run(parent context.Context) error {
	owner, cancel, err := s.claimRun(parent)
	if err != nil {
		return err
	}
	return s.runClaimed(owner, cancel)
}

// Claim synchronously so a coordinator can register the owner before a
// concurrent configuration change attempts to stop it. No goroutine or I/O.
func (s *ControllerService) claimRun(parent context.Context) (context.Context, context.CancelFunc, error) {
	if s == nil || parent == nil || s.ready == nil {
		return nil, nil, ErrControllerNotRunning
	}
	if err := parent.Err(); err != nil {
		return nil, nil, err
	}
	s.mu.Lock()
	if s.phase != ControllerNew {
		s.mu.Unlock()
		return nil, nil, ErrControllerNotRunning
	}
	owner, cancel := context.WithCancel(parent)
	s.cancel, s.phase = cancel, ControllerStarting
	s.mu.Unlock()
	return owner, cancel, nil
}

func (s *ControllerService) runClaimed(owner context.Context, cancel context.CancelFunc) error {
	defer cancel()

	e, _ := s.controller.configuredExecutor() // Immutable since construction.
	drained := false
	entered := false
	var releaseErr error
	runErr := e.withSession(owner, func(session execution) error {
		entered = true
		cause, drainErr := s.controller.runSession(owner, session, s.markReady, s.markStopping)
		for drainErr != nil {
			s.mu.Lock()
			s.phase = ControllerDrainFailed
			// Expose a stable category, not private adapter diagnostics.
			s.attempt.err = ErrControllerDrain
			close(s.attempt.done)
			s.mu.Unlock()
			// No automatic retry, restart or permission renewal while retained.
			<-s.retry
			drainErr = s.controller.drain(owner, session)
		}
		drained = true
		if err := s.resources.Close(); err != nil {
			releaseErr = ErrControllerRelease
			return errors.Join(cause, releaseErr)
		}
		// Persistent cross-process admission is restored only after external
		// inventory, retirement and original reader cleanup all succeeded.
		clean, cancel := context.WithTimeout(context.WithoutCancel(owner), s.controller.CleanupTimeout)
		fenceErr := session.completeWorkspaceDrain(clean)
		cancel()
		if fenceErr != nil {
			releaseErr = ErrControllerRelease
			return errors.Join(cause, releaseErr)
		}
		return cause
	})
	if !entered && s.releaseUnstarted != nil {
		if err := s.releaseUnstarted.Close(); err != nil {
			releaseErr = ErrControllerRelease
			runErr = errors.Join(runErr, releaseErr)
		}
	}

	s.mu.Lock()
	if drained && releaseErr == nil && (runErr == nil || runErr == owner.Err()) {
		s.phase = ControllerStopped
	} else {
		s.phase = ControllerFailed
		s.attempt.err = errors.Join(ErrControllerDrain, releaseErr)
	}
	// Success is delivered only after WithExclusive actually returns.
	close(s.attempt.done)
	close(s.done)
	s.mu.Unlock()
	return runErr
}

func (s *ControllerService) markReady() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.phase == ControllerStarting {
		s.phase = ControllerRunning
		close(s.ready)
	}
}

func (s *ControllerService) markStopping() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.phase == ControllerStarting || s.phase == ControllerRunning {
		s.phase = ControllerDraining
	}
}

// Stop closes admission once, cancels active observation and waits for this
// drain attempt. Canceling the wait does not cancel the independent drain.
// Repeating Stop after failure returns the failure; RetryDrain is explicit.
func (s *ControllerService) Stop(ctx context.Context) error {
	if s == nil || ctx == nil {
		return ErrControllerNotRunning
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	attempt, err := s.requestDrain(false)
	if err != nil {
		return err
	}
	return waitControllerDrain(ctx, attempt)
}

// Close admission without waiting. Coordinators first signal ALL affected
// owners, so a slow/broken scope cannot leave later scopes publishing.
func (s *ControllerService) requestDrain(retry bool) (*controllerDrainAttempt, error) {
	s.mu.Lock()
	if s.phase == ControllerNew || s.attempt == nil {
		s.mu.Unlock()
		return nil, ErrControllerNotRunning
	}
	if retry {
		switch s.phase {
		case ControllerDrainFailed:
			s.attempt = &controllerDrainAttempt{done: make(chan struct{})}
			s.phase = ControllerDraining
			s.retry <- struct{}{}
		case ControllerDraining, ControllerStopped, ControllerFailed:
		default:
			s.mu.Unlock()
			return nil, ErrControllerNotRunning
		}
	} else if s.phase == ControllerStarting || s.phase == ControllerRunning {
		s.phase = ControllerDraining
	}
	attempt, cancel := s.attempt, s.cancel
	s.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	return attempt, nil
}

// RetryDrain only retires recorded targets. It never reads desired work or
// starts a replacement controller. Concurrent waiters join the same attempt.
func (s *ControllerService) RetryDrain(ctx context.Context) error {
	if s == nil || ctx == nil {
		return ErrControllerNotRunning
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	attempt, err := s.requestDrain(true)
	if err != nil {
		return err
	}
	return waitControllerDrain(ctx, attempt)
}

func waitControllerDrain(ctx context.Context, attempt *controllerDrainAttempt) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-attempt.done:
		return attempt.err
	}
}
