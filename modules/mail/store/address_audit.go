package store

import (
	"context"
	"encoding/json"

	"github.com/anas-project/ANAS/internal/directorymail"
)

// Persist an attempt before any LDAP mutation. An interrupted attempt stays
// unknown; retries use the directory's immutable operation id and version.
func (s Store) BeginAddressAudit(ctx context.Context, actor, owner string, r directorymail.MailRequest) (int64, error) {
	if !anchorPattern.MatchString(actor) || !anchorPattern.MatchString(owner) || !r.Valid() {
		return 0, directorymail.ErrDenied
	}
	raw, err := json.Marshal(r)
	if err != nil {
		return 0, err
	}
	var id int64
	err = s.DB.QueryRowContext(ctx, `INSERT INTO mail.address_audit(actor_anchor,owner_anchor,operation_id,request) VALUES($1,$2,$3,$4) RETURNING id`, actor, owner, r.ID, raw).Scan(&id)
	return id, err
}
func (s Store) FinishAddressAudit(ctx context.Context, id int64, outcome string) error {
	result, err := s.DB.ExecContext(ctx, `UPDATE mail.address_audit SET outcome=$2,finished_at=clock_timestamp() WHERE id=$1 AND finished_at IS NULL`, id, outcome)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return directorymail.ErrUnknown
	}
	return nil
}
