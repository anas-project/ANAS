package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
)

type IngressNotice struct {
	SourceID, Generation, ID, MessageID, MailboxID, BodySHA256 string
}
type IngressNoticeTask struct {
	IngressNotice
	Lease    string
	Attempts int
}

func (n IngressNotice) valid() bool {
	return anchorPattern.MatchString(n.SourceID) && anchorPattern.MatchString(n.Generation) && anchorPattern.MatchString(n.ID) && anchorPattern.MatchString(n.MailboxID) && ingressMessageID.MatchString(n.MessageID) && ingressDigest.MatchString(n.BodySHA256)
}
func (s Store) RecordIngressNotice(ctx context.Context, n IngressNotice) error {
	if !n.valid() {
		return ErrIngressChanged
	}
	r, e := s.DB.ExecContext(ctx, `INSERT INTO mail.ingress_notice(source_id,generation,notification_id,message_id,mailbox_id,body_sha256)
 VALUES($1,$2,$3,$4,$5,$6) ON CONFLICT(source_id,generation,notification_id) DO UPDATE SET notification_id=EXCLUDED.notification_id
 WHERE mail.ingress_notice.message_id=EXCLUDED.message_id AND mail.ingress_notice.mailbox_id=EXCLUDED.mailbox_id AND mail.ingress_notice.body_sha256=EXCLUDED.body_sha256`, n.SourceID, n.Generation, n.ID, n.MessageID, n.MailboxID, n.BodySHA256)
	if e != nil {
		return e
	}
	if count, _ := r.RowsAffected(); count != 1 {
		return ErrIngressChanged
	}
	return nil
}
func (s Store) ClaimIngressNotice(ctx context.Context, source string) (IngressNoticeTask, error) {
	if !anchorPattern.MatchString(source) {
		return IngressNoticeTask{}, ErrIngressChanged
	}
	lease, e := mailboxID()
	if e != nil {
		return IngressNoticeTask{}, e
	}
	var t IngressNoticeTask
	e = s.DB.QueryRowContext(ctx, `WITH candidate AS (
 SELECT source_id,generation,notification_id FROM mail.ingress_notice WHERE source_id=$1 AND state='pending'
 AND next_attempt<=clock_timestamp() AND (lease_until IS NULL OR lease_until<=clock_timestamp())
 ORDER BY next_attempt,received_at,notification_id FOR UPDATE SKIP LOCKED LIMIT 1)
 UPDATE mail.ingress_notice n SET lease_token=$2,lease_until=clock_timestamp()+interval '1 minute',attempts=attempts+1
 FROM candidate c WHERE n.source_id=c.source_id AND n.generation=c.generation AND n.notification_id=c.notification_id
 RETURNING n.source_id,n.generation,n.notification_id,n.message_id,n.mailbox_id,n.body_sha256,n.lease_token,n.attempts`, source, lease).Scan(&t.SourceID, &t.Generation, &t.ID, &t.MessageID, &t.MailboxID, &t.BodySHA256, &t.Lease, &t.Attempts)
	if errors.Is(e, sql.ErrNoRows) {
		return IngressNoticeTask{}, ErrIngressIdle
	}
	return t, e
}
func (s Store) FinishIngressNotice(ctx context.Context, t IngressNoticeTask, state, code string) error {
	if !t.IngressNotice.valid() || !anchorPattern.MatchString(t.Lease) || state != "pending" && state != "accepted" && state != "expired" || len(code) > 64 || strings.Trim(code, "abcdefghijklmnopqrstuvwxyz0123456789-_") != "" || state == "pending" && code == "" {
		return ErrIngressChanged
	}
	r, e := s.DB.ExecContext(ctx, `UPDATE mail.ingress_notice SET state=$5,last_error=$6,lease_token=NULL,lease_until=NULL,
 completed_at=CASE WHEN $5='pending' THEN NULL ELSE clock_timestamp() END,
 next_attempt=CASE WHEN $5='pending' THEN clock_timestamp()+make_interval(secs=>LEAST(300,5*attempts)) ELSE next_attempt END
 WHERE source_id=$1 AND generation=$2 AND notification_id=$3 AND lease_token=$4 AND lease_until>clock_timestamp() AND state='pending'`, t.SourceID, t.Generation, t.ID, t.Lease, state, code)
	if e != nil {
		return e
	}
	if count, _ := r.RowsAffected(); count != 1 {
		return ErrIngressChanged
	}
	return nil
}
