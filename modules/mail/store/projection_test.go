package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/anas-project/ANAS/modules/mail/identity"
)

func projectionResource(t *testing.T, kind string) identity.MailboxResource {
	t.Helper()
	id, _ := mailboxID()
	owner, _ := mailboxID()
	version, _ := mailboxID()
	operation, _ := mailboxID()
	address := "p" + strings.ReplaceAll(id, "-", "") + "@example.test"
	aKind := kind
	if kind == "additional" {
		aKind = "additional"
	}
	return identity.MailboxResource{ID: id, OwnerAnchor: owner, Kind: kind, DefaultAddress: address, State: "active", Version: version, OperationID: operation, OperationState: "complete", Addresses: []identity.AddressResource{{Address: address, Kind: aKind, State: "active", Version: version, OperationID: operation, OperationState: "complete"}}}
}
func TestProjectionFingerprintRejectsAmbiguityAndCanonicalizesOrder(t *testing.T) {
	r := projectionResource(t, "primary")
	r.Addresses = append(r.Addresses, identity.AddressResource{Address: "alias@example.test", Kind: "alias", State: "active", Version: r.Version, OperationID: r.OperationID, OperationState: "complete"})
	a, _, err := ProjectionFingerprint(r)
	if err != nil {
		t.Fatal(err)
	}
	r.Addresses[0], r.Addresses[1] = r.Addresses[1], r.Addresses[0]
	b, _, err := ProjectionFingerprint(r)
	if err != nil || a != b {
		t.Fatal("address ordering changed fingerprint")
	}
	for _, change := range []string{"owner", "default", "duplicate", "claim_version", "default_kind", "alias_kind", "unknown_state", "incomplete"} {
		t.Run(change, func(t *testing.T) {
			bad := r
			bad.Addresses = append([]identity.AddressResource(nil), r.Addresses...)
			switch change {
			case "owner":
				bad.OwnerAnchor = "alice"
			case "default":
				bad.DefaultAddress = "Alice@EXAMPLE.TEST"
			case "duplicate":
				bad.Addresses = append(bad.Addresses, bad.Addresses[0])
			case "claim_version":
				bad.Addresses[0].Version = "bad"
			case "default_kind":
				bad.Addresses[1].Kind = "alias"
			case "alias_kind":
				bad.Addresses[0].Kind = "primary"
			case "unknown_state":
				bad.State = "deleted"
			case "incomplete":
				bad.OperationState = "reserved"
			}
			if _, _, err := ProjectionFingerprint(bad); !errors.Is(err, ErrProjectionConflict) {
				t.Fatal("ambiguous resource accepted", err)
			}
		})
	}
}
func projectionStore(t *testing.T) (Store, context.Context, identity.MailboxResource) {
	t.Helper()
	db, ctx := testDatabase(t)
	s := Store{DB: db}
	if err := s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	r := projectionResource(t, "primary")
	cleanupProjectionOwner(t, db, r.OwnerAnchor)
	return s, ctx, r
}

// Register after opening the database so cleanup runs before its connection closes.
// The restart fixture registers this only in its second process.
func cleanupProjectionOwner(t *testing.T, db *sql.DB, owner string) {
	t.Helper()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		for _, q := range []string{`DELETE FROM mail.revocation_task WHERE session_id IN (SELECT id FROM mail.web_session WHERE owner_anchor=$1)`, `DELETE FROM mail.smtp_submission WHERE mailbox_id IN (SELECT id FROM mail.mailbox WHERE owner_anchor=$1)`, `DELETE FROM mail.protocol_credential WHERE session_id IN (SELECT id FROM mail.web_session WHERE owner_anchor=$1)`, `DELETE FROM mail.webmail_handoff WHERE session_id IN (SELECT id FROM mail.web_session WHERE owner_anchor=$1)`, `DELETE FROM mail.mailbox_projection WHERE mailbox_id IN (SELECT id FROM mail.mailbox WHERE owner_anchor=$1)`, `DELETE FROM mail.address WHERE mailbox_id IN (SELECT id FROM mail.mailbox WHERE owner_anchor=$1)`, `DELETE FROM mail.web_session WHERE owner_anchor=$1`, `DELETE FROM mail.mailbox WHERE owner_anchor=$1`, `DELETE FROM mail.person WHERE anchor=$1`} {
			if _, err := db.ExecContext(ctx, q, owner); err != nil {
				t.Error(err)
				return
			}
		}
	})
}
func TestPostgreSQLProjectionKeepsADIdentityAndRequiresDownstream(t *testing.T) {
	s, ctx, r := projectionStore(t)
	p, err := s.StageMailbox(ctx, r, "")
	if err != nil || p.Mailbox.ID != r.ID || p.Applied || p.Mailbox.State != "pending" || p.Epoch < 1 {
		t.Fatal("stage", err)
	}
	if p.Mailbox.ProtocolLogin == r.ID || strings.Contains(p.Mailbox.ProtocolLogin, r.OwnerAnchor) || strings.Contains(p.Mailbox.StorageRef, r.DefaultAddress) {
		t.Fatal("public identity used as protocol/storage key")
	}
	again, err := s.StageMailbox(ctx, r, "")
	if err != nil || again.Revision != p.Revision || again.Mailbox != p.Mailbox {
		t.Fatal("idempotent stage", err)
	}
	if _, err := s.ConfirmMailbox(ctx, r.ID, p.Revision, strings.Repeat("0", 64)); !errors.Is(err, ErrProjectionConflict) {
		t.Fatal("unverified config activated", err)
	}
	active, err := s.ConfirmMailbox(ctx, r.ID, p.Revision, p.Fingerprint)
	if err != nil || !active.Applied || active.Mailbox.State != "active" {
		t.Fatal("confirm", err)
	}
	// Projection cannot enable a person's directory admission.
	var state string
	if err := s.DB.QueryRowContext(ctx, `SELECT directory_state FROM mail.person WHERE anchor=$1`, r.OwnerAnchor).Scan(&state); err != nil || state != "unknown" {
		t.Fatal("projection granted personnel login", err)
	}
	_, session, err := s.AdmitSession(ctx, VerifiedLogin{OwnerAnchor: r.OwnerAnchor, Issuer: "https://projection.example.test", SID: "projection", AuthenticatedAt: time.Now().Add(-time.Minute), Expires: time.Now().Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	ticket, err := s.IssueHandoff(ctx, session, r.ID, strings.Repeat("A", 43))
	if err != nil {
		t.Fatal("confirmed box unavailable", err)
	}
	if err := s.WithholdMailbox(ctx, r.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RedeemHandoff(ctx, ticket.Code, ticket.SessionID, r.ID, strings.Repeat("A", 43)); !errors.Is(err, ErrHandoffDenied) {
		t.Fatal("old handoff survived withdrawal", err)
	}
	if _, err := s.ConfirmMailbox(ctx, r.ID, p.Revision, p.Fingerprint); !errors.Is(err, ErrProjectionConflict) {
		t.Fatal("withdrawal resurrected by old confirm", err)
	}
	restaged, err := s.StageMailbox(ctx, r, p.Revision)
	if err != nil || restaged.Revision == p.Revision || restaged.Epoch <= p.Epoch {
		t.Fatal("reauthorization failed to advance generation", err)
	}
	if restaged.Mailbox.ProtocolLogin != p.Mailbox.ProtocolLogin || restaged.Mailbox.StorageRef != p.Mailbox.StorageRef || restaged.Mailbox.RoundcubeKey != p.Mailbox.RoundcubeKey {
		t.Fatal("reconcile changed private identity")
	}
}
func TestPostgreSQLProjectionAliasChangesPreserveClaimsAndRejectStale(t *testing.T) {
	s, ctx, r := projectionStore(t)
	p, err := s.StageMailbox(ctx, r, "")
	if err != nil {
		t.Fatal(err)
	}
	p, err = s.ConfirmMailbox(ctx, r.ID, p.Revision, p.Fingerprint)
	if err != nil {
		t.Fatal(err)
	}
	r.Version, _ = mailboxID()
	alias := identity.AddressResource{Address: "a" + strings.ReplaceAll(r.ID, "-", "") + "@example.test", Kind: "alias", State: "active", Version: r.Version, OperationID: r.OperationID, OperationState: "complete"}
	r.Addresses = append(r.Addresses, alias)
	if _, err = s.StageMailbox(ctx, r, ""); !errors.Is(err, ErrProjectionConflict) {
		t.Fatal("missing precondition accepted", err)
	}
	added, err := s.StageMailbox(ctx, r, p.Revision)
	if err != nil || added.Mailbox.State != "pending" || added.Epoch <= p.Epoch {
		t.Fatal("alias did not close access", err)
	}
	if _, err = s.ConfirmMailbox(ctx, r.ID, p.Revision, p.Fingerprint); !errors.Is(err, ErrProjectionConflict) {
		t.Fatal("stale worker made alias ready", err)
	}
	r.Version, _ = mailboxID()
	r.Addresses[1].State = "retained"
	r.Addresses[1].Version = r.Version
	removed, err := s.StageMailbox(ctx, r, added.Revision)
	if err != nil {
		t.Fatal(err)
	}
	var state string
	var owner string
	if err = s.DB.QueryRowContext(ctx, `SELECT state,mailbox_id FROM mail.address WHERE address=$1`, alias.Address).Scan(&state, &owner); err != nil || state != "retained" || owner != r.ID {
		t.Fatal("removed address released", err)
	}
	if removed.Mailbox.ProtocolLogin != p.Mailbox.ProtocolLogin || removed.Mailbox.StorageRef != p.Mailbox.StorageRef || removed.Mailbox.RoundcubeKey != p.Mailbox.RoundcubeKey {
		t.Fatal("alias moved historic storage")
	}
	if err = s.ProjectionFailed(ctx, r.ID, removed.Revision, "dms-unavailable"); err != nil {
		t.Fatal(err)
	}
	if err = s.ProjectionFailed(ctx, r.ID, added.Revision, "old-worker"); !errors.Is(err, ErrProjectionConflict) {
		t.Fatal("stale error corrupted current operation", err)
	}
	ready, err := s.ConfirmMailbox(ctx, r.ID, removed.Revision, removed.Fingerprint)
	if err != nil || !ready.Applied || ready.Error != "" {
		t.Fatal("recovery", err)
	}
}
func TestPostgreSQLProjectionConcurrentVersionsAndIndependentBoxes(t *testing.T) {
	s, ctx, r := projectionStore(t)
	p, err := s.StageMailbox(ctx, r, "")
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	out := make(chan error, 12)
	for range 12 {
		wg.Go(func() {
			next := r
			next.Version, _ = mailboxID()
			_, err := s.StageMailbox(ctx, next, p.Revision)
			out <- err
		})
	}
	wg.Wait()
	close(out)
	winners := 0
	for err := range out {
		if err == nil {
			winners++
		} else if !errors.Is(err, ErrProjectionConflict) {
			t.Fatal(err)
		}
	}
	if winners != 1 {
		t.Fatal("more than one version winner", winners)
	}
	additional := projectionResource(t, "additional")
	additional.OwnerAnchor = r.OwnerAnchor
	a, err := s.StageMailbox(ctx, additional, "")
	if err != nil {
		t.Fatal(err)
	}
	if a.Mailbox.ID == p.Mailbox.ID || a.Mailbox.ProtocolLogin == p.Mailbox.ProtocolLogin || a.Mailbox.StorageRef == p.Mailbox.StorageRef || a.Mailbox.RoundcubeKey == p.Mailbox.RoundcubeKey {
		t.Fatal("independent box identities merged")
	}
	foreign := r
	foreign.OwnerAnchor, _ = mailboxID()
	if _, err := s.StageMailbox(ctx, foreign, p.Revision); !errors.Is(err, ErrProjectionConflict) {
		t.Fatal("mailbox transferred to foreign owner", err)
	}
}
func TestPostgreSQLProjectionPendingAndDisabledCannotGrantAccess(t *testing.T) {
	s, ctx, r := projectionStore(t)
	r.Addresses = append(r.Addresses, identity.AddressResource{Address: "pending-" + r.DefaultAddress, Kind: "alias", State: "pending", Version: r.Version, OperationID: r.OperationID, OperationState: "reserved"})
	p, err := s.StageMailbox(ctx, r, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.ConfirmMailbox(ctx, r.ID, p.Revision, p.Fingerprint); !errors.Is(err, ErrProjectionConflict) {
		t.Fatal("pending claim granted access", err)
	}
	r.Addresses = r.Addresses[:1]
	r.State = "disabled"
	r.Version, _ = mailboxID()
	p, err = s.StageMailbox(ctx, r, p.Revision)
	if err != nil {
		t.Fatal(err)
	}
	p, err = s.ConfirmMailbox(ctx, r.ID, p.Revision, p.Fingerprint)
	if err != nil || !p.Applied || p.Mailbox.State != "disabled" {
		t.Fatal("disabled box lost receive config or granted login", err)
	}
	var addressState string
	if err := s.DB.QueryRowContext(ctx, `SELECT state FROM mail.address WHERE address=$1`, r.DefaultAddress).Scan(&addressState); err != nil || addressState != "active" {
		t.Fatal("disabled box lost incoming address", err)
	}
	_, session, err := s.AdmitSession(ctx, VerifiedLogin{OwnerAnchor: r.OwnerAnchor, Issuer: "https://projection.example.test", SID: "disabled-box", AuthenticatedAt: time.Now().Add(-time.Minute), Expires: time.Now().Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.IssueHandoff(ctx, session, r.ID, strings.Repeat("A", 43)); !errors.Is(err, ErrHandoffDenied) {
		t.Fatal("disabled box signed a handoff", err)
	}
}
