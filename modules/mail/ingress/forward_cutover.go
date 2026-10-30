package ingress

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/anas-project/ANAS/modules/mail/store"
)

// PreflightCutoverRules checks the complete public ruleset without provider
// mutation. Failures are recorded locally; success cannot clear public status.
// A rule for a requested address must either be an exactly recorded Worker
// rule of a supplied draining source, or an exactly recorded rule of this
// forward source from an earlier attempt. Callers must separately prove new
// target verification, source readiness and current claims. No unrecorded
// Cloudflare rule is adopted merely because its address or name matches.
func (s *ForwardSync) PreflightCutoverRules(ctx context.Context, routes []Route, draining []*CloudflareRuleSync) error {
	return s.PreflightCutoverWithForwards(ctx, routes, draining, nil)
}

// PreflightCutoverWithForwards also accepts known draining forward sources.
func (s *ForwardSync) PreflightCutoverWithForwards(ctx context.Context, routes []Route, draining []*CloudflareRuleSync, forwards []*ForwardSync) (result error) {
	if s == nil || s.Ledger == nil || s.Client == nil || s.Client.rules == nil || s.Client.rules.source == nil || len(draining)+len(forwards) == 0 {
		return ErrConfiguration
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	c := s.Client
	wanted := make(map[string]Route, len(routes))
	for _, route := range routes {
		if _, err := c.target(route); err != nil {
			return err
		}
		if _, exists := wanted[route.Address]; exists {
			return ErrConfiguration
		}
		wanted[route.Address] = route
	}
	if _, err := RouteFingerprint(routes); err != nil {
		return err
	}
	source, generation := c.rules.source.cfg.ID, c.rules.source.cfg.Generation
	defer func() {
		// A clean preflight alone is not evidence of a published rule.
		if result == nil {
			return
		}
		record, done := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
		err := s.Ledger.CloudflareRuleFailure(record, source, generation, cloudflareErrorCode(result))
		done()
		if err != nil {
			result = errors.Join(result, err)
		}
	}()
	known, err := s.Ledger.ForwardRules(ctx, source, generation, c.account, c.rules.zone, c.domain)
	if err != nil {
		return err
	}
	forward := make(map[string]store.ForwardRuleIntent)
	for _, v := range known {
		d := v.Destination
		if d.Source != source || d.Generation != generation || d.Account != c.account || d.Zone != c.rules.zone || d.Domain != c.domain {
			return ErrRuleConflict
		}
		if v.State == "removed" || v.State == "cancelled" {
			continue
		}
		route, relevant := wanted[d.Route.Address]
		if !relevant {
			continue
		}
		if route != d.Route {
			return ErrRuleConflict
		}
		// An issued request with an unknown outcome, or an outstanding
		// deletion, cannot justify withdrawing the still-working source.
		switch v.State {
		case "prepared":
			if v.Fact != (store.CloudflareRuleFact{}) {
				return ErrRuleConflict
			}
		case "created", "create_responded":
			if v.Fact.ID == "" || v.Fact.Fingerprint == "" {
				return ErrRuleConflict
			}
		default:
			return ErrRuleConflict
		}
		if _, duplicate := forward[d.Route.Address]; duplicate {
			return ErrRuleConflict
		}
		forward[d.Route.Address] = v
	}
	type peerState struct {
		client   *CloudflareRules
		bindings map[string]store.CloudflareRuleBinding
	}
	peers := make([]peerState, 0, len(draining))
	seenSource := map[string]bool{}
	for _, peer := range draining {
		if peer == nil || peer.Ledger == nil || peer.Client == nil || peer.Client.source == nil || peer.Client.zone != c.rules.zone {
			return ErrConfiguration
		}
		pc := peer.Client
		id, gen := pc.source.cfg.ID, pc.source.cfg.Generation
		if id == source || seenSource[id] {
			return ErrConfiguration
		}
		seenSource[id] = true
		bindings, err := peer.Ledger.CloudflareRuleBindings(ctx, id, gen)
		if err != nil {
			return err
		}
		m := make(map[string]store.CloudflareRuleBinding, len(bindings))
		for _, b := range bindings {
			if b.SourceID != id || b.Generation != gen || b.Worker != pc.worker {
				return ErrRuleConflict
			}
			if _, duplicate := m[b.Address]; duplicate {
				return ErrRuleConflict
			}
			m[b.Address] = b
		}
		peers = append(peers, peerState{client: pc, bindings: m})
	}
	seenSource[source] = true
	forwardPeers, err := loadDrainingForwardRules(ctx, c.rules.zone, wanted, seenSource, forwards)
	if err != nil {
		return err
	}
	seenID, seenAddress, seenForward := map[string]bool{}, map[string]bool{}, map[string]bool{}
	seenOldForward := map[string]bool{}
	total, count := -1, 0
	prefix := "ANAS Mail " + source + "/" + generation + "/"
	for page := 1; page <= 100; page++ {
		response, err := c.request(ctx, "GET", "?per_page=50&page="+strconv.Itoa(page), nil)
		if err != nil {
			return err
		}
		observedTotal, pages, pageCount, ok := forwardPage(response.Info, page, total)
		if !ok {
			return ErrIdentity
		}
		total = observedTotal
		var rows []json.RawMessage
		if json.Unmarshal(response.Result, &rows) != nil || rows == nil || len(rows) > 50 || len(rows) != pageCount {
			return ErrIdentity
		}
		for _, raw := range rows {
			r, valid := parseForwardRule(raw)
			if !valid || seenID[r.ID] {
				return ErrIdentity
			}
			seenID[r.ID] = true
			count++
			address := r.Matchers[0].Value
			route, relevant := wanted[strings.ToLower(address)]
			if !relevant && !strings.HasPrefix(r.Name, prefix) {
				continue
			}
			if !relevant || !r.Enabled || r.Matchers[0].Type != "literal" || r.Matchers[0].Field != "to" || seenAddress[route.Address] {
				return ErrRuleConflict
			}
			seenAddress[route.Address] = true
			if c.owns(r, route) {
				v, exists := forward[route.Address]
				if !exists || (v.State != "created" && v.State != "create_responded") || v.Fact != ruleFact(r) {
					return ErrRuleConflict
				}
				seenForward[route.Address] = true
				continue
			}
			matched := false
			for _, peer := range peers {
				if !peer.client.owns(r) {
					continue
				}
				b, exists := peer.bindings[route.Address]
				if !exists || b.Fact.ID == "" || b.Fact != ruleFact(r) {
					return ErrRuleConflict
				}
				matched = true
				break
			}
			if !matched {
				for _, peer := range forwardPeers {
					item, exists := peer.rules[route.Address]
					if !exists || !peer.client.owns(r, item.Destination.Route) {
						continue
					}
					if item.State == "prepared" || item.Fact != ruleFact(r) {
						return ErrRuleConflict
					}
					seenOldForward[peer.client.rules.source.cfg.ID+"\x00"+route.Address] = true
					matched = true
					break
				}
			}
			if !matched {
				return ErrRuleConflict
			}
		}
		if page >= pages {
			if count != total {
				return ErrIdentity
			}
			for address, intent := range forward {
				if (intent.State == "created" || intent.State == "create_responded") && !seenForward[address] {
					return ErrRuleConflict
				}
			}
			for _, peer := range forwardPeers {
				for address, intent := range peer.rules {
					if (intent.State == "created" || intent.State == "create_responded") && !seenOldForward[peer.client.rules.source.cfg.ID+"\x00"+address] {
						return ErrRuleConflict
					}
				}
			}
			return nil
		}
	}
	return ErrIdentity
}

type drainingForwardPeer struct {
	client *CloudflareForwardRules
	rules  map[string]store.ForwardRuleIntent
}

// Read durable snapshots without taking another synchronizer's mutex. Each
// ledger read uses its own consistent database transaction; nesting two
// synchronizer locks would deadlock opposing concurrent preflights. Provider
// reads and the later guarded delete still independently validate each fact.
func loadDrainingForwardRules(ctx context.Context, zone string, wanted map[string]Route, identities map[string]bool, forwards []*ForwardSync) ([]drainingForwardPeer, error) {
	out := make([]drainingForwardPeer, 0, len(forwards))
	for _, peer := range forwards {
		if peer == nil || peer.Ledger == nil || peer.Client == nil || peer.Client.rules == nil || peer.Client.rules.source == nil || peer.Client.rules.zone != zone {
			return nil, ErrConfiguration
		}
		pc := peer.Client
		id, generation := pc.rules.source.cfg.ID, pc.rules.source.cfg.Generation
		if identities[id] {
			return nil, ErrConfiguration
		}
		identities[id] = true
		items, err := peer.Ledger.ForwardRules(ctx, id, generation, pc.account, pc.rules.zone, pc.domain)
		if err != nil {
			return nil, err
		}
		current := drainingForwardPeer{client: pc, rules: map[string]store.ForwardRuleIntent{}}
		for _, item := range items {
			d := item.Destination
			target, err := pc.target(d.Route)
			if err != nil || d.Source != id || d.Generation != generation || d.Account != pc.account || d.Zone != zone || d.Domain != pc.domain || d.Target != target {
				return nil, ErrRuleConflict
			}
			if item.State == "create_issued" {
				return nil, ErrRuleUnknown
			}
			if item.State == "removed" || item.State == "cancelled" {
				continue
			}
			route, relevant := wanted[d.Route.Address]
			if !relevant {
				continue
			}
			if route != d.Route {
				return nil, ErrRuleConflict
			}
			switch item.State {
			case "prepared":
				if item.Fact != (store.CloudflareRuleFact{}) {
					return nil, ErrRuleConflict
				}
			case "created", "create_responded", "delete_issued":
				if item.Fact.ID == "" || item.Fact.Fingerprint == "" {
					return nil, ErrRuleConflict
				}
			default:
				return nil, ErrRuleConflict
			}
			if _, duplicate := current.rules[d.Route.Address]; duplicate {
				return nil, ErrRuleConflict
			}
			current.rules[d.Route.Address] = item
		}
		out = append(out, current)
	}
	return out, nil
}
