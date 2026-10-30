// TEST_CASES: MAILMOD-T-009
package ingress

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/anas-project/ANAS/modules/mail/store"
)

type preflightLedger struct {
	CloudflareRuleLedger // Any provider mutation preparation is unexpected.
	bindings             []store.CloudflareRuleBinding
	observeErr           error
	observations         int
	failures             []string
}

func (l *preflightLedger) CloudflareRuleBindings(context.Context, string, string) ([]store.CloudflareRuleBinding, error) {
	return l.bindings, nil
}
func (l *preflightLedger) ObserveCloudflareRule(context.Context, string, string, string, string, store.CloudflareRuleFact) error {
	l.observations++
	return l.observeErr
}
func (l *preflightLedger) CloudflareRuleFailure(_ context.Context, _, _, code string) error {
	l.failures = append(l.failures, code)
	return nil
}

func TestPublicPreflightAllowsOnlyKnownDrainingRules(t *testing.T) {
	for _, mode := range []string{"known-old", "old-already-removed", "unknown-old", "old-drift", "foreign-rule", "worker-drift", "old-other-zone", "own-unknown", "unrelated", "rate-limited"} {
		t.Run(mode, func(t *testing.T) {
			nextSource, nextClient, _ := cloudRuleFixture(t)
			oldSource, _, route := routeFixture(t)
			oldSource.cfg.ID = "77777777-7777-4777-8777-777777777777"
			oldClient, err := NewCloudflareRules(oldSource, nextClient.zone, "anas-isolated-old", strings.Repeat("e", 40))
			if err != nil {
				t.Fatal(err)
			}
			oldRule := CloudflareRule{ID: strings.Repeat("2", 32), Name: oldClient.name(route.Address), Enabled: true, Source: "api", Actions: []CloudflareAction{{Type: "worker", Value: []string{oldClient.worker}}}, Matchers: []CloudflareMatcher{{Type: "literal", Field: "to", Value: route.Address}}}
			oldLedger := &preflightLedger{bindings: []store.CloudflareRuleBinding{{SourceID: oldSource.cfg.ID, Generation: oldSource.cfg.Generation, Worker: oldClient.worker, Address: route.Address, Fact: ruleFact(oldRule)}}}
			nextLedger := &preflightLedger{}
			old := &CloudflareRuleSync{Ledger: oldLedger, Client: oldClient}
			next := &CloudflareRuleSync{Ledger: nextLedger, Client: nextClient, lastSuccess: time.Now()}
			rows, code := []CloudflareRule{oldRule}, 200
			switch mode {
			case "old-already-removed":
				rows = []CloudflareRule{}
			case "unknown-old":
				oldLedger.bindings[0].Fact = store.CloudflareRuleFact{}
				oldLedger.observeErr = store.ErrIngressChanged
			case "old-drift":
				rows[0].Enabled = false
			case "foreign-rule":
				rows[0].Name = "somebody else's route"
			case "worker-drift":
				rows[0].Actions = []CloudflareAction{{Type: "worker", Value: []string{"other-worker"}}}
			case "old-other-zone":
				oldClient.zone = strings.Repeat("9", 32)
			case "own-unknown":
				rows[0].Name = nextClient.name(route.Address)
				rows[0].Actions = []CloudflareAction{{Type: "worker", Value: []string{nextClient.worker}}}
			case "unrelated":
				rows[0].Name = "unmanaged"
				rows[0].Matchers = []CloudflareMatcher{{Type: "literal", Field: "to", Value: "other@example.test"}}
			case "rate-limited":
				code = 429
			}
			gets := 0
			nextSource.http.Transport = routeTransport(func(r *http.Request) (*http.Response, error) {
				gets++
				if r.Method != "GET" || r.URL.Host != "api.cloudflare.com" {
					t.Error("preflight mutated provider or source")
				}
				return routeResponse(code, map[string]any{"success": true, "result": rows, "result_info": map[string]int{"page": 1, "per_page": 50, "count": len(rows), "total_count": len(rows)}}), nil
			})
			err = next.Preflight(t.Context(), []Route{route}, []*CloudflareRuleSync{old})
			wantOK := mode == "known-old" || mode == "old-already-removed" || mode == "unrelated"
			if (err == nil) != wantOK || gets != 1 {
				t.Fatal("unsafe replacement preflight", err, gets)
			}
			if mode == "unknown-old" && !errors.Is(err, store.ErrIngressChanged) {
				t.Fatal("unknown ID bypassed durable issued proof", err)
			}
			if mode == "known-old" && oldLedger.observations != 1 {
				t.Fatal("old provider fact not checked by its ledger")
			}
		})
	}
}

type freshRouteLedger struct {
	RouteLedger
	op             store.IngressRouteOperation
	observationErr error
}

func (l freshRouteLedger) StageSourceRoutes(context.Context, string, string, []store.IngressRoute, map[string]string) (store.IngressRouteOperation, error) {
	return l.op, nil
}
func (l freshRouteLedger) ObserveSourceRoutes(context.Context, store.IngressRouteResult) error {
	return l.observationErr
}
func (l freshRouteLedger) SourceRoutesFailed(context.Context, store.IngressRouteOperation, string) error {
	return nil
}

func TestReplacementPreparationDoesNotUseWarmRouteCache(t *testing.T) {
	c, state, route := routeFixture(t)
	fp, _ := RouteFingerprint([]Route{route})
	reads := 0
	c.http.Transport = routeTransport(func(r *http.Request) (*http.Response, error) {
		reads++
		if r.Method != "GET" {
			t.Fatal("confirmed preparation wrote a new operation")
		}
		return routeResponse(200, state), nil
	})
	ledger := freshRouteLedger{op: store.IngressRouteOperation{State: "confirmed", Fingerprint: fp}, observationErr: store.ErrIngressChanged}
	s := &RouteSync{Ledger: ledger, Source: c, ID: c.cfg.ID, Generation: c.cfg.Generation, confirmed: fp, checked: time.Now()}
	if err := s.Synchronize(t.Context(), []Route{route}, nil); err != nil || reads != 0 {
		t.Fatal("ordinary cache changed", err, reads)
	}
	if err := s.Prepare(t.Context(), []Route{route}, nil); !errors.Is(err, store.ErrIngressChanged) || reads != 1 {
		t.Fatal("stale cache authorized withdrawing another source", err, reads)
	}
}

func TestWorkerPreflightAcceptsOnlyRecordedForwardRules(t *testing.T) {
	for _, mode := range []string{"created", "responded", "delete-issued-present", "delete-issued-absent", "prepared-absent", "unknown-create", "missing-created", "foreign", "drift", "disabled", "wrong-claim", "unknown-forward", "duplicate", "wrong-zone"} {
		t.Run(mode, func(t *testing.T) {
			oldSource, oldClient, route, target, destID := forwardFixture(t)
			newSource, _, _ := routeFixture(t)
			newSource.cfg.ID = "77777777-7777-4777-8777-777777777777"
			newClient, err := NewCloudflareRules(newSource, oldClient.rules.zone, "new-worker", strings.Repeat("r", 40))
			if err != nil {
				t.Fatal(err)
			}
			rule := CloudflareRule{ID: strings.Repeat("4", 32), Name: oldClient.rules.name(route.Address), Enabled: true, Source: "api", Actions: []CloudflareAction{{Type: "forward", Value: []string{target}}}, Matchers: []CloudflareMatcher{{Type: "literal", Field: "to", Value: route.Address}}}
			dest := store.ForwardDestinationIntent{Source: oldSource.cfg.ID, Generation: oldSource.cfg.Generation, Account: oldClient.account, Zone: oldClient.rules.zone, Domain: oldClient.domain, Route: route, Target: target, BindingID: "synthetic", State: "created", DestinationID: destID}
			intent := store.ForwardRuleIntent{Destination: dest, State: "created", Fact: ruleFact(rule)}
			rows := []CloudflareRule{rule}
			switch mode {
			case "responded":
				intent.State = "create_responded"
			case "delete-issued-present":
				intent.State = "delete_issued"
			case "delete-issued-absent":
				intent.State = "delete_issued"
				rows = []CloudflareRule{}
			case "prepared-absent":
				intent.State = "prepared"
				intent.Fact = store.CloudflareRuleFact{}
				rows = []CloudflareRule{}
			case "unknown-create":
				intent.State = "create_issued"
				intent.Fact = store.CloudflareRuleFact{}
				rows = []CloudflareRule{}
			case "missing-created":
				rows = []CloudflareRule{}
			case "foreign":
				rows[0].Name = "foreign"
			case "drift":
				rows[0].Actions[0].Value[0] = "other@example.test"
			case "disabled":
				rows[0].Enabled = false
				intent.Fact = ruleFact(rows[0])
			case "wrong-claim":
				intent.Destination.Route.ClaimVersion = "66666666-6666-4666-8666-666666666666"
				intent.Destination.Target, _ = oldClient.target(intent.Destination.Route)
			case "unknown-forward":
				intent.State = "removed"
			case "duplicate":
				rows = append(rows, rule)
				rows[1].ID = strings.Repeat("5", 32)
			case "wrong-zone":
				oldClient.rules.zone = strings.Repeat("9", 32)
			}
			oldLedger := &cutoverDestinationTestLedger{knownRules: []store.ForwardRuleIntent{intent}}
			old := &ForwardSync{Ledger: oldLedger, Client: oldClient}
			new := &CloudflareRuleSync{Ledger: &preflightLedger{}, Client: newClient}
			reads := 0
			newSource.http.Transport = routeTransport(func(r *http.Request) (*http.Response, error) {
				if r.Method != http.MethodGet {
					t.Fatal("preflight changed provider rules")
				}
				reads++
				return routeResponse(200, map[string]any{"success": true, "result": rows, "result_info": map[string]any{"page": 1, "per_page": 50, "count": len(rows), "total_count": len(rows)}}), nil
			})
			err = new.PreflightWithForwards(t.Context(), []Route{route}, nil, []*ForwardSync{old})
			switch mode {
			case "created", "responded", "delete-issued-present", "delete-issued-absent", "prepared-absent":
				if err != nil || reads != 1 {
					t.Fatal("recorded forward rejected", err, reads)
				}
			case "unknown-create":
				if !errors.Is(err, ErrRuleUnknown) || reads != 0 {
					t.Fatal("unknown create accepted", err)
				}
			case "wrong-zone":
				if !errors.Is(err, ErrConfiguration) || reads != 0 {
					t.Fatal("wrong zone accepted", err)
				}
			default:
				if !errors.Is(err, ErrRuleConflict) {
					t.Fatal("untrusted rule accepted", err)
				}
			}
		})
	}
}
