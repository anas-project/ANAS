package store

import (
	"context"
	"database/sql"
	"errors"
	"regexp"
	"strings"
	"time"
)

var providerRuleID = regexp.MustCompile(`^[a-f0-9]{32}$`)
var providerWorker = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)

type CloudflareRuleFact struct{ ID, Fingerprint string }

func (r CloudflareRuleFact) valid() bool {
	return r == (CloudflareRuleFact{}) || providerRuleID.MatchString(r.ID) && ingressDigest.MatchString(r.Fingerprint)
}

type CloudflareRuleBinding struct {
	SourceID, Generation, Address, Worker string
	Fact                                  CloudflareRuleFact
	ObservedAt                            *time.Time
}
type CloudflareRuleOperation struct {
	ID, SourceID, Generation, Address, Worker, Action, State, Lease, Error string
	RouteRevision                                                          int64
	Expected                                                               CloudflareRuleFact
	Attempts                                                               int
}

func scanCloudflareBinding(row projectionScanner) (CloudflareRuleBinding, error) {
	var r CloudflareRuleBinding
	e := row.Scan(&r.SourceID, &r.Generation, &r.Address, &r.Worker, &r.Fact.ID, &r.Fact.Fingerprint, &r.ObservedAt)
	return r, e
}

const cloudflareBindingSelect = `SELECT source_id,generation,address,worker,COALESCE(rule_id,''),COALESCE(fingerprint,''),observed_at FROM mail.cloudflare_rule`
const cloudflareOperationSelect = `SELECT o.id,o.source_id,b.generation,o.address,b.worker,o.action,o.state,COALESCE(o.lease_token::text,''),o.last_error,o.route_revision,COALESCE(o.expected_id,''),COALESCE(o.expected_fingerprint,''),o.attempts FROM mail.cloudflare_rule_operation o JOIN mail.cloudflare_rule b ON b.source_id=o.source_id AND b.address=o.address`

func scanCloudflareOperation(row projectionScanner) (CloudflareRuleOperation, error) {
	var o CloudflareRuleOperation
	e := row.Scan(&o.ID, &o.SourceID, &o.Generation, &o.Address, &o.Worker, &o.Action, &o.State, &o.Lease, &o.Error, &o.RouteRevision, &o.Expected.ID, &o.Expected.Fingerprint, &o.Attempts)
	return o, e
}

// PrepareCloudflareRules follows an already-confirmed source permission set.
// A restored unknown source revision cannot reach a provider mutation. A row
// with no provider fact is a known absence, not permission to adopt a foreign
// existing rule. Issued requests keep their superseded history for recovery.
func (s Store) PrepareCloudflareRules(ctx context.Context, source, generation, worker string, routes []IngressRoute) error {
	fp, _, e := IngressRoutesFingerprint(routes)
	if e != nil || !anchorPattern.MatchString(source) || !anchorPattern.MatchString(generation) || !providerWorker.MatchString(worker) {
		return ErrIngressChanged
	}
	tx, e := s.DB.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	if e = sourceRouteLock(ctx, tx, source); e != nil {
		return e
	}
	var actualGeneration, actualFP string
	var revision int64
	if e = tx.QueryRowContext(ctx, `SELECT generation,fingerprint,revision FROM mail.ingress_route_state WHERE source_id=$1 FOR SHARE`, source).Scan(&actualGeneration, &actualFP, &revision); e != nil {
		return ingressError(e)
	}
	if generation != actualGeneration || fp != actualFP || revision < 1 {
		return ErrIngressChanged
	}
	wanted := map[string]bool{}
	for _, r := range routes {
		if len(r.Address) > 90 {
			return ErrIngressChanged
		}
		wanted[r.Address] = true
		if _, e = tx.ExecContext(ctx, `INSERT INTO mail.cloudflare_rule(source_id,generation,address,worker) VALUES($1,$2,$3,$4) ON CONFLICT DO NOTHING`, source, generation, r.Address, worker); e != nil {
			return e
		}
	}
	rows, e := tx.QueryContext(ctx, cloudflareBindingSelect+` WHERE source_id=$1 ORDER BY address FOR UPDATE`, source)
	if e != nil {
		return e
	}
	bindings := []CloudflareRuleBinding{}
	for rows.Next() {
		r, e := scanCloudflareBinding(rows)
		if e != nil {
			rows.Close()
			return e
		}
		bindings = append(bindings, r)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return e
	}
	for _, r := range bindings {
		if r.Generation != generation || r.Worker != worker {
			return ErrIngressChanged
		}
		action := "delete"
		if wanted[r.Address] {
			action = "create"
		}
		pending, e := scanCloudflareOperation(tx.QueryRowContext(ctx, cloudflareOperationSelect+` WHERE o.source_id=$1 AND o.address=$2 AND o.state IN ('prepared','issued')`, source, r.Address))
		if e != nil && !errors.Is(e, sql.ErrNoRows) {
			return e
		}
		if e == nil {
			satisfied := wanted[r.Address] == (r.Fact.ID != "")
			if pending.Action == action && !satisfied {
				continue
			}
			if _, e = tx.ExecContext(ctx, `UPDATE mail.cloudflare_rule_operation SET state=CASE WHEN state='prepared' THEN 'cancelled' ELSE 'superseded' END WHERE id=$1`, pending.ID); e != nil {
				return e
			}
		}
		if wanted[r.Address] == (r.Fact.ID != "") {
			continue
		}
		id, e := mailboxID()
		if e != nil {
			return e
		}
		if _, e = tx.ExecContext(ctx, `INSERT INTO mail.cloudflare_rule_operation(id,source_id,address,route_revision,action,expected_id,expected_fingerprint) VALUES($1,$2,$3,$4,$5,NULLIF($6,''),NULLIF($7,''))`, id, source, r.Address, revision, action, r.Fact.ID, r.Fact.Fingerprint); e != nil {
			return e
		}
	}
	return tx.Commit()
}
func (s Store) CloudflareRuleBindings(ctx context.Context, source, generation string) ([]CloudflareRuleBinding, error) {
	if !anchorPattern.MatchString(source) || !anchorPattern.MatchString(generation) {
		return nil, ErrIngressChanged
	}
	rows, e := s.DB.QueryContext(ctx, cloudflareBindingSelect+` WHERE source_id=$1 ORDER BY address`, source)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []CloudflareRuleBinding{}
	for rows.Next() {
		r, e := scanCloudflareBinding(rows)
		if e != nil {
			return nil, e
		}
		if r.Generation != generation {
			return nil, ErrIngressChanged
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
func (s Store) ClaimCloudflareRule(ctx context.Context, source string) (CloudflareRuleOperation, error) {
	if !anchorPattern.MatchString(source) {
		return CloudflareRuleOperation{}, ErrIngressChanged
	}
	lease, e := mailboxID()
	if e != nil {
		return CloudflareRuleOperation{}, e
	}
	o, e := scanCloudflareOperation(s.DB.QueryRowContext(ctx, `WITH candidate AS (
 SELECT id FROM mail.cloudflare_rule_operation WHERE source_id=$1 AND state IN ('prepared','issued','superseded') AND next_attempt<=clock_timestamp()
 AND (lease_until IS NULL OR lease_until<=clock_timestamp()) ORDER BY (state='superseded'),next_attempt,created_at,id FOR UPDATE SKIP LOCKED LIMIT 1),
 claimed AS (UPDATE mail.cloudflare_rule_operation SET lease_token=$2,lease_until=clock_timestamp()+interval '2 minutes',attempts=attempts+1 WHERE id IN(SELECT id FROM candidate) RETURNING *)
	`+strings.Replace(cloudflareOperationSelect, "FROM mail.cloudflare_rule_operation o", "FROM claimed o", 1), source, lease))
	if errors.Is(e, sql.ErrNoRows) {
		return o, ErrIngressIdle
	}
	return o, ingressError(e)
}
func (s Store) IssueCloudflareRule(ctx context.Context, o CloudflareRuleOperation) error {
	if !anchorPattern.MatchString(o.ID) || !anchorPattern.MatchString(o.Lease) {
		return ErrIngressChanged
	}
	r, e := s.DB.ExecContext(ctx, `UPDATE mail.cloudflare_rule_operation SET state=CASE WHEN state='superseded' THEN state ELSE 'issued' END,issued_at=COALESCE(issued_at,clock_timestamp()) WHERE id=$1 AND lease_token=$2 AND lease_until>clock_timestamp() AND (state IN ('prepared','issued') OR state='superseded' AND action='delete')`, o.ID, o.Lease)
	if e != nil {
		return e
	}
	if n, _ := r.RowsAffected(); n != 1 {
		return ErrIngressChanged
	}
	return nil
}
func (s Store) ConfirmCloudflareRule(ctx context.Context, o CloudflareRuleOperation, result CloudflareRuleFact) error {
	if !anchorPattern.MatchString(o.ID) || !anchorPattern.MatchString(o.Lease) || !result.valid() || o.Action == "create" && result.ID == "" || o.Action == "delete" && result.ID != "" {
		return ErrIngressChanged
	}
	tx, e := s.DB.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	if e = sourceRouteLock(ctx, tx, o.SourceID); e != nil {
		return e
	}
	actual, e := scanCloudflareOperation(tx.QueryRowContext(ctx, cloudflareOperationSelect+` WHERE o.id=$1 AND o.lease_token=$2 AND o.lease_until>clock_timestamp() FOR UPDATE OF o`, o.ID, o.Lease))
	if e != nil {
		return ingressError(e)
	}
	// State may become superseded while an issued request is in flight, but
	// its result can still be recorded without granting mailbox authority.
	claimed := actual
	claimed.State = o.State
	claimed.Attempts = o.Attempts
	claimed.Error = o.Error
	if claimed != o || actual.State != "issued" && actual.State != "superseded" {
		return ErrIngressChanged
	}
	b, e := scanCloudflareBinding(tx.QueryRowContext(ctx, cloudflareBindingSelect+` WHERE source_id=$1 AND address=$2 FOR UPDATE`, o.SourceID, o.Address))
	if e != nil {
		return ingressError(e)
	}
	if b.Fact != o.Expected {
		return ErrIngressChanged
	}
	if _, e = tx.ExecContext(ctx, `UPDATE mail.cloudflare_rule SET rule_id=NULLIF($3,''),fingerprint=NULLIF($4,''),observed_at=clock_timestamp() WHERE source_id=$1 AND address=$2`, o.SourceID, o.Address, result.ID, result.Fingerprint); e != nil {
		return e
	}
	if _, e = tx.ExecContext(ctx, `UPDATE mail.cloudflare_rule_operation SET state='confirmed',result_id=NULLIF($3,''),result_fingerprint=NULLIF($4,''),last_error='',confirmed_at=clock_timestamp(),lease_token=NULL,lease_until=NULL WHERE id=$1 AND lease_token=$2`, o.ID, o.Lease, result.ID, result.Fingerprint); e != nil {
		return e
	}
	return tx.Commit()
}
func (s Store) ReleaseCloudflareRule(ctx context.Context, o CloudflareRuleOperation, code string) error {
	if !anchorPattern.MatchString(o.ID) || !anchorPattern.MatchString(o.Lease) || len(code) > 64 || strings.Trim(code, "abcdefghijklmnopqrstuvwxyz0123456789-_") != "" {
		return ErrIngressChanged
	}
	r, e := s.DB.ExecContext(ctx, `UPDATE mail.cloudflare_rule_operation SET lease_token=NULL,lease_until=NULL,last_error=$3,next_attempt=clock_timestamp()+make_interval(secs=>LEAST(300,5*attempts)) WHERE id=$1 AND lease_token=$2 AND lease_until>clock_timestamp() AND state IN ('prepared','issued','superseded')`, o.ID, o.Lease, code)
	if e != nil {
		return e
	}
	if n, _ := r.RowsAffected(); n != 1 {
		return ErrIngressChanged
	}
	return nil
}

// ObserveCloudflareRule records an exact authenticated provider readback.
// Previously unknown IDs require an issued create in this same namespace;
// a known ID/fingerprint can never be replaced by an unrecognized edit.
func (s Store) ObserveCloudflareRule(ctx context.Context, source, generation, address, worker string, fact CloudflareRuleFact) error {
	if !anchorPattern.MatchString(source) || !anchorPattern.MatchString(generation) || !fact.valid() || fact.ID == "" {
		return ErrIngressChanged
	}
	tx, e := s.DB.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	if e = sourceRouteLock(ctx, tx, source); e != nil {
		return e
	}
	b, e := scanCloudflareBinding(tx.QueryRowContext(ctx, cloudflareBindingSelect+` WHERE source_id=$1 AND address=$2 FOR UPDATE`, source, address))
	if e != nil {
		return ingressError(e)
	}
	if b.Generation != generation || b.Worker != worker || b.Fact.ID != "" && b.Fact != fact {
		return ErrIngressChanged
	}
	if b.Fact.ID == "" {
		var issued bool
		if e = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM mail.cloudflare_rule_operation WHERE source_id=$1 AND address=$2 AND action='create' AND issued_at IS NOT NULL)`, source, address).Scan(&issued); e != nil {
			return e
		}
		if !issued {
			return ErrIngressChanged
		}
	}
	if _, e = tx.ExecContext(ctx, `UPDATE mail.cloudflare_rule SET rule_id=$3,fingerprint=$4,observed_at=clock_timestamp() WHERE source_id=$1 AND address=$2`, source, address, fact.ID, fact.Fingerprint); e != nil {
		return e
	}
	// Equivalent creates are protected by the provider's unique recipient
	// constraint. Reconcile the issued operation; never authorize another box.
	if _, e = tx.ExecContext(ctx, `UPDATE mail.cloudflare_rule_operation SET state='confirmed',result_id=$3,result_fingerprint=$4,confirmed_at=clock_timestamp(),last_error='',lease_token=NULL,lease_until=NULL WHERE source_id=$1 AND address=$2 AND action='create' AND issued_at IS NOT NULL AND state IN ('issued','superseded','cancelled') AND result_id IS NULL`, source, address, fact.ID, fact.Fingerprint); e != nil {
		return e
	}
	return tx.Commit()
}

// CancelSupersededCloudflareCreate requires a permanent fence on the exact
// original source claim. A late provider result remains discoverable through
// issued_at and can only be reconciled as configuration, then cleaned up.
func (s Store) CancelSupersededCloudflareCreate(ctx context.Context, o CloudflareRuleOperation) error {
	if !anchorPattern.MatchString(o.ID) || !anchorPattern.MatchString(o.Lease) {
		return ErrIngressChanged
	}
	r, e := s.DB.ExecContext(ctx, `UPDATE mail.cloudflare_rule_operation o SET state='cancelled',last_error='superseded-source-fenced',lease_token=NULL,lease_until=NULL
 WHERE o.id=$1 AND o.lease_token=$2 AND o.lease_until>clock_timestamp() AND o.state='superseded' AND o.action='create'
 AND EXISTS(SELECT 1 FROM mail.ingress_route_operation ro CROSS JOIN LATERAL jsonb_array_elements(ro.routes) item
 JOIN mail.ingress_binding b ON b.source_id=ro.source_id AND b.generation=ro.generation AND b.address=item->>'address'
 AND b.claim_version::text=item->>'claim_version' AND b.mailbox_id::text=item->>'mailbox_id' AND b.owner_anchor=item->>'owner_anchor'
 WHERE ro.source_id=o.source_id AND ro.expected_revision=o.route_revision-1 AND ro.state='confirmed' AND b.address=o.address AND b.closed_sequence IS NOT NULL)`, o.ID, o.Lease)
	if e != nil {
		return e
	}
	if n, _ := r.RowsAffected(); n != 1 {
		return ErrIngressChanged
	}
	return nil
}

func (s Store) CloudflareRuleFailure(ctx context.Context, source, generation, code string) error {
	if !anchorPattern.MatchString(source) || !anchorPattern.MatchString(generation) || len(code) > 64 || strings.Trim(code, "abcdefghijklmnopqrstuvwxyz0123456789-_") != "" {
		return ErrIngressChanged
	}
	r, e := s.DB.ExecContext(ctx, `UPDATE mail.ingress_route_operation o SET last_error=$3 FROM mail.ingress_route_state s WHERE s.source_id=$1 AND s.generation=$2 AND o.source_id=s.source_id AND o.generation=s.generation AND o.state='confirmed' AND o.expected_revision=s.revision-1 AND ($3<>'' OR o.last_error LIKE 'cloudflare-%')`, source, generation, code)
	if e != nil {
		return e
	}
	_, e = r.RowsAffected()
	return e
}
