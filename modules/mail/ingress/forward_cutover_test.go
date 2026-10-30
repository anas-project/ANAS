// TEST_CASES: MAILMOD-T-009
package ingress

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/anas-project/ANAS/modules/mail/store"
)

type cutoverWorkerLedger struct {
	CloudflareRuleLedger
	bindings []store.CloudflareRuleBinding
}

func (l *cutoverWorkerLedger) CloudflareRuleBindings(context.Context, string, string) ([]store.CloudflareRuleBinding, error) {
	return l.bindings, nil
}

func TestForwardCutoverPublicPreflightRejectsUnknownAndDrift(t *testing.T) {
	for _, mode := range []string{"worker", "empty", "prepared", "forward-recovery", "foreign", "drift", "disabled-recorded", "unknown-worker", "wrong-claim", "unknown-forward", "unknown-create", "delete-issued", "missing-recorded", "duplicate-address", "incomplete-page", "wrong-zone"} {
		t.Run(mode, func(t *testing.T) {
			source, c, route, target, destID := forwardFixture(t)
			cfg := source.cfg
			cfg.ID = "33333333-3333-4333-8333-333333333333"
			cfg.Generation = "44444444-4444-4444-8444-444444444444"
			cfg.Origin = "https://old.example.test"
			oldSource, err := NewSourceClient(cfg)
			if err != nil {
				t.Fatal(err)
			}
			defer oldSource.Close()
			old, err := NewCloudflareRules(oldSource, c.rules.zone, "old-worker", strings.Repeat("r", 40))
			if err != nil {
				t.Fatal(err)
			}
			oldRule := CloudflareRule{ID: strings.Repeat("3", 32), Name: old.name(route.Address), Enabled: true, Source: "api", Actions: []CloudflareAction{{Type: "worker", Value: []string{"old-worker"}}}, Matchers: []CloudflareMatcher{{Type: "literal", Field: "to", Value: route.Address}}}
			newRule := CloudflareRule{ID: strings.Repeat("4", 32), Name: c.rules.name(route.Address), Enabled: true, Source: "api", Actions: []CloudflareAction{{Type: "forward", Value: []string{target}}}, Matchers: []CloudflareMatcher{{Type: "literal", Field: "to", Value: route.Address}}}
			oldLedger := &cutoverWorkerLedger{bindings: []store.CloudflareRuleBinding{{SourceID: cfg.ID, Generation: cfg.Generation, Address: route.Address, Worker: "old-worker", Fact: ruleFact(oldRule)}}}
			peer := &CloudflareRuleSync{Ledger: oldLedger, Client: old}
			ledger := &cutoverDestinationTestLedger{}
			rows := []CloudflareRule{oldRule}
			dest := store.ForwardDestinationIntent{Source: source.cfg.ID, Generation: source.cfg.Generation, Account: c.account, Zone: c.rules.zone, Domain: c.domain, Route: route, Target: target, BindingID: "synthetic", State: "created", DestinationID: destID}
			switch mode {
			case "empty":
				rows = []CloudflareRule{}
			case "forward-recovery":
				rows = []CloudflareRule{newRule}
				ledger.knownRules = []store.ForwardRuleIntent{{Destination: dest, State: "created", Fact: ruleFact(newRule)}}
			case "prepared":
				ledger.knownRules = []store.ForwardRuleIntent{{Destination: dest, State: "prepared"}}
			case "unknown-create":
				ledger.knownRules = []store.ForwardRuleIntent{{Destination: dest, State: "create_issued"}}
			case "delete-issued":
				ledger.knownRules = []store.ForwardRuleIntent{{Destination: dest, State: "delete_issued", Fact: ruleFact(newRule)}}
			case "missing-recorded":
				ledger.knownRules = []store.ForwardRuleIntent{{Destination: dest, State: "created", Fact: ruleFact(newRule)}}
			case "unknown-forward":
				rows = []CloudflareRule{newRule}
			case "wrong-claim":
				wrong := dest
				wrong.Route.ClaimVersion = "66666666-6666-4666-8666-666666666666"
				ledger.knownRules = []store.ForwardRuleIntent{{Destination: wrong, State: "created", Fact: ruleFact(newRule)}}
				rows = []CloudflareRule{newRule}
			case "foreign":
				rows[0].Name = "foreign-rule"
			case "drift":
				rows[0].Enabled = false
			case "disabled-recorded":
				rows[0].Enabled = false
				oldLedger.bindings[0].Fact = ruleFact(rows[0])
			case "unknown-worker":
				oldLedger.bindings[0].Fact = store.CloudflareRuleFact{}
			case "duplicate-address":
				rows = append(rows, newRule)
				ledger.knownRules = []store.ForwardRuleIntent{{Destination: dest, State: "created", Fact: ruleFact(newRule)}}
			case "wrong-zone":
				old.zone = strings.Repeat("9", 32)
			}
			reads := 0
			source.http.Transport = routeTransport(func(r *http.Request) (*http.Response, error) {
				if r.Method != http.MethodGet || !strings.HasSuffix(r.URL.Path, "/rules") {
					t.Fatal("public preflight mutated provider")
				}
				reads++
				total := len(rows)
				if mode == "incomplete-page" {
					total++
				}
				return routeResponse(200, map[string]any{"success": true, "result": rows, "result_info": map[string]any{"page": 1, "per_page": 50, "count": len(rows), "total_count": total}}), nil
			})
			syncer := &ForwardSync{Ledger: ledger, Client: c}
			err = syncer.PreflightCutoverRules(t.Context(), []Route{route}, []*CloudflareRuleSync{peer})
			if err == nil && len(ledger.failures) != 0 {
				t.Fatal("successful preflight cleared public status", ledger.failures)
			}
			if errors.Is(err, ErrRuleConflict) && (len(ledger.failures) != 1 || ledger.failures[0] != "cloudflare-rule-conflict") {
				t.Fatal("public conflict not recorded", ledger.failures)
			}
			switch mode {
			case "worker", "empty", "prepared", "forward-recovery":
				if err != nil || reads != 1 {
					t.Fatal("exact recorded rule rejected", err)
				}
			case "wrong-zone":
				if !errors.Is(err, ErrConfiguration) || reads != 0 {
					t.Fatal("different zone accepted", err)
				}
			case "incomplete-page":
				if !errors.Is(err, ErrIdentity) {
					t.Fatal("incomplete page accepted", err)
				}
			default:
				if !errors.Is(err, ErrRuleConflict) {
					t.Fatal("unowned or changed rule accepted", err)
				}
			}
		})
	}
}

func TestForwardPreflightAcceptsOnlyRecordedDrainingForwardRules(t *testing.T) {
	for _, mode := range []string{"created", "responded", "delete-issued-present", "delete-issued-absent", "prepared-absent", "unknown-create", "missing-created", "foreign", "drift", "disabled", "wrong-claim", "unknown-forward", "duplicate", "wrong-zone", "same-source"} {
		t.Run(mode, func(t *testing.T) {
			oldSource, oldClient, route, target, destID := forwardFixture(t)
			cfg := oldSource.cfg
			cfg.ID = "77777777-7777-4777-8777-777777777777"
			cfg.Generation = "88888888-8888-4888-8888-888888888888"
			newSource, err := NewSourceClient(cfg)
			if err != nil {
				t.Fatal(err)
			}
			defer newSource.Close()
			newClient, err := NewCloudflareForwardRules(newSource, oldClient.account, oldClient.rules.zone, oldClient.domain, strings.Repeat("f", 40), strings.Repeat("g", 40))
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
			case "same-source":
				oldSource.cfg.ID = newSource.cfg.ID
			}
			oldLedger := &cutoverDestinationTestLedger{knownRules: []store.ForwardRuleIntent{intent}}
			old := &ForwardSync{Ledger: oldLedger, Client: oldClient}
			new := &ForwardSync{Ledger: &cutoverDestinationTestLedger{}, Client: newClient}
			reads := 0
			newSource.http.Transport = routeTransport(func(r *http.Request) (*http.Response, error) {
				if r.Method != http.MethodGet {
					t.Fatal("preflight changed provider rules")
				}
				reads++
				return routeResponse(200, map[string]any{"success": true, "result": rows, "result_info": map[string]any{"page": 1, "per_page": 50, "count": len(rows), "total_count": len(rows)}}), nil
			})
			err = new.PreflightCutoverWithForwards(t.Context(), []Route{route}, nil, []*ForwardSync{old})
			switch mode {
			case "created", "responded", "delete-issued-present", "delete-issued-absent", "prepared-absent":
				if err != nil || reads != 1 {
					t.Fatal("recorded forward rejected", err, reads)
				}
			case "unknown-create":
				if !errors.Is(err, ErrRuleUnknown) || reads != 0 {
					t.Fatal("unknown create accepted", err)
				}
			case "wrong-zone", "same-source":
				if !errors.Is(err, ErrConfiguration) || reads != 0 {
					t.Fatal("wrong source accepted", err)
				}
			default:
				if !errors.Is(err, ErrRuleConflict) {
					t.Fatal("untrusted rule accepted", err)
				}
			}
		})
	}
}
