package store

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/anas-project/ANAS/modules/mail/identity"
)

// DenyPerson is called only after an authoritative admission rejection, never
// on a directory timeout. It retains all mailboxes, addresses and mail data.
func (s Store) DenyPerson(ctx context.Context, anchor string) error {
	if !anchorPattern.MatchString(anchor) {
		return ErrSessionDenied
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "anas-mail-owner:"+anchor); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE mail.person SET directory_state='disabled' WHERE anchor=$1`, anchor); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE mail.web_session SET revoked_at=clock_timestamp() WHERE owner_anchor=$1 AND revoked_at IS NULL`, anchor); err != nil {
		return err
	}
	return tx.Commit()
}

// ApplyLogout accepts only an already signature-verified IAM notification.
// Recording the intent and revoking current sessions share one transaction.
// There is no LDAP dependency and no claim of Roundcube/DMS logout yet.
func (s Store) ApplyLogout(ctx context.Context, l identity.Logout) error {
	if !validIssuer(l.Issuer) || l.IssuedAt.IsZero() || len(l.SID) > 1024 || strings.ContainsAny(l.SID, "\x00\r\n") ||
		(l.Anchor == "" && l.SID == "") || (l.Anchor != "" && !anchorPattern.MatchString(l.Anchor)) || l.ID == "" || len(l.ID) > 256 || strings.ContainsAny(l.ID, "\x00\r\n") || l.IssuedAt.After(time.Now().Add(30*time.Second)) {
		return ErrSessionDenied
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "anas-mail-iam:"+l.Issuer); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO mail.iam_logout(issuer,jti,subject,sid,issued_at) VALUES($1,$2,$3,$4,$5) ON CONFLICT DO NOTHING`, l.Issuer, l.ID, l.Anchor, l.SID, l.IssuedAt); err != nil {
		return err
	}
	var identical bool
	if err = tx.QueryRowContext(ctx, `SELECT subject=$3 AND sid=$4 AND issued_at=$5 FROM mail.iam_logout WHERE issuer=$1 AND jti=$2`, l.Issuer, l.ID, l.Anchor, l.SID, l.IssuedAt).Scan(&identical); err != nil {
		return err
	}
	if !identical {
		return errors.New("mail logout identifier conflicts")
	}
	if _, err = tx.ExecContext(ctx, `UPDATE mail.web_session SET revoked_at=clock_timestamp()
		WHERE iam_issuer=$1 AND revoked_at IS NULL AND ($2='' OR owner_anchor=$2)
		AND ($3='' OR iam_sid=$3) AND ($3<>'' OR authenticated_at<=$4)`, l.Issuer, l.Anchor, l.SID, l.IssuedAt); err != nil {
		return err
	}
	return tx.Commit()
}
