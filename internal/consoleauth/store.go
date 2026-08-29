package consoleauth

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"
)

type Store struct {
	directory                  string
	audit                      AuditSink
	random                     io.Reader
	now                        func() time.Time
	currentState               func(context.Context) (ConsoleState, error)
	randomMu                   sync.Mutex
	beforeEnrollmentCommitStep func(string) error
	beforeOwnerCommitStep      func(string) error
	transactionTimeout         time.Duration
}

// Open validates or creates the private authentication directory. State is
// loaded afresh under a cross-process lock for each operation so a CLI and a
// running daemon observe the same committed credentials.
func Open(directory string, audit AuditSink, options StoreOptions) (*Store, error) {
	if !filepath.IsAbs(directory) {
		return nil, fmt.Errorf("console authentication directory must be absolute")
	}
	if audit == nil {
		return nil, fmt.Errorf("console authentication audit sink is required")
	}
	if err := ensureStoreDirectory(filepath.Clean(directory)); err != nil {
		return nil, err
	}
	randomSource := options.Random
	if randomSource == nil {
		randomSource = rand.Reader
	}
	now := options.Now
	if now == nil {
		now = time.Now
	}
	return &Store{
		directory:          filepath.Clean(directory),
		audit:              audit,
		random:             randomSource,
		now:                now,
		currentState:       options.CurrentState,
		transactionTimeout: defaultTransactionConvergenceTimeout,
	}, nil
}

func (store *Store) Directory() string {
	return store.directory
}

func (store *Store) currentTime() time.Time {
	return store.now().UTC()
}

func (store *Store) newCredential() (string, error) {
	raw := make([]byte, credentialRandomBytes)
	store.randomMu.Lock()
	_, err := io.ReadFull(store.random, raw)
	store.randomMu.Unlock()
	if err != nil {
		return "", fmt.Errorf("generate credential: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

func (store *Store) hashOwnerPassword(password string) (string, error) {
	store.randomMu.Lock()
	hash, err := hashPassword(store.random, password)
	store.randomMu.Unlock()
	return hash, err
}

func (store *Store) lock(ctx context.Context) (func(), error) {
	if err := validateStoreDirectory(store.directory); err != nil {
		return nil, err
	}
	unlock, err := acquireStoreLock(ctx, filepath.Join(store.directory, lockFileName))
	if err != nil {
		return nil, err
	}
	for _, pending := range []struct {
		name        string
		description string
	}{
		{enrollmentCommitFileName, "bootstrap enrollment transaction"},
		{ownerCommitFileName, "owner enrollment transaction"},
	} {
		if _, err := os.Lstat(filepath.Join(store.directory, pending.name)); err == nil {
			unlock()
			return nil, ErrRecoveryRequired
		} else if !errors.Is(err, os.ErrNotExist) {
			unlock()
			return nil, fmt.Errorf("inspect %s: %w", pending.description, err)
		}
	}
	return unlock, nil
}

func (store *Store) recordAudit(ctx context.Context, event AuditEvent) error {
	if event.OccurredAt.IsZero() {
		event.OccurredAt = store.currentTime()
	}
	if err := store.audit.Record(ctx, event); err != nil {
		return fmt.Errorf("%w: %v", ErrAuditUnavailable, err)
	}
	return nil
}

func (store *Store) failWithAudit(ctx context.Context, event AuditEvent, operationErr error) error {
	event.Outcome = AuditFailure
	if event.Reason == "" {
		event.Reason = "operation_failed"
	}
	if err := store.recordAudit(ctx, event); err != nil {
		return err
	}
	return operationErr
}
