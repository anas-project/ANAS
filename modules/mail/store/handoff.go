package store

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"regexp"
	"time"
)

var (
	ErrHandoffDenied      = errors.New("mailbox handoff denied")
	handoffCodePattern    = regexp.MustCompile(`^handoff_[a-f0-9]{64}$`)
	handoffBrowserPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{43}$`)
)

// Handoff is returned once to the trusted HTTP caller. Code must travel in a
// POST body, never a URL, log or browser storage. BrowserState comes from the
// destination Roundcube session, not a caller-selected owner or return URL.
type Handoff struct {
	Code, SessionID, MailboxID string
	Expires                    time.Time
}

// MailboxGrant contains identifiers only, not a protocol credential. A later
// credential issuer must recheck this session and epoch transactionally; this
// value cannot independently authorize IMAP, SMTP or subsequent requests.
type MailboxGrant struct {
	SessionID, MailboxID, OwnerAnchor           string
	Issuer                                      string
	ProtocolLogin, RoundcubeKey, DefaultAddress string
	AuthorizationEpoch                          int64
}

// IssueHandoff requires a fresh directory check by the trusted HTTP caller.
// PostgreSQL additionally verifies the current local person/session, ownership,
// mailbox and default-address readiness. Pending resources grant nothing.
func (s Store) IssueHandoff(ctx context.Context, sessionToken, mailboxID, browserState string) (Handoff, error) {
	cookieHash, ok := sessionHash(sessionToken)
	if !ok || !anchorPattern.MatchString(mailboxID) || !handoffBrowserPattern.MatchString(browserState) {
		return Handoff{}, ErrHandoffDenied
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return Handoff{}, err
	}
	code := "handoff_" + hex.EncodeToString(raw)
	codeHash, browserHash := sha256.Sum256([]byte(code)), sha256.Sum256([]byte(browserState))
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return Handoff{}, err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `DELETE FROM mail.webmail_handoff WHERE expires_at<=clock_timestamp()`); err != nil {
		return Handoff{}, err
	}
	var out Handoff
	err = tx.QueryRowContext(ctx, `INSERT INTO mail.webmail_handoff(code_hash,browser_hash,session_id,mailbox_id,authorization_epoch,expires_at)
		SELECT $1,$2,s.id,m.id,m.authorization_epoch,LEAST(s.expires_at,clock_timestamp()+interval '30 seconds')
		FROM mail.web_session s JOIN mail.person p ON p.anchor=s.owner_anchor
		JOIN mail.mailbox m ON m.owner_anchor=p.anchor
		JOIN mail.address a ON a.mailbox_id=m.id AND a.is_default
		WHERE s.token_hash=$3 AND s.revoked_at IS NULL AND s.expires_at>clock_timestamp()
		AND p.directory_state='enabled' AND m.id=$4 AND m.state='active' AND a.state='active'
		RETURNING session_id,mailbox_id,expires_at`, codeHash[:], browserHash[:], cookieHash, mailboxID).Scan(&out.SessionID, &out.MailboxID, &out.Expires)
	if errors.Is(err, sql.ErrNoRows) {
		return Handoff{}, ErrHandoffDenied
	}
	if err != nil {
		return Handoff{}, err
	}
	if err = tx.Commit(); err != nil {
		return Handoff{}, err
	}
	out.Code = code
	return out, nil
}

// RedeemHandoff must be called only after authenticating the fixed Roundcube
// service. All four bindings must match. Correctly bound but no longer valid
// tickets are burned; wrong browser/session/mailbox bindings do not consume it.
func (s Store) RedeemHandoff(ctx context.Context, code, sessionID, mailboxID, browserState string) (MailboxGrant, error) {
	if !handoffCodePattern.MatchString(code) || !anchorPattern.MatchString(sessionID) || !anchorPattern.MatchString(mailboxID) || !handoffBrowserPattern.MatchString(browserState) {
		return MailboxGrant{}, ErrHandoffDenied
	}
	codeHash, browserHash := sha256.Sum256([]byte(code)), sha256.Sum256([]byte(browserState))
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return MailboxGrant{}, err
	}
	defer tx.Rollback()
	// Take the same owner-first lock as directory denial and projection before
	// taking personnel/session share locks below. The session owner is immutable.
	var owner string
	if err = tx.QueryRowContext(ctx, `SELECT owner_anchor FROM mail.web_session WHERE id=$1`, sessionID).Scan(&owner); errors.Is(err, sql.ErrNoRows) {
		return MailboxGrant{}, ErrHandoffDenied
	} else if err != nil {
		return MailboxGrant{}, err
	}
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "anas-mail-owner:"+owner); err != nil {
		return MailboxGrant{}, err
	}
	var epoch int64
	var unexpired bool
	err = tx.QueryRowContext(ctx, `DELETE FROM mail.webmail_handoff
		WHERE code_hash=$1 AND browser_hash=$2 AND session_id=$3 AND mailbox_id=$4
		RETURNING authorization_epoch,expires_at>clock_timestamp()`, codeHash[:], browserHash[:], sessionID, mailboxID).Scan(&epoch, &unexpired)
	if errors.Is(err, sql.ErrNoRows) {
		return MailboxGrant{}, ErrHandoffDenied
	}
	if err != nil {
		return MailboxGrant{}, err
	}
	var grant MailboxGrant
	if unexpired {
		err = tx.QueryRowContext(ctx, `SELECT s.id,m.id,p.anchor,s.iam_issuer,m.protocol_login,m.roundcube_key,a.address,m.authorization_epoch
			FROM mail.web_session s JOIN mail.person p ON p.anchor=s.owner_anchor
			JOIN mail.mailbox m ON m.owner_anchor=p.anchor
			JOIN mail.address a ON a.mailbox_id=m.id AND a.is_default
			WHERE s.id=$1 AND m.id=$2 AND s.revoked_at IS NULL AND s.expires_at>clock_timestamp()
			AND p.directory_state='enabled' AND m.state='active' AND a.state='active' AND m.authorization_epoch=$3
			FOR SHARE OF s,p,m,a`, sessionID, mailboxID, epoch).Scan(&grant.SessionID, &grant.MailboxID, &grant.OwnerAnchor, &grant.Issuer, &grant.ProtocolLogin, &grant.RoundcubeKey, &grant.DefaultAddress, &grant.AuthorizationEpoch)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return MailboxGrant{}, err
		}
	}
	if commitErr := tx.Commit(); commitErr != nil {
		return MailboxGrant{}, commitErr
	}
	if !unexpired || errors.Is(err, sql.ErrNoRows) {
		return MailboxGrant{}, ErrHandoffDenied
	}
	return grant, nil
}

// CheckMailboxGrant closes the directory-read window in the HTTP exchange.
// Identifiers are not protocol credentials: a future credential issuer must
// still recheck these bindings in its own issuance transaction.
func (s Store) CheckMailboxGrant(ctx context.Context, grant MailboxGrant) (MailboxGrant, error) {
	if !anchorPattern.MatchString(grant.SessionID) || !anchorPattern.MatchString(grant.MailboxID) || !anchorPattern.MatchString(grant.OwnerAnchor) || grant.Issuer == "" || grant.AuthorizationEpoch < 1 {
		return MailboxGrant{}, ErrHandoffDenied
	}
	var out MailboxGrant
	err := s.DB.QueryRowContext(ctx, `SELECT s.id,m.id,p.anchor,s.iam_issuer,m.protocol_login,m.roundcube_key,a.address,m.authorization_epoch
		FROM mail.web_session s JOIN mail.person p ON p.anchor=s.owner_anchor
		JOIN mail.mailbox m ON m.owner_anchor=p.anchor
		JOIN mail.address a ON a.mailbox_id=m.id AND a.is_default
		WHERE s.id=$1 AND m.id=$2 AND m.authorization_epoch=$3 AND p.anchor=$4 AND s.iam_issuer=$5
		AND s.revoked_at IS NULL AND s.expires_at>clock_timestamp()
		AND p.directory_state='enabled' AND m.state='active' AND a.state='active'`,
		grant.SessionID, grant.MailboxID, grant.AuthorizationEpoch, grant.OwnerAnchor, grant.Issuer).Scan(
		&out.SessionID, &out.MailboxID, &out.OwnerAnchor, &out.Issuer, &out.ProtocolLogin, &out.RoundcubeKey, &out.DefaultAddress, &out.AuthorizationEpoch)
	if errors.Is(err, sql.ErrNoRows) {
		return MailboxGrant{}, ErrHandoffDenied
	}
	return out, err
}

// WithholdMailbox removes local login/sending readiness after a trusted
// directory or reconciliation decision, without editing AD, addresses or mail.
// Repeated withholding is idempotent. Only a future confirmed projection path
// may reactivate the box; reactivation must retain the advanced local epoch.
func (s Store) WithholdMailbox(ctx context.Context, mailboxID string) error {
	if !anchorPattern.MatchString(mailboxID) {
		return ErrHandoffDenied
	}
	var n int
	err := s.DB.QueryRowContext(ctx, `WITH box AS (UPDATE mail.mailbox SET
		authorization_epoch=authorization_epoch+CASE WHEN state IN ('disabled','retained') THEN 0 ELSE 1 END,
		state=CASE WHEN state='retained' THEN state ELSE 'disabled' END WHERE id=$1 RETURNING id),
 invalidated AS (UPDATE mail.mailbox_projection SET applied_revision=NULL,applied_at=NULL,last_error='authorization-withheld'
 WHERE mailbox_id IN(SELECT id FROM box) RETURNING mailbox_id)
 SELECT count(*) FROM box`, mailboxID).Scan(&n)
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrHandoffDenied
	}
	return nil
}
