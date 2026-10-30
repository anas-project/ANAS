// TEST_CASES: MAILMOD-T-009
package ingress

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/anas-project/ANAS/modules/mail/store"
	"testing"
	"time"
)

func TestForwardBatchIsBoundedFairAndRequiresFreshCompleteObservation(t *testing.T) {
	_, _, route, _, _ := forwardFixture(t)
	clock := time.Now()
	syncer := ForwardSync{now: func() time.Time { return clock }}
	jobs := []forwardJob{}
	for n := 0; n < 25; n++ {
		r := route
		r.Address = fmt.Sprintf("user%03d@example.test", n)
		jobs = append(jobs, forwardJob{route: r})
	}
	calls := map[string]int{}
	run := func(_ context.Context, j forwardJob) error { calls[j.route.Address]++; return nil }
	if err := syncer.runJobs(t.Context(), jobs, run); !errors.Is(err, ErrForwardBatchPending) || len(calls) != 20 {
		t.Fatal("first batch not bounded", err, len(calls))
	}
	if err := syncer.runJobs(t.Context(), jobs, run); err != nil || len(calls) != 25 {
		t.Fatal("remaining jobs not reached", err, len(calls))
	}
	if err := syncer.runJobs(t.Context(), jobs, run); err != nil {
		t.Fatal(err)
	}
	for _, n := range calls {
		if n != 1 {
			t.Fatal("fresh result repeated")
		}
	}
	clock = clock.Add(61 * time.Second)
	if err := syncer.runJobs(t.Context(), jobs, run); !errors.Is(err, ErrForwardBatchPending) {
		t.Fatal("stale results counted as fresh", err)
	}
	if err := syncer.runJobs(t.Context(), jobs, run); err != nil {
		t.Fatal("second complete observation failed", err)
	}
	for _, n := range calls {
		if n != 2 {
			t.Fatal("stale job not rechecked", n)
		}
	}
	if err := syncer.runJobs(t.Context(), jobs[:1], run); err != nil || len(syncer.observed) != 1 {
		t.Fatal("obsolete observations retained", err)
	}
}

func TestForwardBatchBackoffDoesNotBlockOtherAddress(t *testing.T) {
	_, _, route, _, _ := forwardFixture(t)
	clock := time.Now()
	syncer := ForwardSync{now: func() time.Time { return clock }}
	other := route
	other.Address = "bob@example.test"
	jobs := []forwardJob{{route: route}, {route: other}}
	calls := map[string]int{}
	pending := true
	run := func(_ context.Context, j forwardJob) error {
		calls[j.route.Address]++
		if j.route == route && pending {
			return ErrDestinationPending
		}
		return nil
	}
	if err := syncer.runJobs(t.Context(), jobs, run); !errors.Is(err, ErrDestinationPending) || calls[other.Address] != 1 {
		t.Fatal("failure blocked sibling", err)
	}
	if err := syncer.runJobs(t.Context(), jobs, run); !errors.Is(err, ErrDestinationPending) || calls[route.Address] != 1 || calls[other.Address] != 1 {
		t.Fatal("backoff not retained", err)
	}
	pending = false
	clock = clock.Add(5 * time.Second)
	if err := syncer.runJobs(t.Context(), jobs, run); err != nil || calls[route.Address] != 2 || calls[other.Address] != 1 {
		t.Fatal("retry or sibling cache failed", err)
	}
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	if err := syncer.runJobs(cancelled, jobs, run); !errors.Is(err, context.Canceled) {
		t.Fatal("cancelled call reported success", err)
	}
}

func TestForwardBatchCannotSucceedWhenObservationExpiresDuringPass(t *testing.T) {
	_, _, route, _, _ := forwardFixture(t)
	clock := time.Now()
	syncer := ForwardSync{now: func() time.Time { return clock }}
	other := route
	other.Address = "bob@example.test"
	jobs := []forwardJob{{route: route}, {route: other}}
	calls := 0
	run := func(_ context.Context, j forwardJob) error {
		calls++
		if j.route == other && calls == 2 {
			clock = clock.Add(61 * time.Second)
		}
		return nil
	}
	if err := syncer.runJobs(t.Context(), jobs, run); !errors.Is(err, ErrForwardBatchPending) {
		t.Fatal("expired observation reported success", err)
	}
	if err := syncer.runJobs(t.Context(), jobs, run); err != nil || calls != 3 {
		t.Fatal("expired observation not refreshed", err, calls)
	}
}

type cutoverDestinationTestLedger struct {
	forwardRuleTestLedger
	forwardSourceCalls int
	knownRules         []store.ForwardRuleIntent
	failures           []string
}

func (l *cutoverDestinationTestLedger) EnsureForwardSource(context.Context, string, string, string, string, string) error {
	l.forwardSourceCalls++
	return nil
}
func (l *cutoverDestinationTestLedger) ForwardRules(context.Context, string, string, string, string, string) ([]store.ForwardRuleIntent, error) {
	return l.knownRules, nil
}
func (l *cutoverDestinationTestLedger) CloudflareRuleFailure(_ context.Context, _, _, code string) error {
	l.failures = append(l.failures, code)
	return nil
}

func TestForwardDestinationPreparationCannotStandInForPublicRule(t *testing.T) {
	for _, mode := range []string{"pending", "verified", "withdrawn-after-cache", "verification-changed"} {
		t.Run(mode, func(t *testing.T) {
			source, c, route, target, id := forwardFixture(t)
			ledger := &cutoverDestinationTestLedger{forwardRuleTestLedger: forwardRuleTestLedger{destinationTestLedger: destinationTestLedger{state: "created", id: id}, ruleState: "prepared"}}
			rule := CloudflareRule{ID: strings.Repeat("4", 32), Name: c.rules.name(route.Address), Enabled: true, Source: "api", Actions: []CloudflareAction{{Type: "forward", Value: []string{target}}}, Matchers: []CloudflareMatcher{{Type: "literal", Field: "to", Value: route.Address}}}
			var remote *CloudflareRule
			verified := mode != "pending"
			ruleWrites, verifyReads := 0, 0
			source.http.Transport = routeTransport(func(r *http.Request) (*http.Response, error) {
				if strings.Contains(r.URL.Path, "/accounts/") {
					verifyReads++
					stamp := any("2026-01-01T00:00:00Z")
					if !verified {
						stamp = nil
					}
					return routeResponse(200, map[string]any{"success": true, "result": map[string]any{"id": id, "email": target, "verified": stamp}}), nil
				}
				if r.Method == http.MethodPost {
					ruleWrites++
					copy := rule
					remote = &copy
					return routeResponse(200, map[string]any{"success": true, "result": copy}), nil
				}
				if strings.HasSuffix(r.URL.Path, "/rules") {
					rows := []CloudflareRule{}
					if remote != nil {
						rows = append(rows, *remote)
					}
					return routeResponse(200, map[string]any{"success": true, "result": rows, "result_info": map[string]any{"page": 1, "per_page": 50, "count": len(rows), "total_count": len(rows)}}), nil
				}
				if remote == nil {
					return routeResponse(404, nil), nil
				}
				return routeResponse(200, map[string]any{"success": true, "result": *remote}), nil
			})
			syncer := &ForwardSync{Ledger: ledger, Client: c}
			err := syncer.PrepareDestinations(t.Context(), []Route{route})
			if mode == "pending" {
				if !errors.Is(err, ErrDestinationPending) || ruleWrites != 0 {
					t.Fatal("pending target allowed public mutation", err)
				}
				return
			}
			if err != nil || ruleWrites != 0 || verifyReads < 2 {
				t.Fatal("verified target not independently rechecked", err, verifyReads)
			}
			if mode == "withdrawn-after-cache" {
				ledger.withdrawn = true
				if err = syncer.PrepareDestinations(t.Context(), []Route{route}); !errors.Is(err, store.ErrIngressChanged) || ruleWrites != 0 {
					t.Fatal("cached target revived withdrawn claim", err)
				}
				return
			}
			if mode == "verification-changed" {
				verified = false
				if err = syncer.PrepareDestinations(t.Context(), []Route{route}); !errors.Is(err, ErrDestinationPending) || ruleWrites != 0 {
					t.Fatal("cached target hid provider change", err)
				}
				return
			}
			if err = syncer.Synchronize(t.Context(), []Route{route}); err != nil || ruleWrites != 1 || ledger.confirms != 1 {
				t.Fatal("destination cache incorrectly counted as public rule", err, ruleWrites)
			}
		})
	}
}

type multiCutoverDestinationLedger struct {
	ForwardSyncLedger
	state     map[string]string
	ids       map[string]string
	withdrawn string
	errors    []string
}

func (l *multiCutoverDestinationLedger) CloudflareRuleFailure(_ context.Context, _, _, code string) error {
	l.errors = append(l.errors, code)
	return nil
}

func (l *multiCutoverDestinationLedger) EnsureForwardSource(context.Context, string, string, string, string, string) error {
	return nil
}
func (l *multiCutoverDestinationLedger) PrepareForwardDestination(_ context.Context, source, generation, account, zone, domain string, route store.IngressRoute) (store.ForwardDestinationIntent, error) {
	if route.Address == l.withdrawn {
		return store.ForwardDestinationIntent{}, store.ErrIngressChanged
	}
	target, err := store.ForwardDestination(source, generation, route, domain)
	if err != nil {
		return store.ForwardDestinationIntent{}, err
	}
	state := l.state[route.Address]
	if state == "" {
		state = "prepared"
		l.state[route.Address] = state
	}
	return store.ForwardDestinationIntent{Source: source, Generation: generation, Account: account, Zone: zone, Domain: domain, Route: route, Target: target, BindingID: route.Address, State: state, DestinationID: l.ids[route.Address]}, nil
}
func (l *multiCutoverDestinationLedger) IssueForwardDestination(_ context.Context, v store.ForwardDestinationIntent) error {
	if l.state[v.Route.Address] != "prepared" {
		return store.ErrIngressChanged
	}
	l.state[v.Route.Address] = "issued"
	return nil
}
func (l *multiCutoverDestinationLedger) ConfirmForwardDestination(_ context.Context, v store.ForwardDestinationIntent, id string) error {
	if l.state[v.Route.Address] != "issued" {
		return store.ErrIngressChanged
	}
	l.state[v.Route.Address] = "created"
	l.ids[v.Route.Address] = id
	return nil
}

func TestForwardDestinationPreparationBatchesWithoutLosingFreshChecks(t *testing.T) {
	for _, withdraw := range []bool{false, true} {
		t.Run(fmt.Sprintf("withdraw=%t", withdraw), func(t *testing.T) {
			source, c, route, _, _ := forwardFixture(t)
			ledger := &multiCutoverDestinationLedger{state: map[string]string{}, ids: map[string]string{}}
			routes := make([]Route, 25)
			for i := range routes {
				routes[i] = route
				routes[i].Address = fmt.Sprintf("user%02d@example.test", i)
			}
			targets := map[string]string{}
			writes, verifyReads := 0, 0
			source.http.Transport = routeTransport(func(r *http.Request) (*http.Response, error) {
				if !strings.Contains(r.URL.Path, "/accounts/") {
					t.Fatal("public rule API used by destination preparation")
				}
				if r.Method == http.MethodPost {
					var input struct {
						Email string `json:"email"`
					}
					if json.NewDecoder(r.Body).Decode(&input) != nil {
						t.Fatal("invalid input")
					}
					id := fmt.Sprintf("%032x", len(targets)+1)
					targets[id] = input.Email
					writes++
					return routeResponse(200, map[string]any{"success": true, "result": map[string]any{"id": id, "email": input.Email, "verified": nil}}), nil
				}
				if strings.HasSuffix(r.URL.Path, "/addresses") {
					rows := []map[string]any{}
					for id, email := range targets {
						rows = append(rows, map[string]any{"id": id, "email": email})
					}
					return routeResponse(200, map[string]any{"success": true, "result": rows, "result_info": map[string]any{"page": 1, "per_page": 50, "count": len(rows), "total_count": len(rows)}}), nil
				}
				id := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
				verifyReads++
				return routeResponse(200, map[string]any{"success": true, "result": map[string]any{"id": id, "email": targets[id], "verified": "2026-01-01T00:00:00Z"}}), nil
			})
			syncer := &ForwardSync{Ledger: ledger, Client: c}
			if err := syncer.PrepareDestinations(t.Context(), routes); !errors.Is(err, ErrForwardBatchPending) || writes != 20 {
				t.Fatal("first batch did not remain pending", err, writes)
			}
			if len(ledger.errors) != 1 || ledger.errors[0] != "cloudflare-batch-pending" {
				t.Fatal("batch preparation failure not recorded", ledger.errors)
			}
			if withdraw {
				ledger.withdrawn = routes[0].Address
			}
			err := syncer.PrepareDestinations(t.Context(), routes)
			if withdraw {
				if !errors.Is(err, store.ErrIngressChanged) {
					t.Fatal("withdrawn cached claim accepted", err)
				}
				if writes != 25 {
					t.Fatal("other destinations not prepared", writes)
				}
				return
			}
			if err != nil || writes != 25 || verifyReads != 50 {
				t.Fatal("complete batch not independently refreshed", err, writes, verifyReads)
			}
			if len(ledger.errors) != 1 {
				t.Fatal("destination readiness cleared public status", ledger.errors)
			}
		})
	}
}
