// TEST_CASES: MAILMOD-T-012
package store

import (
	"errors"
	"strings"
	"sync"
	"testing"
)

func TestPostgreSQLCloudflareRuleOperationsKeepLateResultsAndExactLease(t *testing.T) {
	s, ctx, r, p, source, generation := sourceRouteFixture(t)
	routes := []IngressRoute{routeFor(r)}
	worker := "anas-isolated-mail-rule"
	if e := s.PrepareCloudflareRules(ctx, source, generation, worker, routes); !errors.Is(e, ErrIngressChanged) {
		t.Fatal("provider operations preceded source confirmation", e)
	}
	op, e := s.StageSourceRoutes(ctx, source, generation, routes, map[string]string{r.ID: p.Revision})
	if e != nil {
		t.Fatal(e)
	}
	if e = s.IssueSourceRoutes(ctx, op); e != nil {
		t.Fatal(e)
	}
	b := bindingFor(op, routes[0], 1)
	if e = s.ObserveSourceRoutes(ctx, routeOutcome(op, 0, []IngressRouteBinding{b})); e != nil {
		t.Fatal(e)
	}
	for range 2 {
		if e = s.PrepareCloudflareRules(ctx, source, generation, worker, routes); e != nil {
			t.Fatal(e)
		}
	}
	var wg sync.WaitGroup
	wins := make(chan CloudflareRuleOperation, 6)
	failures := make(chan error, 6)
	for range 6 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			o, e := s.ClaimCloudflareRule(ctx, source)
			if e == nil {
				wins <- o
			} else {
				failures <- e
			}
		}()
	}
	wg.Wait()
	close(wins)
	close(failures)
	for e := range failures {
		if !errors.Is(e, ErrIngressIdle) {
			t.Fatal(e)
		}
	}
	if len(wins) != 1 {
		t.Fatal("provider intent has multiple writers", len(wins))
	}
	first := <-wins
	if first.Lease == "" || first.Attempts != 1 || first.State != "prepared" || first.Action != "create" || first.RouteRevision != 1 {
		t.Fatal("claim did not return updated lease", first)
	}
	fact := CloudflareRuleFact{ID: strings.Repeat("a", 32), Fingerprint: strings.Repeat("b", 64)}
	if e = s.ConfirmCloudflareRule(ctx, first, fact); !errors.Is(e, ErrIngressChanged) {
		t.Fatal("unissued provider intent confirmed", e)
	}
	if e = s.IssueCloudflareRule(ctx, first); e != nil {
		t.Fatal(e)
	}
	// The provider may have created the rule but its response was lost.
	if e = s.ReleaseCloudflareRule(ctx, first, "cloudflare-outcome-unknown"); e != nil {
		t.Fatal(e)
	}
	if _, e = s.DB.ExecContext(ctx, `UPDATE mail.cloudflare_rule_operation SET next_attempt=clock_timestamp() WHERE id=$1`, first.ID); e != nil {
		t.Fatal(e)
	}
	resumed, e := (Store{DB: s.DB}).ClaimCloudflareRule(ctx, source)
	if e != nil || resumed.ID != first.ID || resumed.Lease == first.Lease || resumed.State != "issued" || resumed.Attempts != 2 {
		t.Fatal("issued provider intent lost on restart", resumed, e)
	}
	if e = s.ConfirmCloudflareRule(ctx, first, fact); !errors.Is(e, ErrIngressChanged) {
		t.Fatal("stale worker completed provider operation", e)
	}
	// The address is removed while this old create is uncertain. The source
	// fence closes first. A late result records configuration history only.
	remove, e := s.StageSourceRoutes(ctx, source, generation, nil, nil)
	if e != nil {
		t.Fatal(e)
	}
	if e = s.IssueSourceRoutes(ctx, remove); e != nil {
		t.Fatal(e)
	}
	fence := int64(0)
	b.ClosedSequence = &fence
	if e = s.ObserveSourceRoutes(ctx, routeOutcome(remove, 0, []IngressRouteBinding{b})); e != nil {
		t.Fatal(e)
	}
	if e = s.PrepareCloudflareRules(ctx, source, generation, worker, nil); e != nil {
		t.Fatal(e)
	}
	if e = s.IssueCloudflareRule(ctx, resumed); !errors.Is(e, ErrIngressChanged) {
		t.Fatal("superseded provider create reissued", e)
	}
	if e = s.ConfirmCloudflareRule(ctx, resumed, fact); e != nil {
		t.Fatal("late result could not be reconciled", e)
	}
	if e = s.PrepareCloudflareRules(ctx, source, generation, worker, nil); e != nil {
		t.Fatal(e)
	}
	deletion, e := s.ClaimCloudflareRule(ctx, source)
	if e != nil || deletion.Action != "delete" || deletion.Expected != fact || deletion.RouteRevision != 2 {
		t.Fatal("late rule was not scheduled for owned cleanup", deletion, e)
	}
	if e = s.IssueCloudflareRule(ctx, deletion); e != nil {
		t.Fatal(e)
	}
	if e = s.ConfirmCloudflareRule(ctx, deletion, CloudflareRuleFact{}); e != nil {
		t.Fatal(e)
	}
	bindings, e := s.CloudflareRuleBindings(ctx, source, generation)
	if e != nil || len(bindings) != 1 || bindings[0].Fact.ID != "" || bindings[0].ObservedAt == nil {
		t.Fatal("provider deletion was not durably confirmed", bindings, e)
	}
	if e = s.PrepareCloudflareRules(ctx, source, generation, worker, routes); !errors.Is(e, ErrIngressChanged) {
		t.Fatal("old desired provider rules bypassed source restore fence", e)
	}
	if e = s.ConfirmIngressBinding(ctx, b.IngressBinding); !errors.Is(e, ErrIngressChanged) {
		t.Fatal("provider result reopened closed source permission", e)
	}
}

func TestPostgreSQLCloudflareCancelledCreateRemainsDiscoverableBehindExactSourceFence(t *testing.T) {
	s, ctx, r, p, source, generation := sourceRouteFixture(t)
	worker := "anas-isolated-late-rule"
	routes := []IngressRoute{routeFor(r)}
	op, e := s.StageSourceRoutes(ctx, source, generation, routes, map[string]string{r.ID: p.Revision})
	if e != nil {
		t.Fatal(e)
	}
	if e = s.IssueSourceRoutes(ctx, op); e != nil {
		t.Fatal(e)
	}
	b := bindingFor(op, routes[0], 1)
	if e = s.ObserveSourceRoutes(ctx, routeOutcome(op, 0, []IngressRouteBinding{b})); e != nil {
		t.Fatal(e)
	}
	if e = s.PrepareCloudflareRules(ctx, source, generation, worker, routes); e != nil {
		t.Fatal(e)
	}
	fact := CloudflareRuleFact{ID: strings.Repeat("c", 32), Fingerprint: strings.Repeat("d", 64)}
	if e = s.ObserveCloudflareRule(ctx, source, generation, r.DefaultAddress, worker, fact); !errors.Is(e, ErrIngressChanged) {
		t.Fatal("unissued create permitted rule adoption", e)
	}
	create, e := s.ClaimCloudflareRule(ctx, source)
	if e != nil {
		t.Fatal(e)
	}
	if e = s.IssueCloudflareRule(ctx, create); e != nil {
		t.Fatal(e)
	}
	if e = s.CancelSupersededCloudflareCreate(ctx, create); !errors.Is(e, ErrIngressChanged) {
		t.Fatal("live source permission permitted cancellation", e)
	}
	remove, e := s.StageSourceRoutes(ctx, source, generation, nil, nil)
	if e != nil {
		t.Fatal(e)
	}
	if e = s.IssueSourceRoutes(ctx, remove); e != nil {
		t.Fatal(e)
	}
	fence := int64(0)
	b.ClosedSequence = &fence
	if e = s.ObserveSourceRoutes(ctx, routeOutcome(remove, 0, []IngressRouteBinding{b})); e != nil {
		t.Fatal(e)
	}
	if e = s.PrepareCloudflareRules(ctx, source, generation, worker, nil); e != nil {
		t.Fatal(e)
	}
	stale := create
	stale.Lease, _ = mailboxID()
	if e = s.CancelSupersededCloudflareCreate(ctx, stale); !errors.Is(e, ErrIngressChanged) {
		t.Fatal("wrong lease cancelled a provider operation", e)
	}
	if e = s.CancelSupersededCloudflareCreate(ctx, create); e != nil {
		t.Fatal("exact permanent source fence did not permit cancellation", e)
	}
	var state string
	var issued bool
	if e = s.DB.QueryRowContext(ctx, `SELECT state,issued_at IS NOT NULL FROM mail.cloudflare_rule_operation WHERE id=$1`, create.ID).Scan(&state, &issued); e != nil || state != "cancelled" || !issued {
		t.Fatal("cancelled request lost its possible late outcome", state, issued, e)
	}
	// A new process sees the provider's late creation after cancellation. It
	// records the exact owned object and schedules deletion, without regranting
	// the permanently closed source claim or mailbox login.
	restarted := Store{DB: s.DB}
	if e = restarted.ObserveCloudflareRule(ctx, source, generation, r.DefaultAddress, worker, fact); e != nil {
		t.Fatal("late owned result was no longer discoverable", e)
	}
	changed := fact
	changed.ID = strings.Repeat("e", 32)
	if e = restarted.ObserveCloudflareRule(ctx, source, generation, r.DefaultAddress, worker, changed); !errors.Is(e, ErrIngressChanged) {
		t.Fatal("known provider ID replaced by unrelated object", e)
	}
	if e = restarted.PrepareCloudflareRules(ctx, source, generation, worker, nil); e != nil {
		t.Fatal(e)
	}
	deletion, e := restarted.ClaimCloudflareRule(ctx, source)
	if e != nil || deletion.Action != "delete" || deletion.Expected != fact || deletion.RouteRevision != 2 {
		t.Fatal("late cancelled create did not become exact owned cleanup", deletion, e)
	}
	if e = restarted.IssueCloudflareRule(ctx, deletion); e != nil {
		t.Fatal(e)
	}
	if e = restarted.ConfirmCloudflareRule(ctx, deletion, CloudflareRuleFact{}); e != nil {
		t.Fatal(e)
	}
	if e = restarted.ConfirmIngressBinding(ctx, b.IngressBinding); !errors.Is(e, ErrIngressChanged) {
		t.Fatal("late cancelled result reopened source permission", e)
	}
	current, e := restarted.MailboxProjection(ctx, r.ID)
	if e != nil || current.Applied {
		t.Fatal("configuration history granted mailbox login", e)
	}
}
