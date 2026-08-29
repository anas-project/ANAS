package consoleauth

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
)

const (
	enrollmentCommitAPIVersion = "anas.console-auth-bootstrap-enrollment-commit/v1"
	enrollmentCommitPrepared   = "prepared"
	enrollmentCommitAuthReady  = "auth_committed"
)

type enrollmentCommitJournal struct {
	APIVersion     string                 `json:"api_version"`
	Phase          string                 `json:"phase"`
	TransactionID  string                 `json:"transaction_id"`
	RecoveryRoutes []string               `json:"recovery_routes"`
	Previous       authenticationSnapshot `json:"previous"`
	Next           authenticationSnapshot `json:"next"`
}

// AdvanceToEnrollment durably prepares the authentication half of the
// bootstrap -> enrollment transition, then invokes transition as the final
// publish step. An active bootstrap token or session for transactionID keeps
// its identity and expiry while being narrowed to the enrollment recovery
// allowlist. An empty credential store is also valid so a CLI can issue a new
// enrollment-scoped token after the state transition.
// After the WAL is durably prepared, convergence runs on an independently
// bounded context so cancellation of the initiating request cannot strand it.
//
// The transition callback must durably commit bootstrap -> enrollment and
// audit that state change before publishing it. Callers must not use a no-op
// callback in production.
func (store *Store) AdvanceToEnrollment(ctx context.Context, transactionID string, recoveryRoutes []string, transition func(context.Context) error) error {
	if transition == nil {
		return errors.New("bootstrap enrollment state transition is required")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if validateTransactionID(transactionID) != nil {
		return store.failWithAudit(ctx, AuditEvent{Action: AuditBootstrapPromote, Reason: "invalid_transaction"}, ErrTransactionMismatch)
	}
	routes, err := normalizeAllowedRoutes(recoveryRoutes)
	if err != nil {
		return store.failWithAudit(ctx, AuditEvent{
			Action: AuditBootstrapPromote, Reason: "invalid_routes", TransactionID: transactionID,
		}, err)
	}

	unlock, err := store.lock(ctx)
	if err != nil {
		return store.failWithAudit(ctx, AuditEvent{
			Action: AuditBootstrapPromote, Reason: "store_unavailable", TransactionID: transactionID,
		}, err)
	}
	defer unlock()

	previous, err := store.loadAuthenticationSnapshot()
	if err != nil {
		return store.failWithAudit(ctx, AuditEvent{
			Action: AuditBootstrapPromote, Reason: "state_unavailable", TransactionID: transactionID,
		}, err)
	}
	next, err := enrollmentAuthenticationCandidate(previous, transactionID, routes)
	if err != nil {
		reason := "candidate_invalid"
		if errors.Is(err, ErrTransactionMismatch) {
			reason = "transaction_mismatch"
		} else if errors.Is(err, ErrStateMismatch) {
			reason = "state_mismatch"
		}
		return store.failWithAudit(ctx, AuditEvent{
			Action: AuditBootstrapPromote, Reason: reason, TransactionID: transactionID,
		}, err)
	}
	journal := enrollmentCommitJournal{
		APIVersion: enrollmentCommitAPIVersion, Phase: enrollmentCommitPrepared,
		TransactionID: transactionID, RecoveryRoutes: cloneRoutes(routes),
		Previous: previous, Next: next,
	}
	if err := validateEnrollmentCommitJournal(journal); err != nil {
		return store.failWithAudit(ctx, AuditEvent{
			Action: AuditBootstrapPromote, Reason: "candidate_invalid", TransactionID: transactionID,
		}, err)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := store.recordAudit(ctx, AuditEvent{
		Action: AuditBootstrapPromote, Outcome: AuditSuccess,
		TransactionID: transactionID, State: StateEnrollment,
	}); err != nil {
		return err
	}
	if err := store.writeEnrollmentCommitJournal(journal); err != nil {
		return store.failWithAudit(ctx, AuditEvent{
			Action: AuditBootstrapPromote, Reason: "journal_failed", TransactionID: transactionID,
		}, err)
	}
	convergenceCtx, cancelConvergence := store.transactionConvergenceContext(ctx)
	defer cancelConvergence()
	if err := store.enrollmentCommitStep("journal_prepared"); err != nil {
		return store.rollbackEnrollmentCommitWithFreshContext(convergenceCtx, journal, err)
	}
	if err := store.writeAuthenticationSnapshot(next); err != nil {
		return store.rollbackEnrollmentCommitWithFreshContext(convergenceCtx, journal, err)
	}
	if err := store.enrollmentCommitStep("authentication_written"); err != nil {
		return store.rollbackEnrollmentCommitWithFreshContext(convergenceCtx, journal, err)
	}
	journal.Phase = enrollmentCommitAuthReady
	if err := store.writeEnrollmentCommitJournal(journal); err != nil {
		return store.rollbackEnrollmentCommitWithFreshContext(convergenceCtx, journal, err)
	}
	if err := store.enrollmentCommitStep("before_state_transition"); err != nil {
		return store.rollbackEnrollmentCommitWithFreshContext(convergenceCtx, journal, err)
	}
	if err := transition(convergenceCtx); err != nil {
		resolutionCtx, cancelResolution := store.transactionConvergenceContext(convergenceCtx)
		defer cancelResolution()
		return store.resolveEnrollmentTransitionError(resolutionCtx, journal, err)
	}
	if err := store.enrollmentCommitStep("before_journal_cleanup"); err != nil {
		store.auditDeferredEnrollmentCleanup(convergenceCtx, journal)
		return nil
	}
	if err := store.removeEnrollmentCommitJournal(); err != nil {
		// Enrollment is already published and the authentication candidate is
		// safe. Recovery will roll it forward and remove the WAL on startup.
		store.auditDeferredEnrollmentCleanup(convergenceCtx, journal)
	}
	return nil
}

// RecoverAdvanceToEnrollment resolves the durable bootstrap -> enrollment WAL
// before listeners open. Bootstrap means the publish did not happen and the
// previous authentication snapshot is restored. Enrollment or full means the
// publish happened and recovery rolls the candidate forward.
func (store *Store) RecoverAdvanceToEnrollment(ctx context.Context, currentState func(context.Context) (ConsoleState, error)) error {
	if currentState == nil {
		return errors.New("bootstrap enrollment recovery requires a state provider")
	}
	if err := validateStoreDirectory(store.directory); err != nil {
		return err
	}
	unlock, err := acquireStoreLock(ctx, filepath.Join(store.directory, lockFileName))
	if err != nil {
		return err
	}
	defer unlock()

	journal, found, err := store.loadEnrollmentCommitJournal()
	if err != nil || !found {
		return err
	}
	if _, err := os.Lstat(filepath.Join(store.directory, ownerCommitFileName)); err == nil {
		return errors.New("multiple authentication recovery journals are present")
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("inspect owner enrollment transaction during recovery: %w", err)
	}

	state, err := currentState(ctx)
	if err != nil {
		return fmt.Errorf("read capability state during bootstrap enrollment recovery: %w", err)
	}
	var snapshot authenticationSnapshot
	var reason string
	switch state {
	case StateBootstrap:
		snapshot = journal.Previous
		reason = "roll_back_bootstrap"
	case StateEnrollment, StateFull:
		snapshot = journal.Next
		reason = "roll_forward_published"
	default:
		return fmt.Errorf("recover bootstrap enrollment: invalid capability state %q", state)
	}
	if err := store.recordAudit(ctx, AuditEvent{
		Action: AuditBootstrapRecover, Outcome: AuditSuccess, Reason: reason,
		TransactionID: journal.TransactionID, State: state,
	}); err != nil {
		return err
	}
	if err := store.writeAuthenticationSnapshot(snapshot); err != nil {
		return fmt.Errorf("recover bootstrap enrollment authentication state: %w", err)
	}
	if err := store.removeEnrollmentCommitJournal(); err != nil {
		return fmt.Errorf("finalize bootstrap enrollment recovery: %w", err)
	}
	return nil
}

func enrollmentAuthenticationCandidate(previous authenticationSnapshot, transactionID string, routes []string) (authenticationSnapshot, error) {
	if previous.Local.OwnerPasswordPHC != "" || len(previous.Local.Sessions) != 0 {
		return authenticationSnapshot{}, ErrStateMismatch
	}
	next := cloneAuthenticationSnapshot(previous)
	if next.Bootstrap.Token != nil {
		if next.Bootstrap.Token.TransactionID != transactionID {
			return authenticationSnapshot{}, ErrTransactionMismatch
		}
		if next.Bootstrap.Token.State != StateBootstrap {
			return authenticationSnapshot{}, ErrStateMismatch
		}
		next.Bootstrap.Token.State = StateEnrollment
		next.Bootstrap.Token.AllowedRoutes = cloneRoutes(routes)
	}
	for digest, session := range next.Bootstrap.Sessions {
		if session.TransactionID != transactionID {
			return authenticationSnapshot{}, ErrTransactionMismatch
		}
		if session.State != StateBootstrap {
			return authenticationSnapshot{}, ErrStateMismatch
		}
		session.State = StateEnrollment
		session.AllowedRoutes = cloneRoutes(routes)
		next.Bootstrap.Sessions[digest] = session
	}
	return next, nil
}

func cloneAuthenticationSnapshot(snapshot authenticationSnapshot) authenticationSnapshot {
	cloned := snapshot
	cloned.Bootstrap = cloneBootstrapState(snapshot.Bootstrap)
	cloned.Local = cloneLocalState(snapshot.Local)
	return cloned
}

func cloneBootstrapState(state bootstrapStateFile) bootstrapStateFile {
	cloned := state
	if state.Token != nil {
		token := *state.Token
		token.AllowedRoutes = cloneRoutes(state.Token.AllowedRoutes)
		cloned.Token = &token
	}
	cloned.Sessions = make(map[string]bootstrapSessionRecord, len(state.Sessions))
	for digest, session := range state.Sessions {
		session.AllowedRoutes = cloneRoutes(session.AllowedRoutes)
		cloned.Sessions[digest] = session
	}
	if state.Handoff != nil {
		handoff := *state.Handoff
		cloned.Handoff = &handoff
	}
	cloned.EnrollmentSessions = make(map[string]enrollmentSessionRecord, len(state.EnrollmentSessions))
	for digest, session := range state.EnrollmentSessions {
		cloned.EnrollmentSessions[digest] = session
	}
	return cloned
}

func cloneLocalState(state localStateFile) localStateFile {
	cloned := state
	cloned.Sessions = make(map[string]localSessionRecord, len(state.Sessions))
	for digest, session := range state.Sessions {
		cloned.Sessions[digest] = session
	}
	return cloned
}

func validateEnrollmentCommitJournal(journal enrollmentCommitJournal) error {
	if journal.APIVersion != enrollmentCommitAPIVersion {
		return fmt.Errorf("bootstrap enrollment commit api_version must be %q", enrollmentCommitAPIVersion)
	}
	if journal.Phase != enrollmentCommitPrepared && journal.Phase != enrollmentCommitAuthReady {
		return errors.New("bootstrap enrollment commit phase is invalid")
	}
	if err := validateTransactionID(journal.TransactionID); err != nil {
		return err
	}
	routes, err := normalizeAllowedRoutes(journal.RecoveryRoutes)
	if err != nil {
		return fmt.Errorf("recovery routes: %w", err)
	}
	if err := validateAuthenticationSnapshot(journal.Previous); err != nil {
		return fmt.Errorf("previous authentication snapshot: %w", err)
	}
	if err := validateAuthenticationSnapshot(journal.Next); err != nil {
		return fmt.Errorf("next authentication snapshot: %w", err)
	}
	expected, err := enrollmentAuthenticationCandidate(journal.Previous, journal.TransactionID, routes)
	if err != nil {
		return fmt.Errorf("derive enrollment authentication candidate: %w", err)
	}
	if !reflect.DeepEqual(journal.Next, expected) {
		return errors.New("next authentication snapshot is not the expected enrollment candidate")
	}
	return nil
}

func (store *Store) writeEnrollmentCommitJournal(journal enrollmentCommitJournal) error {
	if err := validateEnrollmentCommitJournal(journal); err != nil {
		return err
	}
	return writeJSONFile(filepath.Join(store.directory, enrollmentCommitFileName), journal)
}

func (store *Store) loadEnrollmentCommitJournal() (enrollmentCommitJournal, bool, error) {
	var journal enrollmentCommitJournal
	found, err := readJSONFile(filepath.Join(store.directory, enrollmentCommitFileName), &journal)
	if err != nil || !found {
		return enrollmentCommitJournal{}, found, err
	}
	if err := validateEnrollmentCommitJournal(journal); err != nil {
		return enrollmentCommitJournal{}, true, fmt.Errorf("validate bootstrap enrollment transaction: %w", err)
	}
	return journal, true, nil
}

func (store *Store) removeEnrollmentCommitJournal() error {
	return removePrivateFile(filepath.Join(store.directory, enrollmentCommitFileName))
}

func (store *Store) rollbackEnrollmentCommit(ctx context.Context, journal enrollmentCommitJournal, operationErr error) error {
	if err := store.recordAudit(ctx, AuditEvent{
		Action: AuditBootstrapPromote, Outcome: AuditFailure, Reason: "transaction_rolled_back",
		TransactionID: journal.TransactionID, State: StateEnrollment,
	}); err != nil {
		// The candidate remains unreachable behind the WAL. Startup recovery
		// will retry an audited rollback before normal operations are allowed.
		return errors.Join(operationErr, err, ErrRecoveryRequired)
	}
	rollbackErr := store.writeAuthenticationSnapshot(journal.Previous)
	if rollbackErr == nil {
		rollbackErr = store.removeEnrollmentCommitJournal()
	}
	if rollbackErr != nil {
		return errors.Join(operationErr, rollbackErr, ErrRecoveryRequired)
	}
	return operationErr
}

func (store *Store) rollbackEnrollmentCommitWithFreshContext(ctx context.Context, journal enrollmentCommitJournal, operationErr error) error {
	rollbackCtx, cancelRollback := store.transactionConvergenceContext(ctx)
	defer cancelRollback()
	return store.rollbackEnrollmentCommit(rollbackCtx, journal, operationErr)
}

// A transition error is ambiguous because an atomic rename can publish the
// capability state before a later directory sync or validation reports an
// error. Only the durable capability state can decide whether authentication
// must roll back or remain rolled forward. Without a trustworthy reader the
// WAL is deliberately retained and all normal Store operations fail closed.
func (store *Store) resolveEnrollmentTransitionError(ctx context.Context, journal enrollmentCommitJournal, operationErr error) error {
	if store.currentState == nil {
		return errors.Join(operationErr, ErrRecoveryRequired)
	}
	state, err := store.currentState(ctx)
	if err != nil {
		return errors.Join(operationErr, ErrRecoveryRequired, fmt.Errorf("read capability state after bootstrap enrollment transition error: %w", err))
	}
	switch state {
	case StateBootstrap:
		return store.rollbackEnrollmentCommitWithFreshContext(ctx, journal, operationErr)
	case StateEnrollment, StateFull:
		if err := store.removeEnrollmentCommitJournal(); err != nil {
			store.auditDeferredEnrollmentCleanup(ctx, journal)
		}
		return nil
	default:
		return errors.Join(operationErr, ErrRecoveryRequired, fmt.Errorf("invalid capability state after bootstrap enrollment transition error: %q", state))
	}
}

func (store *Store) enrollmentCommitStep(step string) error {
	if store.beforeEnrollmentCommitStep == nil {
		return nil
	}
	return store.beforeEnrollmentCommitStep(step)
}

func (store *Store) auditDeferredEnrollmentCleanup(ctx context.Context, journal enrollmentCommitJournal) {
	_ = store.recordAudit(ctx, AuditEvent{
		Action: AuditBootstrapRecover, Outcome: AuditFailure, Reason: "cleanup_deferred",
		TransactionID: journal.TransactionID, State: StateEnrollment,
	})
}
