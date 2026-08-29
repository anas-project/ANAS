package consoleauth

import (
	"context"
	"fmt"
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
		}
	}
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
	if err := store.recordAudit(ctx, AuditEvent{Action: AuditLocalRevoke, Outcome: AuditSuccess, RevokedSessions: revoked}); err != nil {
		return err
	}
	if err := store.writeLocalState(state); err != nil {
		return store.failWithAudit(ctx, AuditEvent{Action: AuditLocalRevoke, Reason: "persist_failed"}, err)
	}
	return nil
}
