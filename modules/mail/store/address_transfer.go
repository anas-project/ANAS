package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"sort"

	"github.com/anas-project/ANAS/internal/directorymail"
)

var ErrAddressTransfer = errors.New("mail address withdrawal is not confirmed")

// VerifiedAddressWithdrawal is constructed by trusted Go coordination only,
// never decoded from an HTTP request. Its caller must freshly verify the
// administrator, AD eligibility/pending target and exact DMS receipt, then read
// all sources and public rules. Store additionally checks the durable facts.
type VerifiedAddressWithdrawal struct {
	MailboxID, Revision, Fingerprint string
	AuthorizationEpoch               int64
	Sources                          []VerifiedWithdrawalSource
}
type VerifiedWithdrawalSource struct {
	State                IngressRouteResult
	IdentityFingerprint  string
	PublicRuleAbsent     bool
	RemainingDefaultRule CloudflareRuleFact
}

type withdrawalSourceVersion struct {
	SourceID, Generation, Fingerprint, IdentityFingerprint string
	Revision, Sequence                                     int64
	ClosedBindings                                         []IngressRouteBinding
	RemainingDefaultRule                                   CloudflareRuleFact
}

// RecordAddressWithdrawal persists a replayable proof, not an address grant.
// The caller supplies no proof UUID. Old addresses and mailbox keys stay put.
// An identical operation reuses its original proof after a lost reply.
func (s Store) RecordAddressWithdrawal(ctx context.Context, record directorymail.TransferRecord, verified VerifiedAddressWithdrawal) (directorymail.TransferRecord, error) {
	if record.ProofID != "" {
		return directorymail.TransferRecord{}, ErrAddressTransfer
	}
	id, err := mailboxID()
	if err != nil {
		return directorymail.TransferRecord{}, err
	}
	record.ProofID = id
	if !record.Valid() || verified.MailboxID != record.FromMailbox || !anchorPattern.MatchString(verified.Revision) || !ingressDigest.MatchString(verified.Fingerprint) || verified.AuthorizationEpoch < 1 || len(verified.Sources) < 1 || len(verified.Sources) > 8 {
		return directorymail.TransferRecord{}, ErrAddressTransfer
	}
	sources := map[string]VerifiedWithdrawalSource{}
	for _, observation := range verified.Sources {
		state := observation.State
		if !observation.PublicRuleAbsent || !ingressDigest.MatchString(observation.IdentityFingerprint) || !anchorPattern.MatchString(state.SourceID) || !anchorPattern.MatchString(state.Generation) {
			return directorymail.TransferRecord{}, ErrAddressTransfer
		}
		if _, duplicate := sources[state.SourceID]; duplicate {
			return directorymail.TransferRecord{}, ErrAddressTransfer
		}
		if state.Revision > 0 && !anchorPattern.MatchString(state.OperationID) || !observation.RemainingDefaultRule.valid() {
			return directorymail.TransferRecord{}, ErrAddressTransfer
		}
		for _, binding := range state.Bindings {
			if binding.Address == record.Address && binding.ClosedSequence == nil {
				return directorymail.TransferRecord{}, ErrAddressTransfer
			}
		}
		sources[state.SourceID] = observation
	}
	// Known observations may advance their local sequence, but cannot adopt an
	// unknown revision or reopen a closed binding. No cloud mutation occurs.
	for _, observation := range verified.Sources {
		if err = s.ObserveSourceRoutes(ctx, observation.State); err != nil {
			return directorymail.TransferRecord{}, err
		}
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return directorymail.TransferRecord{}, err
	}
	defer tx.Rollback()
	if err = ingressReservationLock(ctx, tx); err != nil {
		return directorymail.TransferRecord{}, err
	}
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "anas-mail-owner:"+record.FromOwner); err != nil {
		return directorymail.TransferRecord{}, err
	}
	p, err := scanProjection(tx.QueryRowContext(ctx, projectionSelect+` WHERE p.mailbox_id=$1 FOR SHARE OF m,p`, record.FromMailbox))
	if err != nil {
		return directorymail.TransferRecord{}, err
	}
	if !p.Applied || p.Mailbox.OwnerAnchor != record.FromOwner || record.FromKind == "additional" && p.Resource.Kind != "additional" || p.Resource.Version != record.FromMailboxVersion || p.Resource.DefaultAddress != record.RemainingDefault || p.Revision != verified.Revision || p.Fingerprint != verified.Fingerprint || p.Epoch != verified.AuthorizationEpoch {
		return directorymail.TransferRecord{}, ErrAddressTransfer
	}
	retained := false
	for _, address := range p.Resource.Addresses {
		if address.Address == record.Address && address.Version == record.FromClaimVersion && address.State == "retained" && address.Kind == "alias" && address.OperationState == "complete" {
			retained = true
		}
	}
	if !retained {
		return directorymail.TransferRecord{}, ErrAddressTransfer
	}
	var eligible bool
	if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM mail.address WHERE address=$1 AND mailbox_id=$2 AND state='retained' AND NOT is_default)
	 AND NOT EXISTS(SELECT 1 FROM mail.revocation_task t JOIN mail.web_session s ON s.id=t.session_id
	 WHERE t.completed_at IS NULL AND (t.mailbox_id=$2 OR t.mailbox_id IS NULL AND s.owner_anchor=$3))
	 AND NOT EXISTS(SELECT 1 FROM mail.person WHERE anchor=$4)
	 AND NOT EXISTS(SELECT 1 FROM mail.mailbox WHERE id=$5)`, record.Address, record.FromMailbox, record.FromOwner, record.ToOwner, record.ToMailbox).Scan(&eligible); err != nil || !eligible {
		if err != nil {
			return directorymail.TransferRecord{}, err
		}
		return directorymail.TransferRecord{}, ErrAddressTransfer
	}
	defaultVersion := ""
	for _, address := range p.Resource.Addresses {
		if address.Address == record.RemainingDefault && address.State == "active" && address.OperationState == "complete" {
			defaultVersion = address.Version
		}
	}
	if defaultVersion == "" {
		return directorymail.TransferRecord{}, ErrAddressTransfer
	}
	if err = verifyWithdrawalSources(ctx, tx, record, defaultVersion, sources); err != nil {
		return directorymail.TransferRecord{}, err
	}
	// Reconstruct only an identical operation, never substitute a new target
	// while keeping an old claim-version proof or idempotency key.
	var prior []byte
	err = tx.QueryRowContext(ctx, `SELECT record FROM mail.address_transfer_proof WHERE operation_id=$1 OR(address=$2 AND from_claim_version=$3) FOR SHARE`, record.OperationID, record.Address, record.FromClaimVersion).Scan(&prior)
	if err == nil {
		known, parseErr := directorymail.ParseTransferRecord(string(prior))
		if parseErr != nil {
			return directorymail.TransferRecord{}, ErrAddressTransfer
		}
		record.ProofID = known.ProofID
		if record != known {
			return directorymail.TransferRecord{}, ErrAddressTransfer
		}
		return known, tx.Commit()
	} else if !errors.Is(err, sql.ErrNoRows) {
		return directorymail.TransferRecord{}, err
	}
	body, _ := json.Marshal(record)
	versions := make([]withdrawalSourceVersion, 0, len(verified.Sources))
	for _, observation := range verified.Sources {
		state := observation.State
		version := withdrawalSourceVersion{SourceID: state.SourceID, Generation: state.Generation, Fingerprint: state.Fingerprint, IdentityFingerprint: observation.IdentityFingerprint, Revision: state.Revision, Sequence: state.Sequence, ClosedBindings: []IngressRouteBinding{}, RemainingDefaultRule: observation.RemainingDefaultRule}
		for _, binding := range state.Bindings {
			if binding.Address == record.Address {
				version.ClosedBindings = append(version.ClosedBindings, binding)
			}
		}
		versions = append(versions, version)
	}
	sort.Slice(versions, func(i, j int) bool { return versions[i].SourceID < versions[j].SourceID })
	versionJSON, _ := json.Marshal(versions)
	if _, err = tx.ExecContext(ctx, `INSERT INTO mail.address_transfer_proof(id,operation_id,address,from_owner,from_mailbox,from_claim_version,to_owner,to_mailbox,record,authorization_epoch,projection_fingerprint,source_versions)
	 VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)`, record.ProofID, record.OperationID, record.Address, record.FromOwner, record.FromMailbox, record.FromClaimVersion, record.ToOwner, record.ToMailbox, body, p.Epoch, p.Fingerprint, versionJSON); err != nil {
		return directorymail.TransferRecord{}, err
	}
	return record, tx.Commit()
}

func verifyWithdrawalSources(ctx context.Context, tx *sql.Tx, record directorymail.TransferRecord, defaultVersion string, observations map[string]VerifiedWithdrawalSource) error {
	rows, err := tx.QueryContext(ctx, `SELECT source_id,generation,revision,fingerprint,COALESCE(source_identity_fingerprint,''),reserved_capacity_bytes FROM mail.ingress_route_state ORDER BY source_id FOR SHARE`)
	if err != nil {
		return err
	}
	count := 0
	for rows.Next() {
		var id, generation, fingerprint, identity string
		var revision, capacity int64
		if err = rows.Scan(&id, &generation, &revision, &fingerprint, &identity, &capacity); err != nil {
			rows.Close()
			return err
		}
		observed, present := observations[id]
		if !present || observed.State.Generation != generation || observed.State.Revision != revision || observed.State.Fingerprint != fingerprint || observed.IdentityFingerprint != identity || capacity < 1 {
			rows.Close()
			return ErrAddressTransfer
		}
		count++
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	if count != len(observations) {
		return ErrAddressTransfer
	}
	defaultPublished := false
	for _, observation := range observations {
		state := observation.State
		if state.Revision == 0 {
			if state.OperationID != "" {
				return ErrAddressTransfer
			}
			continue
		}
		var recognized bool
		if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM mail.ingress_route_operation
	 WHERE id=$1 AND source_id=$2 AND generation=$3 AND expected_revision=$4::bigint-1 AND fingerprint=$5 AND state='confirmed')`, state.OperationID, state.SourceID, state.Generation, state.Revision, state.Fingerprint).Scan(&recognized); err != nil {
			return err
		}
		if !recognized {
			return ErrAddressTransfer
		}
		for _, binding := range state.Bindings {
			if binding.Address == record.RemainingDefault && binding.OwnerAnchor == record.FromOwner && binding.MailboxID == record.FromMailbox && binding.ClaimVersion == defaultVersion && binding.ClosedSequence == nil && observation.RemainingDefaultRule.ID != "" {
				var known bool
				if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM mail.cloudflare_rule WHERE source_id=$1 AND generation=$2 AND address=$3 AND rule_id=$4 AND fingerprint=$5 AND observed_at IS NOT NULL)
				 OR EXISTS(SELECT 1 FROM mail.forward_rule r JOIN mail.forward_source s ON s.source_id=r.source_id
				 JOIN mail.ingress_binding b ON b.id=r.binding_id AND b.source_id=r.source_id
				 WHERE r.source_id=$1 AND s.generation=$2 AND b.generation=$2 AND r.address=$3 AND b.address=$3
				 AND r.rule_id=$4 AND r.fingerprint=$5 AND r.state='created' AND b.closed_sequence IS NULL
				 AND b.owner_anchor=$6 AND b.mailbox_id=$7 AND b.claim_version=$8)`, state.SourceID, state.Generation, record.RemainingDefault, observation.RemainingDefaultRule.ID, observation.RemainingDefaultRule.Fingerprint, record.FromOwner, record.FromMailbox, defaultVersion).Scan(&known); err != nil {
					return err
				}
				defaultPublished = defaultPublished || known
			}
		}
	}
	if !defaultPublished {
		return ErrAddressTransfer
	}
	var unsettled bool
	err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM mail.ingress_binding WHERE address=$1 AND closed_sequence IS NULL)
	 OR EXISTS(SELECT 1 FROM mail.ingress_route_operation o JOIN mail.ingress_route_state s USING(source_id)
	 WHERE o.state IN('prepared','issued','superseded') AND o.expected_revision>=s.revision)
	 OR EXISTS(SELECT 1 FROM mail.cloudflare_rule WHERE address=$1 AND rule_id IS NOT NULL)
	 OR EXISTS(SELECT 1 FROM mail.cloudflare_rule_operation WHERE address=$1 AND state IN('prepared','issued','superseded'))
	 OR EXISTS(SELECT 1 FROM mail.forward_rule WHERE address=$1 AND state NOT IN('removed','cancelled'))`, record.Address).Scan(&unsettled)
	if err != nil {
		return err
	}
	if unsettled {
		return ErrAddressTransfer
	}
	return nil
}
