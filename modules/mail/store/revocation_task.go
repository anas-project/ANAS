package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
)

var ErrTaskChanged = errors.New("mail revocation task changed or leased")

type RevocationTask struct {
	ID, Revision                int64
	SessionID, MailboxID, Lease string
	Attempts                    int
}

// ExpireSessions also creates downstream withdrawals through the same trigger
// as explicit logout. It is independent of directory connectivity.
func (s Store) ExpireSessions(ctx context.Context) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE mail.web_session SET revoked_at=clock_timestamp() WHERE revoked_at IS NULL AND expires_at<=clock_timestamp()`)
	return err
}
func (s Store) ClaimRevocation(ctx context.Context) (RevocationTask, error) {
	lease, err := mailboxID()
	if err != nil {
		return RevocationTask{}, err
	}
	var out RevocationTask
	err = s.DB.QueryRowContext(ctx, `WITH candidate AS (
 SELECT id FROM mail.revocation_task WHERE completed_at IS NULL AND next_attempt<=clock_timestamp()
 AND (lease_until IS NULL OR lease_until<=clock_timestamp()) ORDER BY next_attempt,id FOR UPDATE SKIP LOCKED LIMIT 1)
 UPDATE mail.revocation_task t SET attempts=attempts+1,lease_until=clock_timestamp()+interval '20 seconds',lease_token=$1
 FROM candidate WHERE t.id=candidate.id RETURNING t.id,t.revision,t.session_id,COALESCE(t.mailbox_id::text,''),t.lease_token,t.attempts`, lease).Scan(&out.ID, &out.Revision, &out.SessionID, &out.MailboxID, &out.Lease, &out.Attempts)
	if errors.Is(err, sql.ErrNoRows) {
		return RevocationTask{}, ErrTaskChanged
	}
	return out, err
}
func (s Store) FinishRevocation(ctx context.Context, t RevocationTask, success bool, code string) error {
	if t.ID < 1 || t.Revision < 1 || !anchorPattern.MatchString(t.Lease) || !anchorPattern.MatchString(t.SessionID) || t.MailboxID != "" && !anchorPattern.MatchString(t.MailboxID) || !success && (code == "" || len(code) > 64) {
		return ErrTaskChanged
	}
	if !success && strings.Trim(code, "abcdefghijklmnopqrstuvwxyz0123456789-_") != "" {
		return ErrTaskChanged
	}
	result, err := s.DB.ExecContext(ctx, `UPDATE mail.revocation_task SET completed_at=CASE WHEN $4 THEN clock_timestamp() ELSE NULL END,
 lease_until=NULL,lease_token=NULL,last_error=CASE WHEN $4 THEN '' ELSE $5 END,
 next_attempt=CASE WHEN $4 THEN next_attempt ELSE clock_timestamp()+make_interval(secs=>LEAST(60,5*attempts)) END
 WHERE id=$1 AND revision=$2 AND lease_token=$3 AND completed_at IS NULL`, t.ID, t.Revision, t.Lease, success, code)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count != 1 {
		return ErrTaskChanged
	}
	return nil
}
