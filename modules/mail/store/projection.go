package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sort"
	"strings"

	"github.com/anas-project/ANAS/internal/directorymail"
	"github.com/anas-project/ANAS/modules/mail/identity"
	"github.com/jackc/pgx/v5/pgconn"
)

var ErrProjectionConflict = errors.New("mailbox projection changed or is inconsistent")

// A projection is local configuration work, never an authentication grant.
// The caller supplies a freshly validated AD resource and a local revision
// precondition. Downstream confirmation requires the same fresh fingerprint.
type Projection struct {
	Mailbox     Mailbox                  `json:"mailbox"`
	Revision    string                   `json:"revision"`
	Fingerprint string                   `json:"fingerprint"`
	Epoch       int64                    `json:"authorization_epoch"`
	Resource    identity.MailboxResource `json:"resource"`
	Applied     bool                     `json:"applied"`
	Error       string                   `json:"error,omitempty"`
}
type projectionSnapshot struct {
	Schema   string                   `json:"schema"`
	Resource identity.MailboxResource `json:"resource"`
}

func ProjectionFingerprint(resource identity.MailboxResource) (string, []byte, error) {
	if !anchorPattern.MatchString(resource.ID) || !anchorPattern.MatchString(resource.OwnerAnchor) || !anchorPattern.MatchString(resource.Version) || !anchorPattern.MatchString(resource.OperationID) || resource.Kind != "primary" && resource.Kind != "additional" || resource.State != "active" && resource.State != "disabled" && resource.State != "retained" && resource.State != "pending" || resource.State != "pending" && resource.OperationState != "complete" || len(resource.Addresses) > 5000 {
		return "", nil, ErrProjectionConflict
	}
	canonical, err := primaryAddress(resource.DefaultAddress)
	if err != nil || canonical != resource.DefaultAddress {
		return "", nil, ErrProjectionConflict
	}
	resource.Addresses = append([]identity.AddressResource(nil), resource.Addresses...)
	sort.Slice(resource.Addresses, func(i, j int) bool { return resource.Addresses[i].Address < resource.Addresses[j].Address })
	seen := map[string]bool{}
	defaultActive := false
	for _, a := range resource.Addresses {
		canonical, err := primaryAddress(a.Address)
		if err != nil || a.Address != canonical || seen[a.Address] || !anchorPattern.MatchString(a.Version) || !anchorPattern.MatchString(a.OperationID) || a.State != "active" && a.State != "pending" && a.State != "retained" || a.State != "pending" && a.OperationState != "complete" {
			return "", nil, ErrProjectionConflict
		}
		if a.TransferRecord != "" {
			record, err := directorymail.ParseTransferRecord(a.TransferRecord)
			if err != nil || record.Address != a.Address || record.ToOwner != resource.OwnerAnchor || record.ToMailbox != resource.ID {
				return "", nil, ErrProjectionConflict
			}
		}
		seen[a.Address] = true
		if a.Address == resource.DefaultAddress {
			kind := "primary"
			if resource.Kind == "additional" {
				kind = "additional"
			}
			if a.Kind != kind {
				return "", nil, ErrProjectionConflict
			}
			defaultActive = a.State == "active"
		} else if a.Kind != "alias" {
			return "", nil, ErrProjectionConflict
		}
	}
	if resource.State != "pending" && !defaultActive {
		return "", nil, ErrProjectionConflict
	}
	raw, err := json.Marshal(projectionSnapshot{Schema: "anas.mailbox-projection/v1", Resource: resource})
	if err != nil {
		return "", nil, err
	}
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:]), raw, nil
}

const projectionSelect = `SELECT m.id,m.owner_anchor,m.protocol_login,m.storage_ref,m.roundcube_key,m.state,
 COALESCE(a.address,''),p.revision,p.fingerprint,p.authorization_epoch,p.snapshot,
 p.applied_revision=p.revision,p.last_error
 FROM mail.mailbox_projection p JOIN mail.mailbox m ON m.id=p.mailbox_id
 LEFT JOIN mail.address a ON a.mailbox_id=m.id AND a.is_default`

type projectionScanner interface{ Scan(...any) error }

func scanProjection(row projectionScanner) (Projection, error) {
	var p Projection
	var raw []byte
	var applied sql.NullBool
	err := row.Scan(&p.Mailbox.ID, &p.Mailbox.OwnerAnchor, &p.Mailbox.ProtocolLogin, &p.Mailbox.StorageRef, &p.Mailbox.RoundcubeKey, &p.Mailbox.State, &p.Mailbox.DefaultAddress, &p.Revision, &p.Fingerprint, &p.Epoch, &raw, &applied, &p.Error)
	if err != nil {
		return p, err
	}
	var snapshot projectionSnapshot
	if json.Unmarshal(raw, &snapshot) != nil || snapshot.Schema != "anas.mailbox-projection/v1" {
		return Projection{}, ErrProjectionConflict
	}
	fingerprint, _, err := ProjectionFingerprint(snapshot.Resource)
	if err != nil || fingerprint != p.Fingerprint || snapshot.Resource.ID != p.Mailbox.ID || snapshot.Resource.OwnerAnchor != p.Mailbox.OwnerAnchor {
		return Projection{}, ErrProjectionConflict
	}
	p.Resource = snapshot.Resource
	p.Applied = applied.Valid && applied.Bool
	return p, nil
}

func (s Store) MailboxProjection(ctx context.Context, id string) (Projection, error) {
	if !anchorPattern.MatchString(id) {
		return Projection{}, ErrProjectionConflict
	}
	p, err := scanProjection(s.DB.QueryRowContext(ctx, projectionSelect+` WHERE p.mailbox_id=$1`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Projection{}, ErrProjectionConflict
	}
	return p, err
}

// StageMailbox preserves stable mailbox identities and mail files. Old address
// ownership changes only through an exact durable withdrawal proof. Directory
// changes close login/sending readiness and advance the epoch in the same
// transaction. This cannot grant directory admission.
func (s Store) StageMailbox(ctx context.Context, resource identity.MailboxResource, expected string) (Projection, error) {
	fingerprint, raw, err := ProjectionFingerprint(resource)
	if err != nil || expected != "" && !anchorPattern.MatchString(expected) {
		return Projection{}, ErrProjectionConflict
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return Projection{}, err
	}
	defer tx.Rollback()
	if err = lockProjectionOwners(ctx, tx, resource); err != nil {
		return Projection{}, err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO mail.person(anchor) VALUES($1) ON CONFLICT DO NOTHING`, resource.OwnerAnchor); err != nil {
		return Projection{}, err
	}
	var exists bool
	if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM mail.mailbox WHERE id=$1)`, resource.ID).Scan(&exists); err != nil {
		return Projection{}, err
	}
	if !exists {
		if expected != "" {
			return Projection{}, ErrProjectionConflict
		}
		login, err := token("mbx_")
		if err != nil {
			return Projection{}, err
		}
		storage, err := token("store_")
		if err != nil {
			return Projection{}, err
		}
		webmail, err := token("webmail_")
		if err != nil {
			return Projection{}, err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO mail.mailbox(id,owner_anchor,kind,protocol_login,storage_ref,roundcube_key) VALUES($1,$2,$3,$4,$5,$6)`, resource.ID, resource.OwnerAnchor, resource.Kind, login, storage, webmail)
		if err != nil {
			return Projection{}, projectionError(err)
		}
	} else {
		var owner, kind string
		var epoch int64
		if err = tx.QueryRowContext(ctx, `SELECT owner_anchor,kind,authorization_epoch FROM mail.mailbox WHERE id=$1 FOR UPDATE`, resource.ID).Scan(&owner, &kind, &epoch); err != nil {
			return Projection{}, err
		}
		if owner != resource.OwnerAnchor || kind != resource.Kind {
			return Projection{}, ErrProjectionConflict
		}
		previous, err := scanProjection(tx.QueryRowContext(ctx, projectionSelect+` WHERE p.mailbox_id=$1`, resource.ID))
		if errors.Is(err, sql.ErrNoRows) {
			return Projection{}, ErrProjectionConflict
		}
		if err != nil {
			return Projection{}, err
		}
		if previous.Fingerprint == fingerprint && previous.Epoch == epoch {
			if err = tx.Commit(); err != nil {
				return Projection{}, err
			}
			return previous, nil
		}
		if expected != previous.Revision {
			return Projection{}, ErrProjectionConflict
		}
	}
	state := "pending"
	if resource.State == "disabled" || resource.State == "retained" {
		state = resource.State
	}
	var epoch int64
	if err = tx.QueryRowContext(ctx, `UPDATE mail.mailbox SET state=$2,authorization_epoch=authorization_epoch+1 WHERE id=$1 RETURNING authorization_epoch`, resource.ID, state).Scan(&epoch); err != nil {
		return Projection{}, err
	}
	// Retain this box's previous addresses. A proved incoming address is moved
	// separately below, without reusing either mailbox's stable keys.
	if _, err = tx.ExecContext(ctx, `UPDATE mail.address SET kind='alias',is_default=false,state='retained' WHERE mailbox_id=$1`, resource.ID); err != nil {
		return Projection{}, err
	}
	for _, a := range resource.Addresses {
		kind := a.Kind
		isDefault := a.Address == resource.DefaultAddress
		if kind == "additional" {
			kind = "independent"
		}
		if a.TransferRecord != "" {
			if err = consumeAddressWithdrawal(ctx, tx, resource, a); err != nil {
				return Projection{}, err
			}
		}
		result, err := tx.ExecContext(ctx, `INSERT INTO mail.address AS current_address(address,mailbox_id,kind,is_default,state) VALUES($1,$2,$3,$4,$5)
 ON CONFLICT(address) DO UPDATE SET kind=EXCLUDED.kind,is_default=EXCLUDED.is_default,state=EXCLUDED.state WHERE current_address.mailbox_id=EXCLUDED.mailbox_id`, a.Address, resource.ID, kind, isDefault, a.State)
		if err != nil {
			return Projection{}, projectionError(err)
		}
		n, err := result.RowsAffected()
		if err != nil {
			return Projection{}, err
		}
		if n != 1 {
			return Projection{}, ErrAddressUnavailable
		}
	}
	revision, err := mailboxID()
	if err != nil {
		return Projection{}, err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO mail.mailbox_projection(mailbox_id,revision,fingerprint,directory_version,authorization_epoch,snapshot)
 VALUES($1,$2,$3,$4,$5,$6) ON CONFLICT(mailbox_id) DO UPDATE SET revision=EXCLUDED.revision,fingerprint=EXCLUDED.fingerprint,
 directory_version=EXCLUDED.directory_version,authorization_epoch=EXCLUDED.authorization_epoch,snapshot=EXCLUDED.snapshot,
 applied_revision=NULL,applied_at=NULL,last_error='',observed_at=clock_timestamp()`, resource.ID, revision, fingerprint, resource.Version, epoch, raw)
	if err != nil {
		return Projection{}, err
	}
	p, err := scanProjection(tx.QueryRowContext(ctx, projectionSelect+` WHERE p.mailbox_id=$1`, resource.ID))
	if err != nil {
		return Projection{}, err
	}
	if err = tx.Commit(); err != nil {
		return Projection{}, err
	}
	return p, nil
}
func projectionError(err error) error {
	var pg *pgconn.PgError
	if errors.As(err, &pg) && pg.Code == "23505" {
		return ErrAddressUnavailable
	}
	return err
}

// ConfirmMailbox is only called after authenticated downstream readback and a
// fresh matching AD read. Stale workers and concurrent withdrawals cannot make
// a newer generation ready or resurrect an old authorization epoch.
func (s Store) ConfirmMailbox(ctx context.Context, id, revision, freshFingerprint string) (Projection, error) {
	if !anchorPattern.MatchString(id) || !anchorPattern.MatchString(revision) || len(freshFingerprint) != 64 {
		return Projection{}, ErrProjectionConflict
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return Projection{}, err
	}
	defer tx.Rollback()
	p, err := scanProjection(tx.QueryRowContext(ctx, projectionSelect+` WHERE p.mailbox_id=$1 FOR UPDATE OF m,p`, id))
	if err != nil {
		return Projection{}, err
	}
	if p.Revision != revision || p.Fingerprint != freshFingerprint || p.Resource.State == "pending" {
		return Projection{}, ErrProjectionConflict
	}
	for _, a := range p.Resource.Addresses {
		if a.State == "pending" {
			return Projection{}, ErrProjectionConflict
		}
	}
	result, err := tx.ExecContext(ctx, `UPDATE mail.mailbox SET state=$3 WHERE id=$1 AND authorization_epoch=$2`, id, p.Epoch, p.Resource.State)
	if err != nil {
		return Projection{}, err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return Projection{}, err
	}
	if n != 1 {
		return Projection{}, ErrProjectionConflict
	}
	_, err = tx.ExecContext(ctx, `UPDATE mail.mailbox_projection SET applied_revision=revision,applied_at=clock_timestamp(),last_error='' WHERE mailbox_id=$1`, id)
	if err != nil {
		return Projection{}, err
	}
	p, err = scanProjection(tx.QueryRowContext(ctx, projectionSelect+` WHERE p.mailbox_id=$1`, id))
	if err != nil {
		return Projection{}, err
	}
	if err = tx.Commit(); err != nil {
		return Projection{}, err
	}
	return p, nil
}

func (s Store) ProjectionFailed(ctx context.Context, id, revision, code string) error {
	if !anchorPattern.MatchString(id) || !anchorPattern.MatchString(revision) || code == "" || len(code) > 64 || strings.Trim(code, "abcdefghijklmnopqrstuvwxyz0123456789-_") != "" {
		return ErrProjectionConflict
	}
	result, err := s.DB.ExecContext(ctx, `UPDATE mail.mailbox_projection SET last_error=$3 WHERE mailbox_id=$1 AND revision=$2 AND applied_revision IS DISTINCT FROM revision`, id, revision, code)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrProjectionConflict
	}
	return nil
}
