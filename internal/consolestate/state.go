// Package consolestate persists the monotonic capability state of the anasd
// management plane. It deliberately owns no certificate or account logic;
// callers may only request the next state after those preconditions succeed.
package consolestate

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
)

const (
	// APIVersion is the only on-disk capability-state schema accepted by this
	// package. Incompatible changes require a new value.
	APIVersion = "anas.console-state/v1"

	StateFileName = "capability-state.json"
	LockFileName  = "capability-state.lock"
)

type State string

const (
	StateBootstrap  State = "bootstrap"
	StateEnrollment State = "enrollment"
	StateFull       State = "full"
)

type TransitionReason string

const (
	ReasonBootstrapCompleted TransitionReason = "bootstrap_completed"
	ReasonOwnerEnrolled      TransitionReason = "owner_enrolled"
)

// TransitionEvent is intentionally closed: it has no generic payload in which
// a caller could place a password, token, session, CSRF value, or other
// credential. Reason is selected by this package rather than by callers.
type TransitionEvent struct {
	From   State
	To     State
	Actor  string
	Reason TransitionReason
}

type AuditSink interface {
	RecordTransition(context.Context, TransitionEvent) error
}

type AuditSinkFunc func(context.Context, TransitionEvent) error

func (function AuditSinkFunc) RecordTransition(ctx context.Context, event TransitionEvent) error {
	if function == nil {
		return ErrAuditUnavailable
	}
	return function(ctx, event)
}

var (
	ErrAuditUnavailable  = errors.New("console state transition audit is unavailable")
	ErrInvalidTransition = errors.New("invalid console capability state transition")
	ErrStateUnavailable  = errors.New("console capability state is unavailable")
)

var actorPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:@/-]{0,127}$`)

type stateFile struct {
	APIVersion string `json:"api_version"`
	State      State  `json:"state"`
}

type Store struct {
	directory    string
	audit        AuditSink
	beforeRename func(temporaryPath, statePath string) error
}

// Open initializes a previously unused private console_store to bootstrap, or
// validates the existing state. A persistent lock file distinguishes a new
// store from a missing state file after initialization, so deletion cannot
// silently reset an enrolled service to bootstrap.
func Open(ctx context.Context, directory string, audit AuditSink) (*Store, error) {
	if ctx == nil {
		return nil, fmt.Errorf("%w: context is nil", ErrStateUnavailable)
	}
	if !filepath.IsAbs(directory) {
		return nil, fmt.Errorf("%w: console_store must be absolute", ErrStateUnavailable)
	}
	if audit == nil {
		return nil, ErrAuditUnavailable
	}
	directory = filepath.Clean(directory)
	if err := ensurePrivateDirectory(directory); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrStateUnavailable, err)
	}

	store := &Store{directory: directory, audit: audit}
	unlock, lockCreated, err := acquireStateLock(ctx, directory)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrStateUnavailable, err)
	}
	defer unlock()

	record, found, err := readStateFile(store.statePath())
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrStateUnavailable, err)
	}
	if found {
		if err := validateStateFile(record); err != nil {
			return nil, fmt.Errorf("%w: %v", ErrStateUnavailable, err)
		}
		return store, nil
	}
	if !lockCreated {
		return nil, fmt.Errorf("%w: %s is missing from an initialized console_store", ErrStateUnavailable, StateFileName)
	}
	if err := store.writeState(stateFile{APIVersion: APIVersion, State: StateBootstrap}); err != nil {
		return nil, fmt.Errorf("%w: initialize bootstrap state: %v", ErrStateUnavailable, err)
	}
	return store, nil
}

func (store *Store) Directory() string {
	if store == nil {
		return ""
	}
	return store.directory
}

// Current reloads the canonical file while holding the cross-process lock, so
// separate anasd/CLI processes observe the same committed state.
func (store *Store) Current(ctx context.Context) (State, error) {
	if store == nil || store.audit == nil {
		return "", ErrStateUnavailable
	}
	unlock, _, err := acquireStateLock(ctx, store.directory)
	if err != nil {
		return "", fmt.Errorf("%w: %w", ErrStateUnavailable, err)
	}
	defer unlock()
	record, found, err := readStateFile(store.statePath())
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrStateUnavailable, err)
	}
	if !found {
		return "", fmt.Errorf("%w: %s is missing", ErrStateUnavailable, StateFileName)
	}
	if err := validateStateFile(record); err != nil {
		return "", fmt.Errorf("%w: %v", ErrStateUnavailable, err)
	}
	return record.State, nil
}

// Transition commits exactly one forward edge. The durable audit sink is
// called while the state lock is held and before the atomic state-file rename;
// an audit failure therefore leaves the prior state untouched.
func (store *Store) Transition(ctx context.Context, to State, actor string) (State, error) {
	if store == nil || store.audit == nil {
		return "", ErrStateUnavailable
	}
	if !actorPattern.MatchString(actor) {
		return "", fmt.Errorf("%w: actor must be a bounded stable identifier", ErrInvalidTransition)
	}
	if !validState(to) {
		return "", fmt.Errorf("%w: unsupported target %q", ErrInvalidTransition, to)
	}
	unlock, _, err := acquireStateLock(ctx, store.directory)
	if err != nil {
		return "", fmt.Errorf("%w: %w", ErrStateUnavailable, err)
	}
	defer unlock()

	record, found, err := readStateFile(store.statePath())
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrStateUnavailable, err)
	}
	if !found {
		return "", fmt.Errorf("%w: %s is missing", ErrStateUnavailable, StateFileName)
	}
	if err := validateStateFile(record); err != nil {
		return "", fmt.Errorf("%w: %v", ErrStateUnavailable, err)
	}
	reason, allowed := transitionReason(record.State, to)
	if !allowed {
		return record.State, fmt.Errorf("%w: %s to %s", ErrInvalidTransition, record.State, to)
	}
	event := TransitionEvent{From: record.State, To: to, Actor: actor, Reason: reason}
	if err := store.audit.RecordTransition(ctx, event); err != nil {
		return record.State, errors.Join(ErrAuditUnavailable, err)
	}
	if err := store.writeState(stateFile{APIVersion: APIVersion, State: to}); err != nil {
		return record.State, fmt.Errorf("%w: commit %s: %v", ErrStateUnavailable, to, err)
	}
	return to, nil
}

func (store *Store) statePath() string {
	return filepath.Join(store.directory, StateFileName)
}

func (store *Store) writeState(record stateFile) error {
	return writeStateFile(store.directory, record, store.beforeRename)
}

func transitionReason(from, to State) (TransitionReason, bool) {
	switch {
	case from == StateBootstrap && to == StateEnrollment:
		return ReasonBootstrapCompleted, true
	case from == StateEnrollment && to == StateFull:
		return ReasonOwnerEnrolled, true
	default:
		return "", false
	}
}

func validState(state State) bool {
	return state == StateBootstrap || state == StateEnrollment || state == StateFull
}

func validateStateFile(record stateFile) error {
	if record.APIVersion != APIVersion {
		return fmt.Errorf("api_version must be %q", APIVersion)
	}
	if !validState(record.State) {
		return fmt.Errorf("state must be bootstrap, enrollment, or full")
	}
	return nil
}
