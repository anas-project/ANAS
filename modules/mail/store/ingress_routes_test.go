// TEST_CASES: MAILMOD-T-012
package store

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/anas-project/ANAS/modules/mail/identity"
)

func sourceRouteFixture(t *testing.T) (Store, context.Context, identity.MailboxResource, Projection, string, string) {
	t.Helper()
	s, ctx, r := projectionStore(t)
	p, e := s.StageMailbox(ctx, r, "")
	if e != nil {
		t.Fatal(e)
	}
	source, _ := mailboxID()
	generation, _ := mailboxID()
	t.Cleanup(func() {
		clean, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		for _, q := range []string{`DELETE FROM mail.cloudflare_rule_operation WHERE source_id=$1`, `DELETE FROM mail.cloudflare_rule WHERE source_id=$1`, `DELETE FROM mail.ingress_message WHERE source_id=$1`, `DELETE FROM mail.ingress_binding WHERE source_id=$1`, `DELETE FROM mail.ingress_route_operation WHERE source_id=$1`, `DELETE FROM mail.ingress_route_state WHERE source_id=$1`} {
			if _, e := s.DB.ExecContext(clean, q, source); e != nil {
				t.Error(e)
			}
		}
	})
	return s, ctx, r, p, source, generation
}
func routeFor(r identity.MailboxResource) IngressRoute {
	return IngressRoute{Address: r.DefaultAddress, MailboxID: r.ID, OwnerAnchor: r.OwnerAnchor, ClaimVersion: r.Addresses[0].Version}
}
func routeOutcome(op IngressRouteOperation, seq int64, bindings []IngressRouteBinding) IngressRouteResult {
	return IngressRouteResult{SourceID: op.SourceID, Generation: op.Generation, OperationID: op.ID, Fingerprint: op.Fingerprint, Revision: op.ExpectedRevision + 1, Sequence: seq, Bindings: bindings}
}
func bindingFor(op IngressRouteOperation, r IngressRoute, first int64) IngressRouteBinding {
	return IngressRouteBinding{IngressBinding: IngressBinding{SourceID: op.SourceID, Generation: op.Generation, Address: r.Address, MailboxID: r.MailboxID, OwnerAnchor: r.OwnerAnchor, ClaimVersion: r.ClaimVersion, FirstSequence: first}}
}
func TestPostgreSQLSourceRouteIntentRecoveryAndRestoreFence(t *testing.T) {
	s, ctx, r, p, source, generation := sourceRouteFixture(t)
	routes := []IngressRoute{routeFor(r)}
	if _, e := s.StageSourceRoutes(ctx, source, generation, routes, nil); !errors.Is(e, ErrIngressChanged) {
		t.Fatal("unconfirmed DMS opened route", e)
	}
	ready := map[string]string{r.ID: p.Revision}
	op, e := s.StageSourceRoutes(ctx, source, generation, routes, ready)
	if e != nil || op.State != "prepared" {
		t.Fatal("durable intent", e)
	}
	var count int
	if e = s.DB.QueryRowContext(ctx, `SELECT count(*) FROM mail.ingress_route_operation WHERE id=$1 AND state='prepared'`, op.ID).Scan(&count); e != nil || count != 1 {
		t.Fatal("intent missing before network", e)
	}
	out := routeOutcome(op, 3, []IngressRouteBinding{bindingFor(op, routes[0], 1)})
	if e = s.ObserveSourceRoutes(ctx, out); !errors.Is(e, ErrIngressChanged) {
		t.Fatal("unissued mutation accepted", e)
	}
	var wg sync.WaitGroup
	results := make(chan IngressRouteOperation, 6)
	failures := make(chan error, 6)
	for range 6 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			v, e := s.StageSourceRoutes(ctx, source, generation, routes, ready)
			results <- v
			failures <- e
		}()
	}
	wg.Wait()
	close(results)
	close(failures)
	for e := range failures {
		if e != nil {
			t.Fatal(e)
		}
	}
	for v := range results {
		if v.ID != op.ID {
			t.Fatal("concurrent intent diverged")
		}
	}
	if e = s.IssueSourceRoutes(ctx, op); e != nil {
		t.Fatal(e)
	}
	// The HTTP reply was lost. A new Store instance accepts only this recorded
	// operation, confirms its original target and reuses it on the next retry.
	restarted := Store{DB: s.DB}
	if e = restarted.ObserveSourceRoutes(ctx, out); e != nil {
		t.Fatal("lost response recovery", e)
	}
	if e = restarted.ObserveSourceRoutes(ctx, out); e != nil {
		t.Fatal("repeat readback", e)
	}
	again, e := restarted.StageSourceRoutes(ctx, source, generation, routes, ready)
	if e != nil || again.ID != op.ID || again.State != "confirmed" {
		t.Fatal("confirmed operation repeated", e)
	}
	bad := out
	bad.Revision = 2
	bad.OperationID, _ = mailboxID()
	if e = s.ObserveSourceRoutes(ctx, bad); !errors.Is(e, ErrIngressChanged) {
		t.Fatal("unknown cloud revision adopted", e)
	}
	// A restored old ledger cannot infer permission from a current cloud read.
	if _, e = s.DB.ExecContext(ctx, `UPDATE mail.ingress_route_state SET revision=0 WHERE source_id=$1`, source); e != nil {
		t.Fatal(e)
	}
	if e = s.ObserveSourceRoutes(ctx, out); !errors.Is(e, ErrIngressChanged) {
		t.Fatal("old backup overwrote known operation state", e)
	}
}

func TestPostgreSQLSourceRouteClosureKeepsOldAcceptedMail(t *testing.T) {
	s, ctx, r, p, source, generation := sourceRouteFixture(t)
	routes := []IngressRoute{routeFor(r)}
	ready := map[string]string{r.ID: p.Revision}
	op, e := s.StageSourceRoutes(ctx, source, generation, routes, ready)
	if e != nil {
		t.Fatal(e)
	}
	if e = s.IssueSourceRoutes(ctx, op); e != nil {
		t.Fatal(e)
	}
	b := bindingFor(op, routes[0], 1)
	if e = s.ObserveSourceRoutes(ctx, routeOutcome(op, 4, []IngressRouteBinding{b})); e != nil {
		t.Fatal(e)
	}
	remove, e := s.StageSourceRoutes(ctx, source, generation, nil, nil)
	if e != nil {
		t.Fatal(e)
	}
	if e = s.IssueSourceRoutes(ctx, remove); e != nil {
		t.Fatal(e)
	}
	fence := int64(4)
	b.ClosedSequence = &fence
	closed := routeOutcome(remove, 4, []IngressRouteBinding{b})
	if e = s.ObserveSourceRoutes(ctx, closed); e != nil {
		t.Fatal(e)
	}
	o := IngressObservation{SourceID: source, Generation: generation, MessageID: "accepted-before-removal", MailboxID: r.ID, OwnerAnchor: r.OwnerAnchor, AcceptedAddress: r.DefaultAddress, ClaimVersion: routes[0].ClaimVersion, Sequence: 4, Bytes: 100, ETag: "raw-1", ReceivedAt: time.Now().UTC()}
	if _, e = s.StageIngress(ctx, o); e != nil {
		t.Fatal("old acceptance lost original mailbox", e)
	}
	o.MessageID = "after-removal"
	o.Sequence = 5
	if _, e = s.StageIngress(ctx, o); !errors.Is(e, ErrIngressChanged) {
		t.Fatal("closed route admitted new mail", e)
	}
	widen := closed
	widen.Sequence = 5
	later := int64(5)
	widen.Bindings = []IngressRouteBinding{b}
	widen.Bindings[0].ClosedSequence = &later
	if e = s.ObserveSourceRoutes(ctx, widen); !errors.Is(e, ErrIngressChanged) {
		t.Fatal("closure widened", e)
	}
	reopen, e := s.StageSourceRoutes(ctx, source, generation, routes, ready)
	if e != nil {
		t.Fatal(e)
	}
	if e = s.IssueSourceRoutes(ctx, reopen); e != nil {
		t.Fatal(e)
	}
	b.ClosedSequence = nil
	if e = s.ObserveSourceRoutes(ctx, routeOutcome(reopen, 5, []IngressRouteBinding{b})); !errors.Is(e, ErrIngressChanged) {
		t.Fatal("old closed permission reopened", e)
	}
}

func TestPostgreSQLSourceRoutePendingChangesKeepUnchangedAddresses(t *testing.T) {
	s, ctx, r, p, source, generation := sourceRouteFixture(t)
	ready := map[string]string{r.ID: p.Revision}
	route := routeFor(r)
	op, e := s.StageSourceRoutes(ctx, source, generation, []IngressRoute{route}, ready)
	if e != nil {
		t.Fatal(e)
	}
	if e = s.IssueSourceRoutes(ctx, op); e != nil {
		t.Fatal(e)
	}
	if e = s.ObserveSourceRoutes(ctx, routeOutcome(op, 0, []IngressRouteBinding{bindingFor(op, route, 1)})); e != nil {
		t.Fatal(e)
	}
	if e = s.WithholdMailbox(ctx, r.ID); e != nil {
		t.Fatal(e)
	}
	got, e := s.IngressRouteSnapshot(ctx, source, generation, nil)
	if e != nil || len(got) != 1 || got[0] != route {
		t.Fatal("temporary auth/DMS withdrawal closed receiving", got, e)
	}
	r.State = "pending"
	r.OperationState = "reserved"
	r.Version, _ = mailboxID()
	alias := identity.AddressResource{Address: "alias-" + r.ID + "@example.test", Kind: "alias", State: "pending", Version: r.Version, OperationID: r.OperationID, OperationState: "reserved"}
	r.Addresses = append(r.Addresses, alias)
	p, e = s.StageMailbox(ctx, r, p.Revision)
	if e != nil {
		t.Fatal(e)
	}
	got, e = s.IngressRouteSnapshot(ctx, source, generation, nil)
	if e != nil || len(got) != 1 || got[0] != route {
		t.Fatal("pending alias removed unchanged primary", got, e)
	}
	if _, e = s.StageSourceRoutes(ctx, source, generation, got, nil); e != nil {
		t.Fatal("existing receiving could not survive pending resource", e)
	}
	foreign, _ := mailboxID()
	other, e := s.IngressRouteSnapshot(ctx, foreign, generation, nil)
	if e != nil || len(other) != 0 {
		t.Fatal("unconfirmed new source copied old source permission", e)
	}
	r.State = "active"
	r.OperationState = "complete"
	r.Addresses[1].State = "active"
	r.Addresses[1].OperationState = "complete"
	p, e = s.StageMailbox(ctx, r, p.Revision)
	if e != nil {
		t.Fatal(e)
	}
	got, e = s.IngressRouteSnapshot(ctx, source, generation, nil)
	if e != nil || len(got) != 1 {
		t.Fatal("unconfirmed alias published", e)
	}
	ready[r.ID] = p.Revision
	got, e = s.IngressRouteSnapshot(ctx, source, generation, ready)
	if e != nil || len(got) != 2 {
		t.Fatal("DMS-confirmed alias unavailable", e)
	}
	if _, e = s.StageSourceRoutes(ctx, source, generation, got, ready); e != nil {
		t.Fatal("confirmed alias rejected", e)
	}
}

func TestPostgreSQLSourceRouteReturningToOldIntentSealsInFlightChange(t *testing.T) {
	s, ctx, r, p, source, generation := sourceRouteFixture(t)
	routes := []IngressRoute{routeFor(r)}
	ready := map[string]string{r.ID: p.Revision}
	a, e := s.StageSourceRoutes(ctx, source, generation, routes, ready)
	if e != nil {
		t.Fatal(e)
	}
	if e = s.IssueSourceRoutes(ctx, a); e != nil {
		t.Fatal(e)
	}
	if e = s.ObserveSourceRoutes(ctx, routeOutcome(a, 0, []IngressRouteBinding{bindingFor(a, routes[0], 1)})); e != nil {
		t.Fatal(e)
	}
	b, e := s.StageSourceRoutes(ctx, source, generation, nil, nil)
	if e != nil {
		t.Fatal(e)
	}
	if e = s.IssueSourceRoutes(ctx, b); e != nil {
		t.Fatal(e)
	}
	// B may be in flight. A cannot be reported unchanged merely because the
	// last observed cloud state still matches it.
	seal, e := s.StageSourceRoutes(ctx, source, generation, routes, ready)
	if e != nil || seal.State != "prepared" || seal.ID == a.ID || seal.ExpectedRevision != 1 {
		t.Fatal("in-flight mutation incorrectly reported unchanged", seal, e)
	}
	if e = s.IssueSourceRoutes(ctx, seal); e != nil {
		t.Fatal(e)
	}
	if e = s.ObserveSourceRoutes(ctx, routeOutcome(seal, 0, []IngressRouteBinding{bindingFor(a, routes[0], 1)})); e != nil {
		t.Fatal(e)
	}
	if e = s.ObserveSourceRoutes(ctx, routeOutcome(b, 0, nil)); !errors.Is(e, ErrIngressChanged) {
		t.Fatal("late superseded mutation replaced confirmed seal", e)
	}
}

func TestPostgreSQLSourceRouteWithdrawalBlocksNewWorkBeforeCloudCleanup(t *testing.T) {
	s, ctx, r, p, source, generation := sourceRouteFixture(t)
	r.Version, _ = mailboxID()
	alias := identity.AddressResource{Address: "remove-" + r.ID + "@example.test", Kind: "alias", State: "active", Version: r.Version, OperationID: r.OperationID, OperationState: "complete"}
	r.Addresses = append(r.Addresses, alias)
	p, e := s.StageMailbox(ctx, r, p.Revision)
	if e != nil {
		t.Fatal(e)
	}
	routes := []IngressRoute{routeFor(r), {Address: alias.Address, MailboxID: r.ID, OwnerAnchor: r.OwnerAnchor, ClaimVersion: alias.Version}}
	op, e := s.StageSourceRoutes(ctx, source, generation, routes, map[string]string{r.ID: p.Revision})
	if e != nil {
		t.Fatal(e)
	}
	if e = s.IssueSourceRoutes(ctx, op); e != nil {
		t.Fatal(e)
	}
	if e = s.ObserveSourceRoutes(ctx, routeOutcome(op, 1, []IngressRouteBinding{bindingFor(op, routes[0], 1), bindingFor(op, routes[1], 1)})); e != nil {
		t.Fatal(e)
	}
	o := IngressObservation{SourceID: source, Generation: generation, MessageID: "already-recorded", MailboxID: r.ID, OwnerAnchor: r.OwnerAnchor, AcceptedAddress: alias.Address, ClaimVersion: alias.Version, Sequence: 1, Bytes: 100, ETag: "raw-1", ReceivedAt: time.Now().UTC()}
	if _, e = s.StageIngress(ctx, o); e != nil {
		t.Fatal(e)
	}
	r.Version, _ = mailboxID()
	r.Addresses[1].State = "retained"
	if _, e = s.StageMailbox(ctx, r, p.Revision); e != nil {
		t.Fatal(e)
	}
	// The cloud still has the old open binding because its cleanup failed.
	if _, e = s.StageIngress(ctx, o); e != nil {
		t.Fatal("existing backlog could not resume", e)
	}
	o.MessageID = "stale-cloud-new"
	o.Sequence = 2
	if _, e = s.StageIngress(ctx, o); !errors.Is(e, ErrIngressChanged) {
		t.Fatal("withdrawal waited for cloud cleanup before rejecting new work", e)
	}
	o.MessageID = "unchanged-primary"
	o.AcceptedAddress = r.DefaultAddress
	o.ClaimVersion = r.Addresses[0].Version
	if _, e = s.StageIngress(ctx, o); e != nil {
		t.Fatal("withdrawal also blocked unchanged address", e)
	}
	status, e := s.IngressSummary(ctx, source, generation)
	if e != nil || status.Backlog != 2 || status.Complete != 0 || status.RouteRevision != 1 || status.RouteObservedAt == nil || status.LastPoll != nil || status.NextAttempt == nil {
		t.Fatal("runtime status lost real ledger state", status, e)
	}
	other, _ := mailboxID()
	if _, e = s.IngressSummary(ctx, source, other); !errors.Is(e, ErrIngressChanged) {
		t.Fatal("runtime status ignored source generation", e)
	}
}
