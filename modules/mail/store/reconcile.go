package store

import (
	"context"

	"github.com/anas-project/ANAS/modules/mail/identity"
)

func (s Store) KnownPersons(ctx context.Context) ([]string, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT anchor FROM mail.person ORDER BY anchor LIMIT 5001`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var anchor string
		if err = rows.Scan(&anchor); err != nil {
			return nil, err
		}
		out = append(out, anchor)
	}
	if len(out) > 5000 {
		return nil, ErrProjectionConflict
	}
	return out, rows.Err()
}
func (s Store) MailboxReferences(ctx context.Context) ([]identity.MailboxReference, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT id,owner_anchor FROM mail.mailbox ORDER BY id LIMIT 5001`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []identity.MailboxReference
	for rows.Next() {
		var ref identity.MailboxReference
		if err = rows.Scan(&ref.ID, &ref.OwnerAnchor); err != nil {
			return nil, err
		}
		out = append(out, ref)
	}
	if len(out) > 5000 {
		return nil, ErrProjectionConflict
	}
	return out, rows.Err()
}
