package store

import (
	"context"
	"database/sql"
	"errors"
)

// MailboxForSession derives the personnel owner from the verified cookie. It
// supplies a preflight identifier snapshot, not a protocol credential; callers
// still need fresh AD validation and a transactionally rechecked handoff.
func (s Store) MailboxForSession(ctx context.Context, cookie, id string) (MailboxGrant, error) {
	hash, ok := sessionHash(cookie)
	if !ok || !anchorPattern.MatchString(id) {
		return MailboxGrant{}, ErrHandoffDenied
	}
	var g MailboxGrant
	err := s.DB.QueryRowContext(ctx, `SELECT s.id,m.id,p.anchor,s.iam_issuer,m.protocol_login,m.roundcube_key,a.address,m.authorization_epoch
 FROM mail.web_session s JOIN mail.person p ON p.anchor=s.owner_anchor JOIN mail.mailbox m ON m.owner_anchor=p.anchor
 JOIN mail.address a ON a.mailbox_id=m.id AND a.is_default AND a.state='active'
 JOIN mail.mailbox_projection x ON x.mailbox_id=m.id AND x.applied_revision=x.revision AND x.authorization_epoch=m.authorization_epoch
 WHERE s.token_hash=$1 AND m.id=$2 AND s.revoked_at IS NULL AND s.expires_at>clock_timestamp()
 AND p.directory_state='enabled' AND m.state='active'`, hash, id).Scan(&g.SessionID, &g.MailboxID, &g.OwnerAnchor, &g.Issuer, &g.ProtocolLogin, &g.RoundcubeKey, &g.DefaultAddress, &g.AuthorizationEpoch)
	if errors.Is(err, sql.ErrNoRows) {
		return MailboxGrant{}, ErrHandoffDenied
	}
	return g, err
}
