package consoleauth

import (
	"context"
	"fmt"
	"time"
)

// SetOwnerPassword replaces the independent local-owner Argon2id record and
// invalidates every existing local session. The password and resulting PHC are
// never included in audit events.
func (store *Store) SetOwnerPassword(ctx context.Context, password string) error {
	if err := validatePasswordInput(password); err != nil {
		return store.failWithAudit(ctx, AuditEvent{Action: AuditOwnerPasswordSet, Reason: "invalid_password"}, err)
	}
	passwordPHC, err := store.hashOwnerPassword(password)
	if err != nil {
		return store.failWithAudit(ctx, AuditEvent{Action: AuditOwnerPasswordSet, Reason: "hash_failed"}, err)
	}
	unlock, err := store.lock(ctx)
	if err != nil {
		return store.failWithAudit(ctx, AuditEvent{Action: AuditOwnerPasswordSet, Reason: "store_unavailable"}, err)
	}
	defer unlock()
	state, err := store.loadLocalState()
	if err != nil {
		return store.failWithAudit(ctx, AuditEvent{Action: AuditOwnerPasswordSet, Reason: "state_unavailable"}, err)
	}
	revokedSessions := len(state.Sessions)
	state.OwnerPasswordPHC = passwordPHC
	state.Sessions = map[string]localSessionRecord{}
	state.StepUps = map[string]localStepUpRecord{}
	event := AuditEvent{
		Action: AuditOwnerPasswordSet, Outcome: AuditSuccess, RevokedSessions: revokedSessions,
	}
	if err := store.recordAudit(ctx, event); err != nil {
		return err
	}
	if err := store.writeLocalState(state); err != nil {
		return store.failWithAudit(ctx, AuditEvent{Action: AuditOwnerPasswordSet, Reason: "persist_failed"}, err)
	}
	return nil
}

func (store *Store) LoginLocal(ctx context.Context, request LocalLoginRequest) (LocalSessionCredential, error) {
	origin, err := NormalizeOrigin(request.Origin)
	if err != nil {
		return LocalSessionCredential{}, store.failWithAudit(ctx, AuditEvent{Action: AuditLocalLogin, Reason: "invalid_origin"}, ErrOriginMismatch)
	}
	if len(request.Password) > maximumPasswordBytes {
		return LocalSessionCredential{}, store.failWithAudit(ctx, AuditEvent{Action: AuditLocalLogin, Reason: "invalid_credentials", Origin: origin}, ErrInvalidCredentials)
	}
	unlock, err := store.lock(ctx)
	if err != nil {
		return LocalSessionCredential{}, store.failWithAudit(ctx, AuditEvent{Action: AuditLocalLogin, Reason: "store_unavailable", Origin: origin}, err)
	}
	defer unlock()
	state, err := store.loadLocalState()
	if err != nil {
		return LocalSessionCredential{}, store.failWithAudit(ctx, AuditEvent{Action: AuditLocalLogin, Reason: "state_unavailable", Origin: origin}, err)
	}
	if state.OwnerPasswordPHC == "" {
		consumeDummyPasswordWork(request.Password)
		return LocalSessionCredential{}, store.failWithAudit(ctx, AuditEvent{Action: AuditLocalLogin, Reason: "owner_not_configured", Origin: origin}, ErrOwnerNotConfigured)
	}
	verified, err := VerifyPassword(state.OwnerPasswordPHC, request.Password)
	if err != nil {
		return LocalSessionCredential{}, store.failWithAudit(ctx, AuditEvent{Action: AuditLocalLogin, Reason: "state_unavailable", Origin: origin}, err)
	}
	if !verified {
		return LocalSessionCredential{}, store.failWithAudit(ctx, AuditEvent{Action: AuditLocalLogin, Reason: "invalid_credentials", Origin: origin}, ErrInvalidCredentials)
	}
	sessionToken, err := store.newCredential()
	if err != nil {
		return LocalSessionCredential{}, store.failWithAudit(ctx, AuditEvent{Action: AuditLocalLogin, Reason: "random_failed", Origin: origin}, err)
	}
	csrfToken, err := store.newCredential()
	if err != nil {
		return LocalSessionCredential{}, store.failWithAudit(ctx, AuditEvent{Action: AuditLocalLogin, Reason: "random_failed", Origin: origin}, err)
	}
	now := store.currentTime()
	for digest, session := range state.Sessions {
		if !now.Before(session.ExpiresAt) || !now.Before(session.IdleExpiresAt) {
			delete(state.Sessions, digest)
			deleteLocalSessionStepUps(&state, digest)
		}
	}
	pruneExpiredLocalStepUps(&state, now)
	if len(state.Sessions) >= 1024 {
		return LocalSessionCredential{}, store.failWithAudit(ctx, AuditEvent{Action: AuditLocalLogin, Reason: "session_capacity", Origin: origin}, ErrSessionUnauthorized)
	}
	record := localSessionRecord{
		CSRFDigest:    credentialDigest(csrfToken),
		Origin:        origin,
		CreatedAt:     now,
		ExpiresAt:     now.Add(LocalSessionAbsoluteTTL),
		IdleExpiresAt: now.Add(LocalSessionIdleTTL),
	}
	state.Sessions[credentialDigest(sessionToken)] = record
	if err := store.recordAudit(ctx, AuditEvent{Action: AuditLocalLogin, Outcome: AuditSuccess, Origin: origin}); err != nil {
		return LocalSessionCredential{}, err
	}
	if err := store.writeLocalState(state); err != nil {
		return LocalSessionCredential{}, store.failWithAudit(ctx, AuditEvent{Action: AuditLocalLogin, Reason: "persist_failed", Origin: origin}, err)
	}
	return LocalSessionCredential{
		Token: sessionToken, CSRFToken: csrfToken, Origin: origin,
		CreatedAt: now, ExpiresAt: record.ExpiresAt, IdleExpiresAt: record.IdleExpiresAt,
	}, nil
}

func (store *Store) AuthenticateLocal(ctx context.Context, request LocalAuthenticationRequest) (LocalPrincipal, error) {
	origin, err := NormalizeOrigin(request.Origin)
	if err != nil {
		return LocalPrincipal{}, ErrOriginMismatch
	}
	unlock, err := store.lock(ctx)
	if err != nil {
		return LocalPrincipal{}, err
	}
	defer unlock()
	state, err := store.loadLocalState()
	if err != nil {
		return LocalPrincipal{}, err
	}
	digest := credentialDigest(request.SessionToken)
	record, exists := state.Sessions[digest]
	if !exists || !digestMatches(digest, request.SessionToken) {
		return LocalPrincipal{}, ErrSessionUnauthorized
	}
	now := store.currentTime()
	if !now.Before(record.ExpiresAt) || !now.Before(record.IdleExpiresAt) {
		return LocalPrincipal{}, ErrCredentialExpired
	}
	if record.Origin != origin {
		return LocalPrincipal{}, ErrOriginMismatch
	}
	if request.RequireCSRF && !digestMatches(record.CSRFDigest, request.CSRFToken) {
		return LocalPrincipal{}, ErrCSRFMismatch
	}
	if !request.ObserveOnly {
		newIdleExpiry := nextIdleExpiry(now, record.ExpiresAt, LocalSessionIdleTTL)
		if newIdleExpiry.After(record.IdleExpiresAt) {
			record.IdleExpiresAt = newIdleExpiry
			state.Sessions[digest] = record
			if err := store.writeLocalState(state); err != nil {
				return LocalPrincipal{}, fmt.Errorf("persist local session activity: %w", err)
			}
		}
	}
	return LocalPrincipal{
		Origin: record.Origin, CreatedAt: record.CreatedAt,
		ExpiresAt: record.ExpiresAt, IdleExpiresAt: record.IdleExpiresAt,
	}, nil
}

// RefreshLocalSession atomically rotates the SPA-visible CSRF credential while
// preserving the HttpOnly session token and absolute lifetime.
func (store *Store) RefreshLocalSession(ctx context.Context, request LocalSessionRefreshRequest) (LocalSessionCredential, error) {
	origin, err := NormalizeOrigin(request.Origin)
	if err != nil {
		return LocalSessionCredential{}, ErrOriginMismatch
	}
	if request.SessionToken == "" {
		return LocalSessionCredential{}, ErrSessionUnauthorized
	}
	csrfToken, err := store.newCredential()
	if err != nil {
		return LocalSessionCredential{}, err
	}
	unlock, err := store.lock(ctx)
	if err != nil {
		return LocalSessionCredential{}, err
	}
	defer unlock()
	state, err := store.loadLocalState()
	if err != nil {
		return LocalSessionCredential{}, err
	}
	digest := credentialDigest(request.SessionToken)
	record, exists := state.Sessions[digest]
	if !exists || !digestMatches(digest, request.SessionToken) {
		return LocalSessionCredential{}, ErrSessionUnauthorized
	}
	now := store.currentTime()
	if !now.Before(record.ExpiresAt) || !now.Before(record.IdleExpiresAt) {
		return LocalSessionCredential{}, ErrCredentialExpired
	}
	if record.Origin != origin {
		return LocalSessionCredential{}, ErrOriginMismatch
	}
	record.CSRFDigest = credentialDigest(csrfToken)
	record.IdleExpiresAt = nextIdleExpiry(now, record.ExpiresAt, LocalSessionIdleTTL)
	state.Sessions[digest] = record
	if err := store.writeLocalState(state); err != nil {
		return LocalSessionCredential{}, fmt.Errorf("persist local session refresh: %w", err)
	}
	return LocalSessionCredential{
		CSRFToken: csrfToken, Origin: record.Origin, CreatedAt: record.CreatedAt,
		ExpiresAt: record.ExpiresAt, IdleExpiresAt: record.IdleExpiresAt,
	}, nil
}

func (store *Store) LogoutLocal(ctx context.Context, request LocalLogoutRequest) error {
	origin, err := NormalizeOrigin(request.Origin)
	if err != nil {
		return store.failWithAudit(ctx, AuditEvent{Action: AuditLocalLogout, Reason: "invalid_origin"}, ErrOriginMismatch)
	}
	unlock, err := store.lock(ctx)
	if err != nil {
		return store.failWithAudit(ctx, AuditEvent{Action: AuditLocalLogout, Reason: "store_unavailable", Origin: origin}, err)
	}
	defer unlock()
	state, err := store.loadLocalState()
	if err != nil {
		return store.failWithAudit(ctx, AuditEvent{Action: AuditLocalLogout, Reason: "state_unavailable", Origin: origin}, err)
	}
	digest := credentialDigest(request.SessionToken)
	record, exists := state.Sessions[digest]
	if !exists || record.Origin != origin {
		return store.failWithAudit(ctx, AuditEvent{Action: AuditLocalLogout, Reason: "session_not_found", Origin: origin}, ErrSessionUnauthorized)
	}
	if !digestMatches(record.CSRFDigest, request.CSRFToken) {
		return store.failWithAudit(ctx, AuditEvent{Action: AuditLocalLogout, Reason: "csrf_mismatch", Origin: origin}, ErrCSRFMismatch)
	}
	delete(state.Sessions, digest)
	deleteLocalSessionStepUps(&state, digest)
	if err := store.recordAudit(ctx, AuditEvent{Action: AuditLocalLogout, Outcome: AuditSuccess, Origin: origin, RevokedSessions: 1}); err != nil {
		return err
	}
	if err := store.writeLocalState(state); err != nil {
		return store.failWithAudit(ctx, AuditEvent{Action: AuditLocalLogout, Reason: "persist_failed", Origin: origin}, err)
	}
	return nil
}

// RevokeLocalSessions invalidates every local session after a password,
// permission-source, or administrative revocation event.
func (store *Store) RevokeLocalSessions(ctx context.Context) error {
	unlock, err := store.lock(ctx)
	if err != nil {
		return store.failWithAudit(ctx, AuditEvent{Action: AuditLocalRevoke, Reason: "store_unavailable"}, err)
	}
	defer unlock()
	state, err := store.loadLocalState()
	if err != nil {
		return store.failWithAudit(ctx, AuditEvent{Action: AuditLocalRevoke, Reason: "state_unavailable"}, err)
	}
	revoked := len(state.Sessions)
	state.Sessions = map[string]localSessionRecord{}
	state.StepUps = map[string]localStepUpRecord{}
	if err := store.recordAudit(ctx, AuditEvent{Action: AuditLocalRevoke, Outcome: AuditSuccess, RevokedSessions: revoked}); err != nil {
		return err
	}
	if err := store.writeLocalState(state); err != nil {
		return store.failWithAudit(ctx, AuditEvent{Action: AuditLocalRevoke, Reason: "persist_failed"}, err)
	}
	return nil
}

// IssueLocalStepUp re-verifies the current owner password and persists only a
// digest of the returned proof. The proof is bound to the exact local session,
// action, registered workspace, typed target, and server-computed state digest.
func (store *Store) IssueLocalStepUp(ctx context.Context, request LocalStepUpRequest) (LocalStepUpCredential, error) {
	origin, err := NormalizeOrigin(request.Origin)
	auditEvent := AuditEvent{
		Action: AuditLocalStepUp, Origin: origin, AuthorizedAction: request.Action,
		WorkspaceID: request.WorkspaceID, TargetID: request.TargetID,
	}
	if err != nil || len(request.Password) > maximumPasswordBytes ||
		validateLocalStepUpBinding(request.Action, request.WorkspaceID, request.TargetID, request.StateDigest) != nil {
		return LocalStepUpCredential{}, store.failWithAudit(ctx, withAuditReason(auditEvent, "invalid_request"), ErrStepUpUnauthorized)
	}
	unlock, err := store.lock(ctx)
	if err != nil {
		return LocalStepUpCredential{}, store.failWithAudit(ctx, withAuditReason(auditEvent, "store_unavailable"), err)
	}
	defer unlock()
	state, err := store.loadLocalState()
	if err != nil {
		return LocalStepUpCredential{}, store.failWithAudit(ctx, withAuditReason(auditEvent, "state_unavailable"), err)
	}
	sessionDigest := credentialDigest(request.SessionToken)
	session, exists := state.Sessions[sessionDigest]
	now := store.currentTime()
	switch {
	case !exists || !digestMatches(sessionDigest, request.SessionToken):
		return LocalStepUpCredential{}, store.failWithAudit(ctx, withAuditReason(auditEvent, "session_not_found"), ErrSessionUnauthorized)
	case !now.Before(session.ExpiresAt) || !now.Before(session.IdleExpiresAt):
		return LocalStepUpCredential{}, store.failWithAudit(ctx, withAuditReason(auditEvent, "session_expired"), ErrCredentialExpired)
	case session.Origin != origin:
		return LocalStepUpCredential{}, store.failWithAudit(ctx, withAuditReason(auditEvent, "origin_mismatch"), ErrOriginMismatch)
	case !digestMatches(session.CSRFDigest, request.CSRFToken):
		return LocalStepUpCredential{}, store.failWithAudit(ctx, withAuditReason(auditEvent, "csrf_mismatch"), ErrCSRFMismatch)
	}
	verified, err := VerifyPassword(state.OwnerPasswordPHC, request.Password)
	if err != nil {
		return LocalStepUpCredential{}, store.failWithAudit(ctx, withAuditReason(auditEvent, "state_unavailable"), err)
	}
	if !verified {
		return LocalStepUpCredential{}, store.failWithAudit(ctx, withAuditReason(auditEvent, "invalid_credentials"), ErrInvalidCredentials)
	}
	pruneExpiredLocalStepUps(&state, now)
	if len(state.StepUps) >= 1024 {
		return LocalStepUpCredential{}, store.failWithAudit(ctx, withAuditReason(auditEvent, "step_up_capacity"), ErrStepUpUnauthorized)
	}
	random, err := store.newCredential()
	if err != nil {
		return LocalStepUpCredential{}, store.failWithAudit(ctx, withAuditReason(auditEvent, "random_failed"), err)
	}
	token := "sup_" + random
	digest := credentialDigest(token)
	record := localStepUpRecord{
		SessionDigest: sessionDigest, Action: request.Action, WorkspaceID: request.WorkspaceID,
		TargetID: request.TargetID, StateDigest: request.StateDigest, CreatedAt: now, ExpiresAt: now.Add(LocalStepUpTTL),
	}
	state.StepUps[digest] = record
	if err := store.recordAudit(ctx, withAuditOutcome(auditEvent, AuditSuccess)); err != nil {
		return LocalStepUpCredential{}, err
	}
	if err := store.writeLocalState(state); err != nil {
		return LocalStepUpCredential{}, store.failWithAudit(ctx, withAuditReason(auditEvent, "persist_failed"), err)
	}
	return LocalStepUpCredential{
		Token: token, Digest: digest, SessionDigest: sessionDigest, Action: record.Action,
		WorkspaceID: record.WorkspaceID, TargetID: record.TargetID, StateDigest: record.StateDigest,
		CreatedAt: record.CreatedAt, ExpiresAt: record.ExpiresAt,
	}, nil
}

// AuthenticateLocalStepUp validates without consuming. Consumption is later
// recorded atomically with confirmation consumption and apply-job creation in
// the durable job store.
func (store *Store) AuthenticateLocalStepUp(ctx context.Context, request LocalStepUpAuthenticationRequest) (LocalStepUpBinding, error) {
	origin, err := NormalizeOrigin(request.Origin)
	if err != nil || validateLocalStepUpBinding(request.Action, request.WorkspaceID, request.TargetID, request.StateDigest) != nil {
		return LocalStepUpBinding{}, ErrStepUpUnauthorized
	}
	unlock, err := store.lock(ctx)
	if err != nil {
		return LocalStepUpBinding{}, err
	}
	defer unlock()
	state, err := store.loadLocalState()
	if err != nil {
		return LocalStepUpBinding{}, err
	}
	now := store.currentTime()
	sessionDigest := credentialDigest(request.SessionToken)
	session, exists := state.Sessions[sessionDigest]
	if !exists || !digestMatches(sessionDigest, request.SessionToken) || !now.Before(session.ExpiresAt) ||
		!now.Before(session.IdleExpiresAt) || session.Origin != origin {
		return LocalStepUpBinding{}, ErrStepUpUnauthorized
	}
	digest := credentialDigest(request.Token)
	record, exists := state.StepUps[digest]
	if !exists || !digestMatches(digest, request.Token) || !now.Before(record.ExpiresAt) ||
		record.SessionDigest != sessionDigest || record.Action != request.Action || record.WorkspaceID != request.WorkspaceID ||
		record.TargetID != request.TargetID || record.StateDigest != request.StateDigest {
		return LocalStepUpBinding{}, ErrStepUpUnauthorized
	}
	return LocalStepUpBinding{
		Digest: digest, SessionDigest: record.SessionDigest, Action: record.Action, WorkspaceID: record.WorkspaceID,
		TargetID: record.TargetID, StateDigest: record.StateDigest, CreatedAt: record.CreatedAt, ExpiresAt: record.ExpiresAt,
	}, nil
}

func pruneExpiredLocalStepUps(state *localStateFile, now time.Time) {
	for digest, record := range state.StepUps {
		if !now.Before(record.ExpiresAt) {
			delete(state.StepUps, digest)
		}
	}
}

func deleteLocalSessionStepUps(state *localStateFile, sessionDigest string) {
	for digest, record := range state.StepUps {
		if record.SessionDigest == sessionDigest {
			delete(state.StepUps, digest)
		}
	}
}

func withAuditReason(event AuditEvent, reason string) AuditEvent {
	event.Reason = reason
	return event
}

func withAuditOutcome(event AuditEvent, outcome AuditOutcome) AuditEvent {
	event.Outcome = outcome
	return event
}
