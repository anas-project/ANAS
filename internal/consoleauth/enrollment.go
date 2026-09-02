package consoleauth

import (
	"context"
	"errors"
	"strings"
	"time"
)

const (
	EnrollmentHandoffRoute         = "/api/v1/auth/enrollment/handoffs"
	EnrollmentHandoffExchangeRoute = "/api/v1/auth/enrollment/exchange"
	EnrollmentOwnerRoute           = "/api/v1/auth/enrollment/owner"
)

// EnrollmentRecoveryRoutePatterns returns the closed route surface available
// while the console is in enrollment state. Callers receive an independent
// slice so local changes cannot alter the policy used by another component.
func EnrollmentRecoveryRoutePatterns() []string {
	return []string{
		"/api/v1/system",
		"/api/v1/system/ca",
		"/api/v1/auth/session",
		"/api/v1/jobs",
		"/api/v1/jobs/{id}",
		"/api/v1/jobs/{id}/events",
		EnrollmentHandoffRoute,
	}
}

// IssueEnrollmentHandoff authenticates and consumes the handoff privilege of
// the bound enrollment bootstrap session under the same auth lock that writes
// the handoff digest. A session can issue at most one handoff, and issuance
// never replaces an existing handoff or HTTPS enrollment session.
func (store *Store) IssueEnrollmentHandoff(ctx context.Context, request IssueEnrollmentHandoffRequest) (EnrollmentHandoffCredential, error) {
	source, target, spki, err := exactEnrollmentBinding(request.SourceOrigin, request.TargetOrigin, request.SPKISHA256)
	if err != nil || request.SessionToken == "" || request.CSRFToken == "" {
		return EnrollmentHandoffCredential{}, store.failWithAudit(ctx, AuditEvent{Action: AuditHandoffIssue, Reason: "invalid_request"}, ErrHandoffUnauthorized)
	}
	unlock, err := store.lock(ctx)
	if err != nil {
		return EnrollmentHandoffCredential{}, store.failWithAudit(ctx, AuditEvent{Action: AuditHandoffIssue, Reason: "store_unavailable"}, err)
	}
	defer unlock()
	state, err := store.loadBootstrapState()
	if err != nil {
		return EnrollmentHandoffCredential{}, store.failWithAudit(ctx, AuditEvent{Action: AuditHandoffIssue, Reason: "state_unavailable"}, err)
	}
	now := store.currentTime()
	sessionDigest, session, err := authenticateBootstrapForHandoff(state, now, request, source)
	if err != nil {
		return EnrollmentHandoffCredential{}, store.failWithAudit(ctx, AuditEvent{
			Action: AuditHandoffIssue, Reason: "session_unauthorized", Origin: source,
			TargetOrigin: target, State: StateEnrollment,
		}, err)
	}
	if state.Handoff != nil || len(state.EnrollmentSessions) != 0 || session.HandoffIssued {
		return EnrollmentHandoffCredential{}, store.failWithAudit(ctx, AuditEvent{
			Action: AuditHandoffIssue, Reason: "handoff_already_issued", TransactionID: session.TransactionID,
			Origin: source, TargetOrigin: target, State: StateEnrollment,
		}, ErrHandoffUnauthorized)
	}
	token, err := store.newCredential()
	if err != nil {
		return EnrollmentHandoffCredential{}, store.failWithAudit(ctx, AuditEvent{
			Action: AuditHandoffIssue, Reason: "random_failed", TransactionID: session.TransactionID,
			Origin: source, TargetOrigin: target, State: StateEnrollment,
		}, err)
	}
	session.HandoffIssued = true
	state.Sessions[sessionDigest] = session
	state.Handoff = &enrollmentHandoffRecord{
		Digest: credentialDigest(token), TransactionID: session.TransactionID,
		SourceOrigin: source, TargetOrigin: target, SPKISHA256: spki,
		CreatedAt: now, ExpiresAt: now.Add(EnrollmentHandoffTTL),
	}
	if err := store.recordAudit(ctx, AuditEvent{
		Action: AuditHandoffIssue, Outcome: AuditSuccess, TransactionID: session.TransactionID,
		Origin: source, TargetOrigin: target, State: StateEnrollment,
	}); err != nil {
		return EnrollmentHandoffCredential{}, err
	}
	if err := store.writeBootstrapState(state); err != nil {
		return EnrollmentHandoffCredential{}, store.failWithAudit(ctx, AuditEvent{
			Action: AuditHandoffIssue, Reason: "persist_failed", TransactionID: session.TransactionID,
			Origin: source, TargetOrigin: target, State: StateEnrollment,
		}, err)
	}
	return EnrollmentHandoffCredential{
		Token: token, TransactionID: session.TransactionID, SourceOrigin: source,
		TargetOrigin: target, SPKISHA256: spki, CreatedAt: now, ExpiresAt: now.Add(EnrollmentHandoffTTL),
	}, nil
}

// ExchangeEnrollmentHandoff validates every server-observed source, target and
// certificate binding before consuming the one-time digest. Inputs must already
// be canonical; the package never normalizes a permissive client spelling into
// a match.
func (store *Store) ExchangeEnrollmentHandoff(ctx context.Context, request ExchangeEnrollmentHandoffRequest) (EnrollmentSessionCredential, error) {
	source, target, spki, err := exactEnrollmentBinding(request.SourceOrigin, request.TargetOrigin, request.SPKISHA256)
	if err != nil || request.Token == "" {
		return EnrollmentSessionCredential{}, store.failWithAudit(ctx, AuditEvent{
			Action: AuditHandoffExchange, Reason: "invalid_request", State: StateEnrollment,
		}, ErrHandoffUnauthorized)
	}
	unlock, err := store.lock(ctx)
	if err != nil {
		return EnrollmentSessionCredential{}, store.failWithAudit(ctx, AuditEvent{Action: AuditHandoffExchange, Reason: "store_unavailable"}, err)
	}
	defer unlock()
	state, err := store.loadBootstrapState()
	if err != nil {
		return EnrollmentSessionCredential{}, store.failWithAudit(ctx, AuditEvent{Action: AuditHandoffExchange, Reason: "state_unavailable"}, err)
	}
	record := state.Handoff
	if record == nil || !digestMatches(record.Digest, request.Token) {
		return EnrollmentSessionCredential{}, store.failWithAudit(ctx, AuditEvent{
			Action: AuditHandoffExchange, Reason: "handoff_not_found", Origin: source,
			TargetOrigin: target, State: StateEnrollment,
		}, ErrHandoffUnauthorized)
	}
	now := store.currentTime()
	if !now.Before(record.ExpiresAt) {
		return EnrollmentSessionCredential{}, store.failWithAudit(ctx, AuditEvent{
			Action: AuditHandoffExchange, Reason: "handoff_expired", TransactionID: record.TransactionID,
			Origin: source, TargetOrigin: target, State: StateEnrollment,
		}, ErrCredentialExpired)
	}
	if record.SourceOrigin != source || record.TargetOrigin != target || record.SPKISHA256 != spki {
		return EnrollmentSessionCredential{}, store.failWithAudit(ctx, AuditEvent{
			Action: AuditHandoffExchange, Reason: "binding_mismatch", TransactionID: record.TransactionID,
			Origin: source, TargetOrigin: target, State: StateEnrollment,
		}, ErrHandoffUnauthorized)
	}
	sessionToken, err := store.newCredential()
	if err != nil {
		return EnrollmentSessionCredential{}, store.failWithAudit(ctx, AuditEvent{Action: AuditHandoffExchange, Reason: "random_failed"}, err)
	}
	csrfToken, err := store.newCredential()
	if err != nil {
		return EnrollmentSessionCredential{}, store.failWithAudit(ctx, AuditEvent{Action: AuditHandoffExchange, Reason: "random_failed"}, err)
	}
	session := enrollmentSessionRecord{
		CSRFDigest: credentialDigest(csrfToken), TransactionID: record.TransactionID,
		Origin: target, CreatedAt: now, ExpiresAt: now.Add(EnrollmentSessionTTL),
	}
	state.Handoff = nil
	state.EnrollmentSessions = map[string]enrollmentSessionRecord{credentialDigest(sessionToken): session}
	if err := store.recordAudit(ctx, AuditEvent{
		Action: AuditHandoffExchange, Outcome: AuditSuccess, TransactionID: record.TransactionID,
		Origin: source, TargetOrigin: target, State: StateEnrollment,
	}); err != nil {
		return EnrollmentSessionCredential{}, err
	}
	if err := store.writeBootstrapState(state); err != nil {
		return EnrollmentSessionCredential{}, store.failWithAudit(ctx, AuditEvent{
			Action: AuditHandoffExchange, Reason: "persist_failed", TransactionID: record.TransactionID,
			Origin: source, TargetOrigin: target, State: StateEnrollment,
		}, err)
	}
	return EnrollmentSessionCredential{
		Token: sessionToken, CSRFToken: csrfToken, TransactionID: record.TransactionID,
		Origin: target, CreatedAt: now, ExpiresAt: session.ExpiresAt,
	}, nil
}

func (store *Store) AuthenticateEnrollment(ctx context.Context, request EnrollmentAuthenticationRequest) (EnrollmentPrincipal, error) {
	origin, err := NormalizeOrigin(request.Origin)
	if err != nil || origin != request.Origin || !strings.HasPrefix(origin, "https://") {
		return EnrollmentPrincipal{}, store.failWithAudit(ctx, AuditEvent{Action: AuditEnrollmentAuth, Reason: "invalid_origin"}, ErrOriginMismatch)
	}
	if validateTransactionID(request.TransactionID) != nil {
		return EnrollmentPrincipal{}, store.failWithAudit(ctx, AuditEvent{Action: AuditEnrollmentAuth, Reason: "invalid_transaction", Origin: origin}, ErrTransactionMismatch)
	}
	if request.Route != EnrollmentOwnerRoute {
		return EnrollmentPrincipal{}, store.failWithAudit(ctx, AuditEvent{
			Action: AuditEnrollmentAuth, Reason: "route_not_allowed", TransactionID: request.TransactionID,
			Origin: origin, State: StateEnrollment,
		}, ErrRouteNotAllowed)
	}
	unlock, err := store.lock(ctx)
	if err != nil {
		return EnrollmentPrincipal{}, store.failWithAudit(ctx, AuditEvent{Action: AuditEnrollmentAuth, Reason: "store_unavailable"}, err)
	}
	defer unlock()
	state, err := store.loadBootstrapState()
	if err != nil {
		return EnrollmentPrincipal{}, store.failWithAudit(ctx, AuditEvent{Action: AuditEnrollmentAuth, Reason: "state_unavailable"}, err)
	}
	digest := credentialDigest(request.SessionToken)
	record, exists := state.EnrollmentSessions[digest]
	if !exists || !digestMatches(digest, request.SessionToken) {
		return EnrollmentPrincipal{}, store.failWithAudit(ctx, AuditEvent{Action: AuditEnrollmentAuth, Reason: "session_not_found"}, ErrSessionUnauthorized)
	}
	now := store.currentTime()
	if !now.Before(record.ExpiresAt) {
		return EnrollmentPrincipal{}, store.failWithAudit(ctx, AuditEvent{
			Action: AuditEnrollmentAuth, Reason: "session_expired", TransactionID: record.TransactionID,
			Origin: origin, State: StateEnrollment,
		}, ErrCredentialExpired)
	}
	if record.Origin != origin {
		return EnrollmentPrincipal{}, store.failWithAudit(ctx, AuditEvent{Action: AuditEnrollmentAuth, Reason: "origin_mismatch"}, ErrOriginMismatch)
	}
	if record.TransactionID != request.TransactionID {
		return EnrollmentPrincipal{}, store.failWithAudit(ctx, AuditEvent{Action: AuditEnrollmentAuth, Reason: "transaction_mismatch"}, ErrTransactionMismatch)
	}
	if request.RequireCSRF && !digestMatches(record.CSRFDigest, request.CSRFToken) {
		return EnrollmentPrincipal{}, store.failWithAudit(ctx, AuditEvent{Action: AuditEnrollmentAuth, Reason: "csrf_mismatch"}, ErrCSRFMismatch)
	}
	return EnrollmentPrincipal{
		TransactionID: record.TransactionID, Origin: record.Origin,
		CreatedAt: record.CreatedAt, ExpiresAt: record.ExpiresAt,
	}, nil
}

func (store *Store) CurrentEnrollmentTransaction(ctx context.Context) (string, error) {
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
	if state.Handoff != nil {
		if !now.Before(state.Handoff.ExpiresAt) {
			return "", ErrCredentialExpired
		}
		return state.Handoff.TransactionID, nil
	}
	for _, session := range state.EnrollmentSessions {
		if !now.Before(session.ExpiresAt) {
			return "", ErrCredentialExpired
		}
		return session.TransactionID, nil
	}
	return "", ErrSessionUnauthorized
}

func (store *Store) RevokeEnrollment(ctx context.Context, transactionID string) error {
	if validateTransactionID(transactionID) != nil {
		return store.failWithAudit(ctx, AuditEvent{Action: AuditEnrollmentRevoke, Reason: "invalid_request"}, ErrTransactionMismatch)
	}
	unlock, err := store.lock(ctx)
	if err != nil {
		return store.failWithAudit(ctx, AuditEvent{Action: AuditEnrollmentRevoke, Reason: "store_unavailable"}, err)
	}
	defer unlock()
	state, err := store.loadBootstrapState()
	if err != nil {
		return store.failWithAudit(ctx, AuditEvent{Action: AuditEnrollmentRevoke, Reason: "state_unavailable"}, err)
	}
	if transaction := enrollmentTransaction(state); transaction != transactionID {
		return store.failWithAudit(ctx, AuditEvent{Action: AuditEnrollmentRevoke, Reason: "credential_not_found", TransactionID: transactionID}, ErrSessionUnauthorized)
	}
	revoked := len(state.EnrollmentSessions)
	state.Handoff = nil
	state.EnrollmentSessions = map[string]enrollmentSessionRecord{}
	if err := store.recordAudit(ctx, AuditEvent{
		Action: AuditEnrollmentRevoke, Outcome: AuditSuccess, TransactionID: transactionID,
		State: StateEnrollment, RevokedSessions: revoked,
	}); err != nil {
		return err
	}
	if err := store.writeBootstrapState(state); err != nil {
		return store.failWithAudit(ctx, AuditEvent{Action: AuditEnrollmentRevoke, Reason: "persist_failed", TransactionID: transactionID}, err)
	}
	return nil
}

func authenticateBootstrapForHandoff(state bootstrapStateFile, now time.Time, request IssueEnrollmentHandoffRequest, source string) (string, bootstrapSessionRecord, error) {
	digest := credentialDigest(request.SessionToken)
	record, exists := state.Sessions[digest]
	if !exists || !digestMatches(digest, request.SessionToken) {
		return "", bootstrapSessionRecord{}, ErrSessionUnauthorized
	}
	if !now.Before(record.ExpiresAt) || !now.Before(record.IdleExpiresAt) {
		return "", bootstrapSessionRecord{}, ErrCredentialExpired
	}
	if record.State != StateEnrollment {
		return "", bootstrapSessionRecord{}, ErrStateMismatch
	}
	if record.Origin != source {
		return "", bootstrapSessionRecord{}, ErrOriginMismatch
	}
	if !routeAllowed(record.AllowedRoutes, EnrollmentHandoffRoute) {
		return "", bootstrapSessionRecord{}, ErrRouteNotAllowed
	}
	if !digestMatches(record.CSRFDigest, request.CSRFToken) {
		return "", bootstrapSessionRecord{}, ErrCSRFMismatch
	}
	return digest, record, nil
}

func exactEnrollmentBinding(sourceOrigin, targetOrigin, spki string) (string, string, string, error) {
	source, err := NormalizeOrigin(sourceOrigin)
	if err != nil || source != sourceOrigin {
		return "", "", "", errors.New("enrollment source origin must be canonical")
	}
	target, err := NormalizeOrigin(targetOrigin)
	if err != nil || target != targetOrigin || !strings.HasPrefix(target, "https://") {
		return "", "", "", errors.New("enrollment target origin must be canonical HTTPS")
	}
	if spki != strings.ToLower(spki) || validateDigest(spki) != nil {
		return "", "", "", errors.New("certificate SPKI must be a lowercase SHA-256 digest")
	}
	return source, target, spki, nil
}

func validateEnrollmentBinding(sourceOrigin, targetOrigin, spki string) error {
	_, _, _, err := exactEnrollmentBinding(sourceOrigin, targetOrigin, spki)
	return err
}

func enrollmentTransaction(state bootstrapStateFile) string {
	if state.Handoff != nil {
		return state.Handoff.TransactionID
	}
	for _, session := range state.EnrollmentSessions {
		return session.TransactionID
	}
	return ""
}
