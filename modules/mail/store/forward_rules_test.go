// TEST_CASES: MAILMOD-T-012
package store

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestPostgreSQLForwardRuleIntentReadbackAndWithdrawal(t *testing.T) {
	s, ctx, resource, projection, source, generation := sourceRouteFixture(t)
	t.Cleanup(func() {
		clean, done := context.WithTimeout(context.Background(), 10*time.Second)
		defer done()
		for _, q := range []string{`DELETE FROM mail.forward_rule WHERE source_id=$1`, `DELETE FROM mail.forward_destination WHERE source_id=$1`, `DELETE FROM mail.forward_source WHERE source_id=$1`} {
			if _, e := s.DB.ExecContext(clean, q, source); e != nil {
				t.Error(e)
			}
		}
	})
	route := routeFor(resource)
	op, e := s.StageSourceRoutes(ctx, source, generation, []IngressRoute{route}, map[string]string{resource.ID: projection.Revision})
	if e != nil {
		t.Fatal(e)
	}
	if e = s.IssueSourceRoutes(ctx, op); e != nil {
		t.Fatal(e)
	}
	b := bindingFor(op, route, 1)
	if e = s.ObserveSourceRoutes(ctx, routeOutcome(op, 3, []IngressRouteBinding{b})); e != nil {
		t.Fatal(e)
	}
	dest, e := s.PrepareForwardDestination(ctx, source, generation, strings.Repeat("1", 32), strings.Repeat("2", 32), "in.example.test", route)
	if e != nil {
		t.Fatal(e)
	}
	if e = s.IssueForwardDestination(ctx, dest); e != nil {
		t.Fatal(e)
	}
	if e = s.ConfirmForwardDestination(ctx, dest, strings.Repeat("3", 32)); e != nil {
		t.Fatal(e)
	}
	dest, e = s.PrepareForwardDestination(ctx, source, generation, dest.Account, dest.Zone, dest.Domain, route)
	if e != nil {
		t.Fatal(e)
	}
	intent, e := s.PrepareForwardRule(ctx, dest)
	if e != nil || intent.State != "prepared" {
		t.Fatal("prepare rule", e)
	}
	fact := CloudflareRuleFact{ID: strings.Repeat("4", 32), Fingerprint: strings.Repeat("a", 64)}
	if e = s.ConfirmForwardRuleCreate(ctx, intent, fact); !errors.Is(e, ErrIngressChanged) {
		t.Fatal("unissued fact confirmed", e)
	}
	if e = s.RecordForwardRuleResponse(ctx, intent, fact); !errors.Is(e, ErrIngressChanged) {
		t.Fatal("unissued response accepted", e)
	}
	var wg sync.WaitGroup
	results := make(chan error, 8)
	for range 8 {
		wg.Add(1)
		go func() { defer wg.Done(); results <- (Store{DB: s.DB}).IssueForwardRuleCreate(ctx, intent) }()
	}
	wg.Wait()
	close(results)
	wins := 0
	for err := range results {
		if err == nil {
			wins++
		} else if !errors.Is(err, ErrIngressChanged) {
			t.Fatal(err)
		}
	}
	if wins != 1 {
		t.Fatal("nonexclusive issue", wins)
	}
	state, e := (Store{DB: s.DB}).PrepareForwardRule(ctx, dest)
	if e != nil || state.State != "create_issued" {
		t.Fatal("issued state lost", e)
	}
	if e = s.RecordForwardRuleResponse(ctx, intent, fact); e != nil {
		t.Fatal(e)
	}
	state, e = (Store{DB: s.DB}).PrepareForwardRule(ctx, dest)
	if e != nil || state.State != "create_responded" || state.Fact != fact {
		t.Fatal("response not persisted", e)
	}
	wrong := fact
	wrong.Fingerprint = strings.Repeat("b", 64)
	if e = s.ConfirmForwardRuleCreate(ctx, state, wrong); !errors.Is(e, ErrIngressChanged) {
		t.Fatal("different readback accepted", e)
	}
	if e = s.ConfirmForwardRuleCreate(ctx, state, fact); e != nil {
		t.Fatal(e)
	}
	if e = s.RecordForwardRuleResponse(ctx, intent, fact); e != nil {
		t.Fatal("late repeat", e)
	}
	state, e = s.PrepareForwardRule(ctx, dest)
	if e != nil || state.State != "created" {
		t.Fatal("created not retained", e)
	}
	listed, e := s.ForwardRules(ctx, source, generation, dest.Account, dest.Zone, dest.Domain)
	if e != nil || len(listed) != 1 || listed[0].Fact != fact || listed[0].Destination.Route != route {
		t.Fatal("recorded rule not listed", e)
	}
	if _, e = s.ForwardRules(ctx, source, generation, strings.Repeat("9", 32), dest.Zone, dest.Domain); !errors.Is(e, ErrIngressChanged) {
		t.Fatal("other account read source records", e)
	}
	if e = s.IssueForwardRuleDelete(ctx, state); !errors.Is(e, ErrIngressChanged) {
		t.Fatal("open route rule deleted", e)
	}
	if e = s.CloseIngressBinding(ctx, b.IngressBinding, 3); e != nil {
		t.Fatal(e)
	}
	// A later claim for the same address cannot create another public rule
	// until the old rule has been independently removed.
	nextResource := resource
	nextResource.Addresses = append(nextResource.Addresses[:0:0], nextResource.Addresses...)
	nextVersion, e := mailboxID()
	if e != nil {
		t.Fatal(e)
	}
	nextResource.Version, nextResource.OperationID = nextVersion, nextVersion
	nextResource.Addresses[0].Version, nextResource.Addresses[0].OperationID = nextVersion, nextVersion
	nextProjection, e := s.StageMailbox(ctx, nextResource, projection.Revision)
	if e != nil {
		t.Fatal(e)
	}
	nextRoute := routeFor(nextResource)
	nextOp, e := s.StageSourceRoutes(ctx, source, generation, []IngressRoute{nextRoute}, map[string]string{resource.ID: nextProjection.Revision})
	if e != nil {
		t.Fatal(e)
	}
	if e = s.IssueSourceRoutes(ctx, nextOp); e != nil {
		t.Fatal(e)
	}
	closed := int64(3)
	b.ClosedSequence = &closed
	nextBinding := bindingFor(nextOp, nextRoute, 4)
	if e = s.ObserveSourceRoutes(ctx, routeOutcome(nextOp, 3, []IngressRouteBinding{b, nextBinding})); e != nil {
		t.Fatal(e)
	}
	nextDest, e := s.PrepareForwardDestination(ctx, source, generation, dest.Account, dest.Zone, dest.Domain, nextRoute)
	if e != nil || nextDest.Target == dest.Target {
		t.Fatal("new claim reused old target", e)
	}
	if e = s.IssueForwardDestination(ctx, nextDest); e != nil {
		t.Fatal(e)
	}
	if e = s.ConfirmForwardDestination(ctx, nextDest, strings.Repeat("6", 32)); e != nil {
		t.Fatal(e)
	}
	nextDest, e = s.PrepareForwardDestination(ctx, source, generation, dest.Account, dest.Zone, dest.Domain, nextRoute)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.PrepareForwardRule(ctx, nextDest); !errors.Is(e, ErrIngressChanged) {
		t.Fatal("old rule did not fence new claim", e)
	}
	if e = s.IssueForwardRuleDelete(ctx, state); e != nil {
		t.Fatal(e)
	}
	if e = s.IssueForwardRuleDelete(ctx, state); !errors.Is(e, ErrIngressChanged) {
		t.Fatal("delete reissued", e)
	}
	if e = s.ConfirmForwardRuleDelete(ctx, state); e != nil {
		t.Fatal(e)
	}
	if e = s.ConfirmForwardRuleDelete(ctx, state); e != nil {
		t.Fatal("delete repeat", e)
	}
	next, e := s.PrepareForwardRule(ctx, nextDest)
	if e != nil || next.State != "prepared" {
		t.Fatal("new claim could not start after old removal", e)
	}
	if e = s.CancelForwardRule(ctx, next); !errors.Is(e, ErrIngressChanged) {
		t.Fatal("open claim cancelled", e)
	}
	if e = s.CloseIngressBinding(ctx, nextBinding.IngressBinding, 3); e != nil {
		t.Fatal(e)
	}
	if e = s.CancelForwardRule(ctx, next); e != nil {
		t.Fatal("never-issued rule was not cancelled", e)
	}
	listed, e = s.ForwardRules(ctx, source, generation, dest.Account, dest.Zone, dest.Domain)
	if e != nil || len(listed) != 2 {
		t.Fatal("removed history missing", e)
	}
	found := false
	for _, r := range listed {
		if r.Destination.BindingID == dest.BindingID && r.State == "removed" && r.Fact == fact {
			found = true
		}
	}
	if !found {
		t.Fatal("original rule history changed")
	}
	if _, e = s.PrepareForwardRule(ctx, dest); !errors.Is(e, ErrIngressChanged) {
		t.Fatal("closed route reopened", e)
	}
	var actual, id string
	if e = s.DB.QueryRowContext(ctx, `SELECT state,rule_id FROM mail.forward_rule WHERE binding_id=$1`, dest.BindingID).Scan(&actual, &id); e != nil || actual != "removed" || id != fact.ID {
		t.Fatal("history lost", e)
	}
}
