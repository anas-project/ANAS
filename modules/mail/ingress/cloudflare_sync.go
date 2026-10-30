package ingress

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/anas-project/ANAS/modules/mail/store"
)

type CloudflareRuleLedger interface {
	PrepareCloudflareRules(context.Context, string, string, string, []store.IngressRoute) error
	CloudflareRuleBindings(context.Context, string, string) ([]store.CloudflareRuleBinding, error)
	ClaimCloudflareRule(context.Context, string) (store.CloudflareRuleOperation, error)
	IssueCloudflareRule(context.Context, store.CloudflareRuleOperation) error
	ConfirmCloudflareRule(context.Context, store.CloudflareRuleOperation, store.CloudflareRuleFact) error
	ReleaseCloudflareRule(context.Context, store.CloudflareRuleOperation, string) error
	ObserveCloudflareRule(context.Context, string, string, string, string, store.CloudflareRuleFact) error
	CancelSupersededCloudflareCreate(context.Context, store.CloudflareRuleOperation) error
	CloudflareRuleFailure(context.Context, string, string, string) error
}
type CloudflareRuleSync struct {
	Ledger                   CloudflareRuleLedger
	Client                   *CloudflareRules
	mu                       sync.Mutex
	fingerprint              string
	lastSuccess, timeAttempt time.Time
	lastFailure              error
}

func ruleFact(r CloudflareRule) store.CloudflareRuleFact {
	r.Tag = ""
	raw, _ := json.Marshal(r)
	sum := sha256.Sum256(raw)
	return store.CloudflareRuleFact{ID: r.ID, Fingerprint: hex.EncodeToString(sum[:])}
}
func cloudflareErrorCode(e error) string {
	switch {
	case errors.Is(e, ErrAuthentication):
		return "cloudflare-authentication"
	case errors.Is(e, ErrRateLimited):
		return "cloudflare-rate-limited"
	case errors.Is(e, ErrRuleUnknown):
		return "cloudflare-outcome-unknown"
	case errors.Is(e, ErrGone):
		return "cloudflare-rule-missing"
	case errors.Is(e, ErrRuleConflict), errors.Is(e, store.ErrIngressChanged):
		return "cloudflare-rule-conflict"
	case errors.Is(e, ErrDestinationPending):
		return "cloudflare-destination-pending"
	case errors.Is(e, ErrForwardBatchPending):
		return "cloudflare-batch-pending"
	case errors.Is(e, ErrRuleRejected):
		return "cloudflare-request-rejected"
	default:
		return "cloudflare-unavailable"
	}
}
func (s *CloudflareRuleSync) read(ctx context.Context, wanted map[string]bool) (map[string]CloudflareRule, error) {
	c := s.Client
	rows, e := c.List(ctx)
	if e != nil {
		return nil, e
	}
	out := map[string]CloudflareRule{}
	prefix := "ANAS Mail " + c.source.cfg.ID + "/" + c.source.cfg.Generation + "/"
	for _, r := range rows {
		if strings.HasPrefix(r.Name, prefix) {
			if !c.owns(r) {
				return nil, ErrRuleConflict
			}
			address := r.Matchers[0].Value
			if _, duplicate := out[address]; duplicate {
				return nil, ErrRuleConflict
			}
			out[address] = r
		} else {
			for _, m := range r.Matchers {
				if m.Type == "literal" && m.Field == "to" && wanted[strings.ToLower(m.Value)] {
					return nil, ErrRuleConflict
				}
			}
		}
	}
	return out, nil
}

// Preflight reads the public API before withdrawing old entrances. A conflicting
// recipient is allowed only when its exact rule belongs to a draining source
// supplied by the trusted runtime and its durable ledger confirms the readback.
// It does not create, disable or delete provider rules, or adopt unknown IDs.
func (s *CloudflareRuleSync) Preflight(ctx context.Context, routes []Route, draining []*CloudflareRuleSync) error {
	return s.PreflightWithForwards(ctx, routes, draining, nil)
}

// PreflightWithForwards also recognizes exact, durably recorded rules of a
// draining Cloudflare-forwarded source. It never mutates provider rules.
func (s *CloudflareRuleSync) PreflightWithForwards(ctx context.Context, routes []Route, draining []*CloudflareRuleSync, forwards []*ForwardSync) (result error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.Ledger == nil || s.Client == nil {
		return ErrConfiguration
	}
	c := s.Client
	if c.source == nil {
		return ErrConfiguration
	}
	defer func() {
		// Rule preparation is not proof that the public route exists.
		if result == nil {
			return
		}
		record, done := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
		err := s.Ledger.CloudflareRuleFailure(record, c.source.cfg.ID, c.source.cfg.Generation, cloudflareErrorCode(result))
		done()
		if err != nil {
			result = errors.Join(result, err)
		}
	}()
	wanted := map[string]Route{}
	for _, r := range routes {
		if !c.addressAllowed(r.Address) {
			return ErrConfiguration
		}
		if _, duplicate := wanted[r.Address]; duplicate {
			return ErrConfiguration
		}
		wanted[r.Address] = r
	}
	if _, err := RouteFingerprint(routes); err != nil {
		return err
	}
	peers := append([]*CloudflareRuleSync{s}, draining...)
	known := make(map[*CloudflareRuleSync]map[string]store.CloudflareRuleBinding)
	identities := map[string]bool{}
	for _, peer := range peers {
		if peer == nil || peer.Client == nil || peer.Ledger == nil {
			return ErrConfiguration
		}
		pc := peer.Client
		if pc.zone != c.zone {
			continue
		}
		id, generation := pc.source.cfg.ID, pc.source.cfg.Generation
		if identities[id] {
			return ErrConfiguration
		}
		identities[id] = true
		bindings, err := peer.Ledger.CloudflareRuleBindings(ctx, id, generation)
		if err != nil {
			return err
		}
		known[peer] = map[string]store.CloudflareRuleBinding{}
		for _, b := range bindings {
			if b.SourceID != id || b.Generation != generation || b.Worker != pc.worker {
				return ErrRuleConflict
			}
			known[peer][b.Address] = b
		}
	}
	forwardPeers, err := loadDrainingForwardRules(ctx, c.zone, wanted, identities, forwards)
	if err != nil {
		return err
	}
	rows, err := c.List(ctx)
	if err != nil {
		return err
	}
	prefix := "ANAS Mail " + c.source.cfg.ID + "/" + c.source.cfg.Generation + "/"
	seenAddress := map[string]bool{}
	seenForward := map[string]bool{}
	for _, r := range rows {
		check := strings.HasPrefix(r.Name, prefix)
		for _, matcher := range r.Matchers {
			_, requested := wanted[strings.ToLower(matcher.Value)]
			check = check || matcher.Type == "literal" && matcher.Field == "to" && requested
		}
		if !check {
			continue
		}
		if !r.Enabled || len(r.Matchers) != 1 || r.Matchers[0].Type != "literal" || r.Matchers[0].Field != "to" {
			return ErrRuleConflict
		}
		address := strings.ToLower(r.Matchers[0].Value)
		if _, requested := wanted[address]; !requested || seenAddress[address] {
			return ErrRuleConflict
		}
		seenAddress[address] = true
		matched := false
		for peer, bindings := range known {
			pc := peer.Client
			if !pc.owns(r) {
				continue
			}
			address := r.Matchers[0].Value
			b, exists := bindings[address]
			if !exists || b.Fact.ID != "" && b.Fact != ruleFact(r) {
				return ErrRuleConflict
			}
			if err = peer.Ledger.ObserveCloudflareRule(ctx, pc.source.cfg.ID, pc.source.cfg.Generation, address, pc.worker, ruleFact(r)); err != nil {
				return err
			}
			matched = true
			break
		}
		if !matched {
			for _, peer := range forwardPeers {
				item, exists := peer.rules[address]
				if !exists || !peer.client.owns(r, item.Destination.Route) {
					continue
				}
				if item.State == "prepared" || item.Fact != ruleFact(r) {
					return ErrRuleConflict
				}
				seenForward[peer.client.rules.source.cfg.ID+"\x00"+address] = true
				matched = true
				break
			}
		}
		if !matched {
			return ErrRuleConflict
		}
	}
	for _, peer := range forwardPeers {
		for address, item := range peer.rules {
			if (item.State == "created" || item.State == "create_responded") && !seenForward[peer.client.rules.source.cfg.ID+"\x00"+address] {
				return ErrRuleConflict
			}
		}
	}
	return nil
}

func (s *CloudflareRuleSync) Synchronize(ctx context.Context, routes []Route) (result error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.Ledger == nil || s.Client == nil {
		return ErrConfiguration
	}
	c := s.Client
	source, generation := c.source.cfg.ID, c.source.cfg.Generation
	fingerprint, e := RouteFingerprint(routes)
	if e != nil {
		return e
	}
	if s.fingerprint == fingerprint {
		if s.lastFailure != nil && time.Since(s.timeAttempt) < 5*time.Second {
			record, done := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
			_ = s.Ledger.CloudflareRuleFailure(record, source, generation, cloudflareErrorCode(s.lastFailure))
			done()
			return s.lastFailure
		}
		if s.lastFailure == nil && time.Since(s.lastSuccess) < time.Minute {
			return nil
		}
	}
	defer func() {
		record, done := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
		defer done()
		code := ""
		if result != nil {
			code = cloudflareErrorCode(result)
		}
		if e := s.Ledger.CloudflareRuleFailure(record, source, generation, code); e != nil && result == nil {
			result = e
		}
		s.fingerprint = fingerprint
		s.timeAttempt = time.Now()
		s.lastFailure = result
		if result == nil {
			s.lastSuccess = s.timeAttempt
		}
	}()
	wanted := map[string]bool{}
	for _, r := range routes {
		if !c.addressAllowed(r.Address) {
			return ErrConfiguration
		}
		wanted[r.Address] = true
	}
	// The internal source permission is already confirmed by RouteSync. This
	// step records each public rule's intent before any Cloudflare mutation.
	if e := s.Ledger.PrepareCloudflareRules(ctx, source, generation, c.worker, routes); e != nil {
		return e
	}
	remote, e := s.read(ctx, wanted)
	if e != nil {
		return e
	}
	known, e := s.Ledger.CloudflareRuleBindings(ctx, source, generation)
	if e != nil {
		return e
	}
	knownAddresses := map[string]bool{}
	for _, b := range known {
		knownAddresses[b.Address] = true
	}
	for address, r := range remote {
		if !knownAddresses[address] {
			return ErrRuleConflict
		}
		if e = s.Ledger.ObserveCloudflareRule(ctx, source, generation, address, c.worker, ruleFact(r)); e != nil {
			return e
		}
	}
	for range 20 {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if e = s.Ledger.PrepareCloudflareRules(ctx, source, generation, c.worker, routes); e != nil {
			return e
		}
		o, e := s.Ledger.ClaimCloudflareRule(ctx, source)
		if errors.Is(e, store.ErrIngressIdle) {
			break
		}
		if e != nil {
			return e
		}
		work := func() error {
			if o.Action == "create" {
				if existing, ok := remote[o.Address]; ok {
					if !existing.Enabled {
						return ErrRuleConflict
					}
					return s.Ledger.ConfirmCloudflareRule(ctx, o, ruleFact(existing))
				}
				if o.State == "superseded" {
					return s.Ledger.CancelSupersededCloudflareCreate(ctx, o)
				}
				if !wanted[o.Address] {
					return ErrRuleConflict
				}
				if e := s.Ledger.IssueCloudflareRule(ctx, o); e != nil {
					return e
				}
				created, e := c.Create(ctx, o.Address)
				if errors.Is(e, ErrRuleConflict) {
					// Real API rejects duplicate literal recipients (409/2014).
					// A retry may meet the earlier successful unknown request.
					now, readErr := s.read(ctx, wanted)
					if readErr != nil {
						return readErr
					}
					var ok bool
					created, ok = now[o.Address]
					if !ok {
						return ErrRuleConflict
					}
				} else if e != nil {
					return e
				}
				actual, e := c.Read(ctx, created.ID)
				if e != nil {
					return e
				}
				if !c.owns(actual) || !actual.Enabled || ruleFact(actual) != ruleFact(created) {
					return ErrRuleConflict
				}
				if e = s.Ledger.ConfirmCloudflareRule(ctx, o, ruleFact(actual)); e != nil {
					return e
				}
				remote[o.Address] = actual
				return nil
			}
			actual, e := c.Read(ctx, o.Expected.ID)
			if errors.Is(e, ErrGone) {
				if e = s.Ledger.IssueCloudflareRule(ctx, o); e != nil {
					return e
				}
				delete(remote, o.Address)
				return s.Ledger.ConfirmCloudflareRule(ctx, o, store.CloudflareRuleFact{})
			}
			if e != nil {
				return e
			}
			if !c.owns(actual) || ruleFact(actual) != o.Expected {
				return ErrRuleConflict
			}
			if e = s.Ledger.IssueCloudflareRule(ctx, o); e != nil {
				return e
			}
			if e = c.Delete(ctx, actual); e != nil && !errors.Is(e, ErrGone) {
				return e
			}
			if _, e = c.Read(ctx, actual.ID); !errors.Is(e, ErrGone) {
				if e == nil {
					return ErrRuleUnknown
				}
				return e
			}
			delete(remote, o.Address)
			return s.Ledger.ConfirmCloudflareRule(ctx, o, store.CloudflareRuleFact{})
		}()
		if work != nil {
			release, done := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
			_ = s.Ledger.ReleaseCloudflareRule(release, o, cloudflareErrorCode(work))
			done()
			return work
		}
	}
	// A fresh complete read is mandatory; list/create success cannot by itself
	// report public rules ready, and an older expected ID cannot be replaced.
	remote, e = s.read(ctx, wanted)
	if e != nil {
		return e
	}
	known, e = s.Ledger.CloudflareRuleBindings(ctx, source, generation)
	if e != nil {
		return e
	}
	if len(remote) != len(wanted) {
		return ErrRuleConflict
	}
	seen := map[string]bool{}
	for _, b := range known {
		r, present := remote[b.Address]
		if wanted[b.Address] {
			seen[b.Address] = true
			if !present || !r.Enabled || b.Fact.ID == "" || ruleFact(r) != b.Fact {
				return ErrRuleConflict
			}
		} else if present || b.Fact.ID != "" {
			return ErrRuleUnknown
		}
	}
	for address := range wanted {
		if !seen[address] {
			return ErrRuleUnknown
		}
	}
	return nil
}
