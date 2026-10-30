package store

import (
	"context"
	"database/sql"
	"errors"
)

var ErrIngressReservation = errors.New("mail source reservation requires reconciliation")

// IngressReservation contains public source identity and reserved space only.
// Active and draining instances both remain present until explicit retirement
// has independently proved their routes, pending work and retained mail safe.
type IngressReservation struct {
	SourceID, Generation, IdentityFingerprint string
	CapacityBytes                             int64
}

func ingressReservationLock(ctx context.Context, tx *sql.Tx) error {
	_, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('anas-mail-ingress-reservations',0))`)
	return err
}

// ReserveIngressSources prevents a new config/restart from forgetting a known
// source, shrinking its reservation or reusing its identity. It never polls or
// deletes cloud mail. Retirement/shrink proof is intentionally a separate path;
// absence or an unreachable provider cannot act as that proof.
func (s Store) ReserveIngressSources(ctx context.Context, total int64, sources []IngressReservation) error {
	if total < 0 || total > 9007199254740991 || len(sources) > 8 {
		return ErrIngressReservation
	}
	byID := map[string]IngressReservation{}
	remaining := total
	for _, r := range sources {
		if !anchorPattern.MatchString(r.SourceID) || !anchorPattern.MatchString(r.Generation) || !ingressDigest.MatchString(r.IdentityFingerprint) || r.CapacityBytes < 1 || r.CapacityBytes > remaining {
			return ErrIngressReservation
		}
		if _, duplicate := byID[r.SourceID]; duplicate {
			return ErrIngressReservation
		}
		byID[r.SourceID] = r
		remaining -= r.CapacityBytes
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = ingressReservationLock(ctx, tx); err != nil {
		return err
	}
	// Old versions may have accepted work before a route-state row existed.
	// Include every persisted source, not only currently configured instances.
	rows, err := tx.QueryContext(ctx, `SELECT source_id,generation FROM mail.ingress_route_state
	 UNION SELECT source_id,generation FROM mail.ingress_binding
	 UNION SELECT source_id,generation FROM mail.ingress_poll
	 UNION SELECT source_id,generation FROM mail.ingress_notice`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var id, generation string
		if err = rows.Scan(&id, &generation); err != nil {
			rows.Close()
			return err
		}
		r, present := byID[id]
		if !present || r.Generation != generation {
			rows.Close()
			return ErrIngressReservation
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	empty, _, _ := IngressRoutesFingerprint(nil)
	for _, r := range sources {
		result, err := tx.ExecContext(ctx, `INSERT INTO mail.ingress_route_state(source_id,generation,fingerprint,reserved_capacity_bytes,source_identity_fingerprint)
	 VALUES($1,$2,$3,$4,$5) ON CONFLICT(source_id) DO UPDATE
	 SET reserved_capacity_bytes=EXCLUDED.reserved_capacity_bytes,source_identity_fingerprint=EXCLUDED.source_identity_fingerprint
	 WHERE ingress_route_state.generation=EXCLUDED.generation
	 AND ingress_route_state.reserved_capacity_bytes<=EXCLUDED.reserved_capacity_bytes
	 AND (ingress_route_state.source_identity_fingerprint IS NULL OR ingress_route_state.source_identity_fingerprint=EXCLUDED.source_identity_fingerprint)`, r.SourceID, r.Generation, empty, r.CapacityBytes, r.IdentityFingerprint)
		if err != nil {
			return err
		}
		if n, err := result.RowsAffected(); err != nil {
			return err
		} else if n != 1 {
			return ErrIngressReservation
		}
	}
	return tx.Commit()
}
