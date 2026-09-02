package consoleauth

import (
	"context"
	"fmt"
	"time"
)

func (store *Store) RefreshProxySession(ctx context.Context, request ProxySessionRefreshRequest) (ProxySessionCredential, error) {
	origin, err := NormalizeOrigin(request.Origin)
	auditEvent := proxyAuditEvent(AuditProxySession, request.Identity, origin)
	if err != nil || validateProxyIdentity(request.Identity) != nil {
		return ProxySessionCredential{}, store.failWithAudit(ctx, withAuditReason(auditEvent, "invalid_identity"), ErrSessionUnauthorized)
	}
	now := store.currentTime()
	if !request.Identity.ExpiresAt.After(now) {
		return ProxySessionCredential{}, store.failWithAudit(ctx, withAuditReason(auditEvent, "identity_expired"), ErrCredentialExpired)
	}
	csrfToken, err := store.newCredential()
	if err != nil {
		return ProxySessionCredential{}, store.failWithAudit(ctx, withAuditReason(auditEvent, "random_failed"), err)
	}
	unlock, err := store.lock(ctx)
	if err != nil {
		return ProxySessionCredential{}, store.failWithAudit(ctx, withAuditReason(auditEvent, "store_unavailable"), err)
	}
	defer unlock()
	state, err := store.loadProxyState()
	if err != nil {
		return ProxySessionCredential{}, store.failWithAudit(ctx, withAuditReason(auditEvent, "state_unavailable"), err)
	}
	pruneProxyState(&state, now)
	var token, digest string
	var record proxySessionRecord
	if request.SessionToken != "" {
		digest = credentialDigest(request.SessionToken)
		existing, ok := state.Sessions[digest]
		if ok && digestMatches(digest, request.SessionToken) && proxySessionMatchesIdentity(existing, request.Identity) && existing.Origin == origin && now.Before(existing.ExpiresAt) && now.Before(existing.IdleExpiresAt) {
			token, record = request.SessionToken, existing
		}
	}
	if token == "" {
		if len(state.Sessions) >= 1024 {
			return ProxySessionCredential{}, store.failWithAudit(ctx, withAuditReason(auditEvent, "session_capacity"), ErrSessionUnauthorized)
		}
		token, err = store.newCredential()
		if err != nil {
			return ProxySessionCredential{}, store.failWithAudit(ctx, withAuditReason(auditEvent, "random_failed"), err)
		}
		digest = credentialDigest(token)
		expiresAt := earlierTime(now.Add(ProxySessionAbsoluteTTL), request.Identity.ExpiresAt)
		record = proxySessionRecord{
			Origin: origin, Issuer: request.Identity.Issuer, Subject: request.Identity.Subject,
			SemanticRole: request.Identity.SemanticRole, DirectoryGroup: request.Identity.DirectoryGroup,
			CreatedAt: now, ExpiresAt: expiresAt,
		}
	}
	record.CSRFDigest = credentialDigest(csrfToken)
	record.IdleExpiresAt = nextIdleExpiry(now, record.ExpiresAt, ProxySessionIdleTTL)
	state.Sessions[digest] = record
	if err := store.recordAudit(ctx, withAuditOutcome(auditEvent, AuditSuccess)); err != nil {
		return ProxySessionCredential{}, err
	}
	if err := store.writeProxyState(state); err != nil {
		return ProxySessionCredential{}, store.failWithAudit(ctx, withAuditReason(auditEvent, "persist_failed"), err)
	}
	return ProxySessionCredential{Token: token, CSRFToken: csrfToken, Origin: origin, Identity: request.Identity, CreatedAt: record.CreatedAt, ExpiresAt: record.ExpiresAt, IdleExpiresAt: record.IdleExpiresAt}, nil
}

func (store *Store) AuthenticateProxy(ctx context.Context, request ProxyAuthenticationRequest) (ProxyPrincipal, error) {
	origin, err := NormalizeOrigin(request.Origin)
	if err != nil || validateProxyIdentity(request.Identity) != nil || request.SessionToken == "" {
		return ProxyPrincipal{}, ErrSessionUnauthorized
	}
	unlock, err := store.lock(ctx)
	if err != nil {
		return ProxyPrincipal{}, err
	}
	defer unlock()
	state, err := store.loadProxyState()
	if err != nil {
		return ProxyPrincipal{}, err
	}
	now := store.currentTime()
	digest := credentialDigest(request.SessionToken)
	record, exists := state.Sessions[digest]
	if !exists || !digestMatches(digest, request.SessionToken) || !proxySessionMatchesIdentity(record, request.Identity) || record.Origin != origin {
		return ProxyPrincipal{}, ErrSessionUnauthorized
	}
	if !now.Before(record.ExpiresAt) || !now.Before(record.IdleExpiresAt) || !now.Before(request.Identity.ExpiresAt) {
		return ProxyPrincipal{}, ErrCredentialExpired
	}
	if request.RequireCSRF && !digestMatches(record.CSRFDigest, request.CSRFToken) {
		return ProxyPrincipal{}, ErrCSRFMismatch
	}
	if !request.ObserveOnly {
		idle := nextIdleExpiry(now, record.ExpiresAt, ProxySessionIdleTTL)
		if idle.After(record.IdleExpiresAt) {
			record.IdleExpiresAt = idle
			state.Sessions[digest] = record
			if err := store.writeProxyState(state); err != nil {
				return ProxyPrincipal{}, fmt.Errorf("persist proxy session activity: %w", err)
			}
		}
	}
	return ProxyPrincipal{SessionDigest: digest, Origin: origin, Identity: request.Identity, CreatedAt: record.CreatedAt, ExpiresAt: record.ExpiresAt, IdleExpiresAt: record.IdleExpiresAt}, nil
}

func (store *Store) IssueProxyStepUp(ctx context.Context, request ProxyStepUpRequest) (ProxyStepUpCredential, error) {
	origin, err := NormalizeOrigin(request.Origin)
	auditEvent := proxyAuditEvent(AuditProxyStepUp, request.Identity, origin)
	auditEvent.AuthorizedAction, auditEvent.WorkspaceID, auditEvent.TargetID = request.Action, request.WorkspaceID, request.TargetID
	if err != nil || validateProxyIdentity(request.Identity) != nil || validateLocalStepUpBinding(request.Action, request.WorkspaceID, request.TargetID, request.StateDigest) != nil {
		return ProxyStepUpCredential{}, store.failWithAudit(ctx, withAuditReason(auditEvent, "invalid_request"), ErrStepUpUnauthorized)
	}
	unlock, err := store.lock(ctx)
	if err != nil {
		return ProxyStepUpCredential{}, store.failWithAudit(ctx, withAuditReason(auditEvent, "store_unavailable"), err)
	}
	defer unlock()
	state, err := store.loadProxyState()
	if err != nil {
		return ProxyStepUpCredential{}, store.failWithAudit(ctx, withAuditReason(auditEvent, "state_unavailable"), err)
	}
	now := store.currentTime()
	sessionDigest := credentialDigest(request.SessionToken)
	session, exists := state.Sessions[sessionDigest]
	if !exists || !digestMatches(sessionDigest, request.SessionToken) || !proxySessionMatchesIdentity(session, request.Identity) || session.Origin != origin {
		return ProxyStepUpCredential{}, store.failWithAudit(ctx, withAuditReason(auditEvent, "session_not_found"), ErrSessionUnauthorized)
	}
	if !now.Before(session.ExpiresAt) || !now.Before(session.IdleExpiresAt) || !now.Before(request.Identity.ExpiresAt) {
		return ProxyStepUpCredential{}, store.failWithAudit(ctx, withAuditReason(auditEvent, "session_expired"), ErrCredentialExpired)
	}
	if !digestMatches(session.CSRFDigest, request.CSRFToken) {
		return ProxyStepUpCredential{}, store.failWithAudit(ctx, withAuditReason(auditEvent, "csrf_mismatch"), ErrCSRFMismatch)
	}
	if request.Identity.AuthenticatedAt.After(now.Add(time.Minute)) || now.Sub(request.Identity.AuthenticatedAt) > ProxyRecentAuthenticationTTL {
		return ProxyStepUpCredential{}, store.failWithAudit(ctx, withAuditReason(auditEvent, "recent_auth_required"), ErrStepUpUnauthorized)
	}
	pruneProxyState(&state, now)
	if len(state.StepUps) >= 1024 {
		return ProxyStepUpCredential{}, store.failWithAudit(ctx, withAuditReason(auditEvent, "step_up_capacity"), ErrStepUpUnauthorized)
	}
	random, err := store.newCredential()
	if err != nil {
		return ProxyStepUpCredential{}, store.failWithAudit(ctx, withAuditReason(auditEvent, "random_failed"), err)
	}
	token := "sup_" + random
	digest := credentialDigest(token)
	expiresAt := earlierTime(now.Add(LocalStepUpTTL), request.Identity.AuthenticatedAt.Add(ProxyRecentAuthenticationTTL), session.ExpiresAt, request.Identity.ExpiresAt)
	record := proxyStepUpRecord{SessionDigest: sessionDigest, AssertionDigest: request.Identity.AssertionDigest, Action: request.Action, WorkspaceID: request.WorkspaceID, TargetID: request.TargetID, StateDigest: request.StateDigest, CreatedAt: now, ExpiresAt: expiresAt}
	state.StepUps[digest] = record
	if err := store.recordAudit(ctx, withAuditOutcome(auditEvent, AuditSuccess)); err != nil {
		return ProxyStepUpCredential{}, err
	}
	if err := store.writeProxyState(state); err != nil {
		return ProxyStepUpCredential{}, store.failWithAudit(ctx, withAuditReason(auditEvent, "persist_failed"), err)
	}
	return ProxyStepUpCredential{Token: token, Digest: digest, SessionDigest: sessionDigest, Action: record.Action, WorkspaceID: record.WorkspaceID, TargetID: record.TargetID, StateDigest: record.StateDigest, CreatedAt: record.CreatedAt, ExpiresAt: record.ExpiresAt}, nil
}

func (store *Store) AuthenticateProxyStepUp(ctx context.Context, request ProxyStepUpAuthenticationRequest) (ProxyStepUpBinding, error) {
	if _, err := store.AuthenticateProxy(ctx, ProxyAuthenticationRequest{SessionToken: request.SessionToken, Origin: request.Origin, Identity: request.Identity, ObserveOnly: true}); err != nil {
		return ProxyStepUpBinding{}, err
	}
	if validateLocalStepUpBinding(request.Action, request.WorkspaceID, request.TargetID, request.StateDigest) != nil {
		return ProxyStepUpBinding{}, ErrStepUpUnauthorized
	}
	unlock, err := store.lock(ctx)
	if err != nil {
		return ProxyStepUpBinding{}, err
	}
	defer unlock()
	state, err := store.loadProxyState()
	if err != nil {
		return ProxyStepUpBinding{}, err
	}
	digest := credentialDigest(request.Token)
	record, exists := state.StepUps[digest]
	sessionDigest := credentialDigest(request.SessionToken)
	if !exists || !digestMatches(digest, request.Token) || !store.currentTime().Before(record.ExpiresAt) || record.SessionDigest != sessionDigest || record.AssertionDigest != request.Identity.AssertionDigest || record.Action != request.Action || record.WorkspaceID != request.WorkspaceID || record.TargetID != request.TargetID || record.StateDigest != request.StateDigest {
		return ProxyStepUpBinding{}, ErrStepUpUnauthorized
	}
	return ProxyStepUpBinding{Digest: digest, SessionDigest: record.SessionDigest, Action: record.Action, WorkspaceID: record.WorkspaceID, TargetID: record.TargetID, StateDigest: record.StateDigest, CreatedAt: record.CreatedAt, ExpiresAt: record.ExpiresAt}, nil
}

func proxySessionMatchesIdentity(record proxySessionRecord, identity ProxyIdentity) bool {
	return record.Issuer == identity.Issuer && record.Subject == identity.Subject && record.SemanticRole == identity.SemanticRole && record.DirectoryGroup == identity.DirectoryGroup
}

func pruneProxyState(state *proxyStateFile, now time.Time) {
	for digest, session := range state.Sessions {
		if !now.Before(session.ExpiresAt) || !now.Before(session.IdleExpiresAt) {
			delete(state.Sessions, digest)
			for proofDigest, proof := range state.StepUps {
				if proof.SessionDigest == digest {
					delete(state.StepUps, proofDigest)
				}
			}
		}
	}
	for digest, proof := range state.StepUps {
		if !now.Before(proof.ExpiresAt) {
			delete(state.StepUps, digest)
		}
	}
}

func proxyAuditEvent(action AuditAction, identity ProxyIdentity, origin string) AuditEvent {
	return AuditEvent{Action: action, Origin: origin, IdentityIssuer: identity.Issuer, IdentitySubject: identity.Subject, SemanticRole: identity.SemanticRole, DirectoryGroup: identity.DirectoryGroup}
}

func earlierTime(values ...time.Time) time.Time {
	var result time.Time
	for _, value := range values {
		if value.IsZero() {
			continue
		}
		if result.IsZero() || value.Before(result) {
			result = value
		}
	}
	return result
}
