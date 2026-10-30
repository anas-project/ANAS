// TEST_CASES: MAILMOD-T-012
package main

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/anas-project/ANAS/modules/mail/dms"
	"github.com/anas-project/ANAS/modules/mail/identity"
	"github.com/anas-project/ANAS/modules/mail/ingress"
	"github.com/anas-project/ANAS/modules/mail/store"
)

type syncDirectory struct {
	resource          identity.MailboxResource
	err, admissionErr error
	reads             int
	changeAfter       int
}

func TestPostgreSQLAppliedMailboxRejectsUnrecognizedSourceButKeepsTransientOutageAccess(t *testing.T) {
	file := os.Getenv("MAIL_REVOCATION_TEST_CONFIG")
	if file == "" {
		t.Skip("isolated coordinator database required; no real-host pass")
	}
	t.Setenv("MAIL_STORE_TEST_CONFIG", file)
	for _, mode := range []string{"unknown-version", "changed-identity", "public-rule-drift", "temporary-outage"} {
		t.Run(mode, func(t *testing.T) {
			db := loginTestDatabase(t)
			data := store.Store{DB: db}
			owner := handoffPerson(t, db)
			address := "known-" + owner + "@example.test"
			r := identity.MailboxResource{ID: webmailTestBox, OwnerAnchor: owner, Kind: "primary", DefaultAddress: address, State: "active", Version: webmailTestBox, OperationID: webmailTestBox, OperationState: "complete", Addresses: []identity.AddressResource{{Address: address, Kind: "primary", State: "active", Version: webmailTestBox, OperationID: webmailTestBox, OperationState: "complete"}}}
			directory := &syncDirectory{resource: r}
			backend := &syncDMS{}
			c := mailCoordinator{store: data, directory: directory, dms: backend}
			ref := identity.MailboxReference{ID: r.ID, OwnerAnchor: owner}
			if e := c.reconcileMailbox(t.Context(), ref); e != nil {
				t.Fatal(e)
			}
			_, cookie, e := data.AdmitSession(t.Context(), store.VerifiedLogin{OwnerAnchor: owner, Issuer: fixtureIssuer, SID: "source-mismatch-" + owner, AuthenticatedAt: time.Now().Add(-time.Minute), Expires: time.Now().Add(time.Hour)})
			if e != nil {
				t.Fatal(e)
			}
			grant, e := data.MailboxForSession(t.Context(), cookie, r.ID)
			if e != nil {
				t.Fatal(e)
			}
			if _, e = data.IssueCredentials(t.Context(), grant); e != nil {
				t.Fatal(e)
			}
			cause := error(store.ErrIngressChanged)
			switch mode {
			case "changed-identity":
				cause = ingress.ErrIdentity
			case "public-rule-drift":
				cause = ingress.ErrRuleConflict
			case "temporary-outage":
				cause = ingress.ErrUnavailable
			}
			c.sources = &syncSources{run: func(map[string]string) error { return cause }}
			if e = c.reconcileMailbox(t.Context(), ref); e == nil || e.Error() != "source-routes-unconfirmed" {
				t.Fatal("source failure hidden or leaked", e)
			}
			current, e := data.MailboxProjection(t.Context(), r.ID)
			if e != nil {
				t.Fatal(e)
			}
			_, grantErr := data.CheckMailboxGrant(t.Context(), grant)
			if mode == "temporary-outage" {
				if current.Mailbox.State != "active" || grantErr != nil {
					t.Fatal("ordinary source outage revoked an approved local mailbox")
				}
				return
			}
			if current.Mailbox.State != "disabled" || current.Error != "source-state-unrecognized" || !errors.Is(grantErr, store.ErrHandoffDenied) {
				t.Fatal("old applied snapshot authorized access after source mismatch")
			}
			var tasks int
			if e = data.DB.QueryRowContext(t.Context(), `SELECT count(*) FROM mail.revocation_task WHERE mailbox_id=$1`, r.ID).Scan(&tasks); e != nil || tasks == 0 {
				t.Fatal("source mismatch did not persist downstream revocation", e)
			}
			var addressState string
			if e = data.DB.QueryRowContext(t.Context(), `SELECT state FROM mail.address WHERE address=$1`, address).Scan(&addressState); e != nil || addressState != "active" {
				t.Fatal("authorization hold altered receiving ownership", e)
			}
			c.sources = &syncSources{run: func(map[string]string) error { return nil }}
			if e = c.reconcileMailbox(t.Context(), ref); e != nil {
				t.Fatal("matching verified source did not recover", e)
			}
			if _, e = data.MailboxForSession(t.Context(), cookie, r.ID); e != nil {
				t.Fatal("fresh mailbox grant unavailable after reconciliation", e)
			}
			if _, e = data.CheckMailboxGrant(t.Context(), grant); !errors.Is(e, store.ErrHandoffDenied) {
				t.Fatal("old grant resurrected after source recovery")
			}
		})
	}
}

func (d *syncDirectory) Person(_ context.Context, owner string) (identity.Person, error) {
	return identity.Person{Anchor: owner}, d.admissionErr
}
func (d *syncDirectory) MailboxReferences(context.Context) ([]identity.MailboxReference, error) {
	return []identity.MailboxReference{{ID: d.resource.ID, OwnerAnchor: d.resource.OwnerAnchor}}, d.err
}
func (d *syncDirectory) MailboxConfiguration(context.Context, string, string) (identity.MailboxResource, error) {
	d.reads++
	r := d.resource
	if d.changeAfter > 0 && d.reads >= d.changeAfter {
		r.Version = "eeeeeeee-eeee-4eee-8eee-eeeeeeeeeeee"
	}
	return r, d.err
}

type syncDMS struct {
	receipt       dms.Receipt
	applied       int
	down, unknown bool
	afterApply    func()
}

type syncSources struct {
	run   func(map[string]string) error
	calls int
}

func (s *syncSources) Synchronize(_ context.Context, ready map[string]string) error {
	s.calls++
	return s.run(ready)
}

func (d *syncDMS) Read(context.Context, string) (dms.Receipt, error) {
	if d.down {
		r := d.receipt
		r.Applied = false
		return r, dms.ErrUnavailable
	}
	if d.receipt.MailboxID == "" {
		return dms.Receipt{}, dms.ErrConflict
	}
	return d.receipt, nil
}
func (d *syncDMS) Apply(_ context.Context, in dms.ApplyRequest) (dms.Receipt, error) {
	if d.receipt.Revision != "" && d.receipt.Revision != in.Projection.Revision && in.ExpectedRevision != d.receipt.Revision {
		return dms.Receipt{}, dms.ErrConflict
	}
	d.applied++
	d.receipt = dms.Receipt{MailboxID: in.Projection.Mailbox.ID, Revision: in.Projection.Revision, Fingerprint: in.Projection.Fingerprint, Applied: !d.unknown}
	if d.afterApply != nil {
		d.afterApply()
	}
	if d.unknown || d.down {
		return d.receipt, dms.ErrUnavailable
	}
	return d.receipt, nil
}

// Actual PG state/recovery with controlled AD and DMS responses. Fixed DMS and
// actual LDAP remain separate acceptance steps, not inferred from this test.
func TestPostgreSQLMailboxCoordinatorRecovery(t *testing.T) {
	file := os.Getenv("MAIL_REVOCATION_TEST_CONFIG")
	if file == "" {
		t.Skip("isolated coordinator database required; no real-host pass")
	}
	t.Setenv("MAIL_STORE_TEST_CONFIG", file)
	for _, mode := range []string{"success", "dms-unknown", "directory-changed", "directory-down", "concurrent-withdrawal", "source-unconfirmed", "directory-changed-after-source", "forward-pending", "batch-pending", "raw-pending", "pending-directory-changed", "pending-conflict", "typed-pending-conflict"} {
		t.Run(mode, func(t *testing.T) {
			db := loginTestDatabase(t)
			data := store.Store{DB: db}
			owner := handoffPerson(t, db)
			r := identity.MailboxResource{ID: webmailTestBox, OwnerAnchor: owner, Kind: "primary", DefaultAddress: "sync-" + owner + "@example.test", State: "active", Version: webmailTestBox, OperationID: webmailTestBox, OperationState: "complete", Addresses: []identity.AddressResource{{Address: "sync-" + owner + "@example.test", Kind: "primary", State: "active", Version: webmailTestBox, OperationID: webmailTestBox, OperationState: "complete"}}}
			directory := &syncDirectory{resource: r}
			backend := &syncDMS{}
			c := mailCoordinator{store: data, directory: directory, dms: backend}
			ref := identity.MailboxReference{ID: r.ID, OwnerAnchor: owner}
			switch mode {
			case "dms-unknown":
				backend.unknown = true
			case "directory-changed":
				directory.changeAfter = 2
			case "directory-down":
				directory.err = identity.ErrDirectoryUnavailable
			case "concurrent-withdrawal":
				backend.afterApply = func() {
					if err := data.WithholdMailbox(t.Context(), r.ID); err != nil {
						t.Error(err)
					}
				}
			case "source-unconfirmed", "directory-changed-after-source", "forward-pending", "batch-pending", "raw-pending", "pending-directory-changed", "pending-conflict", "typed-pending-conflict":
				c.sources = &syncSources{run: func(ready map[string]string) error {
					if !backend.receipt.Applied || ready[r.ID] != backend.receipt.Revision {
						t.Fatal("source published before DMS readback")
					}
					if mode == "source-unconfirmed" {
						return errors.New("cloud unavailable")
					}
					if mode == "forward-pending" {
						return forwardPublicPending{cause: ingress.ErrDestinationPending}
					}
					if mode == "batch-pending" {
						return forwardPublicPending{cause: ingress.ErrForwardBatchPending}
					}
					if mode == "raw-pending" {
						return ingress.ErrDestinationPending
					}
					if mode == "pending-conflict" {
						return errors.Join(ingress.ErrDestinationPending, ingress.ErrRuleConflict)
					}
					if mode == "typed-pending-conflict" {
						return errors.Join(forwardPublicPending{cause: ingress.ErrDestinationPending}, ingress.ErrRuleConflict)
					}
					directory.changeAfter = directory.reads + 1
					if mode == "pending-directory-changed" {
						return forwardPublicPending{cause: ingress.ErrDestinationPending}
					}
					return nil
				}}
			}
			err := c.reconcileMailbox(t.Context(), ref)
			if mode == "success" {
				if err != nil {
					t.Fatal(err)
				}
				p, err := data.MailboxProjection(t.Context(), r.ID)
				if err != nil || !p.Applied || p.Mailbox.State != "active" {
					t.Fatal("verified projection not ready", err)
				}
				before := backend.applied
				if err = c.reconcileMailbox(t.Context(), ref); err != nil || backend.applied != before {
					t.Fatal("unchanged verified box reapplied", err)
				}
				return
			}
			if err == nil {
				t.Fatal("failure reported success", mode)
			}
			p, pErr := data.MailboxProjection(t.Context(), r.ID)
			if mode == "forward-pending" || mode == "batch-pending" {
				if pErr != nil || !p.Applied || p.Mailbox.State != "active" || !errors.Is(err, map[string]error{"forward-pending": ingress.ErrDestinationPending, "batch-pending": ingress.ErrForwardBatchPending}[mode]) {
					t.Fatal("local readiness or pending status lost", err, pErr)
				}
				_, cookie, e := data.AdmitSession(t.Context(), store.VerifiedLogin{OwnerAnchor: owner, Issuer: fixtureIssuer, SID: "pending-" + owner, AuthenticatedAt: time.Now().Add(-time.Minute), Expires: time.Now().Add(time.Hour)})
				if e != nil {
					t.Fatal(e)
				}
				grant, e := data.MailboxForSession(t.Context(), cookie, r.ID)
				if e != nil {
					t.Fatal("owner cannot open locally confirmed mailbox", e)
				}
				if _, e = data.IssueCredentials(t.Context(), grant); e != nil {
					t.Fatal("owner cannot obtain local mailbox credential", e)
				}
				if e = data.DenyPerson(t.Context(), owner); e != nil {
					t.Fatal(e)
				}
				if _, e = data.MailboxForSession(t.Context(), cookie, r.ID); !errors.Is(e, store.ErrHandoffDenied) {
					t.Fatal("revoked owner retained access", e)
				}
				return
			}
			if mode == "directory-down" {
				if !errors.Is(pErr, store.ErrProjectionConflict) || backend.applied != 0 {
					t.Fatal("directory fault fabricated a projection", pErr)
				}
				return
			}
			if pErr != nil || p.Applied || p.Mailbox.State == "active" {
				t.Fatal("unconfirmed projection active", mode, pErr)
			}
			if mode == "dms-unknown" {
				previous := backend.receipt.Revision
				directory.resource.Version = "dddddddd-dddd-4ddd-8ddd-dddddddddddd"
				backend.unknown = false
				if err = c.reconcileMailbox(t.Context(), ref); err != nil {
					t.Fatal("newer AD version cannot recover interrupted older DMS apply", err)
				}
				p, err = data.MailboxProjection(t.Context(), r.ID)
				if err != nil || !p.Applied || p.Revision == previous {
					t.Fatal("newer recovered projection not confirmed", err)
				}
			}
		})
	}
}
