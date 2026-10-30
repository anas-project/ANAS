package ingress

import (
	"context"
	"errors"
	"time"

	"github.com/anas-project/ANAS/modules/mail/store"
)

type ForwardDestinationLedger interface {
	PrepareForwardDestination(context.Context, string, string, string, string, string, store.IngressRoute) (store.ForwardDestinationIntent, error)
	IssueForwardDestination(context.Context, store.ForwardDestinationIntent) error
	ConfirmForwardDestination(context.Context, store.ForwardDestinationIntent, string) error
}

// EnsureForwardDestination persists intent before creating an account target.
// The caller must still persist and independently verify each public rule.
// Unknown creates are held for administrator reconciliation, not retried or
// adopted from a matching account object. This is not a readiness API.
func (c *CloudflareForwardRules) EnsureForwardDestination(ctx context.Context, ledger ForwardDestinationLedger, route Route) (string, error) {
	if ledger == nil {
		return "", ErrConfiguration
	}
	if _, err := c.target(route); err != nil {
		return "", err
	}
	prepare := func(ctx context.Context) (store.ForwardDestinationIntent, error) {
		return ledger.PrepareForwardDestination(ctx, c.rules.source.cfg.ID, c.rules.source.cfg.Generation, c.account, c.rules.zone, c.domain, route)
	}
	v, err := prepare(ctx)
	if err != nil {
		return "", err
	}
	switch v.State {
	case "issued":
		return "", store.ErrForwardDestinationUnknown
	case "prepared":
		// Read-only preflight can fail without making the durable operation
		// ambiguous. The source claim is checked again when issuing.
		if err = c.CheckDestinationAbsent(ctx, route); err != nil {
			return "", err
		}
		if err = ledger.IssueForwardDestination(ctx, v); err != nil {
			return "", err
		}
		id, createErr := c.createDestination(ctx, route)
		if createErr != nil {
			return "", errors.Join(store.ErrForwardDestinationUnknown, createErr)
		}
		saveCtx, done := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
		err = ledger.ConfirmForwardDestination(saveCtx, v, id)
		done()
		if err != nil {
			return "", errors.Join(store.ErrForwardDestinationUnknown, err)
		}
		v.DestinationID = id
	case "created":
		if !cloudflareID.MatchString(v.DestinationID) {
			return "", ErrIdentity
		}
	default:
		return "", ErrIdentity
	}
	if err = c.VerifyDestination(ctx, v.DestinationID, route); err != nil {
		return "", err
	}
	// A provider result can arrive after withdrawal, transfer or source drift.
	// Recheck the exact current local claim before reporting the verified ID.
	fresh, err := prepare(ctx)
	if err != nil {
		return "", err
	}
	if fresh.State != "created" || fresh.BindingID != v.BindingID || fresh.Target != v.Target || fresh.DestinationID != v.DestinationID {
		return "", ErrIdentity
	}
	return fresh.DestinationID, nil
}
