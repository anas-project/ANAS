package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

type IngressStatus struct {
	LastPoll        *time.Time `json:"last_poll"`
	PollError       string     `json:"poll_error"`
	Backlog         int64      `json:"backlog"`
	Complete        int64      `json:"complete"`
	TaskErrors      int64      `json:"task_errors"`
	NextAttempt     *time.Time `json:"next_attempt"`
	PendingNotices  int64      `json:"pending_notices"`
	ExpiredNotices  int64      `json:"expired_notices"`
	RouteRevision   int64      `json:"route_revision"`
	RouteObservedAt *time.Time `json:"route_observed_at"`
	RouteError      string     `json:"route_error"`
}

// IngressSummary excludes raw metadata, message/owner identities, opaque
// cursors and credentials. Missing observations remain explicitly unknown.
func (s Store) IngressSummary(ctx context.Context, source, generation string) (IngressStatus, error) {
	var out IngressStatus
	if !anchorPattern.MatchString(source) || !anchorPattern.MatchString(generation) {
		return out, ErrIngressChanged
	}
	tx, e := s.DB.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if e != nil {
		return out, e
	}
	defer tx.Rollback()
	var actual string
	e = tx.QueryRowContext(ctx, `SELECT generation,last_success,last_error FROM mail.ingress_poll WHERE source_id=$1`, source).Scan(&actual, &out.LastPoll, &out.PollError)
	if e != nil && !errors.Is(e, sql.ErrNoRows) {
		return out, e
	}
	if e == nil && actual != generation {
		return out, ErrIngressChanged
	}
	e = tx.QueryRowContext(ctx, `SELECT generation,revision,observed_at FROM mail.ingress_route_state WHERE source_id=$1`, source).Scan(&actual, &out.RouteRevision, &out.RouteObservedAt)
	if e != nil && !errors.Is(e, sql.ErrNoRows) {
		return out, e
	}
	if e == nil && actual != generation {
		return out, ErrIngressChanged
	}
	e = tx.QueryRowContext(ctx, `SELECT count(*) FILTER(WHERE phase<>'complete'),count(*) FILTER(WHERE phase='complete'),count(*) FILTER(WHERE phase<>'complete' AND last_error<>''),min(next_attempt) FILTER(WHERE phase<>'complete') FROM mail.ingress_message WHERE source_id=$1 AND generation=$2`, source, generation).Scan(&out.Backlog, &out.Complete, &out.TaskErrors, &out.NextAttempt)
	if e != nil {
		return out, e
	}
	e = tx.QueryRowContext(ctx, `SELECT count(*) FILTER(WHERE state='pending'),count(*) FILTER(WHERE state='expired') FROM mail.ingress_notice WHERE source_id=$1 AND generation=$2`, source, generation).Scan(&out.PendingNotices, &out.ExpiredNotices)
	if e != nil {
		return out, e
	}
	e = tx.QueryRowContext(ctx, `SELECT o.last_error FROM mail.ingress_route_operation o JOIN mail.ingress_route_state s ON s.source_id=o.source_id
 WHERE o.source_id=$1 AND o.generation=$2 AND (o.state IN ('prepared','issued') OR o.state='confirmed' AND o.expected_revision=s.revision-1)
 ORDER BY (o.state IN ('prepared','issued')) DESC,o.created_at DESC LIMIT 1`, source, generation).Scan(&out.RouteError)
	if e != nil && !errors.Is(e, sql.ErrNoRows) {
		return out, e
	}
	return out, tx.Commit()
}
