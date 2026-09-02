package consoleauth

import (
	"context"
	"errors"
	"fmt"
	"time"
)

func (store *Store) IssueBootstrapToken(ctx context.Context, request IssueBootstrapTokenRequest) (IssuedBootstrapToken, error) {
	ttl := request.TTL
	if ttl == 0 {
		ttl = DefaultBootstrapTokenTTL
	}
	routes, err := normalizeAllowedRoutes(request.AllowedRoutes)
	if err != nil || !bootstrapTokenTTLValid(ttl) || validateTransactionID(request.TransactionID) != nil || validateBootstrapStateValue(request.State) != nil {
		if err == nil {
			err = errors.New("bootstrap token request has an invalid TTL, transaction, or state")
		}
		return IssuedBootstrapToken{}, store.failWithAudit(ctx, AuditEvent{
			Action: AuditBootstrapIssue,
			Reason: "invalid_request",
		}, err)
	}
	token, err := store.newCredential()
	if err != nil {
		return IssuedBootstrapToken{}, store.failWithAudit(ctx, AuditEvent{Action: AuditBootstrapIssue, Reason: "random_failed"}, err)
	}

	unlock, err := store.lock(ctx)
	if err != nil {
		return IssuedBootstrapToken{}, store.failWithAudit(ctx, AuditEvent{Action: AuditBootstrapIssue, Reason: "store_unavailable"}, err)
	}
	defer unlock()
	state, err := store.loadBootstrapState()
	if err != nil {
		return IssuedBootstrapToken{}, store.failWithAudit(ctx, AuditEvent{Action: AuditBootstrapIssue, Reason: "state_unavailable"}, err)
	}
	now := store.currentTime()
	replacedToken := state.Token != nil
	revokedSessions := len(state.Sessions) + len(state.EnrollmentSessions)
	state.Token = &bootstrapTokenRecord{
		Digest:        credentialDigest(token),
		IssuedAt:      now,
		ExpiresAt:     now.Add(ttl),
		TransactionID: request.TransactionID,
		State:         request.State,
		AllowedRoutes: routes,
	}
	state.Sessions = map[string]bootstrapSessionRecord{}
	state.Handoff = nil
	state.EnrollmentSessions = map[string]enrollmentSessionRecord{}
	event := AuditEvent{
		Action:          AuditBootstrapIssue,
		Outcome:         AuditSuccess,
		TransactionID:   request.TransactionID,
		State:           request.State,
		ReplacedToken:   replacedToken,
		RevokedSessions: revokedSessions,
	}
	if err := store.recordAudit(ctx, event); err != nil {
		return IssuedBootstrapToken{}, err
	}
	if err := store.writeBootstrapState(state); err != nil {
		return IssuedBootstrapToken{}, store.failWithAudit(ctx, AuditEvent{
			Action: AuditBootstrapIssue, Reason: "persist_failed", TransactionID: request.TransactionID, State: request.State,
		}, err)
	}
	return IssuedBootstrapToken{
		Token: token, IssuedAt: now, ExpiresAt: now.Add(ttl),
		TransactionID: request.TransactionID, State: request.State,
	}, nil
}

func (store *Store) ExchangeBootstrapToken(ctx context.Context, request ExchangeBootstrapTokenRequest) (BootstrapSessionCredential, error) {
	origin, err := NormalizeOrigin(request.Origin)
	if err != nil {
		return BootstrapSessionCredential{}, store.failWithAudit(ctx, AuditEvent{
			Action: AuditBootstrapExchange, Reason: "invalid_request",
		}, ErrOriginMismatch)
	}
	if request.Token == "" {
		return BootstrapSessionCredential{}, store.failWithAudit(ctx, AuditEvent{
			Action: AuditBootstrapExchange, Reason: "invalid_request", Origin: origin,
		}, ErrInvalidToken)
	}
	unlock, err := store.lock(ctx)
	if err != nil {
		return BootstrapSessionCredential{}, store.failWithAudit(ctx, AuditEvent{Action: AuditBootstrapExchange, Reason: "store_unavailable"}, err)
	}
	defer unlock()
	state, err := store.loadBootstrapState()
	if err != nil {
		return BootstrapSessionCredential{}, store.failWithAudit(ctx, AuditEvent{Action: AuditBootstrapExchange, Reason: "state_unavailable"}, err)
	}
	if state.Token == nil || !digestMatches(state.Token.Digest, request.Token) {
		return BootstrapSessionCredential{}, store.failWithAudit(ctx, AuditEvent{
			Action: AuditBootstrapExchange, Reason: "invalid_token",
		}, ErrInvalidToken)
	}
	now := store.currentTime()
	if !now.Before(state.Token.ExpiresAt) {
		return BootstrapSessionCredential{}, store.failWithAudit(ctx, AuditEvent{
			Action: AuditBootstrapExchange, Reason: "expired_token", TransactionID: state.Token.TransactionID, State: state.Token.State,
		}, ErrCredentialExpired)
	}
	sessionToken, err := store.newCredential()
	if err != nil {
		return BootstrapSessionCredential{}, store.failWithAudit(ctx, AuditEvent{
			Action: AuditBootstrapExchange, Reason: "random_failed", TransactionID: state.Token.TransactionID, State: state.Token.State,
		}, err)
	}
	csrfToken, err := store.newCredential()
	if err != nil {
		return BootstrapSessionCredential{}, store.failWithAudit(ctx, AuditEvent{
			Action: AuditBootstrapExchange, Reason: "random_failed", TransactionID: state.Token.TransactionID, State: state.Token.State,
		}, err)
	}
	record := bootstrapSessionRecord{
		CSRFDigest:    credentialDigest(csrfToken),
		TransactionID: state.Token.TransactionID,
		Origin:        origin,
		State:         state.Token.State,
		AllowedRoutes: cloneRoutes(state.Token.AllowedRoutes),
		CreatedAt:     now,
		ExpiresAt:     now.Add(BootstrapSessionAbsoluteTTL),
		IdleExpiresAt: now.Add(BootstrapSessionIdleTTL),
	}
	state.Token = nil
	state.Sessions = map[string]bootstrapSessionRecord{credentialDigest(sessionToken): record}
	event := AuditEvent{
		Action: AuditBootstrapExchange, Outcome: AuditSuccess,
		TransactionID: record.TransactionID, Origin: origin, State: record.State,
	}
	if err := store.recordAudit(ctx, event); err != nil {
		return BootstrapSessionCredential{}, err
	}
	if err := store.writeBootstrapState(state); err != nil {
		return BootstrapSessionCredential{}, store.failWithAudit(ctx, AuditEvent{
			Action: AuditBootstrapExchange, Reason: "persist_failed",
			TransactionID: record.TransactionID, Origin: origin, State: record.State,
		}, err)
	}
	return BootstrapSessionCredential{
		Token: sessionToken, CSRFToken: csrfToken,
		TransactionID: record.TransactionID, Origin: origin, State: record.State,
		AllowedRoutes: cloneRoutes(record.AllowedRoutes), CreatedAt: now,
		ExpiresAt: record.ExpiresAt, IdleExpiresAt: record.IdleExpiresAt,
	}, nil
}

func (store *Store) AuthenticateBootstrap(ctx context.Context, request BootstrapAuthenticationRequest) (BootstrapPrincipal, error) {
	origin, err := NormalizeOrigin(request.Origin)
	if err != nil {
		return BootstrapPrincipal{}, store.failWithAudit(ctx, AuditEvent{Action: AuditBootstrapAuth, Reason: "invalid_origin"}, ErrOriginMismatch)
	}
	if err := validateTransactionID(request.TransactionID); err != nil {
		return BootstrapPrincipal{}, store.failWithAudit(ctx, AuditEvent{Action: AuditBootstrapAuth, Reason: "invalid_transaction", Origin: origin}, ErrTransactionMismatch)
	}
	if err := validateBootstrapStateValue(request.State); err != nil {
		return BootstrapPrincipal{}, store.failWithAudit(ctx, AuditEvent{Action: AuditBootstrapAuth, Reason: "invalid_state", TransactionID: request.TransactionID, Origin: origin}, ErrStateMismatch)
	}
	if request.Route == "" {
		return BootstrapPrincipal{}, store.failWithAudit(ctx, AuditEvent{Action: AuditBootstrapAuth, Reason: "route_not_allowed", TransactionID: request.TransactionID, Origin: origin, State: request.State}, ErrRouteNotAllowed)
	}
	unlock, err := store.lock(ctx)
	if err != nil {
		return BootstrapPrincipal{}, store.failWithAudit(ctx, AuditEvent{Action: AuditBootstrapAuth, Reason: "store_unavailable", TransactionID: request.TransactionID, Origin: origin, State: request.State}, err)
	}
	defer unlock()
	state, err := store.loadBootstrapState()
	if err != nil {
		return BootstrapPrincipal{}, store.failWithAudit(ctx, AuditEvent{Action: AuditBootstrapAuth, Reason: "state_unavailable", TransactionID: request.TransactionID, Origin: origin, State: request.State}, err)
	}
	digest := credentialDigest(request.SessionToken)
	record, exists := state.Sessions[digest]
	if !exists || !digestMatches(digest, request.SessionToken) {
		return BootstrapPrincipal{}, store.failWithAudit(ctx, AuditEvent{Action: AuditBootstrapAuth, Reason: "session_not_found", TransactionID: request.TransactionID, Origin: origin, State: request.State}, ErrSessionUnauthorized)
	}
	now := store.currentTime()
	if !now.Before(record.ExpiresAt) || !now.Before(record.IdleExpiresAt) {
		return BootstrapPrincipal{}, store.failWithAudit(ctx, AuditEvent{Action: AuditBootstrapAuth, Reason: "session_expired", TransactionID: record.TransactionID, Origin: origin, State: record.State}, ErrCredentialExpired)
	}
	if record.Origin != origin {
		return BootstrapPrincipal{}, store.failWithAudit(ctx, AuditEvent{Action: AuditBootstrapAuth, Reason: "origin_mismatch", TransactionID: record.TransactionID, Origin: origin, State: record.State}, ErrOriginMismatch)
	}
	if record.TransactionID != request.TransactionID {
		return BootstrapPrincipal{}, store.failWithAudit(ctx, AuditEvent{Action: AuditBootstrapAuth, Reason: "transaction_mismatch", TransactionID: request.TransactionID, Origin: origin, State: record.State}, ErrTransactionMismatch)
	}
	if record.State != request.State {
		return BootstrapPrincipal{}, store.failWithAudit(ctx, AuditEvent{Action: AuditBootstrapAuth, Reason: "state_mismatch", TransactionID: record.TransactionID, Origin: origin, State: request.State}, ErrStateMismatch)
	}
	if !routeAllowed(record.AllowedRoutes, request.Route) {
		return BootstrapPrincipal{}, store.failWithAudit(ctx, AuditEvent{Action: AuditBootstrapAuth, Reason: "route_not_allowed", TransactionID: record.TransactionID, Origin: origin, State: record.State}, ErrRouteNotAllowed)
	}
	if request.RequireCSRF && !digestMatches(record.CSRFDigest, request.CSRFToken) {
		return BootstrapPrincipal{}, store.failWithAudit(ctx, AuditEvent{Action: AuditBootstrapAuth, Reason: "csrf_mismatch", TransactionID: record.TransactionID, Origin: origin, State: record.State}, ErrCSRFMismatch)
	}
	if !request.ObserveOnly {
		newIdleExpiry := nextIdleExpiry(now, record.ExpiresAt, BootstrapSessionIdleTTL)
		if newIdleExpiry.After(record.IdleExpiresAt) {
			record.IdleExpiresAt = newIdleExpiry
			state.Sessions[digest] = record
			if err := store.writeBootstrapState(state); err != nil {
				operationErr := fmt.Errorf("persist bootstrap session activity: %w", err)
				return BootstrapPrincipal{}, store.failWithAudit(ctx, AuditEvent{Action: AuditBootstrapAuth, Reason: "persist_failed", TransactionID: record.TransactionID, Origin: origin, State: record.State}, operationErr)
			}
		}
	}
	return BootstrapPrincipal{
		TransactionID: record.TransactionID, Origin: record.Origin, State: record.State,
		AllowedRoutes: cloneRoutes(record.AllowedRoutes), CreatedAt: record.CreatedAt,
		ExpiresAt: record.ExpiresAt, IdleExpiresAt: record.IdleExpiresAt,
	}, nil
}

// RefreshBootstrapSession atomically rotates the CSRF credential while
// preserving the HttpOnly session token and its absolute lifetime. The caller
// still has to pass every server-owned binding because this method is a
// durable authentication boundary, not a credential lookup shortcut.
func (store *Store) RefreshBootstrapSession(ctx context.Context, request BootstrapSessionRefreshRequest) (BootstrapSessionCredential, error) {
	origin, err := NormalizeOrigin(request.Origin)
	if err != nil {
		return BootstrapSessionCredential{}, ErrOriginMismatch
	}
	if request.SessionToken == "" || validateTransactionID(request.TransactionID) != nil ||
		validateBootstrapStateValue(request.State) != nil || request.Route == "" {
		return BootstrapSessionCredential{}, ErrSessionUnauthorized
	}
	csrfToken, err := store.newCredential()
	if err != nil {
		return BootstrapSessionCredential{}, err
	}
	unlock, err := store.lock(ctx)
	if err != nil {
		return BootstrapSessionCredential{}, err
	}
	defer unlock()
	state, err := store.loadBootstrapState()
	if err != nil {
		return BootstrapSessionCredential{}, err
	}
	digest := credentialDigest(request.SessionToken)
	record, exists := state.Sessions[digest]
	if !exists || !digestMatches(digest, request.SessionToken) {
		return BootstrapSessionCredential{}, ErrSessionUnauthorized
	}
	now := store.currentTime()
	if !now.Before(record.ExpiresAt) || !now.Before(record.IdleExpiresAt) {
		return BootstrapSessionCredential{}, ErrCredentialExpired
	}
	if record.Origin != origin {
		return BootstrapSessionCredential{}, ErrOriginMismatch
	}
	if record.TransactionID != request.TransactionID {
		return BootstrapSessionCredential{}, ErrTransactionMismatch
	}
	if record.State != request.State {
		return BootstrapSessionCredential{}, ErrStateMismatch
	}
	if !routeAllowed(record.AllowedRoutes, request.Route) {
		return BootstrapSessionCredential{}, ErrRouteNotAllowed
	}
	record.CSRFDigest = credentialDigest(csrfToken)
	record.IdleExpiresAt = nextIdleExpiry(now, record.ExpiresAt, BootstrapSessionIdleTTL)
	state.Sessions[digest] = record
	if err := store.writeBootstrapState(state); err != nil {
		return BootstrapSessionCredential{}, fmt.Errorf("persist bootstrap session refresh: %w", err)
	}
	return BootstrapSessionCredential{
		CSRFToken: csrfToken, TransactionID: record.TransactionID, Origin: record.Origin,
		State: record.State, AllowedRoutes: cloneRoutes(record.AllowedRoutes), CreatedAt: record.CreatedAt,
		ExpiresAt: record.ExpiresAt, IdleExpiresAt: record.IdleExpiresAt,
	}, nil
}

// CurrentBootstrapTransaction returns the transaction represented by the sole
// active bootstrap token or session. It is a read-only bridge for route
// authorizers; AuthenticateBootstrap still rechecks the value under a fresh
// lock, so concurrent credential replacement fails closed.
func (store *Store) CurrentBootstrapTransaction(ctx context.Context, expectedState ConsoleState) (string, error) {
	if err := validateBootstrapStateValue(expectedState); err != nil {
		return "", ErrStateMismatch
	}
	unlock, err := store.lock(ctx)
	if err != nil {
		return "", err
	}
	defer unlock()
	state, err := store.loadBootstrapState()
	if err != nil {
		return "", err
	}
	now := store.currentTime()
	if state.Token != nil {
		if state.Token.State != expectedState {
			return "", ErrStateMismatch
		}
		if !now.Before(state.Token.ExpiresAt) {
			return "", ErrCredentialExpired
		}
		return state.Token.TransactionID, nil
	}
	for _, session := range state.Sessions {
		if session.State != expectedState {
			return "", ErrStateMismatch
		}
		if !now.Before(session.ExpiresAt) || !now.Before(session.IdleExpiresAt) {
			return "", ErrCredentialExpired
		}
		return session.TransactionID, nil
	}
	return "", ErrSessionUnauthorized
}

// PromoteBootstrapToEnrollment preserves the current transaction and expiry
// while narrowing its state and route allowlist. A higher-level enrollment
// aggregate must publish this change atomically with the capability-state
// transition; this method provides the authentication-side candidate update.
func (store *Store) PromoteBootstrapToEnrollment(ctx context.Context, transactionID string, allowedRoutes []string) error {
	if validateTransactionID(transactionID) != nil {
		return store.failWithAudit(ctx, AuditEvent{Action: AuditBootstrapPromote, Reason: "invalid_transaction"}, ErrTransactionMismatch)
	}
	routes, err := normalizeAllowedRoutes(allowedRoutes)
	if err != nil {
		return store.failWithAudit(ctx, AuditEvent{Action: AuditBootstrapPromote, Reason: "invalid_routes", TransactionID: transactionID}, err)
	}
	unlock, err := store.lock(ctx)
	if err != nil {
		return store.failWithAudit(ctx, AuditEvent{Action: AuditBootstrapPromote, Reason: "store_unavailable", TransactionID: transactionID}, err)
	}
	defer unlock()
	state, err := store.loadBootstrapState()
	if err != nil {
		return store.failWithAudit(ctx, AuditEvent{Action: AuditBootstrapPromote, Reason: "state_unavailable", TransactionID: transactionID}, err)
	}
	matched := false
	if state.Token != nil && state.Token.TransactionID == transactionID {
		if state.Token.State != StateBootstrap {
			return store.failWithAudit(ctx, AuditEvent{Action: AuditBootstrapPromote, Reason: "state_mismatch", TransactionID: transactionID}, ErrStateMismatch)
		}
		state.Token.State = StateEnrollment
		state.Token.AllowedRoutes = cloneRoutes(routes)
		matched = true
	}
	for digest, session := range state.Sessions {
		if session.TransactionID != transactionID {
			continue
		}
		if session.State != StateBootstrap {
			return store.failWithAudit(ctx, AuditEvent{Action: AuditBootstrapPromote, Reason: "state_mismatch", TransactionID: transactionID}, ErrStateMismatch)
		}
		session.State = StateEnrollment
		session.AllowedRoutes = cloneRoutes(routes)
		state.Sessions[digest] = session
		matched = true
	}
	if !matched {
		return store.failWithAudit(ctx, AuditEvent{Action: AuditBootstrapPromote, Reason: "credential_not_found", TransactionID: transactionID}, ErrSessionUnauthorized)
	}
	if err := store.recordAudit(ctx, AuditEvent{
		Action: AuditBootstrapPromote, Outcome: AuditSuccess,
		TransactionID: transactionID, State: StateEnrollment,
	}); err != nil {
		return err
	}
	if err := store.writeBootstrapState(state); err != nil {
		return store.failWithAudit(ctx, AuditEvent{Action: AuditBootstrapPromote, Reason: "persist_failed", TransactionID: transactionID}, err)
	}
	return nil
}

// RevokeBootstrap invalidates the active token or session for a transaction.
func (store *Store) RevokeBootstrap(ctx context.Context, transactionID string) error {
	if err := validateTransactionID(transactionID); err != nil {
		return store.failWithAudit(ctx, AuditEvent{Action: AuditBootstrapRevoke, Reason: "invalid_request"}, err)
	}
	unlock, err := store.lock(ctx)
	if err != nil {
		return store.failWithAudit(ctx, AuditEvent{Action: AuditBootstrapRevoke, Reason: "store_unavailable"}, err)
	}
	defer unlock()
	state, err := store.loadBootstrapState()
	if err != nil {
		return store.failWithAudit(ctx, AuditEvent{Action: AuditBootstrapRevoke, Reason: "state_unavailable"}, err)
	}
	credentialState := ConsoleState("")
	matched := false
	if state.Token != nil && state.Token.TransactionID == transactionID {
		credentialState = state.Token.State
		matched = true
	}
	for _, session := range state.Sessions {
		if session.TransactionID == transactionID {
			credentialState = session.State
			matched = true
		}
	}
	if state.Handoff != nil && state.Handoff.TransactionID == transactionID {
		credentialState = StateEnrollment
		matched = true
	}
	for _, session := range state.EnrollmentSessions {
		if session.TransactionID == transactionID {
			credentialState = StateEnrollment
			matched = true
		}
	}
	if !matched {
		return store.failWithAudit(ctx, AuditEvent{
			Action: AuditBootstrapRevoke, Reason: "credential_not_found", TransactionID: transactionID,
		}, ErrInvalidToken)
	}
	revokedSessions := len(state.Sessions) + len(state.EnrollmentSessions)
	state.Token = nil
	state.Sessions = map[string]bootstrapSessionRecord{}
	state.Handoff = nil
	state.EnrollmentSessions = map[string]enrollmentSessionRecord{}
	event := AuditEvent{
		Action: AuditBootstrapRevoke, Outcome: AuditSuccess,
		TransactionID: transactionID, State: credentialState, RevokedSessions: revokedSessions,
	}
	if err := store.recordAudit(ctx, event); err != nil {
		return err
	}
	if err := store.writeBootstrapState(state); err != nil {
		return store.failWithAudit(ctx, AuditEvent{
			Action: AuditBootstrapRevoke, Reason: "persist_failed", TransactionID: transactionID, State: credentialState,
		}, err)
	}
	return nil
}

func bootstrapTokenTTLValid(ttl time.Duration) bool {
	return ttl >= MinimumBootstrapTokenTTL && ttl <= MaximumBootstrapTokenTTL
}
