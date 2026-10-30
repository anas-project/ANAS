package store

import (
	"context"
	"database/sql"
	"errors"
)

// A service-held refresh value may end only its exact personnel session, even
// when expired, rotated or already revoked. It never authorizes reading mail.
func (s Store) RevokeCredentialSession(ctx context.Context, refresh string) (string, error) {
	hash, ok := protocolHash(refresh, "ref_")
	if !ok {
		return "", ErrCredentialDenied
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	var id, owner string
	err = tx.QueryRowContext(ctx, `SELECT s.id,s.owner_anchor FROM mail.protocol_credential c JOIN mail.web_session s ON s.id=c.session_id WHERE c.refresh_hash=$1`, hash).Scan(&id, &owner)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrCredentialDenied
	}
	if err != nil {
		return "", err
	}
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "anas-mail-owner:"+owner); err != nil {
		return "", err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE mail.web_session SET revoked_at=clock_timestamp() WHERE id=$1 AND revoked_at IS NULL`, id); err != nil {
		return "", err
	}
	return id, tx.Commit()
}

// Logout-only lookup includes terminal sessions so that a Roundcube server
// withdrawal can be matched to the browser cookie before central logout.
func (s Store) LogoutSessionID(ctx context.Context, cookie, issuer string) (string, error) {
	hash, ok := sessionHash(cookie)
	if !ok {
		return "", ErrSessionDenied
	}
	var id string
	err := s.DB.QueryRowContext(ctx, `SELECT id FROM mail.web_session WHERE token_hash=$1 AND iam_issuer=$2`, hash, issuer).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrSessionDenied
	}
	return id, err
}
