package identity

import (
	"context"
	"sort"

	"github.com/anas-project/ANAS/internal/directorymail"
)

type MailboxReference struct{ ID, OwnerAnchor string }

// MailboxReferences enumerates the fixed non-login resource OU, not personnel
// passwords or editable profile addresses. Incomplete/referral results fail.
func (d *Directory) MailboxReferences(ctx context.Context) ([]MailboxReference, error) {
	base, _, err := directorymail.ResourceBases(d.config.BaseDN)
	if err != nil {
		return nil, ErrMailResources
	}
	conn, err := d.dial(ctx)
	if err != nil {
		return nil, ErrDirectoryUnavailable
	}
	defer conn.Close()
	stop := context.AfterFunc(ctx, func() { conn.Close() })
	defer stop()
	entries, err := resourceSearch(conn, base, "(objectClass="+directorymail.MailboxClass+")", mailboxAttributes, 5000)
	if err != nil {
		return nil, err
	}
	out := make([]MailboxReference, 0, len(entries))
	seen := map[string]bool{}
	for _, entry := range entries {
		values, err := resourceValues(entry, directorymail.MailboxIDAttribute, directorymail.OwnerAnchorAttribute)
		if err != nil || !anchorPattern.MatchString(values[0]) || !anchorPattern.MatchString(values[1]) || seen[values[0]] {
			return nil, ErrMailResources
		}
		if _, err = d.mailboxResource(entry, values[1], values[0]); err != nil {
			return nil, err
		}
		seen[values[0]] = true
		out = append(out, MailboxReference{ID: values[0], OwnerAnchor: values[1]})
	}
	if ctx.Err() != nil {
		return nil, ErrDirectoryUnavailable
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].OwnerAnchor == out[j].OwnerAnchor {
			return out[i].ID < out[j].ID
		}
		return out[i].OwnerAnchor < out[j].OwnerAnchor
	})
	return out, nil
}
