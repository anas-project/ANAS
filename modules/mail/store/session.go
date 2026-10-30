package store

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"net/url"
	"regexp"
	"strings"
	"time"
)

var (
	ErrSessionDenied = errors.New("mail session is not authorized")
	sessionPattern   = regexp.MustCompile(`^ses_[a-f0-9]{64}$`)
)

// VerifiedLogin is supplied only after IAM token verification and a current
// directory admission check. It is never decoded from an HTTP request.
// Expiry must be capped by the verified upstream token and application policy.
type VerifiedLogin struct {
	OwnerAnchor, Issuer, SID string
	AuthenticatedAt, Expires time.Time
}

type Session struct {
	ID, OwnerAnchor, Issuer, SID        string
	AuthenticatedAt, CreatedAt, Expires time.Time
}

func validLogin(l VerifiedLogin) bool {
	return validIssuer(l.Issuer) && anchorPattern.MatchString(l.OwnerAnchor) && len(l.SID) <= 1024 && !strings.ContainsAny(l.SID, "\x00\r\n") &&
		!l.AuthenticatedAt.IsZero() && l.Expires.After(l.AuthenticatedAt)
}

func validIssuer(issuer string) bool {
	u, err := url.Parse(issuer)
	return err == nil && u.Scheme == "https" && u.Hostname() != "" && u.User == nil && u.RawQuery == "" && !u.ForceQuery && u.Fragment == "" && len(issuer) <= 2048 && !strings.ContainsAny(issuer, "\x00\r\n")
}

// CreateSession returns the secret once. PostgreSQL stores only its SHA-256.
// An unknown, disabled or deleted person cannot receive a session. The caller
// must never derive directory admission from an editable profile or email.
func (s Store) CreateSession(ctx context.Context, l VerifiedLogin) (Session, string, error) {
	return s.createSession(ctx, l, false)
}

// AdmitSession records a fresh successful directory read and creates a session
// atomically. It does not provision mailboxes or activate any address. A person
// returning from a denied state cannot resurrect their earlier session tokens.
func (s Store) AdmitSession(ctx context.Context, l VerifiedLogin) (Session, string, error) {
	return s.createSession(ctx, l, true)
}

func (s Store) createSession(ctx context.Context, l VerifiedLogin, admit bool) (Session, string, error) {
	if !validLogin(l) {
		return Session{}, "", ErrSessionDenied
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return Session{}, "", err
	}
	defer tx.Rollback()
	// All writers take owner then issuer locks. Logout only takes the issuer
	// lock, so a verified logout and a late callback cannot pass each other.
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "anas-mail-owner:"+l.OwnerAnchor); err != nil {
		return Session{}, "", err
	}
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "anas-mail-iam:"+l.Issuer); err != nil {
		return Session{}, "", err
	}
	if admit {
		if _, err = tx.ExecContext(ctx, `INSERT INTO mail.person(anchor) VALUES($1) ON CONFLICT DO NOTHING`, l.OwnerAnchor); err != nil {
			return Session{}, "", err
		}
		if _, err = tx.ExecContext(ctx, `UPDATE mail.web_session s SET revoked_at=clock_timestamp()
			FROM mail.person p WHERE p.anchor=$1 AND p.directory_state<>'enabled'
			AND s.owner_anchor=p.anchor AND s.revoked_at IS NULL`, l.OwnerAnchor); err != nil {
			return Session{}, "", err
		}
		if _, err = tx.ExecContext(ctx, `UPDATE mail.person SET directory_state='enabled' WHERE anchor=$1`, l.OwnerAnchor); err != nil {
			return Session{}, "", err
		}
	}
	id, err := mailboxID()
	if err != nil {
		return Session{}, "", err
	}
	secret := make([]byte, 32)
	if _, err = rand.Read(secret); err != nil {
		return Session{}, "", err
	}
	token := "ses_" + hex.EncodeToString(secret)
	digest := sha256.Sum256([]byte(token))
	var session Session
	err = tx.QueryRowContext(ctx, `
		INSERT INTO mail.web_session(id,token_hash,owner_anchor,iam_issuer,iam_sid,authenticated_at,expires_at)
		SELECT $1,$2,p.anchor,$4,$5,$6,$7 FROM mail.person p
		WHERE p.anchor=$3 AND p.directory_state='enabled'
		  AND $6::timestamptz<=clock_timestamp()+interval '30 seconds'
		  AND $7::timestamptz>clock_timestamp()
		  AND NOT EXISTS (SELECT 1 FROM mail.iam_logout x WHERE x.issuer=$4
		    AND (x.subject='' OR x.subject=$3) AND (x.sid='' OR x.sid=$5)
		    AND (x.sid<>'' OR $6::timestamptz<=x.issued_at))
		RETURNING id,owner_anchor,iam_issuer,iam_sid,authenticated_at,created_at,expires_at`,
		id, digest[:], l.OwnerAnchor, l.Issuer, l.SID, l.AuthenticatedAt, l.Expires).
		Scan(&session.ID, &session.OwnerAnchor, &session.Issuer, &session.SID, &session.AuthenticatedAt, &session.CreatedAt, &session.Expires)
	if errors.Is(err, sql.ErrNoRows) {
		return Session{}, "", ErrSessionDenied
	}
	if err != nil {
		return Session{}, "", err
	}
	if err = tx.Commit(); err != nil {
		return Session{}, "", err
	}
	return session, token, nil
}

func sessionHash(token string) ([]byte, bool) {
	if !sessionPattern.MatchString(token) {
		return nil, false
	}
	h := sha256.Sum256([]byte(token))
	return h[:], true
}

// Session checks expiry, permanent revocation and current directory state on
// every lookup. This is local session state, not a claim of downstream logout.
func (s Store) Session(ctx context.Context, token string) (Session, error) {
	h, ok := sessionHash(token)
	if !ok {
		return Session{}, ErrSessionDenied
	}
	var session Session
	err := s.DB.QueryRowContext(ctx, `
		SELECT s.id,s.owner_anchor,s.iam_issuer,s.iam_sid,s.authenticated_at,s.created_at,s.expires_at
		FROM mail.web_session s JOIN mail.person p ON p.anchor=s.owner_anchor
		WHERE s.token_hash=$1 AND s.revoked_at IS NULL AND s.expires_at>clock_timestamp()
		  AND p.directory_state='enabled'`, h).
		Scan(&session.ID, &session.OwnerAnchor, &session.Issuer, &session.SID, &session.AuthenticatedAt, &session.CreatedAt, &session.Expires)
	if errors.Is(err, sql.ErrNoRows) {
		return Session{}, ErrSessionDenied
	}
	return session, err
}

// RevokeSession is idempotent and terminal for this token. It does not contact
// IAM, Roundcube or DMS; their persistent revocation tasks belong to M2/M4.
func (s Store) RevokeSession(ctx context.Context, token string) error {
	h, ok := sessionHash(token)
	if !ok {
		return ErrSessionDenied
	}
	_, err := s.DB.ExecContext(ctx, `UPDATE mail.web_session SET revoked_at=clock_timestamp() WHERE token_hash=$1 AND revoked_at IS NULL`, h)
	return err
}
