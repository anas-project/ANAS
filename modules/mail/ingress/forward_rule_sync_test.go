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

type forwardRuleTestLedger struct {
	destinationTestLedger
	ruleState                 string
	fact                      store.CloudflareRuleFact
	issues, records, confirms int
	closed                    bool
}

func (l *forwardRuleTestLedger) PrepareForwardRule(_ context.Context, d store.ForwardDestinationIntent) (store.ForwardRuleIntent, error) {
	return store.ForwardRuleIntent{Destination: d, State: l.ruleState, Fact: l.fact}, nil
}
func (l *forwardRuleTestLedger) IssueForwardRuleCreate(context.Context, store.ForwardRuleIntent) error {
	if l.ruleState != "prepared" {
		return store.ErrIngressChanged
	}
	l.issues++
	l.ruleState = "create_issued"
	return nil
}
func (l *forwardRuleTestLedger) RecordForwardRuleResponse(_ context.Context, _ store.ForwardRuleIntent, f store.CloudflareRuleFact) error {
	if l.ruleState != "create_issued" {
		return store.ErrIngressChanged
	}
	l.records++
	l.ruleState, l.fact = "create_responded", f
	return nil
}
func (l *forwardRuleTestLedger) ConfirmForwardRuleCreate(_ context.Context, _ store.ForwardRuleIntent, f store.CloudflareRuleFact) error {
	if l.ruleState != "create_responded" || f != l.fact {
		return store.ErrIngressChanged
	}
	l.confirms++
	l.ruleState = "created"
	return nil
}

func (l *forwardRuleTestLedger) IssueForwardRuleDelete(context.Context, store.ForwardRuleIntent) error {
	if !l.closed || l.ruleState != "created" {
		return store.ErrIngressChanged
	}
	l.ruleState = "delete_issued"
	return nil
}
func (l *forwardRuleTestLedger) ConfirmForwardRuleDelete(context.Context, store.ForwardRuleIntent) error {
	if l.ruleState != "delete_issued" {
		return store.ErrIngressChanged
	}
	l.ruleState = "removed"
	return nil
}
func (l *forwardRuleTestLedger) CancelForwardRule(context.Context, store.ForwardRuleIntent) error {
	if !l.closed || l.ruleState != "prepared" {
		return store.ErrIngressChanged
	}
	l.ruleState = "cancelled"
	return nil
}

func TestForwardRuleRemovalRechecksOwnershipAndRecoversLostDelete(t *testing.T) {
	for _, mode := range []string{"normal", "lost-delete", "missing", "modified", "open", "prepared", "issued"} {
		t.Run(mode, func(t *testing.T) {
			source, c, route, target, destinationID := forwardFixture(t)
			ledger := &forwardRuleTestLedger{ruleState: "created", closed: mode != "open"}
			rule := CloudflareRule{ID: strings.Repeat("4", 32), Name: c.rules.name(route.Address), Enabled: true, Source: "api", Actions: []CloudflareAction{{Type: "forward", Value: []string{target}}}, Matchers: []CloudflareMatcher{{Type: "literal", Field: "to", Value: route.Address}}}
			fact := ruleFact(rule)
			dest := store.ForwardDestinationIntent{Source: source.cfg.ID, Generation: source.cfg.Generation, Account: c.account, Zone: c.rules.zone, Domain: c.domain, Route: route, Target: target, DestinationID: destinationID}
			intent := store.ForwardRuleIntent{Destination: dest, State: "created", Fact: fact}
			if mode == "prepared" {
				ledger.ruleState, intent.State, intent.Fact = "prepared", "prepared", store.CloudflareRuleFact{}
			}
			if mode == "issued" {
				ledger.ruleState, intent.State, intent.Fact = "create_issued", "create_issued", store.CloudflareRuleFact{}
			}
			present, deletes := mode != "missing", 0
			source.http.Transport = routeTransport(func(r *http.Request) (*http.Response, error) {
				if r.Method == http.MethodGet {
					if !present {
						return routeResponse(404, nil), nil
					}
					v := rule
					if mode == "modified" {
						v.Enabled = false
					}
					return routeResponse(200, map[string]any{"success": true, "result": v}), nil
				}
				deletes++
				if ledger.ruleState != "delete_issued" {
					t.Fatal("delete before intent")
				}
				present = false
				if mode == "lost-delete" {
					return nil, errors.New("private")
				}
				return routeResponse(200, map[string]any{"success": true, "result": map[string]string{"id": rule.ID}}), nil
			})
			err := c.RemoveForwardRule(t.Context(), ledger, intent)
			switch mode {
			case "normal", "missing":
				if err != nil || ledger.ruleState != "removed" {
					t.Fatal("deletion not confirmed", err)
				}
			case "lost-delete":
				if !errors.Is(err, ErrRuleUnknown) || ledger.ruleState != "delete_issued" || deletes != 1 {
					t.Fatal("lost delete not retained", err)
				}
				intent.State = "delete_issued"
				if err = c.RemoveForwardRule(t.Context(), ledger, intent); err != nil || deletes != 1 || ledger.ruleState != "removed" {
					t.Fatal("lost delete did not reconcile absence", err)
				}
			case "modified":
				if !errors.Is(err, ErrRuleConflict) || deletes != 0 {
					t.Fatal("modified rule deleted", err)
				}
			case "open":
				if !errors.Is(err, store.ErrIngressChanged) || deletes != 0 {
					t.Fatal("open claim rule deleted", err)
				}
			case "prepared":
				if err != nil || ledger.ruleState != "cancelled" || deletes != 0 {
					t.Fatal("unsent rule not cancelled", err)
				}
			case "issued":
				if !errors.Is(err, ErrRuleUnknown) || deletes != 0 {
					t.Fatal("unknown create was deleted", err)
				}
			}
		})
	}
}

func TestForwardRuleSyncPreservesIntentsAndReadback(t *testing.T) {
	for _, mode := range []string{"normal", "lost-create", "lost-read", "external", "changed", "withdrawn"} {
		t.Run(mode, func(t *testing.T) {
			source, c, route, target, id := forwardFixture(t)
			ledger := &forwardRuleTestLedger{destinationTestLedger: destinationTestLedger{state: "created", id: id}, ruleState: "prepared"}
			ruleID := strings.Repeat("4", 32)
			rule := CloudflareRule{ID: ruleID, Name: c.rules.name(route.Address), Enabled: true, Source: "api", Actions: []CloudflareAction{{Type: "forward", Value: []string{target}}}, Matchers: []CloudflareMatcher{{Type: "literal", Field: "to", Value: route.Address}}}
			var remote *CloudflareRule
			if mode == "external" {
				r := rule
				r.Name = "foreign"
				remote = &r
			}
			posts, reads, calls := 0, 0, 0
			source.http.Transport = routeTransport(func(r *http.Request) (*http.Response, error) {
				calls++
				if strings.Contains(r.URL.Path, "/accounts/") {
					return routeResponse(200, map[string]any{"success": true, "result": map[string]any{"id": id, "email": target, "verified": "2026-01-01T00:00:00Z"}}), nil
				}
				if r.Method == http.MethodPost {
					posts++
					if ledger.ruleState != "create_issued" {
						t.Fatal("write preceded intent")
					}
					copy := rule
					remote = &copy
					if mode == "lost-create" {
						return nil, errors.New("private result")
					}
					if mode == "withdrawn" {
						ledger.withdrawn = true
					}
					return routeResponse(200, map[string]any{"success": true, "result": rule}), nil
				}
				if strings.HasSuffix(r.URL.Path, "/rules") {
					rows := []CloudflareRule{}
					if remote != nil {
						rows = append(rows, *remote)
					}
					return routeResponse(200, map[string]any{"success": true, "result": rows, "result_info": map[string]any{"page": 1, "per_page": 50, "count": len(rows), "total_count": len(rows)}}), nil
				}
				reads++
				if mode == "lost-read" && reads == 1 {
					return nil, errors.New("private result")
				}
				if mode == "changed" {
					bad := rule
					bad.Enabled = false
					return routeResponse(200, map[string]any{"success": true, "result": bad}), nil
				}
				return routeResponse(200, map[string]any{"success": true, "result": rule}), nil
			})
			got, err := c.EnsureForwardRule(t.Context(), ledger, route)
			switch mode {
			case "normal":
				if err != nil || got.ID != ruleID || posts != 1 || ledger.ruleState != "created" || ledger.confirms != 1 {
					t.Fatal("rule not confirmed", err)
				}
				if _, err = c.EnsureForwardRule(t.Context(), ledger, route); err != nil || posts != 1 {
					t.Fatal("repeat created again", err)
				}
			case "lost-create":
				if !errors.Is(err, ErrRuleUnknown) || ledger.ruleState != "create_issued" || posts != 1 {
					t.Fatal("lost create misreported", err)
				}
				before := calls
				if _, err = c.EnsureForwardRule(t.Context(), ledger, route); !errors.Is(err, ErrRuleUnknown) || posts != 1 || calls != before+1 {
					t.Fatal("unknown was retried", err)
				}
			case "lost-read":
				if !errors.Is(err, ErrUnavailable) || ledger.ruleState != "create_responded" || posts != 1 {
					t.Fatal("response not retained", err)
				}
				if _, err = c.EnsureForwardRule(t.Context(), ledger, route); err != nil || ledger.ruleState != "created" || posts != 1 {
					t.Fatal("readback recovery failed", err)
				}
			case "external":
				if !errors.Is(err, ErrRuleConflict) || posts != 0 || ledger.ruleState != "prepared" {
					t.Fatal("foreign rule adopted", err)
				}
			case "changed":
				if !errors.Is(err, ErrRuleConflict) || ledger.ruleState != "create_responded" {
					t.Fatal("changed readback accepted", err)
				}
			case "withdrawn":
				if !errors.Is(err, store.ErrIngressChanged) || ledger.ruleState != "created" {
					t.Fatal("withdrawal reopened", err)
				}
			}
		})
	}
}
