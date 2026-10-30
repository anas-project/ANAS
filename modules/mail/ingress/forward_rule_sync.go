package ingress

import (
	"context"
	"errors"
	"time"

	"github.com/anas-project/ANAS/modules/mail/store"
)

type ForwardRuleLedger interface {
	ForwardDestinationLedger
	PrepareForwardRule(context.Context, store.ForwardDestinationIntent) (store.ForwardRuleIntent, error)
	IssueForwardRuleCreate(context.Context, store.ForwardRuleIntent) error
	RecordForwardRuleResponse(context.Context, store.ForwardRuleIntent, store.CloudflareRuleFact) error
	ConfirmForwardRuleCreate(context.Context, store.ForwardRuleIntent, store.CloudflareRuleFact) error
}

// EnsureForwardRule first verifies the independently persisted destination,
// checks the complete provider rule list, and persists intent before a create.
// It never adopts an existing rule and unknown writes are not reissued. The
// result is an observed rule, not end-to-end delivery or product readiness.
func (c *CloudflareForwardRules) EnsureForwardRule(ctx context.Context, ledger ForwardRuleLedger, route Route) (CloudflareRule, error) {
	if ledger == nil {
		return CloudflareRule{}, ErrConfiguration
	}
	destinationID, err := c.EnsureForwardDestination(ctx, ledger, route)
	if err != nil {
		return CloudflareRule{}, err
	}
	prepareDestination := func(ctx context.Context) (store.ForwardDestinationIntent, error) {
		return ledger.PrepareForwardDestination(ctx, c.rules.source.cfg.ID, c.rules.source.cfg.Generation, c.account, c.rules.zone, c.domain, route)
	}
	dest, err := prepareDestination(ctx)
	if err != nil {
		return CloudflareRule{}, err
	}
	if dest.State != "created" || dest.DestinationID != destinationID {
		return CloudflareRule{}, ErrIdentity
	}
	intent, err := ledger.PrepareForwardRule(ctx, dest)
	if err != nil {
		return CloudflareRule{}, err
	}
	if intent.State == "create_issued" {
		return CloudflareRule{}, ErrRuleUnknown
	}
	if intent.State != "prepared" && intent.State != "create_responded" && intent.State != "created" {
		return CloudflareRule{}, ErrRuleConflict
	}
	observed, err := c.Inspect(ctx, []Route{route})
	if err != nil {
		return CloudflareRule{}, err
	}
	existing, present := observed[route.Address]
	if intent.State == "prepared" {
		if present {
			return CloudflareRule{}, ErrRuleConflict
		}
		if err = ledger.IssueForwardRuleCreate(ctx, intent); err != nil {
			return CloudflareRule{}, err
		}
		created, e := c.createRule(ctx, route)
		if e != nil {
			return CloudflareRule{}, errors.Join(ErrRuleUnknown, e)
		}
		fact := ruleFact(created)
		saveCtx, done := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
		e = ledger.RecordForwardRuleResponse(saveCtx, intent, fact)
		done()
		if e != nil {
			return CloudflareRule{}, errors.Join(ErrRuleUnknown, e)
		}
		intent.State, intent.Fact = "create_responded", fact
	} else if !present || !existing.Enabled || ruleFact(existing) != intent.Fact {
		return CloudflareRule{}, ErrRuleConflict
	}
	actual, err := c.Read(ctx, intent.Fact.ID, route)
	if err != nil {
		return CloudflareRule{}, err
	}
	if !actual.Enabled || ruleFact(actual) != intent.Fact {
		return CloudflareRule{}, ErrRuleConflict
	}
	if intent.State == "create_responded" {
		saveCtx, done := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
		err = ledger.ConfirmForwardRuleCreate(saveCtx, intent, intent.Fact)
		done()
		if err != nil {
			return CloudflareRule{}, errors.Join(ErrRuleUnknown, err)
		}
	}
	// Read the full ruleset again after any write and require the local claim
	// to remain current. A provider change after this observation is possible.
	observed, err = c.Inspect(ctx, []Route{route})
	if err != nil {
		return CloudflareRule{}, err
	}
	actual, present = observed[route.Address]
	if !present || !actual.Enabled || ruleFact(actual) != intent.Fact {
		return CloudflareRule{}, ErrRuleConflict
	}
	fresh, err := prepareDestination(ctx)
	if err != nil {
		return CloudflareRule{}, err
	}
	if fresh.State != "created" || fresh.BindingID != dest.BindingID || fresh.Target != dest.Target || fresh.DestinationID != dest.DestinationID {
		return CloudflareRule{}, ErrIdentity
	}
	return actual, nil
}

type ForwardRuleRemovalLedger interface {
	IssueForwardRuleDelete(context.Context, store.ForwardRuleIntent) error
	ConfirmForwardRuleDelete(context.Context, store.ForwardRuleIntent) error
	ConfirmForwardRuleCreate(context.Context, store.ForwardRuleIntent, store.CloudflareRuleFact) error
	CancelForwardRule(context.Context, store.ForwardRuleIntent) error
}

// RemoveForwardRule never touches an external or modified rule. Only a closed
// source claim can issue deletion; a lost deletion response is reconciled by
// reading the original ID, never by blindly resending the mutation.
func (c *CloudflareForwardRules) RemoveForwardRule(ctx context.Context, ledger ForwardRuleRemovalLedger, intent store.ForwardRuleIntent) error {
	if c == nil || c.rules == nil || ledger == nil {
		return ErrConfiguration
	}
	d := intent.Destination
	target, err := c.target(d.Route)
	if err != nil || d.Source != c.rules.source.cfg.ID || d.Generation != c.rules.source.cfg.Generation || d.Account != c.account || d.Zone != c.rules.zone || d.Domain != c.domain || d.Target != target {
		return ErrConfiguration
	}
	switch intent.State {
	case "cancelled":
		return nil
	case "prepared":
		return ledger.CancelForwardRule(ctx, intent)
	case "create_issued":
		return ErrRuleUnknown
	case "create_responded", "created", "delete_issued", "removed":
	default:
		return ErrIdentity
	}
	if !cloudflareID.MatchString(intent.Fact.ID) {
		return ErrIdentity
	}
	actual, readErr := c.Read(ctx, intent.Fact.ID, d.Route)
	if readErr != nil && !errors.Is(readErr, ErrGone) {
		return readErr
	}
	if readErr == nil && ruleFact(actual) != intent.Fact {
		return ErrRuleConflict
	}
	if intent.State == "removed" {
		if errors.Is(readErr, ErrGone) {
			return nil
		}
		return ErrRuleConflict
	}
	if intent.State == "create_responded" {
		if readErr != nil {
			return ErrRuleUnknown
		}
		if err = ledger.ConfirmForwardRuleCreate(ctx, intent, intent.Fact); err != nil {
			return err
		}
		intent.State = "created"
	}
	if intent.State == "created" {
		if err = ledger.IssueForwardRuleDelete(ctx, intent); err != nil {
			return err
		}
		intent.State = "delete_issued"
		if readErr == nil {
			if err = c.Delete(ctx, actual, d.Route); err != nil && !errors.Is(err, ErrGone) {
				return errors.Join(ErrRuleUnknown, err)
			}
		}
	} else if readErr == nil {
		// The earlier DELETE may still be in flight; do not issue another.
		return ErrRuleUnknown
	}
	if _, err = c.Read(ctx, intent.Fact.ID, d.Route); !errors.Is(err, ErrGone) {
		if err == nil {
			return ErrRuleUnknown
		}
		return err
	}
	saveCtx, done := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
	err = ledger.ConfirmForwardRuleDelete(saveCtx, intent)
	done()
	if err != nil {
		return errors.Join(ErrRuleUnknown, err)
	}
	return nil
}
