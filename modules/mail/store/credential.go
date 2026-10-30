package store

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"strings"
	"time"
)

var ErrCredentialDenied = errors.New("mail protocol credential denied")

// Credentials are returned once to the authenticated Roundcube server. Neither
// personnel nor SMTP relay passwords appear here or in PostgreSQL token rows.
type Credentials struct {
	AccessToken    string    `json:"access_token"`
	SMTPToken      string    `json:"smtp_token"`
	RefreshToken   string    `json:"refresh_token"`
	Username       string    `json:"username"`
	SMTPUsername   string    `json:"smtp_username"`
	SessionID      string    `json:"session_id"`
	MailboxID      string    `json:"mailbox_id"`
	RoundcubeKey   string    `json:"roundcube_key"`
	DefaultAddress string    `json:"default_address"`
	Addresses      []string  `json:"addresses"`
	Expires        time.Time `json:"expires_at"`
	RefreshExpires time.Time `json:"refresh_expires_at"`
}

type ProtocolIdentity struct {
	CredentialID, FamilyID, SMTPLogin string
	Grant                             MailboxGrant
	Expires                           time.Time
}

const credentialSelect = `SELECT c.id,c.family_id,c.smtp_login,s.id,m.id,p.anchor,s.iam_issuer,
 m.protocol_login,m.roundcube_key,a.address,m.authorization_epoch,c.expires_at
 FROM mail.protocol_credential c JOIN mail.web_session s ON s.id=c.session_id
 JOIN mail.person p ON p.anchor=s.owner_anchor JOIN mail.mailbox m ON m.id=c.mailbox_id AND m.owner_anchor=p.anchor
 JOIN mail.address a ON a.mailbox_id=m.id AND a.is_default AND a.state='active'
 JOIN mail.mailbox_projection x ON x.mailbox_id=m.id AND x.applied_revision=x.revision AND x.authorization_epoch=m.authorization_epoch
 WHERE s.revoked_at IS NULL AND s.expires_at>clock_timestamp() AND p.directory_state='enabled'
 AND m.state='active' AND c.authorization_epoch=m.authorization_epoch AND c.rotated_at IS NULL AND c.revoked_at IS NULL`

func scanProtocol(row projectionScanner) (ProtocolIdentity, error) {
	var out ProtocolIdentity
	err := row.Scan(&out.CredentialID, &out.FamilyID, &out.SMTPLogin, &out.Grant.SessionID, &out.Grant.MailboxID, &out.Grant.OwnerAnchor, &out.Grant.Issuer, &out.Grant.ProtocolLogin, &out.Grant.RoundcubeKey, &out.Grant.DefaultAddress, &out.Grant.AuthorizationEpoch, &out.Expires)
	if errors.Is(err, sql.ErrNoRows) {
		return ProtocolIdentity{}, ErrCredentialDenied
	}
	return out, err
}
func protocolSecret(prefix string) (string, []byte, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", nil, err
	}
	secret := prefix + hex.EncodeToString(raw)
	hash := sha256.Sum256([]byte(secret))
	return secret, hash[:], nil
}
func protocolHash(secret, prefix string) ([]byte, bool) {
	if !strings.HasPrefix(secret, prefix) || len(secret) != len(prefix)+64 {
		return nil, false
	}
	if _, err := hex.DecodeString(secret[len(prefix):]); err != nil {
		return nil, false
	}
	h := sha256.Sum256([]byte(secret))
	return h[:], true
}
func validGrant(g MailboxGrant) bool {
	return anchorPattern.MatchString(g.SessionID) && anchorPattern.MatchString(g.MailboxID) && anchorPattern.MatchString(g.OwnerAnchor) && validIssuer(g.Issuer) && g.AuthorizationEpoch > 0
}

func (s Store) IssueCredentials(ctx context.Context, g MailboxGrant) (Credentials, error) {
	if !validGrant(g) {
		return Credentials{}, ErrCredentialDenied
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return Credentials{}, err
	}
	defer tx.Rollback()
	family, err := mailboxID()
	if err != nil {
		return Credentials{}, err
	}
	out, err := issueCredentials(ctx, tx, g, family)
	if err != nil {
		return Credentials{}, err
	}
	if err = tx.Commit(); err != nil {
		return Credentials{}, err
	}
	return out, nil
}
func issueCredentials(ctx context.Context, tx *sql.Tx, g MailboxGrant, family string) (Credentials, error) {
	// Match the owner-first lock order used by directory admission/projection.
	// A session share lock must not precede the personnel lock in DenyPerson.
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "anas-mail-owner:"+g.OwnerAnchor); err != nil {
		return Credentials{}, err
	}
	var current MailboxGrant
	err := tx.QueryRowContext(ctx, `SELECT s.id,m.id,p.anchor,s.iam_issuer,m.protocol_login,m.roundcube_key,a.address,m.authorization_epoch
 FROM mail.web_session s JOIN mail.person p ON p.anchor=s.owner_anchor JOIN mail.mailbox m ON m.owner_anchor=p.anchor
 JOIN mail.address a ON a.mailbox_id=m.id AND a.is_default AND a.state='active'
 JOIN mail.mailbox_projection x ON x.mailbox_id=m.id AND x.applied_revision=x.revision AND x.authorization_epoch=m.authorization_epoch
 WHERE s.id=$1 AND m.id=$2 AND p.anchor=$3 AND s.iam_issuer=$4 AND m.authorization_epoch=$5
 AND s.revoked_at IS NULL AND s.expires_at>clock_timestamp() AND p.directory_state='enabled' AND m.state='active'
 FOR SHARE OF s,p,m,a,x`, g.SessionID, g.MailboxID, g.OwnerAnchor, g.Issuer, g.AuthorizationEpoch).Scan(&current.SessionID, &current.MailboxID, &current.OwnerAnchor, &current.Issuer, &current.ProtocolLogin, &current.RoundcubeKey, &current.DefaultAddress, &current.AuthorizationEpoch)
	if errors.Is(err, sql.ErrNoRows) {
		return Credentials{}, ErrCredentialDenied
	}
	if err != nil {
		return Credentials{}, err
	}
	access, accessHash, err := protocolSecret("acc_")
	if err != nil {
		return Credentials{}, err
	}
	smtp, smtpHash, err := protocolSecret("smt_")
	if err != nil {
		return Credentials{}, err
	}
	refresh, refreshHash, err := protocolSecret("ref_")
	if err != nil {
		return Credentials{}, err
	}
	id, err := mailboxID()
	if err != nil {
		return Credentials{}, err
	}
	smtpLogin, err := token("sub_")
	if err != nil {
		return Credentials{}, err
	}
	out := Credentials{AccessToken: access, SMTPToken: smtp, RefreshToken: refresh, Username: current.ProtocolLogin, SMTPUsername: smtpLogin, SessionID: current.SessionID, MailboxID: current.MailboxID, RoundcubeKey: current.RoundcubeKey, DefaultAddress: current.DefaultAddress, Addresses: []string{}}
	err = tx.QueryRowContext(ctx, `INSERT INTO mail.protocol_credential(id,family_id,session_id,mailbox_id,authorization_epoch,access_hash,smtp_hash,refresh_hash,smtp_login,expires_at,refresh_expires_at)
 SELECT $1,$2,s.id,$4,$5,$6,$7,$8,$9,LEAST(s.expires_at,clock_timestamp()+interval '90 seconds'),LEAST(s.expires_at,clock_timestamp()+interval '15 minutes')
 FROM mail.web_session s WHERE s.id=$3 RETURNING expires_at,refresh_expires_at`, id, family, current.SessionID, current.MailboxID, current.AuthorizationEpoch, accessHash, smtpHash, refreshHash, smtpLogin).Scan(&out.Expires, &out.RefreshExpires)
	if err != nil {
		return Credentials{}, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT address FROM mail.address WHERE mailbox_id=$1 AND state='active' ORDER BY address`, current.MailboxID)
	if err != nil {
		return Credentials{}, err
	}
	defer rows.Close()
	for rows.Next() {
		var address string
		if err := rows.Scan(&address); err != nil {
			return Credentials{}, err
		}
		out.Addresses = append(out.Addresses, address)
	}
	if err := rows.Err(); err != nil {
		return Credentials{}, err
	}
	return out, nil
}

func (s Store) IntrospectCredential(ctx context.Context, secret, protocol string) (ProtocolIdentity, error) {
	column, prefix := "access_hash", "acc_"
	if protocol == "smtp" {
		column, prefix = "smtp_hash", "smt_"
	} else if protocol != "imap" {
		return ProtocolIdentity{}, ErrCredentialDenied
	}
	hash, ok := protocolHash(secret, prefix)
	if !ok {
		return ProtocolIdentity{}, ErrCredentialDenied
	}
	return scanProtocol(s.DB.QueryRowContext(ctx, credentialSelect+` AND c.`+column+`=$1 AND c.expires_at>clock_timestamp()`, hash))
}

// RefreshGrant reveals no secret. It lets the server validate current AD
// admission and the applied projection before consuming a refresh generation.
func (s Store) RefreshGrant(ctx context.Context, secret string) (ProtocolIdentity, error) {
	hash, ok := protocolHash(secret, "ref_")
	if !ok {
		return ProtocolIdentity{}, ErrCredentialDenied
	}
	return scanProtocol(s.DB.QueryRowContext(ctx, credentialSelect+` AND c.refresh_hash=$1 AND c.refresh_expires_at>clock_timestamp()`, hash))
}

// Refresh consumes one generation transactionally. Concurrent refreshes have
// one winner; old access/SMTP tokens stop working in that same commit.
func (s Store) RefreshCredentials(ctx context.Context, secret string) (Credentials, error) {
	hash, ok := protocolHash(secret, "ref_")
	if !ok {
		return Credentials{}, ErrCredentialDenied
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return Credentials{}, err
	}
	defer tx.Rollback()
	old, err := scanProtocol(tx.QueryRowContext(ctx, credentialSelect+` AND c.refresh_hash=$1 AND c.refresh_expires_at>clock_timestamp()`, hash))
	if err != nil {
		return Credentials{}, err
	}
	// Submission authorization also locks the owner before credential rows.
	// Recheck after obtaining that lock, then consume this refresh generation.
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "anas-mail-owner:"+old.Grant.OwnerAnchor); err != nil {
		return Credentials{}, err
	}
	old, err = scanProtocol(tx.QueryRowContext(ctx, credentialSelect+` AND c.refresh_hash=$1 AND c.refresh_expires_at>clock_timestamp() FOR UPDATE OF c`, hash))
	if err != nil {
		return Credentials{}, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE mail.protocol_credential SET rotated_at=clock_timestamp() WHERE id=$1`, old.CredentialID); err != nil {
		return Credentials{}, err
	}
	out, err := issueCredentials(ctx, tx, old.Grant, old.FamilyID)
	if err != nil {
		return Credentials{}, err
	}
	if err = tx.Commit(); err != nil {
		return Credentials{}, err
	}
	return out, nil
}

// SenderGrant is a read-only authorization check. It is repeated after fresh
// directory validation by the HTTP caller. Relay authentication never bypasses
// this mailbox/session-bound decision for either SMTP envelope or visible From.
func (s Store) SenderGrant(ctx context.Context, login, envelope, from string) (ProtocolIdentity, error) {
	if len(login) != 36 || !strings.HasPrefix(login, "sub_") {
		return ProtocolIdentity{}, ErrCredentialDenied
	}
	canonical, eErr := primaryAddress(envelope)
	visible, fErr := primaryAddress(from)
	if eErr != nil || fErr != nil || canonical != envelope || visible != from {
		return ProtocolIdentity{}, ErrCredentialDenied
	}
	return scanProtocol(s.DB.QueryRowContext(ctx, credentialSelect+` AND c.smtp_login=$1 AND c.expires_at>clock_timestamp()
 AND EXISTS(SELECT 1 FROM mail.address e WHERE e.mailbox_id=m.id AND e.address=$2 AND e.state='active')
 AND EXISTS(SELECT 1 FROM mail.address f WHERE f.mailbox_id=m.id AND f.address=$3 AND f.state='active')`, login, envelope, from))
}
