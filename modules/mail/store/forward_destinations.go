package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"strings"

	"github.com/anas-project/ANAS/internal/directorymail"
)

// ForwardDestination is a routing identifier, not an authentication secret.
// Preserve the v1 derivation: published VPS targets must survive upgrades.
func ForwardDestination(source, generation string, route IngressRoute, domain string) (string, error) {
	local, publicDomain, ok := strings.Cut(route.Address, "@")
	address, err := directorymail.Address(local, publicDomain)
	destinationDomain, domainErr := directorymail.Domain(domain, "")
	if !ok || err != nil || address != route.Address || domainErr != nil || destinationDomain != domain || domain == publicDomain || !anchorPattern.MatchString(source) || !anchorPattern.MatchString(generation) || !anchorPattern.MatchString(route.MailboxID) || !anchorPattern.MatchString(route.OwnerAnchor) || !anchorPattern.MatchString(route.ClaimVersion) {
		return "", ErrIngressChanged
	}
	sum := sha256.Sum256([]byte("anas.mail-vps-forward-target/v1\x00" + source + "\x00" + generation + "\x00" + route.Address + "\x00" + route.MailboxID + "\x00" + route.ClaimVersion))
	target := "anas-" + hex.EncodeToString(sum[:16]) + "@" + domain
	if len(target) > 90 {
		return "", ErrIngressChanged
	}
	if canonical, e := directorymail.Address("anas-"+hex.EncodeToString(sum[:16]), domain); e != nil || canonical != target {
		return "", ErrIngressChanged
	}
	return target, nil
}

// ForwardDestinationIntent binds the provider operation to one immutable
// confirmed source claim. It never implies verification or public readiness.
type ForwardDestinationIntent struct {
	Source, Generation, Account, Zone, Domain string
	Route                                     IngressRoute
	Target, BindingID, State, DestinationID   string
}

func forwardBinding(source, generation string, r IngressRoute) IngressBinding {
	return IngressBinding{SourceID: source, Generation: generation, Address: r.Address, MailboxID: r.MailboxID, OwnerAnchor: r.OwnerAnchor, ClaimVersion: r.ClaimVersion}
}

func validateForwardIntent(v ForwardDestinationIntent) bool {
	target, err := ForwardDestination(v.Source, v.Generation, v.Route, v.Domain)
	return err == nil && providerRuleID.MatchString(v.Account) && providerRuleID.MatchString(v.Zone) && v.Target == target && v.BindingID == forwardBinding(v.Source, v.Generation, v.Route).Key()
}

// EnsureForwardSource binds the account, zone and delivery domain before a
// batch reads historical operations, including when it has no current routes.
func (s Store) EnsureForwardSource(ctx context.Context, source, generation, account, zone, domain string) error {
	canonical, err := directorymail.Domain(domain, "")
	if err != nil || canonical != domain || len("anas-"+strings.Repeat("0", 32)+"@"+domain) > 90 || !anchorPattern.MatchString(source) || !anchorPattern.MatchString(generation) || !providerRuleID.MatchString(account) || !providerRuleID.MatchString(zone) {
		return ErrIngressChanged
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = sourceRouteLock(ctx, tx, source); err != nil {
		return err
	}
	var actual string
	var revision int64
	var observed sql.NullTime
	if err = tx.QueryRowContext(ctx, `SELECT generation,revision,observed_at FROM mail.ingress_route_state WHERE source_id=$1 FOR SHARE`, source).Scan(&actual, &revision, &observed); err != nil {
		return ingressError(err)
	}
	if actual != generation || revision < 1 || !observed.Valid {
		return ErrIngressChanged
	}
	result, err := tx.ExecContext(ctx, `INSERT INTO mail.forward_source(source_id,generation,account_id,zone_id,delivery_domain) VALUES($1,$2,$3,$4,$5)
 ON CONFLICT(source_id) DO UPDATE SET source_id=EXCLUDED.source_id WHERE mail.forward_source.generation=EXCLUDED.generation AND mail.forward_source.account_id=EXCLUDED.account_id AND mail.forward_source.zone_id=EXCLUDED.zone_id AND mail.forward_source.delivery_domain=EXCLUDED.delivery_domain`, source, generation, account, zone, domain)
	if err != nil {
		return ingressError(err)
	}
	if n, _ := result.RowsAffected(); n != 1 {
		return ErrIngressChanged
	}
	return tx.Commit()
}

// checkForwardCurrent shares the same source and projection lock order as
// source route changes. It never upgrades a projection or grants user access.
func checkForwardCurrent(ctx context.Context, tx *sql.Tx, v ForwardDestinationIntent) error {
	if err := sourceRouteLock(ctx, tx, v.Source); err != nil {
		return err
	}
	var generation string
	var revision int64
	var observed sql.NullTime
	if err := tx.QueryRowContext(ctx, `SELECT generation,revision,observed_at FROM mail.ingress_route_state WHERE source_id=$1 FOR SHARE`, v.Source).Scan(&generation, &revision, &observed); err != nil {
		return ingressError(err)
	}
	if generation != v.Generation || revision < 1 || !observed.Valid {
		return ErrIngressChanged
	}
	var id string
	if err := tx.QueryRowContext(ctx, `SELECT id FROM mail.mailbox WHERE id=$1 FOR SHARE`, v.Route.MailboxID).Scan(&id); err != nil {
		return ingressError(err)
	}
	if err := tx.QueryRowContext(ctx, `SELECT mailbox_id FROM mail.mailbox_projection WHERE mailbox_id=$1 FOR SHARE`, v.Route.MailboxID).Scan(&id); err != nil {
		return ingressError(err)
	}
	p, err := scanProjection(tx.QueryRowContext(ctx, projectionSelect+` WHERE p.mailbox_id=$1`, v.Route.MailboxID))
	if err != nil {
		return ingressError(err)
	}
	if p.Mailbox.OwnerAnchor != v.Route.OwnerAnchor || p.Resource.State == "pending" || p.Error == "directory-resource-missing" {
		return ErrIngressChanged
	}
	valid := false
	for _, a := range p.Resource.Addresses {
		if a.Address == v.Route.Address && a.Version == v.Route.ClaimVersion && a.State == "active" && a.OperationState == "complete" {
			valid = true
		}
	}
	if !valid {
		return ErrIngressChanged
	}
	var open bool
	if err = tx.QueryRowContext(ctx, `SELECT closed_sequence IS NULL FROM mail.ingress_binding WHERE id=$1 AND source_id=$2 AND generation=$3 AND address=$4 AND mailbox_id=$5 AND owner_anchor=$6 AND claim_version=$7 FOR SHARE`, v.BindingID, v.Source, v.Generation, v.Route.Address, v.Route.MailboxID, v.Route.OwnerAnchor, v.Route.ClaimVersion).Scan(&open); err != nil {
		return ingressError(err)
	}
	if !open {
		return ErrIngressChanged
	}
	return nil
}

// PrepareForwardDestination persists the complete claim and immutable source
// identity before any provider request. Repeat calls cannot change a binding.
func (s Store) PrepareForwardDestination(ctx context.Context, source, generation, account, zone, domain string, route IngressRoute) (ForwardDestinationIntent, error) {
	v := ForwardDestinationIntent{Source: source, Generation: generation, Account: account, Zone: zone, Domain: domain, Route: route}
	target, err := ForwardDestination(source, generation, route, domain)
	if err != nil {
		return v, err
	}
	v.Target, v.BindingID = target, forwardBinding(source, generation, route).Key()
	if !validateForwardIntent(v) {
		return v, ErrIngressChanged
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return v, err
	}
	defer tx.Rollback()
	if err = checkForwardCurrent(ctx, tx, v); err != nil {
		return v, err
	}
	result, err := tx.ExecContext(ctx, `INSERT INTO mail.forward_source(source_id,generation,account_id,zone_id,delivery_domain) VALUES($1,$2,$3,$4,$5)
 ON CONFLICT(source_id) DO UPDATE SET source_id=EXCLUDED.source_id WHERE mail.forward_source.generation=EXCLUDED.generation AND mail.forward_source.account_id=EXCLUDED.account_id AND mail.forward_source.zone_id=EXCLUDED.zone_id AND mail.forward_source.delivery_domain=EXCLUDED.delivery_domain`, source, generation, account, zone, domain)
	if err != nil {
		return v, ingressError(err)
	}
	if n, _ := result.RowsAffected(); n != 1 {
		return v, ErrIngressChanged
	}
	result, err = tx.ExecContext(ctx, `INSERT INTO mail.forward_destination(binding_id,source_id,target) VALUES($1,$2,$3) ON CONFLICT(binding_id) DO UPDATE SET binding_id=EXCLUDED.binding_id WHERE mail.forward_destination.source_id=EXCLUDED.source_id AND mail.forward_destination.target=EXCLUDED.target`, v.BindingID, source, target)
	if err != nil {
		return v, ingressError(err)
	}
	if n, _ := result.RowsAffected(); n != 1 {
		return v, ErrIngressChanged
	}
	if err = tx.QueryRowContext(ctx, `SELECT state,COALESCE(destination_id,'') FROM mail.forward_destination WHERE binding_id=$1`, v.BindingID).Scan(&v.State, &v.DestinationID); err != nil {
		return v, ingressError(err)
	}
	return v, tx.Commit()
}

// IssueForwardDestination permits exactly one process to attempt creation. An
// issued operation is deliberately not reissued when its result is unknown.
func (s Store) IssueForwardDestination(ctx context.Context, v ForwardDestinationIntent) error {
	if !validateForwardIntent(v) || v.State != "prepared" || v.DestinationID != "" {
		return ErrIngressChanged
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = checkForwardCurrent(ctx, tx, v); err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, `UPDATE mail.forward_destination d SET state='issued',issued_at=clock_timestamp() FROM mail.forward_source s WHERE d.binding_id=$1 AND d.source_id=$2 AND d.target=$3 AND d.state='prepared' AND s.source_id=d.source_id AND s.generation=$4 AND s.account_id=$5 AND s.zone_id=$6 AND s.delivery_domain=$7`, v.BindingID, v.Source, v.Target, v.Generation, v.Account, v.Zone, v.Domain)
	if err != nil {
		return ingressError(err)
	}
	if n, _ := result.RowsAffected(); n != 1 {
		return ErrIngressChanged
	}
	return tx.Commit()
}

// ConfirmForwardDestination records only an ID from the issued create's own
// response, including a late response after withdrawal. It cannot reopen the
// claim; callers must recheck the current binding and verify the provider ID.
func (s Store) ConfirmForwardDestination(ctx context.Context, v ForwardDestinationIntent, id string) error {
	if !validateForwardIntent(v) || !providerRuleID.MatchString(id) {
		return ErrIngressChanged
	}
	result, err := s.DB.ExecContext(ctx, `UPDATE mail.forward_destination d SET state='created',destination_id=$8,created_at=COALESCE(d.created_at,clock_timestamp()) FROM mail.forward_source s WHERE d.binding_id=$1 AND d.source_id=$2 AND d.target=$3 AND s.source_id=d.source_id AND s.generation=$4 AND s.account_id=$5 AND s.zone_id=$6 AND s.delivery_domain=$7 AND (d.state='issued' OR d.state='created' AND d.destination_id=$8)`, v.BindingID, v.Source, v.Target, v.Generation, v.Account, v.Zone, v.Domain, id)
	if err != nil {
		return ingressError(err)
	}
	if n, _ := result.RowsAffected(); n != 1 {
		return ErrIngressChanged
	}
	return nil
}

var ErrForwardDestinationUnknown = errors.New("forward destination creation outcome unknown")
