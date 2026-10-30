package store

import (
	"context"
	"database/sql"
	"errors"
	"regexp"
	"strings"
)

var ErrSendLimit = errors.New("mailbox sending rate exceeded")
var queuePattern = regexp.MustCompile(`^[A-Za-z0-9]{5,64}$`)
var submissionRequestPattern = regexp.MustCompile(`^[a-f0-9]{32}$`)

// AuthorizeSubmission repeats the sender decision after the caller's fresh AD
// validation. Counting and deduplication commit together. A repeated
// request ID must retain the same credential, queue, mailbox, session and sender
// addresses; this authorization does not identify content or prove acceptance.
func (s Store) AuthorizeSubmission(ctx context.Context, g ProtocolIdentity, requestID, queue, envelope, from string, limit int) error {
	if !validGrant(g.Grant) || !anchorPattern.MatchString(g.CredentialID) || !submissionRequestPattern.MatchString(requestID) || !queuePattern.MatchString(queue) || strings.EqualFold(queue, "NOQUEUE") || strings.EqualFold(queue, "undef") || strings.EqualFold(queue, "unknown") || limit < 1 || limit > 1000000 {
		return ErrCredentialDenied
	}
	e, err := primaryAddress(envelope)
	if err != nil || e != envelope {
		return ErrCredentialDenied
	}
	f, err := primaryAddress(from)
	if err != nil || f != from {
		return ErrCredentialDenied
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "anas-mail-owner:"+g.Grant.OwnerAnchor); err != nil {
		return err
	}
	current, err := scanProtocol(tx.QueryRowContext(ctx, credentialSelect+` AND c.id=$1 AND c.smtp_login=$2 AND c.expires_at>clock_timestamp()
 AND EXISTS(SELECT 1 FROM mail.address e WHERE e.mailbox_id=m.id AND e.address=$3 AND e.state='active')
 AND EXISTS(SELECT 1 FROM mail.address f WHERE f.mailbox_id=m.id AND f.address=$4 AND f.state='active') FOR SHARE OF c,s,p,m,a,x`, g.CredentialID, g.SMTPLogin, envelope, from))
	if err != nil {
		return err
	}
	if current.Grant != g.Grant {
		return ErrCredentialDenied
	}
	var same bool
	err = tx.QueryRowContext(ctx, `SELECT credential_id=$2 AND queue_id=$3 AND mailbox_id=$4 AND session_id=$5 AND envelope=$6 AND visible_from=$7 FROM mail.smtp_submission WHERE request_id=$1`, requestID, current.CredentialID, queue, current.Grant.MailboxID, current.Grant.SessionID, envelope, from).Scan(&same)
	if err == nil {
		if !same {
			return ErrCredentialDenied
		}
		return tx.Commit()
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	var count int
	if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM mail.smtp_submission WHERE mailbox_id=$1 AND accepted_at>clock_timestamp()-interval '60 seconds'`, current.Grant.MailboxID).Scan(&count); err != nil {
		return err
	}
	if count >= limit {
		return ErrSendLimit
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO mail.smtp_submission(request_id,queue_id,credential_id,mailbox_id,session_id,envelope,visible_from) VALUES($1,$2,$3,$4,$5,$6,$7)`, requestID, queue, current.CredentialID, current.Grant.MailboxID, current.Grant.SessionID, envelope, from); err != nil {
		return err
	}
	return tx.Commit()
}
