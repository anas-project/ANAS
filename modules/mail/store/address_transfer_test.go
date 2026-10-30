// TEST_CASES: MAILMOD-T-015
package store

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/anas-project/ANAS/internal/directorymail"
	"github.com/anas-project/ANAS/modules/mail/identity"
)

// AD, provider/DMS observations and kick completion are controlled facts here.
// PostgreSQL, route versions, closing boundaries, leases and proof writes are
// real. This is not live withdrawal or permission to perform AD transfer.
func withdrawalProofFixture(t *testing.T) (Store, context.Context, directorymail.TransferRecord, VerifiedAddressWithdrawal) {
	t.Helper()
	s, ctx, resource, prior, source, generation := sourceRouteFixture(t)
	alias := "proof-" + resource.ID + "@example.test"
	claim, _ := mailboxID()
	resource.Addresses = append(resource.Addresses, identity.AddressResource{Address: alias, Kind: "alias", State: "active", Version: claim, OperationID: claim, OperationState: "complete"})
	resource.Version, _ = mailboxID()
	p, err := s.StageMailbox(ctx, resource, prior.Revision)
	if err != nil {
		t.Fatal(err)
	}
	if p, err = s.ConfirmMailbox(ctx, resource.ID, p.Revision, p.Fingerprint); err != nil {
		t.Fatal(err)
	}
	active := []IngressRoute{routeFor(resource), {Address: alias, MailboxID: resource.ID, OwnerAnchor: resource.OwnerAnchor, ClaimVersion: claim}}
	op, err := s.StageSourceRoutes(ctx, source, generation, active, nil)
	if err != nil || s.IssueSourceRoutes(ctx, op) != nil {
		t.Fatal("initial source permission", err)
	}
	bindings := []IngressRouteBinding{bindingFor(op, active[0], 1), bindingFor(op, active[1], 1)}
	if err = s.ObserveSourceRoutes(ctx, routeOutcome(op, 2, bindings)); err != nil {
		t.Fatal(err)
	}
	_, cookie, err := s.AdmitSession(ctx, VerifiedLogin{OwnerAnchor: resource.OwnerAnchor, Issuer: "https://withdrawal.example.test", SID: "withdrawal-" + resource.ID, AuthenticatedAt: time.Now().Add(-time.Minute), Expires: time.Now().Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	grant, err := s.MailboxForSession(ctx, cookie, resource.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.IssueCredentials(ctx, grant); err != nil {
		t.Fatal(err)
	}
	resource.Version, _ = mailboxID()
	resource.Addresses[1].Version, _ = mailboxID()
	resource.Addresses[1].State = "retained"
	if p, err = s.StageMailbox(ctx, resource, p.Revision); err != nil {
		t.Fatal(err)
	}
	if p, err = s.ConfirmMailbox(ctx, resource.ID, p.Revision, p.Fingerprint); err != nil {
		t.Fatal(err)
	}
	op, err = s.StageSourceRoutes(ctx, source, generation, active[:1], nil)
	if err != nil || s.IssueSourceRoutes(ctx, op) != nil {
		t.Fatal("closed source permission", err)
	}
	closed := int64(2)
	bindings[1].ClosedSequence = &closed
	state := routeOutcome(op, 2, bindings)
	if err = s.ObserveSourceRoutes(ctx, state); err != nil {
		t.Fatal(err)
	}
	identityHash := strings.Repeat("a", 64)
	if err = s.ReserveIngressSources(ctx, 2<<30, []IngressReservation{{SourceID: source, Generation: generation, IdentityFingerprint: identityHash, CapacityBytes: 2 << 30}}); err != nil {
		t.Fatal(err)
	}
	if err = s.PrepareCloudflareRules(ctx, source, generation, "withdrawal-fixture", active[:1]); err != nil {
		t.Fatal(err)
	}
	public, err := s.ClaimCloudflareRule(ctx, source)
	if err != nil || s.IssueCloudflareRule(ctx, public) != nil {
		t.Fatal("default public rule", err)
	}
	fact := CloudflareRuleFact{ID: strings.Repeat("b", 32), Fingerprint: strings.Repeat("c", 64)}
	if err = s.ConfirmCloudflareRule(ctx, public, fact); err != nil {
		t.Fatal(err)
	}
	toOwner, _ := mailboxID()
	toBox, _ := mailboxID()
	toOperation, _ := mailboxID()
	operation, _ := mailboxID()
	r := directorymail.TransferRecord{Schema: directorymail.TransferRecordSchema, OperationID: operation, Actor: resource.OwnerAnchor, Address: alias, FromKind: "alias", FromOwner: resource.OwnerAnchor, FromMailbox: resource.ID, FromClaimVersion: resource.Addresses[1].Version, FromMailboxVersion: resource.Version, RemainingDefault: resource.DefaultAddress, ToOwner: toOwner, ToMailbox: toBox, ToCreationOperation: toOperation}
	verified := VerifiedAddressWithdrawal{MailboxID: resource.ID, Revision: p.Revision, Fingerprint: p.Fingerprint, AuthorizationEpoch: p.Epoch, Sources: []VerifiedWithdrawalSource{{State: state, IdentityFingerprint: identityHash, PublicRuleAbsent: true, RemainingDefaultRule: fact}}}
	t.Cleanup(func() {
		clean, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if _, err := s.DB.ExecContext(clean, `DELETE FROM mail.address_transfer_proof WHERE from_mailbox=$1`, resource.ID); err != nil {
			t.Error(err)
		}
	})
	return s, ctx, r, verified
}

func completeWithdrawalFixtureKicks(t *testing.T, s Store, ctx context.Context, box string) {
	t.Helper()
	// These are synthetic tasks; real DMS/Roundcube completion is separate.
	if _, err := s.DB.ExecContext(ctx, `UPDATE mail.revocation_task SET completed_at=clock_timestamp() WHERE mailbox_id=$1`, box); err != nil {
		t.Fatal(err)
	}
}

func TestPostgreSQLWithdrawalProofRequiresAllFactsAndDoesNotGrantAddress(t *testing.T) {
	s, ctx, request, verified := withdrawalProofFixture(t)
	if _, err := s.RecordAddressWithdrawal(ctx, request, verified); !errors.Is(err, ErrAddressTransfer) {
		t.Fatal("pending downstream kicks were ignored", err)
	}
	completeWithdrawalFixtureKicks(t, s, ctx, request.FromMailbox)
	proof, err := s.RecordAddressWithdrawal(ctx, request, verified)
	if err != nil || !proof.Valid() {
		t.Fatal("verified withdrawal not persisted", err)
	}
	again, err := (Store{DB: s.DB}).RecordAddressWithdrawal(ctx, request, verified)
	if err != nil || proof != again {
		t.Fatal("lost-response retry changed the proof", err)
	}
	changed := request
	changed.ToOwner, _ = mailboxID()
	if _, err = s.RecordAddressWithdrawal(ctx, changed, verified); !errors.Is(err, ErrAddressTransfer) {
		t.Fatal("operation changed its intended new owner", err)
	}
	changed = request
	changed.OperationID, _ = mailboxID()
	if _, err = s.RecordAddressWithdrawal(ctx, changed, verified); !errors.Is(err, ErrAddressTransfer) {
		t.Fatal("same claim version received another transfer proof", err)
	}
	var box, state string
	var noGrant bool
	if err = s.DB.QueryRowContext(ctx, `SELECT mailbox_id,state,transfer_proof_id IS NULL FROM mail.address WHERE address=$1`, request.Address).Scan(&box, &state, &noGrant); err != nil || box != request.FromMailbox || state != "retained" || !noGrant {
		t.Fatal("proof registration changed address ownership", err)
	}
	var count int
	if err = s.DB.QueryRowContext(ctx, `SELECT count(*) FROM mail.address_transfer_proof WHERE operation_id=$1 AND consumed_at IS NULL AND applied_to IS NULL`, request.OperationID).Scan(&count); err != nil || count != 1 {
		t.Fatal("proof duplicated or was consumed before AD transfer", err)
	}
	if err = s.DB.QueryRowContext(ctx, `SELECT count(*) FROM mail.person WHERE anchor=$1`, request.ToOwner).Scan(&count); err != nil || count != 0 {
		t.Fatal("proof registration created a target person", err)
	}
	var versions []byte
	if err = s.DB.QueryRowContext(ctx, `SELECT source_versions FROM mail.address_transfer_proof WHERE id=$1`, proof.ProofID).Scan(&versions); err != nil {
		t.Fatal(err)
	}
	var evidence []withdrawalSourceVersion
	if json.Unmarshal(versions, &evidence) != nil || len(evidence) != 1 || len(evidence[0].ClosedBindings) != 1 || evidence[0].ClosedBindings[0].MailboxID != request.FromMailbox || *evidence[0].ClosedBindings[0].ClosedSequence != 2 {
		t.Fatal("original accepted-mail target/boundary was not retained")
	}
}

func TestPostgreSQLWithdrawalProofRejectsMissingDriftedOrUnsettledFacts(t *testing.T) {
	for _, mode := range []string{"public-present", "default-unverified", "source-omitted", "unknown-operation", "identity", "stale-dms", "stale-epoch", "pending-source", "primary-as-additional"} {
		t.Run(mode, func(t *testing.T) {
			s, ctx, request, verified := withdrawalProofFixture(t)
			completeWithdrawalFixtureKicks(t, s, ctx, request.FromMailbox)
			switch mode {
			case "public-present":
				verified.Sources[0].PublicRuleAbsent = false
			case "default-unverified":
				verified.Sources[0].RemainingDefaultRule = CloudflareRuleFact{}
			case "source-omitted":
				verified.Sources = nil
			case "unknown-operation":
				verified.Sources[0].State.OperationID, _ = mailboxID()
			case "identity":
				verified.Sources[0].IdentityFingerprint = strings.Repeat("d", 64)
			case "stale-dms":
				verified.Revision, _ = mailboxID()
			case "stale-epoch":
				verified.AuthorizationEpoch++
			case "primary-as-additional":
				request.FromKind = "additional"
			case "pending-source":
				if _, err := s.StageSourceRoutes(ctx, verified.Sources[0].State.SourceID, verified.Sources[0].State.Generation, nil, nil); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := s.RecordAddressWithdrawal(ctx, request, verified); err == nil {
				t.Fatal("unproved withdrawal was recorded", mode)
			}
			var count int
			if err := s.DB.QueryRowContext(ctx, `SELECT count(*) FROM mail.address_transfer_proof WHERE operation_id=$1`, request.OperationID).Scan(&count); err != nil || count != 0 {
				t.Fatal("failed evidence check left a proof", err)
			}
		})
	}
}

func TestPostgreSQLWithdrawalProofWaitsForForwardRuleCleanup(t *testing.T) {
	for _, terminal := range []string{"cancelled", "removed"} {
		t.Run(terminal, func(t *testing.T) {
			s, ctx, request, verified := withdrawalProofFixture(t)
			completeWithdrawalFixtureKicks(t, s, ctx, request.FromMailbox)
			source := verified.Sources[0].State.SourceID
			var binding string
			if err := s.DB.QueryRowContext(ctx, `SELECT id FROM mail.ingress_binding WHERE source_id=$1 AND address=$2 AND closed_sequence IS NOT NULL`, source, request.Address).Scan(&binding); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				clean, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				for _, q := range []string{`DELETE FROM mail.forward_rule WHERE source_id=$1`, `DELETE FROM mail.forward_destination WHERE source_id=$1`, `DELETE FROM mail.forward_source WHERE source_id=$1`} {
					if _, err := s.DB.ExecContext(clean, q, source); err != nil {
						t.Error(err)
					}
				}
			})
			if _, err := s.DB.ExecContext(ctx, `INSERT INTO mail.forward_source(source_id,generation,account_id,zone_id,delivery_domain) VALUES($1,$2,$3,$4,'example.test')`, source, verified.Sources[0].State.Generation, strings.Repeat("a", 32), strings.Repeat("b", 32)); err != nil {
				t.Fatal(err)
			}
			if _, err := s.DB.ExecContext(ctx, `INSERT INTO mail.forward_destination(binding_id,source_id,target) VALUES($1,$2,$3)`, binding, source, binding+"@example.test"); err != nil {
				t.Fatal(err)
			}
			if _, err := s.DB.ExecContext(ctx, `INSERT INTO mail.forward_rule(binding_id,source_id,address) VALUES($1,$2,$3)`, binding, source, request.Address); err != nil {
				t.Fatal(err)
			}
			for _, state := range []string{"prepared", "create_issued", "create_responded", "created", "delete_issued"} {
				var ruleID, fingerprint any
				if state == "create_responded" || state == "created" || state == "delete_issued" {
					ruleID, fingerprint = strings.Repeat("c", 32), strings.Repeat("d", 64)
				}
				if _, err := s.DB.ExecContext(ctx, `UPDATE mail.forward_rule SET state=$2,rule_id=$3,fingerprint=$4,issued_at=CASE WHEN $2='prepared' THEN NULL ELSE clock_timestamp() END WHERE binding_id=$1`, binding, state, ruleID, fingerprint); err != nil {
					t.Fatal(err)
				}
				if _, err := s.RecordAddressWithdrawal(ctx, request, verified); !errors.Is(err, ErrAddressTransfer) {
					t.Fatalf("%s forward rule did not block withdrawal: %v", state, err)
				}
			}
			var ruleID, fingerprint any
			if terminal == "removed" {
				ruleID, fingerprint = strings.Repeat("c", 32), strings.Repeat("d", 64)
			}
			if _, err := s.DB.ExecContext(ctx, `UPDATE mail.forward_rule SET state=$2,rule_id=$3,fingerprint=$4,issued_at=CASE WHEN $2='cancelled' THEN NULL ELSE clock_timestamp() END WHERE binding_id=$1`, binding, terminal, ruleID, fingerprint); err != nil {
				t.Fatal(err)
			}
			if _, err := s.RecordAddressWithdrawal(ctx, request, verified); err != nil {
				t.Fatalf("%s forward rule blocked withdrawal: %v", terminal, err)
			}
		})
	}
}

func TestPostgreSQLWithdrawalProofAcceptsVerifiedForwardedDefault(t *testing.T) {
	s, ctx, request, verified := withdrawalProofFixture(t)
	completeWithdrawalFixtureKicks(t, s, ctx, request.FromMailbox)
	source := verified.Sources[0].State.SourceID
	var binding string
	if err := s.DB.QueryRowContext(ctx, `SELECT id FROM mail.ingress_binding WHERE source_id=$1 AND address=$2 AND closed_sequence IS NULL`, source, request.RemainingDefault).Scan(&binding); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		clean, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		for _, q := range []string{`DELETE FROM mail.forward_rule WHERE source_id=$1`, `DELETE FROM mail.forward_destination WHERE source_id=$1`, `DELETE FROM mail.forward_source WHERE source_id=$1`} {
			if _, err := s.DB.ExecContext(clean, q, source); err != nil {
				t.Error(err)
			}
		}
	})
	for _, q := range []string{`DELETE FROM mail.cloudflare_rule_operation WHERE source_id=$1`, `DELETE FROM mail.cloudflare_rule WHERE source_id=$1`} {
		if _, err := s.DB.ExecContext(ctx, q, source); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.DB.ExecContext(ctx, `INSERT INTO mail.forward_source(source_id,generation,account_id,zone_id,delivery_domain) VALUES($1,$2,$3,$4,'example.test')`, source, verified.Sources[0].State.Generation, strings.Repeat("a", 32), strings.Repeat("b", 32)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.ExecContext(ctx, `INSERT INTO mail.forward_destination(binding_id,source_id,target) VALUES($1,$2,$3)`, binding, source, binding+"@example.test"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.ExecContext(ctx, `INSERT INTO mail.forward_rule(binding_id,source_id,address) VALUES($1,$2,$3)`, binding, source, request.RemainingDefault); err != nil {
		t.Fatal(err)
	}
	fact := verified.Sources[0].RemainingDefaultRule
	for _, state := range []string{"prepared", "create_issued", "create_responded", "delete_issued"} {
		var ruleID, fingerprint any
		if state == "create_responded" || state == "delete_issued" {
			ruleID, fingerprint = fact.ID, fact.Fingerprint
		}
		if _, err := s.DB.ExecContext(ctx, `UPDATE mail.forward_rule SET state=$2,rule_id=$3,fingerprint=$4,issued_at=CASE WHEN $2='prepared' THEN NULL ELSE clock_timestamp() END WHERE binding_id=$1`, binding, state, ruleID, fingerprint); err != nil {
			t.Fatal(err)
		}
		if _, err := s.RecordAddressWithdrawal(ctx, request, verified); !errors.Is(err, ErrAddressTransfer) {
			t.Fatalf("%s forward default accepted: %v", state, err)
		}
	}
	if _, err := s.DB.ExecContext(ctx, `UPDATE mail.forward_rule SET state='created',rule_id=$2,fingerprint=$3,issued_at=clock_timestamp() WHERE binding_id=$1`, binding, fact.ID, strings.Repeat("e", 64)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RecordAddressWithdrawal(ctx, request, verified); !errors.Is(err, ErrAddressTransfer) {
		t.Fatal("changed forward fact accepted", err)
	}
	if _, err := s.DB.ExecContext(ctx, `UPDATE mail.forward_rule SET fingerprint=$2 WHERE binding_id=$1`, binding, fact.Fingerprint); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RecordAddressWithdrawal(ctx, request, verified); err != nil {
		t.Fatal("verified forwarding default rejected", err)
	}
}

func preparedTransferResource(t *testing.T, s Store, record directorymail.TransferRecord) identity.MailboxResource {
	t.Helper()
	resource := projectionResource(t, "primary")
	resource.ID, resource.OwnerAnchor = record.ToMailbox, record.ToOwner
	resource.DefaultAddress, resource.OperationID = record.Address, record.ToCreationOperation
	body, err := json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	resource.Addresses = []identity.AddressResource{{Address: record.Address, Kind: "primary", State: "active", Version: resource.Version, OperationID: resource.OperationID, OperationState: "complete", TransferRecord: string(body)}}
	t.Cleanup(func() {
		ctx, done := context.WithTimeout(context.Background(), 10*time.Second)
		defer done()
		for _, query := range []string{`DELETE FROM mail.mailbox_projection WHERE mailbox_id=$1`, `DELETE FROM mail.address WHERE mailbox_id=$1`} {
			if _, err := s.DB.ExecContext(ctx, query, record.ToMailbox); err != nil {
				t.Error(err)
			}
		}
		// Drop the exact proof before its target FK; the fixture separately
		// checks that the old mailbox and source history remain unchanged.
		if _, err := s.DB.ExecContext(ctx, `DELETE FROM mail.address_transfer_proof WHERE id=$1`, record.ProofID); err != nil {
			t.Error(err)
		}
		if _, err := s.DB.ExecContext(ctx, `DELETE FROM mail.mailbox WHERE id=$1`, record.ToMailbox); err != nil {
			t.Error(err)
		}
		if _, err := s.DB.ExecContext(ctx, `DELETE FROM mail.person WHERE anchor=$1`, record.ToOwner); err != nil {
			t.Error(err)
		}
	})
	return resource
}

func TestPostgreSQLWithdrawalProofConsumesOnceAndKeepsAcceptedTarget(t *testing.T) {
	s, ctx, request, verified := withdrawalProofFixture(t)
	completeWithdrawalFixtureKicks(t, s, ctx, request.FromMailbox)
	proof, err := s.RecordAddressWithdrawal(ctx, request, verified)
	if err != nil {
		t.Fatal(err)
	}
	old, err := s.MailboxProjection(ctx, request.FromMailbox)
	if err != nil {
		t.Fatal(err)
	}
	resource := preparedTransferResource(t, s, proof)
	var wg sync.WaitGroup
	var results [2]Projection
	var failures [2]error
	for i := range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results[i], failures[i] = (Store{DB: s.DB}).StageMailbox(ctx, resource, "")
		}()
	}
	wg.Wait()
	for i := range 2 {
		if failures[i] != nil || results[i].Applied || results[i].Mailbox.State != "pending" || results[i].Revision != results[0].Revision {
			t.Fatal("concurrent transfer did not preserve one unconfirmed target", failures[i])
		}
	}
	var target, linked string
	if err = s.DB.QueryRowContext(ctx, `SELECT mailbox_id,transfer_proof_id FROM mail.address WHERE address=$1`, proof.Address).Scan(&target, &linked); err != nil || target != proof.ToMailbox || linked != proof.ProofID {
		t.Fatal("consumed proof not linked to its exact target", err)
	}
	var consumed bool
	if err = s.DB.QueryRowContext(ctx, `SELECT applied_to=$2 AND consumed_at IS NOT NULL FROM mail.address_transfer_proof WHERE id=$1`, proof.ProofID, proof.ToMailbox).Scan(&consumed); err != nil || !consumed {
		t.Fatal("proof consumption missing", err)
	}
	current, err := s.MailboxProjection(ctx, request.FromMailbox)
	if err != nil || current.Mailbox != old.Mailbox || current.Revision != old.Revision || current.Fingerprint != old.Fingerprint {
		t.Fatal("old box identity or projection moved with the address", err)
	}
	if results[0].Mailbox.ProtocolLogin == old.Mailbox.ProtocolLogin || results[0].Mailbox.StorageRef == old.Mailbox.StorageRef || results[0].Mailbox.RoundcubeKey == old.Mailbox.RoundcubeKey {
		t.Fatal("new person inherited an old mailbox key")
	}
	for _, binding := range verified.Sources[0].State.Bindings {
		if binding.Address != proof.Address {
			continue
		}
		observation := IngressObservation{SourceID: binding.SourceID, Generation: binding.Generation, AcceptedAddress: binding.Address, ClaimVersion: binding.ClaimVersion, MailboxID: binding.MailboxID, OwnerAnchor: binding.OwnerAnchor, MessageID: "pre-transfer", Sequence: 2, Bytes: 128, ETag: "pre-transfer", ReceivedAt: time.Now().Add(-time.Second)}
		task, err := s.StageIngress(ctx, observation)
		if err != nil || task.Observation.MailboxID != proof.FromMailbox {
			t.Fatal("preaccepted backlog lost its original target", err)
		}
		for _, mode := range []string{"new-owner", "after-close"} {
			bad := observation
			if mode == "new-owner" {
				bad.MailboxID, bad.OwnerAnchor = proof.ToMailbox, proof.ToOwner
			} else {
				bad.MessageID, bad.Sequence = "after-close", 3
			}
			if _, err = s.StageIngress(ctx, bad); !errors.Is(err, ErrIngressChanged) {
				t.Fatal("closed acceptance rebound or reopened", mode, err)
			}
		}
	}
	// An ordinary later alias change keeps the same consumed proof and keys.
	resource.Version, _ = mailboxID()
	resource.OperationID, _ = mailboxID()
	updated, err := s.StageMailbox(ctx, resource, results[0].Revision)
	if err != nil || updated.Mailbox.ProtocolLogin != results[0].Mailbox.ProtocolLogin || updated.Applied {
		t.Fatal("later directory version could not retain its proven address", err)
	}
}

func TestPostgreSQLWithdrawalProofRollsBackBeforeConflictingProjection(t *testing.T) {
	s, ctx, request, verified := withdrawalProofFixture(t)
	completeWithdrawalFixtureKicks(t, s, ctx, request.FromMailbox)
	proof, err := s.RecordAddressWithdrawal(ctx, request, verified)
	if err != nil {
		t.Fatal(err)
	}
	resource := preparedTransferResource(t, s, proof)
	resource.Addresses = append(resource.Addresses, identity.AddressResource{Address: proof.RemainingDefault, Kind: "alias", State: "active", Version: resource.Version, OperationID: resource.OperationID, OperationState: "complete"})
	if _, err = s.StageMailbox(ctx, resource, ""); !errors.Is(err, ErrAddressUnavailable) {
		t.Fatal("another address conflict was ignored", err)
	}
	var intact bool
	if err = s.DB.QueryRowContext(ctx, `SELECT applied_to IS NULL AND consumed_at IS NULL AND NOT EXISTS(SELECT 1 FROM mail.person WHERE anchor=$2) AND EXISTS(SELECT 1 FROM mail.address WHERE address=$3 AND mailbox_id=$4 AND state='retained' AND transfer_proof_id IS NULL) FROM mail.address_transfer_proof WHERE id=$1`, proof.ProofID, proof.ToOwner, proof.Address, proof.FromMailbox).Scan(&intact); err != nil || !intact {
		t.Fatal("failed projection consumed proof or moved ownership", err)
	}
}

func TestPostgreSQLWithdrawalProofRejectsChangedRecordTargetAndOldClaim(t *testing.T) {
	for _, mode := range []string{"record-drift", "creation-operation", "old-reactivated", "target-kind"} {
		t.Run(mode, func(t *testing.T) {
			s, ctx, request, verified := withdrawalProofFixture(t)
			completeWithdrawalFixtureKicks(t, s, ctx, request.FromMailbox)
			proof, err := s.RecordAddressWithdrawal(ctx, request, verified)
			if err != nil {
				t.Fatal(err)
			}
			resource := preparedTransferResource(t, s, proof)
			switch mode {
			case "record-drift":
				changed := proof
				changed.Actor, _ = mailboxID()
				body, _ := json.Marshal(changed)
				resource.Addresses[0].TransferRecord = string(body)
			case "creation-operation":
				resource.OperationID, _ = mailboxID()
				resource.Addresses[0].OperationID = resource.OperationID
			case "old-reactivated":
				old, err := s.MailboxProjection(ctx, proof.FromMailbox)
				if err != nil {
					t.Fatal(err)
				}
				old.Resource.Version, _ = mailboxID()
				old.Resource.Addresses[1].State = "active"
				old.Resource.Addresses[1].Version, _ = mailboxID()
				if _, err = s.StageMailbox(ctx, old.Resource, old.Revision); err != nil {
					t.Fatal(err)
				}
			case "target-kind":
				resource.Kind, resource.Addresses[0].Kind = "additional", "additional"
			}
			if _, err = s.StageMailbox(ctx, resource, ""); !errors.Is(err, ErrAddressUnavailable) {
				t.Fatal("changed proof/target or a new old-owner grant was accepted", err)
			}
			var safe bool
			if err = s.DB.QueryRowContext(ctx, `SELECT consumed_at IS NULL AND applied_to IS NULL AND NOT EXISTS(SELECT 1 FROM mail.person WHERE anchor=$2) FROM mail.address_transfer_proof WHERE id=$1`, proof.ProofID, proof.ToOwner).Scan(&safe); err != nil || !safe {
				t.Fatal("rejected transfer left partial target authority", err)
			}
		})
	}
}
