package store

import (
	"context"
	"database/sql"
	"errors"
	"sort"

	"github.com/anas-project/ANAS/internal/directorymail"
	"github.com/anas-project/ANAS/modules/mail/identity"
)

// A transfer locks the reservation fence first, then both owners in stable
// order. Ordinary projection writers keep their existing single-owner lock.
func lockProjectionOwners(ctx context.Context, tx *sql.Tx, resource identity.MailboxResource) error {
	owners := map[string]bool{resource.OwnerAnchor: true}
	transfer := false
	for _, address := range resource.Addresses {
		if address.TransferRecord == "" {
			continue
		}
		record, err := directorymail.ParseTransferRecord(address.TransferRecord)
		if err != nil {
			return ErrAddressUnavailable
		}
		owners[record.FromOwner] = true
		transfer = true
	}
	if transfer {
		if err := ingressReservationLock(ctx, tx); err != nil {
			return err
		}
	}
	ordered := make([]string, 0, len(owners))
	for owner := range owners {
		ordered = append(ordered, owner)
	}
	sort.Strings(ordered)
	for _, owner := range ordered {
		if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "anas-mail-owner:"+owner); err != nil {
			return err
		}
	}
	return nil
}

// The directory record alone is never authority. Only the exact durable Go
// proof can move a retained non-default address to its prepared primary box.
// Proof consumption and the address foreign key commit in the same transaction
// as the new, still-unconfirmed projection. Neither mailbox's stable keys move.
func consumeAddressWithdrawal(ctx context.Context, tx *sql.Tx, resource identity.MailboxResource, address identity.AddressResource) error {
	record, err := directorymail.ParseTransferRecord(address.TransferRecord)
	if err != nil || resource.Kind != "primary" || address.Kind != "primary" || address.Address != resource.DefaultAddress || record.ToOwner != resource.OwnerAnchor || record.ToMailbox != resource.ID || record.Address != address.Address {
		return ErrAddressUnavailable
	}
	var body []byte
	var applied sql.NullString
	var consumed bool
	var epoch int64
	err = tx.QueryRowContext(ctx, `SELECT record,applied_to::text,consumed_at IS NOT NULL,authorization_epoch FROM mail.address_transfer_proof WHERE id=$1 FOR UPDATE`, record.ProofID).Scan(&body, &applied, &consumed, &epoch)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrAddressUnavailable
	}
	if err != nil {
		return err
	}
	known, err := directorymail.ParseTransferRecord(string(body))
	if err != nil || known != record {
		return ErrAddressUnavailable
	}
	if consumed {
		var matches bool
		if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM mail.address WHERE address=$1 AND mailbox_id=$2 AND transfer_proof_id=$3)`, record.Address, resource.ID, record.ProofID).Scan(&matches); err != nil {
			return err
		}
		if !applied.Valid || applied.String != resource.ID || !matches {
			return ErrAddressUnavailable
		}
		return nil
	}
	if applied.Valid || resource.OperationID != record.ToCreationOperation {
		return ErrAddressUnavailable
	}
	old, err := scanProjection(tx.QueryRowContext(ctx, projectionSelect+` WHERE p.mailbox_id=$1 FOR SHARE OF m,p`, record.FromMailbox))
	if err != nil {
		return err
	}
	if old.Mailbox.OwnerAnchor != record.FromOwner || old.Epoch < epoch || old.Resource.DefaultAddress != record.RemainingDefault {
		return ErrAddressUnavailable
	}
	// The old directory projection can already have removed the moved claim.
	// If it still contains it, it must be the exact retired claim, never a new
	// active grant or a later unrelated address version.
	for _, previous := range old.Resource.Addresses {
		if previous.Address == record.Address && (previous.Version != record.FromClaimVersion || previous.State != "retained") {
			return ErrAddressUnavailable
		}
	}
	var owner string
	var retained bool
	err = tx.QueryRowContext(ctx, `SELECT mailbox_id,state='retained' AND NOT is_default AND transfer_proof_id IS NULL FROM mail.address WHERE address=$1 FOR UPDATE`, record.Address).Scan(&owner, &retained)
	if err != nil {
		return err
	}
	if owner != record.FromMailbox || !retained {
		return ErrAddressUnavailable
	}
	if _, err = tx.ExecContext(ctx, `UPDATE mail.address_transfer_proof SET applied_to=$2,consumed_at=clock_timestamp() WHERE id=$1`, record.ProofID, resource.ID); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `UPDATE mail.address SET mailbox_id=$2,kind='primary',is_default=true,state='pending',transfer_proof_id=$3 WHERE address=$1`, record.Address, resource.ID, record.ProofID)
	return err
}
