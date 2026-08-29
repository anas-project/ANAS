package consoleauth

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"time"
)

const (
	ownerCommitAPIVersion = "anas.console-auth-owner-commit/v1"
	ownerCommitPrepared   = "prepared"
	ownerCommitAuthReady  = "auth_committed"
)

type authenticationSnapshot struct {
	Bootstrap        bootstrapStateFile `json:"bootstrap"`
	BootstrapExisted bool               `json:"bootstrap_existed"`
	Local            localStateFile     `json:"local"`
	LocalExisted     bool               `json:"local_existed"`
}

type ownerCommitJournal struct {
	APIVersion    string                 `json:"api_version"`
	Phase         string                 `json:"phase"`
	TransactionID string                 `json:"transaction_id"`
	Previous      authenticationSnapshot `json:"previous"`
	Next          authenticationSnapshot `json:"next"`
}

// CompleteInitialOwner performs the authentication side of enrollment as one
// recoverable transaction. It authenticates the bound enrollment session,
// installs the independent local-owner PHC, revokes every bootstrap,
// enrollment and local session, then invokes transition as the final publish
// step. If transition fails, CurrentState determines whether the state publish
// happened: enrollment rolls back, full rolls forward, and an unavailable or
// unexpected state keeps the WAL so startup recovery can decide safely.
// After the WAL is durably prepared, convergence runs on an independently
// bounded context so an HTTP client disconnect cannot strand the transaction.
//
// The transition callback must commit enrollment -> full with durable audit
// before its state rename. Callers must not use a no-op callback in production.
func (store *Store) CompleteInitialOwner(ctx context.Context, request CompleteInitialOwnerRequest, transition func(context.Context) error) error {
	if transition == nil {
		return errors.New("owner enrollment state transition is required")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	origin, err := NormalizeOrigin(request.Origin)
	if err != nil || len(origin) < len("https://") || origin[:len("https://")] != "https://" {
		return store.failWithAudit(ctx, AuditEvent{Action: AuditOwnerEnroll, Reason: "invalid_origin"}, ErrOriginMismatch)
	}
	if validateTransactionID(request.TransactionID) != nil {
		return store.failWithAudit(ctx, AuditEvent{Action: AuditOwnerEnroll, Reason: "invalid_transaction", Origin: origin}, ErrTransactionMismatch)
	}
	if err := validatePasswordInput(request.Password); err != nil {
		return store.failWithAudit(ctx, AuditEvent{
			Action: AuditOwnerEnroll, Reason: "invalid_password", TransactionID: request.TransactionID,
			Origin: origin, TargetOrigin: origin, State: StateEnrollment,
		}, err)
	}
	passwordPHC, err := store.hashOwnerPassword(request.Password)
	if err != nil {
		return store.failWithAudit(ctx, AuditEvent{Action: AuditOwnerEnroll, Reason: "hash_failed", TransactionID: request.TransactionID}, err)
	}
	request.Password = ""

	unlock, err := store.lock(ctx)
	if err != nil {
		return store.failWithAudit(ctx, AuditEvent{Action: AuditOwnerEnroll, Reason: "store_unavailable", TransactionID: request.TransactionID}, err)
	}
	defer unlock()
	previous, err := store.loadAuthenticationSnapshot()
	if err != nil {
		return store.failWithAudit(ctx, AuditEvent{Action: AuditOwnerEnroll, Reason: "state_unavailable", TransactionID: request.TransactionID}, err)
	}
	if previous.Local.OwnerPasswordPHC != "" {
		return store.failWithAudit(ctx, AuditEvent{Action: AuditOwnerEnroll, Reason: "owner_exists", TransactionID: request.TransactionID}, ErrInvalidCredentials)
	}
	if err := authenticateEnrollmentSnapshot(previous.Bootstrap, store.currentTime(), request, origin); err != nil {
		return store.failWithAudit(ctx, AuditEvent{
			Action: AuditOwnerEnroll, Reason: "session_unauthorized", TransactionID: request.TransactionID,
			Origin: origin, TargetOrigin: origin, State: StateEnrollment,
		}, err)
	}

	next := previous
	next.Bootstrap = newBootstrapState()
	next.BootstrapExisted = true
	next.Local = newLocalState()
	next.Local.OwnerPasswordPHC = passwordPHC
	next.LocalExisted = true
	journal := ownerCommitJournal{
		APIVersion: ownerCommitAPIVersion, Phase: ownerCommitPrepared,
		TransactionID: request.TransactionID, Previous: previous, Next: next,
	}
	if err := validateOwnerCommitJournal(journal); err != nil {
		return store.failWithAudit(ctx, AuditEvent{Action: AuditOwnerEnroll, Reason: "candidate_invalid", TransactionID: request.TransactionID}, err)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := store.recordAudit(ctx, AuditEvent{
		Action: AuditOwnerEnroll, Outcome: AuditSuccess, TransactionID: request.TransactionID,
		Origin: origin, TargetOrigin: origin, State: StateEnrollment,
		RevokedSessions: len(previous.Bootstrap.Sessions) + len(previous.Bootstrap.EnrollmentSessions) + len(previous.Local.Sessions),
	}); err != nil {
		return err
	}
	if err := store.writeOwnerCommitJournal(journal); err != nil {
		return store.failWithAudit(ctx, AuditEvent{Action: AuditOwnerEnroll, Reason: "journal_failed", TransactionID: request.TransactionID}, err)
	}
	convergenceCtx, cancelConvergence := store.transactionConvergenceContext(ctx)
	defer cancelConvergence()
	if err := store.ownerCommitStep("journal_prepared"); err != nil {
		return store.rollbackOwnerCommitWithFreshContext(convergenceCtx, journal, err)
	}
	if err := store.writeAuthenticationSnapshot(next); err != nil {
		return store.rollbackOwnerCommitWithFreshContext(convergenceCtx, journal, err)
	}
	if err := store.ownerCommitStep("authentication_written"); err != nil {
		return store.rollbackOwnerCommitWithFreshContext(convergenceCtx, journal, err)
	}
	journal.Phase = ownerCommitAuthReady
	if err := store.writeOwnerCommitJournal(journal); err != nil {
		return store.rollbackOwnerCommitWithFreshContext(convergenceCtx, journal, err)
	}
	if err := store.ownerCommitStep("before_state_transition"); err != nil {
		return store.rollbackOwnerCommitWithFreshContext(convergenceCtx, journal, err)
	}
	if err := transition(convergenceCtx); err != nil {
		resolutionCtx, cancelResolution := store.transactionConvergenceContext(convergenceCtx)
		defer cancelResolution()
		return store.resolveOwnerTransitionError(resolutionCtx, journal, err)
	}
	if err := store.removeOwnerCommitJournal(); err != nil {
		// State is already full and the authentication candidate is published.
		// A cleanup failure must not make the caller retry creation; the WAL is
		// safe to roll forward and remove at the next startup recovery.
		_ = store.recordAudit(convergenceCtx, AuditEvent{
			Action: AuditOwnerRecover, Outcome: AuditFailure, Reason: "cleanup_deferred",
			TransactionID: journal.TransactionID, State: StateFull,
		})
	}
	return nil
}

// RecoverInitialOwnerCommit resolves a durable owner-enrollment WAL before
// listeners open. Full means the final state publish happened, so recovery
// rolls the authentication snapshot forward; bootstrap/enrollment means it did
// not, so recovery restores the prior snapshot.
func (store *Store) RecoverInitialOwnerCommit(ctx context.Context, currentState func(context.Context) (ConsoleState, error)) error {
	if currentState == nil {
		return errors.New("owner enrollment recovery requires a state provider")
	}
	if err := validateStoreDirectory(store.directory); err != nil {
		return err
	}
	unlock, err := acquireStoreLock(ctx, filepath.Join(store.directory, lockFileName))
	if err != nil {
		return err
	}
	defer unlock()
	journal, found, err := store.loadOwnerCommitJournal()
	if err != nil || !found {
		return err
	}
	if _, err := os.Lstat(filepath.Join(store.directory, enrollmentCommitFileName)); err == nil {
		return errors.New("multiple authentication recovery journals are present")
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("inspect bootstrap enrollment transaction during recovery: %w", err)
	}
	state, err := currentState(ctx)
	if err != nil {
		return fmt.Errorf("read capability state during owner enrollment recovery: %w", err)
	}
	var snapshot authenticationSnapshot
	var reason string
	switch state {
	case StateFull:
		snapshot = journal.Next
		reason = "roll_forward_full"
	case StateBootstrap, StateEnrollment:
		snapshot = journal.Previous
		reason = "roll_back_unpublished"
	default:
		return fmt.Errorf("recover owner enrollment: invalid capability state %q", state)
	}
	if err := store.recordAudit(ctx, AuditEvent{
		Action: AuditOwnerRecover, Outcome: AuditSuccess, Reason: reason,
		TransactionID: journal.TransactionID, State: state,
	}); err != nil {
		return err
	}
	if err := store.writeAuthenticationSnapshot(snapshot); err != nil {
		return fmt.Errorf("recover owner enrollment authentication state: %w", err)
	}
	if err := store.removeOwnerCommitJournal(); err != nil {
		return fmt.Errorf("finalize owner enrollment recovery: %w", err)
	}
	return nil
}

func authenticateEnrollmentSnapshot(state bootstrapStateFile, now time.Time, request CompleteInitialOwnerRequest, origin string) error {
	digest := credentialDigest(request.SessionToken)
	record, exists := state.EnrollmentSessions[digest]
	if !exists || !digestMatches(digest, request.SessionToken) {
		return ErrSessionUnauthorized
	}
	if !now.Before(record.ExpiresAt) {
		return ErrCredentialExpired
	}
	if record.Origin != origin {
		return ErrOriginMismatch
	}
	if record.TransactionID != request.TransactionID {
		return ErrTransactionMismatch
	}
	if !digestMatches(record.CSRFDigest, request.CSRFToken) {
		return ErrCSRFMismatch
	}
	return nil
}

func (store *Store) loadAuthenticationSnapshot() (authenticationSnapshot, error) {
	bootstrap, bootstrapExisted, err := store.loadBootstrapStateWithExistence()
	if err != nil {
		return authenticationSnapshot{}, err
	}
	local, localExisted, err := store.loadLocalStateWithExistence()
	if err != nil {
		return authenticationSnapshot{}, err
	}
	return authenticationSnapshot{
		Bootstrap: bootstrap, BootstrapExisted: bootstrapExisted,
		Local: local, LocalExisted: localExisted,
	}, nil
}

func (store *Store) writeAuthenticationSnapshot(snapshot authenticationSnapshot) error {
	if err := validateAuthenticationSnapshot(snapshot); err != nil {
		return err
	}
	entries := []struct {
		name    string
		existed bool
		write   func() error
	}{
		{bootstrapFileName, snapshot.BootstrapExisted, func() error { return store.writeBootstrapState(snapshot.Bootstrap) }},
		{localFileName, snapshot.LocalExisted, func() error { return store.writeLocalState(snapshot.Local) }},
	}
	for _, entry := range entries {
		path := filepath.Join(store.directory, entry.name)
		if entry.existed {
			if err := entry.write(); err != nil {
				return err
			}
		} else if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("remove absent authentication state %s: %w", entry.name, err)
		}
	}
	return syncDirectory(store.directory)
}

func validateAuthenticationSnapshot(snapshot authenticationSnapshot) error {
	if err := validateBootstrapState(snapshot.Bootstrap); err != nil {
		return fmt.Errorf("bootstrap snapshot: %w", err)
	}
	if err := validateLocalState(snapshot.Local); err != nil {
		return fmt.Errorf("local snapshot: %w", err)
	}
	if !snapshot.BootstrapExisted && !reflect.DeepEqual(snapshot.Bootstrap, newBootstrapState()) {
		return errors.New("absent bootstrap snapshot must contain pristine empty state")
	}
	if !snapshot.LocalExisted && !reflect.DeepEqual(snapshot.Local, newLocalState()) {
		return errors.New("absent local snapshot must contain pristine empty state")
	}
	return nil
}

func validateOwnerCommitJournal(journal ownerCommitJournal) error {
	if journal.APIVersion != ownerCommitAPIVersion {
		return fmt.Errorf("owner commit api_version must be %q", ownerCommitAPIVersion)
	}
	if journal.Phase != ownerCommitPrepared && journal.Phase != ownerCommitAuthReady {
		return errors.New("owner commit phase is invalid")
	}
	if err := validateTransactionID(journal.TransactionID); err != nil {
		return err
	}
	if err := validateAuthenticationSnapshot(journal.Previous); err != nil {
		return fmt.Errorf("previous authentication snapshot: %w", err)
	}
	if err := validateAuthenticationSnapshot(journal.Next); err != nil {
		return fmt.Errorf("next authentication snapshot: %w", err)
	}
	if !journal.Next.BootstrapExisted || !journal.Next.LocalExisted {
		return errors.New("next owner enrollment snapshot must persist bootstrap and local state files")
	}
	if journal.Previous.Local.OwnerPasswordPHC != "" || len(journal.Previous.Local.Sessions) != 0 {
		return errors.New("previous owner enrollment snapshot must not contain local owner credentials")
	}
	matchedTransaction := false
	for _, session := range journal.Previous.Bootstrap.EnrollmentSessions {
		if session.TransactionID == journal.TransactionID {
			matchedTransaction = true
		}
	}
	if !matchedTransaction {
		return errors.New("previous owner enrollment snapshot has no matching enrollment session")
	}
	if journal.Next.Local.OwnerPasswordPHC == "" || journal.Next.Bootstrap.Token != nil || len(journal.Next.Bootstrap.Sessions) != 0 ||
		journal.Next.Bootstrap.Handoff != nil || len(journal.Next.Bootstrap.EnrollmentSessions) != 0 || len(journal.Next.Local.Sessions) != 0 {
		return errors.New("next owner enrollment snapshot does not revoke all prior credentials")
	}
	return nil
}

func (store *Store) writeOwnerCommitJournal(journal ownerCommitJournal) error {
	if err := validateOwnerCommitJournal(journal); err != nil {
		return err
	}
	return writeJSONFile(filepath.Join(store.directory, ownerCommitFileName), journal)
}

func (store *Store) loadOwnerCommitJournal() (ownerCommitJournal, bool, error) {
	var journal ownerCommitJournal
	found, err := readJSONFile(filepath.Join(store.directory, ownerCommitFileName), &journal)
	if err != nil || !found {
		return ownerCommitJournal{}, found, err
	}
	if err := validateOwnerCommitJournal(journal); err != nil {
		return ownerCommitJournal{}, true, fmt.Errorf("validate owner enrollment transaction: %w", err)
	}
	return journal, true, nil
}

func (store *Store) removeOwnerCommitJournal() error {
	return removePrivateFile(filepath.Join(store.directory, ownerCommitFileName))
}

func (store *Store) rollbackOwnerCommit(ctx context.Context, journal ownerCommitJournal, operationErr error) error {
	if err := store.recordAudit(ctx, AuditEvent{
		Action: AuditOwnerEnroll, Outcome: AuditFailure, Reason: "transaction_rolled_back",
		TransactionID: journal.TransactionID, State: StateEnrollment,
	}); err != nil {
		// Keep the candidate quarantined behind the WAL until startup can
		// perform and audit a deterministic recovery.
		return errors.Join(operationErr, err, ErrRecoveryRequired)
	}
	rollbackErr := store.writeAuthenticationSnapshot(journal.Previous)
	if rollbackErr == nil {
		rollbackErr = store.removeOwnerCommitJournal()
	}
	if rollbackErr != nil {
		return errors.Join(operationErr, rollbackErr, ErrRecoveryRequired)
	}
	return operationErr
}

func (store *Store) rollbackOwnerCommitWithFreshContext(ctx context.Context, journal ownerCommitJournal, operationErr error) error {
	rollbackCtx, cancelRollback := store.transactionConvergenceContext(ctx)
	defer cancelRollback()
	return store.rollbackOwnerCommit(rollbackCtx, journal, operationErr)
}

// See resolveEnrollmentTransitionError: a callback error can arrive after the
// capability-state rename. Full is therefore a committed aggregate outcome;
// enrollment is the only state in which restoring the prior authentication
// snapshot is safe. Any other or unreadable state retains the WAL for startup
// recovery and blocks normal authentication operations.
func (store *Store) resolveOwnerTransitionError(ctx context.Context, journal ownerCommitJournal, operationErr error) error {
	if store.currentState == nil {
		return errors.Join(operationErr, ErrRecoveryRequired)
	}
	state, err := store.currentState(ctx)
	if err != nil {
		return errors.Join(operationErr, ErrRecoveryRequired, fmt.Errorf("read capability state after owner enrollment transition error: %w", err))
	}
	switch state {
	case StateEnrollment:
		return store.rollbackOwnerCommitWithFreshContext(ctx, journal, operationErr)
	case StateFull:
		if err := store.removeOwnerCommitJournal(); err != nil {
			_ = store.recordAudit(ctx, AuditEvent{
				Action: AuditOwnerRecover, Outcome: AuditFailure, Reason: "cleanup_deferred",
				TransactionID: journal.TransactionID, State: StateFull,
			})
		}
		return nil
	default:
		return errors.Join(operationErr, ErrRecoveryRequired, fmt.Errorf("invalid capability state after owner enrollment transition error: %q", state))
	}
}

func (store *Store) ownerCommitStep(step string) error {
	if store.beforeOwnerCommitStep == nil {
		return nil
	}
	return store.beforeOwnerCommitStep(step)
}
