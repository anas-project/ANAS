package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"sort"
	"strings"
)

type IngressRoute struct {
	Address      string `json:"address"`
	MailboxID    string `json:"mailbox_id"`
	OwnerAnchor  string `json:"owner_anchor"`
	ClaimVersion string `json:"claim_version"`
}
type IngressRouteOperation struct {
	ID, SourceID, Generation, Fingerprint, State, Error string
	ExpectedRevision                                    int64
	Routes                                              []IngressRoute
}
type IngressRouteBinding struct {
	IngressBinding
	ClosedSequence *int64
}
type IngressRouteResult struct {
	SourceID, Generation, OperationID, Fingerprint string
	Revision, Sequence                             int64
	Bindings                                       []IngressRouteBinding
}

// IngressRouteSnapshot keeps previously published, unchanged claims while DMS
// or a different address operation is pending. Only confirmed local projections
// or the exact revisions just read back from DMS may introduce new claims.
// Disabled people and boxes still receive. StageSourceRoutes checks again
// under row locks before recording an operation.
func (s Store) IngressRouteSnapshot(ctx context.Context, source, generation string, ready map[string]string) ([]IngressRoute, error) {
	if !anchorPattern.MatchString(source) || !anchorPattern.MatchString(generation) {
		return nil, ErrIngressChanged
	}
	bindings, e := s.DB.QueryContext(ctx, `SELECT id FROM mail.ingress_binding WHERE source_id=$1 AND generation=$2 AND closed_sequence IS NULL`, source, generation)
	if e != nil {
		return nil, e
	}
	published := map[string]bool{}
	for bindings.Next() {
		var id string
		if e = bindings.Scan(&id); e != nil {
			bindings.Close()
			return nil, e
		}
		published[id] = true
	}
	e = bindings.Err()
	bindings.Close()
	if e != nil {
		return nil, e
	}
	rows, e := s.DB.QueryContext(ctx, projectionSelect+` ORDER BY p.mailbox_id`)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	routes := []IngressRoute{}
	for rows.Next() {
		p, e := scanProjection(rows)
		if e != nil {
			return nil, e
		}
		if p.Error == "directory-resource-missing" {
			continue
		}
		for _, a := range p.Resource.Addresses {
			b := IngressBinding{SourceID: source, Generation: generation, Address: a.Address, MailboxID: p.Mailbox.ID, OwnerAnchor: p.Mailbox.OwnerAnchor, ClaimVersion: a.Version}
			newReady := p.Resource.State != "pending" && (p.Applied || ready[p.Mailbox.ID] == p.Revision)
			if a.State == "active" && a.OperationState == "complete" && (newReady || published[b.Key()]) {
				routes = append(routes, IngressRoute{Address: a.Address, MailboxID: p.Mailbox.ID, OwnerAnchor: p.Mailbox.OwnerAnchor, ClaimVersion: a.Version})
			}
		}
	}
	return routes, rows.Err()
}

func IngressRoutesFingerprint(routes []IngressRoute) (string, []byte, error) {
	if len(routes) > 5000 {
		return "", nil, ErrIngressChanged
	}
	sorted := append([]IngressRoute{}, routes...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Address < sorted[j].Address })
	previous := ""
	for _, r := range sorted {
		a, e := primaryAddress(r.Address)
		if e != nil || a != r.Address || r.Address == previous || !anchorPattern.MatchString(r.MailboxID) || !anchorPattern.MatchString(r.OwnerAnchor) || !anchorPattern.MatchString(r.ClaimVersion) {
			return "", nil, ErrIngressChanged
		}
		previous = r.Address
	}
	raw, e := json.Marshal(sorted)
	if e != nil {
		return "", nil, e
	}
	return ingressHash(string(raw)), raw, nil
}

const ingressOperationSelect = `SELECT id,source_id,generation,expected_revision,fingerprint,routes,state,last_error FROM mail.ingress_route_operation`

func scanIngressOperation(row projectionScanner) (IngressRouteOperation, error) {
	var op IngressRouteOperation
	var raw []byte
	e := row.Scan(&op.ID, &op.SourceID, &op.Generation, &op.ExpectedRevision, &op.Fingerprint, &raw, &op.State, &op.Error)
	if e != nil {
		return op, e
	}
	if json.Unmarshal(raw, &op.Routes) != nil {
		return IngressRouteOperation{}, ErrIngressChanged
	}
	fp, _, e := IngressRoutesFingerprint(op.Routes)
	if e != nil || fp != op.Fingerprint {
		return IngressRouteOperation{}, ErrIngressChanged
	}
	return op, nil
}
func sourceRouteLock(ctx context.Context, tx *sql.Tx, source string) error {
	_, e := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "anas-mail-source-routes:"+source)
	return e
}

// StageSourceRoutes persists desired state and its old cloud revision before
// any network mutation. ready identifies only pending projections whose DMS
// readback the trusted coordinator has just confirmed. This grants receiving,
// never mailbox login. Existing issued intents remain as superseded history.
func (s Store) StageSourceRoutes(ctx context.Context, source, generation string, routes []IngressRoute, ready map[string]string) (IngressRouteOperation, error) {
	fp, raw, e := IngressRoutesFingerprint(routes)
	if e != nil || !anchorPattern.MatchString(source) || !anchorPattern.MatchString(generation) {
		return IngressRouteOperation{}, ErrIngressChanged
	}
	tx, e := s.DB.BeginTx(ctx, nil)
	if e != nil {
		return IngressRouteOperation{}, e
	}
	defer tx.Rollback()
	if e = sourceRouteLock(ctx, tx, source); e != nil {
		return IngressRouteOperation{}, e
	}
	empty, _, _ := IngressRoutesFingerprint(nil)
	if _, e = tx.ExecContext(ctx, `INSERT INTO mail.ingress_route_state(source_id,generation,fingerprint) VALUES($1,$2,$3) ON CONFLICT DO NOTHING`, source, generation, empty); e != nil {
		return IngressRouteOperation{}, e
	}
	var actualGeneration string
	var revision int64
	if e = tx.QueryRowContext(ctx, `SELECT generation,revision FROM mail.ingress_route_state WHERE source_id=$1 FOR UPDATE`, source).Scan(&actualGeneration, &revision); e != nil {
		return IngressRouteOperation{}, e
	}
	if actualGeneration != generation || revision >= 9007199254740991 {
		return IngressRouteOperation{}, ErrIngressChanged
	}
	boxIDs := []string{}
	seen := map[string]bool{}
	for _, r := range routes {
		if !seen[r.MailboxID] {
			boxIDs = append(boxIDs, r.MailboxID)
			seen[r.MailboxID] = true
		}
	}
	sort.Strings(boxIDs)
	projections := map[string]Projection{}
	for _, id := range boxIDs {
		var locked string
		if e = tx.QueryRowContext(ctx, `SELECT id FROM mail.mailbox WHERE id=$1 FOR SHARE`, id).Scan(&locked); e != nil {
			return IngressRouteOperation{}, ingressError(e)
		}
		if e = tx.QueryRowContext(ctx, `SELECT mailbox_id FROM mail.mailbox_projection WHERE mailbox_id=$1 FOR SHARE`, id).Scan(&locked); e != nil {
			return IngressRouteOperation{}, ingressError(e)
		}
		p, err := scanProjection(tx.QueryRowContext(ctx, projectionSelect+` WHERE p.mailbox_id=$1`, id))
		if err != nil {
			return IngressRouteOperation{}, ingressError(err)
		}
		if p.Error == "directory-resource-missing" {
			return IngressRouteOperation{}, ErrIngressChanged
		}
		projections[id] = p
	}
	for _, r := range routes {
		p := projections[r.MailboxID]
		if p.Mailbox.OwnerAnchor != r.OwnerAnchor {
			return IngressRouteOperation{}, ErrIngressChanged
		}
		found := false
		for _, a := range p.Resource.Addresses {
			if a.Address == r.Address && a.Version == r.ClaimVersion && a.State == "active" && a.OperationState == "complete" {
				found = true
			}
		}
		if !found {
			return IngressRouteOperation{}, ErrIngressChanged
		}
		if p.Resource.State == "pending" || !p.Applied && ready[p.Mailbox.ID] != p.Revision {
			b := IngressBinding{SourceID: source, Generation: generation, Address: r.Address, MailboxID: r.MailboxID, OwnerAnchor: r.OwnerAnchor, ClaimVersion: r.ClaimVersion}
			var published bool
			if e = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM mail.ingress_binding WHERE id=$1 AND closed_sequence IS NULL)`, b.Key()).Scan(&published); e != nil {
				return IngressRouteOperation{}, e
			}
			if !published {
				return IngressRouteOperation{}, ErrIngressChanged
			}
		}
	}
	previous, err := scanIngressOperation(tx.QueryRowContext(ctx, ingressOperationSelect+` WHERE source_id=$1 AND state IN ('prepared','issued')`, source))
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return IngressRouteOperation{}, err
	}
	if err == nil && previous.Generation == generation && previous.ExpectedRevision == revision && previous.Fingerprint == fp {
		return previous, tx.Commit()
	}
	if err == nil {
		if _, e = tx.ExecContext(ctx, `UPDATE mail.ingress_route_operation SET state=CASE WHEN state='prepared' THEN 'cancelled' ELSE 'superseded' END WHERE id=$1`, previous.ID); e != nil {
			return IngressRouteOperation{}, e
		}
	}
	// An earlier issued request may still commit after its caller timed out.
	// Even when returning to the known fingerprint, seal the old revision with
	// a new CAS operation rather than treating this as an unchanged route set.
	var uncertain bool
	if e = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM mail.ingress_route_operation WHERE source_id=$1 AND expected_revision=$2 AND state IN ('issued','superseded'))`, source, revision).Scan(&uncertain); e != nil {
		return IngressRouteOperation{}, e
	}
	// A matching confirmed operation is reused only at the currently known
	// revision. A newer unrecognized cloud revision can never be adopted here.
	confirmed, err := scanIngressOperation(tx.QueryRowContext(ctx, ingressOperationSelect+` WHERE source_id=$1 AND generation=$2 AND state='confirmed' AND expected_revision=$3-1 AND fingerprint=$4`, source, generation, revision, fp))
	if err == nil && !uncertain {
		return confirmed, tx.Commit()
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return IngressRouteOperation{}, err
	}
	id, e := mailboxID()
	if e != nil {
		return IngressRouteOperation{}, e
	}
	_, e = tx.ExecContext(ctx, `INSERT INTO mail.ingress_route_operation(id,source_id,generation,expected_revision,fingerprint,routes) VALUES($1,$2,$3,$4,$5,$6)`, id, source, generation, revision, fp, raw)
	if e != nil {
		return IngressRouteOperation{}, e
	}
	op, e := scanIngressOperation(tx.QueryRowContext(ctx, ingressOperationSelect+` WHERE id=$1`, id))
	if e != nil {
		return op, e
	}
	return op, tx.Commit()
}
func (s Store) IssueSourceRoutes(ctx context.Context, op IngressRouteOperation) error {
	if !anchorPattern.MatchString(op.ID) || !anchorPattern.MatchString(op.SourceID) {
		return ErrIngressChanged
	}
	r, e := s.DB.ExecContext(ctx, `UPDATE mail.ingress_route_operation SET state='issued' WHERE id=$1 AND source_id=$2 AND generation=$3 AND expected_revision=$4 AND fingerprint=$5 AND state IN ('prepared','issued')`, op.ID, op.SourceID, op.Generation, op.ExpectedRevision, op.Fingerprint)
	if e != nil {
		return e
	}
	if count, _ := r.RowsAffected(); count != 1 {
		return ErrIngressChanged
	}
	return nil
}
func (s Store) SourceRoutesFailed(ctx context.Context, op IngressRouteOperation, code string) error {
	if !anchorPattern.MatchString(op.ID) || len(code) > 64 || strings.Trim(code, "abcdefghijklmnopqrstuvwxyz0123456789-_") != "" {
		return ErrIngressChanged
	}
	r, e := s.DB.ExecContext(ctx, `UPDATE mail.ingress_route_operation SET last_error=$2 WHERE id=$1 AND state IN ('prepared','issued','superseded','confirmed')`, op.ID, code)
	if e != nil {
		return e
	}
	if count, _ := r.RowsAffected(); count != 1 {
		return ErrIngressChanged
	}
	return nil
}

// ObserveSourceRoutes accepts only the current state or an exact issued
// operation advancing one known revision. Unknown future/rolled-back state
// blocks; it cannot be read and used as an overwrite precondition.
func (s Store) ObserveSourceRoutes(ctx context.Context, result IngressRouteResult) error {
	if !anchorPattern.MatchString(result.SourceID) || !anchorPattern.MatchString(result.Generation) || result.Revision < 0 || result.Revision > 9007199254740991 || result.Sequence < 0 || result.Sequence > 9007199254740991 || len(result.Bindings) > 5000 {
		return ErrIngressChanged
	}
	active := []IngressRoute{}
	ids := map[string]bool{}
	for _, b := range result.Bindings {
		if !b.IngressBinding.valid() || b.SourceID != result.SourceID || b.Generation != result.Generation || b.FirstSequence > result.Sequence+1 || b.ClosedSequence != nil && (*b.ClosedSequence < b.FirstSequence-1 || *b.ClosedSequence > result.Sequence) || ids[b.Key()] {
			return ErrIngressChanged
		}
		ids[b.Key()] = true
		if b.ClosedSequence == nil {
			active = append(active, IngressRoute{Address: b.Address, MailboxID: b.MailboxID, OwnerAnchor: b.OwnerAnchor, ClaimVersion: b.ClaimVersion})
		}
	}
	fp, _, e := IngressRoutesFingerprint(active)
	if e != nil || fp != result.Fingerprint {
		return ErrIngressChanged
	}
	tx, e := s.DB.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	if e = sourceRouteLock(ctx, tx, result.SourceID); e != nil {
		return e
	}
	var generation, knownFP string
	var knownRevision, sequence int64
	if e = tx.QueryRowContext(ctx, `SELECT generation,revision,fingerprint,sequence FROM mail.ingress_route_state WHERE source_id=$1 FOR UPDATE`, result.SourceID).Scan(&generation, &knownRevision, &knownFP, &sequence); e != nil {
		return ingressError(e)
	}
	if generation != result.Generation || result.Sequence < sequence {
		return ErrIngressChanged
	}
	var op IngressRouteOperation
	if result.Revision == knownRevision {
		if result.Fingerprint != knownFP {
			return ErrIngressChanged
		}
	} else {
		if result.Revision != knownRevision+1 || !anchorPattern.MatchString(result.OperationID) {
			return ErrIngressChanged
		}
		op, e = scanIngressOperation(tx.QueryRowContext(ctx, ingressOperationSelect+` WHERE id=$1 AND source_id=$2 AND generation=$3`, result.OperationID, result.SourceID, result.Generation))
		if e != nil {
			return ingressError(e)
		}
		if op.ExpectedRevision != knownRevision || op.Fingerprint != result.Fingerprint || op.State != "issued" && op.State != "superseded" {
			return ErrIngressChanged
		}
	}
	rows, e := tx.QueryContext(ctx, `SELECT id,first_sequence,closed_sequence FROM mail.ingress_binding WHERE source_id=$1 AND generation=$2 FOR UPDATE`, result.SourceID, result.Generation)
	if e != nil {
		return e
	}
	type existingBinding struct {
		first  int64
		closed sql.NullInt64
	}
	existing := map[string]existingBinding{}
	for rows.Next() {
		var id string
		var b existingBinding
		if e = rows.Scan(&id, &b.first, &b.closed); e != nil {
			rows.Close()
			return e
		}
		existing[id] = b
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return e
	}
	for id := range existing {
		if !ids[id] {
			return ErrIngressChanged
		}
	}
	// Close known permissions before inserting newly opened addresses so the
	// unique current-address index prevents overlap, including after transfer.
	for _, b := range result.Bindings {
		previous, known := existing[b.Key()]
		if !known {
			continue
		}
		if previous.first != b.FirstSequence || previous.closed.Valid && (b.ClosedSequence == nil || previous.closed.Int64 != *b.ClosedSequence) {
			return ErrIngressChanged
		}
		if b.ClosedSequence != nil {
			if _, e = tx.ExecContext(ctx, `UPDATE mail.ingress_binding SET closed_sequence=$2 WHERE id=$1`, b.Key(), *b.ClosedSequence); e != nil {
				return ingressError(e)
			}
		}
	}
	for _, b := range result.Bindings {
		if _, known := existing[b.Key()]; known {
			continue
		}
		if op.ID == "" || b.ClosedSequence != nil {
			return ErrIngressChanged
		}
		authorized := false
		for _, r := range op.Routes {
			if r.Address == b.Address && r.MailboxID == b.MailboxID && r.OwnerAnchor == b.OwnerAnchor && r.ClaimVersion == b.ClaimVersion {
				authorized = true
			}
		}
		if !authorized {
			return ErrIngressChanged
		}
		if _, e = tx.ExecContext(ctx, `INSERT INTO mail.ingress_binding(id,source_id,generation,address,claim_version,mailbox_id,owner_anchor,first_sequence) VALUES($1,$2,$3,$4,$5,$6,$7,$8)`, b.Key(), b.SourceID, b.Generation, b.Address, b.ClaimVersion, b.MailboxID, b.OwnerAnchor, b.FirstSequence); e != nil {
			return ingressError(e)
		}
	}
	if op.ID != "" {
		if _, e = tx.ExecContext(ctx, `UPDATE mail.ingress_route_operation SET state='confirmed',last_error='',confirmed_at=clock_timestamp() WHERE id=$1`, op.ID); e != nil {
			return e
		}
		if _, e = tx.ExecContext(ctx, `UPDATE mail.ingress_route_operation SET state=CASE WHEN state='prepared' THEN 'cancelled' ELSE 'superseded' END WHERE source_id=$1 AND expected_revision<$2 AND state IN ('prepared','issued')`, result.SourceID, result.Revision); e != nil {
			return e
		}
	}
	if _, e = tx.ExecContext(ctx, `UPDATE mail.ingress_route_operation SET last_error='' WHERE source_id=$1 AND state='confirmed' AND expected_revision=$2-1`, result.SourceID, result.Revision); e != nil {
		return e
	}
	if _, e = tx.ExecContext(ctx, `UPDATE mail.ingress_route_state SET revision=$2,fingerprint=$3,sequence=$4,observed_at=clock_timestamp() WHERE source_id=$1`, result.SourceID, result.Revision, result.Fingerprint, result.Sequence); e != nil {
		return e
	}
	return tx.Commit()
}
