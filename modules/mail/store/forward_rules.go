package store

import (
	"context"
	"database/sql"
)

// ForwardRuleIntent is provider configuration bound to one original claim.
// Neither a rule fact nor an issued operation grants receiving permission.
type ForwardRuleIntent struct {
	Destination ForwardDestinationIntent
	State       string
	Fact        CloudflareRuleFact
}

// PrepareForwardRule requires the recorded provider destination and a still
// open source claim; only one unremoved claim can occupy a public address.
func (s Store) PrepareForwardRule(ctx context.Context, destination ForwardDestinationIntent) (ForwardRuleIntent, error) {
	v := ForwardRuleIntent{Destination: destination}
	if !validateForwardIntent(destination) || destination.State != "created" || !providerRuleID.MatchString(destination.DestinationID) {
		return v, ErrIngressChanged
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return v, err
	}
	defer tx.Rollback()
	if err = checkForwardCurrent(ctx, tx, destination); err != nil {
		return v, err
	}
	var exists bool
	err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM mail.forward_destination d JOIN mail.forward_source s ON s.source_id=d.source_id WHERE d.binding_id=$1 AND d.source_id=$2 AND d.target=$3 AND d.state='created' AND d.destination_id=$4 AND s.generation=$5 AND s.account_id=$6 AND s.zone_id=$7 AND s.delivery_domain=$8)`, destination.BindingID, destination.Source, destination.Target, destination.DestinationID, destination.Generation, destination.Account, destination.Zone, destination.Domain).Scan(&exists)
	if err != nil {
		return v, err
	}
	if !exists {
		return v, ErrIngressChanged
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO mail.forward_rule(binding_id,source_id,address) VALUES($1,$2,$3) ON CONFLICT(binding_id) DO NOTHING`, destination.BindingID, destination.Source, destination.Route.Address)
	if err != nil {
		return v, ingressError(err)
	}
	err = tx.QueryRowContext(ctx, `SELECT state,COALESCE(rule_id,''),COALESCE(fingerprint,'') FROM mail.forward_rule WHERE binding_id=$1 AND source_id=$2 AND address=$3`, destination.BindingID, destination.Source, destination.Route.Address).Scan(&v.State, &v.Fact.ID, &v.Fact.Fingerprint)
	if err != nil {
		return v, ingressError(err)
	}
	if v.State == "removed" || v.State == "cancelled" || !v.Fact.valid() {
		return v, ErrIngressChanged
	}
	return v, tx.Commit()
}

func (s Store) IssueForwardRuleCreate(ctx context.Context, v ForwardRuleIntent) error {
	d := v.Destination
	if !validateForwardIntent(d) || d.State != "created" || !providerRuleID.MatchString(d.DestinationID) || v.State != "prepared" || v.Fact != (CloudflareRuleFact{}) {
		return ErrIngressChanged
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = checkForwardCurrent(ctx, tx, d); err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, `UPDATE mail.forward_rule r SET state='create_issued',issued_at=clock_timestamp(),updated_at=clock_timestamp() FROM mail.forward_destination d,mail.forward_source s WHERE r.binding_id=$1 AND r.source_id=$2 AND r.address=$3 AND r.state='prepared' AND d.binding_id=r.binding_id AND d.source_id=r.source_id AND d.target=$4 AND d.state='created' AND d.destination_id=$5 AND s.source_id=r.source_id AND s.generation=$6 AND s.account_id=$7 AND s.zone_id=$8 AND s.delivery_domain=$9`, d.BindingID, d.Source, d.Route.Address, d.Target, d.DestinationID, d.Generation, d.Account, d.Zone, d.Domain)
	if err != nil {
		return ingressError(err)
	}
	if n, _ := result.RowsAffected(); n != 1 {
		return ErrIngressChanged
	}
	return tx.Commit()
}

// RecordForwardRuleResponse stores the exact response before independent
// readback. A late response preserves history without reopening the claim.
func (s Store) RecordForwardRuleResponse(ctx context.Context, v ForwardRuleIntent, fact CloudflareRuleFact) error {
	d := v.Destination
	if !validateForwardIntent(d) || !fact.valid() || fact.ID == "" {
		return ErrIngressChanged
	}
	result, err := s.DB.ExecContext(ctx, `UPDATE mail.forward_rule r SET state=CASE WHEN r.state='created' THEN r.state ELSE 'create_responded' END,rule_id=$4,fingerprint=$5,updated_at=clock_timestamp() FROM mail.forward_source s WHERE r.binding_id=$1 AND r.source_id=$2 AND r.address=$3 AND s.source_id=r.source_id AND s.generation=$6 AND s.account_id=$7 AND s.zone_id=$8 AND s.delivery_domain=$9 AND (r.state='create_issued' OR r.state IN ('create_responded','created') AND r.rule_id=$4 AND r.fingerprint=$5)`, d.BindingID, d.Source, d.Route.Address, fact.ID, fact.Fingerprint, d.Generation, d.Account, d.Zone, d.Domain)
	if err != nil {
		return ingressError(err)
	}
	if n, _ := result.RowsAffected(); n != 1 {
		return ErrIngressChanged
	}
	return nil
}

// ConfirmForwardRuleCreate must follow independent provider readback of fact.
func (s Store) ConfirmForwardRuleCreate(ctx context.Context, v ForwardRuleIntent, fact CloudflareRuleFact) error {
	d := v.Destination
	if !validateForwardIntent(d) || !fact.valid() || fact.ID == "" {
		return ErrIngressChanged
	}
	result, err := s.DB.ExecContext(ctx, `UPDATE mail.forward_rule r SET state='created',updated_at=clock_timestamp() FROM mail.forward_source s WHERE r.binding_id=$1 AND r.source_id=$2 AND r.address=$3 AND r.rule_id=$4 AND r.fingerprint=$5 AND r.state IN ('create_responded','created') AND s.source_id=r.source_id AND s.generation=$6 AND s.account_id=$7 AND s.zone_id=$8 AND s.delivery_domain=$9`, d.BindingID, d.Source, d.Route.Address, fact.ID, fact.Fingerprint, d.Generation, d.Account, d.Zone, d.Domain)
	if err != nil {
		return ingressError(err)
	}
	if n, _ := result.RowsAffected(); n != 1 {
		return ErrIngressChanged
	}
	return nil
}

// IssueForwardRuleDelete requires a closed cloud acceptance binding and the
// exact recorded rule. In-flight writes are not mistaken for completed deletion.
func (s Store) IssueForwardRuleDelete(ctx context.Context, v ForwardRuleIntent) error {
	d := v.Destination
	if !validateForwardIntent(d) || v.State != "created" || !v.Fact.valid() || v.Fact.ID == "" {
		return ErrIngressChanged
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = sourceRouteLock(ctx, tx, d.Source); err != nil {
		return err
	}
	var closed sql.NullInt64
	if err = tx.QueryRowContext(ctx, `SELECT closed_sequence FROM mail.ingress_binding WHERE id=$1 AND source_id=$2 AND generation=$3 FOR SHARE`, d.BindingID, d.Source, d.Generation).Scan(&closed); err != nil {
		return ingressError(err)
	}
	if !closed.Valid {
		return ErrIngressChanged
	}
	result, err := tx.ExecContext(ctx, `UPDATE mail.forward_rule r SET state='delete_issued',updated_at=clock_timestamp() FROM mail.forward_source s WHERE r.binding_id=$1 AND r.source_id=$2 AND r.address=$3 AND r.state='created' AND r.rule_id=$4 AND r.fingerprint=$5 AND s.source_id=r.source_id AND s.generation=$6 AND s.account_id=$7 AND s.zone_id=$8 AND s.delivery_domain=$9`, d.BindingID, d.Source, d.Route.Address, v.Fact.ID, v.Fact.Fingerprint, d.Generation, d.Account, d.Zone, d.Domain)
	if err != nil {
		return ingressError(err)
	}
	if n, _ := result.RowsAffected(); n != 1 {
		return ErrIngressChanged
	}
	return tx.Commit()
}

// The trusted caller must independently observe the provider ID absent before
// recording removal; history and the original rule fact are retained.
func (s Store) ConfirmForwardRuleDelete(ctx context.Context, v ForwardRuleIntent) error {
	d := v.Destination
	if !validateForwardIntent(d) || !v.Fact.valid() || v.Fact.ID == "" {
		return ErrIngressChanged
	}
	result, err := s.DB.ExecContext(ctx, `UPDATE mail.forward_rule r SET state='removed',updated_at=clock_timestamp() FROM mail.forward_source s WHERE r.binding_id=$1 AND r.source_id=$2 AND r.address=$3 AND r.rule_id=$4 AND r.fingerprint=$5 AND r.state IN ('delete_issued','removed') AND s.source_id=r.source_id AND s.generation=$6 AND s.account_id=$7 AND s.zone_id=$8 AND s.delivery_domain=$9`, d.BindingID, d.Source, d.Route.Address, v.Fact.ID, v.Fact.Fingerprint, d.Generation, d.Account, d.Zone, d.Domain)
	if err != nil {
		return ingressError(err)
	}
	if n, _ := result.RowsAffected(); n != 1 {
		return ErrIngressChanged
	}
	return nil
}

// ForwardRules lists only records belonging to the exact source identity,
// including closed/removed claims needed for safe cleanup and audit.
func (s Store) ForwardRules(ctx context.Context, source, generation, account, zone, domain string) ([]ForwardRuleIntent, error) {
	if !anchorPattern.MatchString(source) || !anchorPattern.MatchString(generation) || !providerRuleID.MatchString(account) || !providerRuleID.MatchString(zone) {
		return nil, ErrIngressChanged
	}
	tx, err := s.DB.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var present bool
	if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM mail.forward_source WHERE source_id=$1 AND generation=$2 AND account_id=$3 AND zone_id=$4 AND delivery_domain=$5)`, source, generation, account, zone, domain).Scan(&present); err != nil {
		return nil, err
	}
	if !present {
		return nil, ErrIngressChanged
	}
	rows, err := tx.QueryContext(ctx, `SELECT r.binding_id,b.address,b.mailbox_id,b.owner_anchor,b.claim_version,d.target,COALESCE(d.destination_id,''),r.state,COALESCE(r.rule_id,''),COALESCE(r.fingerprint,'') FROM mail.forward_rule r JOIN mail.forward_destination d ON d.binding_id=r.binding_id AND d.source_id=r.source_id JOIN mail.ingress_binding b ON b.id=r.binding_id AND b.source_id=r.source_id AND b.generation=$2 WHERE r.source_id=$1 ORDER BY r.binding_id LIMIT 5001`, source, generation)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ForwardRuleIntent{}
	for rows.Next() {
		v := ForwardRuleIntent{Destination: ForwardDestinationIntent{Source: source, Generation: generation, Account: account, Zone: zone, Domain: domain, State: "created"}}
		d := &v.Destination
		if err = rows.Scan(&d.BindingID, &d.Route.Address, &d.Route.MailboxID, &d.Route.OwnerAnchor, &d.Route.ClaimVersion, &d.Target, &d.DestinationID, &v.State, &v.Fact.ID, &v.Fact.Fingerprint); err != nil {
			return nil, err
		}
		if !validateForwardIntent(*d) || !providerRuleID.MatchString(d.DestinationID) || !v.Fact.valid() || len(out) >= 5000 {
			return nil, ErrIngressChanged
		}
		out = append(out, v)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	if err = rows.Close(); err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return out, nil
}

// CancelForwardRule only cancels a never-issued rule after source closure.
func (s Store) CancelForwardRule(ctx context.Context, v ForwardRuleIntent) error {
	d := v.Destination
	if !validateForwardIntent(d) || v.State != "prepared" || v.Fact != (CloudflareRuleFact{}) {
		return ErrIngressChanged
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = sourceRouteLock(ctx, tx, d.Source); err != nil {
		return err
	}
	var closed sql.NullInt64
	if err = tx.QueryRowContext(ctx, `SELECT closed_sequence FROM mail.ingress_binding WHERE id=$1 AND source_id=$2 AND generation=$3 FOR SHARE`, d.BindingID, d.Source, d.Generation).Scan(&closed); err != nil {
		return ingressError(err)
	}
	if !closed.Valid {
		return ErrIngressChanged
	}
	result, err := tx.ExecContext(ctx, `UPDATE mail.forward_rule r SET state='cancelled',updated_at=clock_timestamp() FROM mail.forward_source s WHERE r.binding_id=$1 AND r.source_id=$2 AND r.address=$3 AND r.state='prepared' AND s.source_id=r.source_id AND s.generation=$4 AND s.account_id=$5 AND s.zone_id=$6 AND s.delivery_domain=$7`, d.BindingID, d.Source, d.Route.Address, d.Generation, d.Account, d.Zone, d.Domain)
	if err != nil {
		return ingressError(err)
	}
	if n, _ := result.RowsAffected(); n != 1 {
		return ErrIngressChanged
	}
	return tx.Commit()
}
