package ingress

import (
	"context"
	"errors"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/anas-project/ANAS/modules/mail/store"
)

type ForwardSyncLedger interface {
	ForwardRuleLedger
	ForwardRuleRemovalLedger
	EnsureForwardSource(context.Context, string, string, string, string, string) error
	ForwardRules(context.Context, string, string, string, string, string) ([]store.ForwardRuleIntent, error)
	CloudflareRuleFailure(context.Context, string, string, string) error
}

type forwardJob struct {
	route  Route
	remove *store.ForwardRuleIntent
}

var ErrForwardBatchPending = errors.New("forward routing batch incomplete")

type forwardObservation struct {
	at  time.Time
	err error
}

// ForwardSync reconciles a complete source snapshot. Each claim is independent:
// a pending or failed item does not stop other jobs in the same pass. A bounded
// per-item context and rotating start let later items advance after timeouts.
// A successful pass means every item was checked, not public delivery readiness.
type ForwardSync struct {
	Ledger   ForwardSyncLedger
	Client   *CloudflareForwardRules
	mu       sync.Mutex
	next     int
	observed map[string]forwardObservation
	now      func() time.Time
	// Destination observations are separate from public-rule observations:
	// a verified destination must never be mistaken for a confirmed rule.
	destinationBatch *ForwardSync
}

func forwardJobKey(job forwardJob) string {
	if job.remove != nil {
		v := job.remove
		return strings.Join([]string{"remove", v.Destination.BindingID, v.State, v.Fact.ID, v.Fact.Fingerprint}, "\x00")
	}
	fp, _ := RouteFingerprint([]Route{job.route})
	return "ensure\x00" + fp
}

// runJobs bounds provider work while keeping success observations fresh and
// retrying failures without sliding their backoff. State is only an ephemeral
// optimization; a restart must independently reobserve every item.
func (s *ForwardSync) runJobs(ctx context.Context, jobs []forwardJob, run func(context.Context, forwardJob) error) error {
	if len(jobs) == 0 {
		s.next = 0
		s.observed = nil
		return nil
	}
	if s.observed == nil {
		s.observed = make(map[string]forwardObservation)
	}
	clock := s.now
	if clock == nil {
		clock = time.Now
	}
	keys := make(map[string]bool, len(jobs))
	for _, job := range jobs {
		keys[forwardJobKey(job)] = true
	}
	for key := range s.observed {
		if !keys[key] {
			delete(s.observed, key)
		}
	}
	start, attempts := s.next%len(jobs), 0
	work, stop := context.WithTimeout(ctx, 45*time.Second)
	defer stop()
	var failures []error
	incomplete := false
	for n := 0; n < len(jobs); n++ {
		if work.Err() != nil {
			s.next = (start + n) % len(jobs)
			return errors.Join(append(failures, work.Err())...)
		}
		at := (start + n) % len(jobs)
		key := forwardJobKey(jobs[at])
		if old, ok := s.observed[key]; ok {
			ttl := time.Minute
			if old.err != nil {
				ttl = 5 * time.Second
			}
			age := clock().Sub(old.at)
			if age >= 0 && age < ttl {
				if old.err != nil {
					failures = append(failures, old.err)
				}
				continue
			}
		}
		if attempts >= 20 {
			incomplete = true
			continue
		}
		attempts++
		item, done := context.WithTimeout(work, 5*time.Second)
		err := run(item, jobs[at])
		done()
		s.observed[key] = forwardObservation{at: clock(), err: err}
		if err != nil {
			failures = append(failures, err)
		}
		s.next = (at + 1) % len(jobs)
	}
	// A cached success can expire while later jobs are running. Do not report
	// a complete observation unless every success is still fresh at the end.
	for _, job := range jobs {
		observation, ok := s.observed[forwardJobKey(job)]
		age := clock().Sub(observation.at)
		if !ok || observation.err == nil && (age < 0 || age >= time.Minute) {
			incomplete = true
		}
	}
	if incomplete {
		failures = append(failures, ErrForwardBatchPending)
	}
	return errors.Join(failures...)
}

// PrepareDestinations prepares and independently verifies each destination
// before a caller withdraws an existing public entry. It does not create or
// delete public rules and must only be called after fresh local route readback.
// It deliberately uses a separate observation cache from Synchronize: a
// verified destination is not proof that a public rule exists. The caller must
// still check old-rule ownership and current mailbox claims before cutover.
func (s *ForwardSync) PrepareDestinations(ctx context.Context, routes []Route) (result error) {
	if s == nil || s.Ledger == nil || s.Client == nil || s.Client.rules == nil || s.Client.rules.source == nil {
		return ErrConfiguration
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := RouteFingerprint(routes); err != nil {
		return err
	}
	c := s.Client
	source, generation := c.rules.source.cfg.ID, c.rules.source.cfg.Generation
	defer func() {
		// Preparation success is not public routing success; only the full
		// synchronization may clear a prior public error.
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
	seen := make(map[string]bool, len(routes))
	jobs := make([]forwardJob, 0, len(routes))
	for _, route := range routes {
		if _, err := c.target(route); err != nil || seen[route.Address] {
			return ErrConfiguration
		}
		seen[route.Address] = true
		jobs = append(jobs, forwardJob{route: route})
	}
	sort.Slice(jobs, func(i, j int) bool { return jobs[i].route.Address < jobs[j].route.Address })
	if err := s.Ledger.EnsureForwardSource(ctx, source, generation, c.account, c.rules.zone, c.domain); err != nil {
		return err
	}
	if s.destinationBatch == nil {
		s.destinationBatch = &ForwardSync{now: s.now}
	}
	if err := s.destinationBatch.runJobs(ctx, jobs, func(item context.Context, job forwardJob) error {
		_, err := c.EnsureForwardDestination(item, s.Ledger, job.route)
		return err
	}); err != nil {
		return err
	}
	// Cached observations only bound the preparation work; they cannot
	// authorize withdrawing an old entry. Recheck every provider target and
	// its local claim in this pass. A timeout remains pending/failure.
	fresh, stop := context.WithTimeout(ctx, 45*time.Second)
	defer stop()
	for _, job := range jobs {
		item, done := context.WithTimeout(fresh, 5*time.Second)
		_, err := c.EnsureForwardDestination(item, s.Ledger, job.route)
		done()
		if err != nil {
			return err
		}
	}
	return fresh.Err()
}

func (s *ForwardSync) Synchronize(ctx context.Context, routes []Route) (result error) {
	if s == nil || s.Ledger == nil || s.Client == nil || s.Client.rules == nil || s.Client.rules.source == nil {
		return ErrConfiguration
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	c := s.Client
	if _, err := RouteFingerprint(routes); err != nil {
		return err
	}
	wanted := map[string]Route{}
	for _, route := range routes {
		if _, err := c.target(route); err != nil {
			return err
		}
		if _, exists := wanted[route.Address]; exists {
			return ErrConfiguration
		}
		wanted[route.Address] = route
	}
	source, generation := c.rules.source.cfg.ID, c.rules.source.cfg.Generation
	defer func() {
		code := ""
		if result != nil {
			code = cloudflareErrorCode(result)
		}
		record, done := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
		err := s.Ledger.CloudflareRuleFailure(record, source, generation, code)
		done()
		if err != nil {
			result = errors.Join(result, err)
		}
	}()
	if err := s.Ledger.EnsureForwardSource(ctx, source, generation, c.account, c.rules.zone, c.domain); err != nil {
		return err
	}
	known, err := s.Ledger.ForwardRules(ctx, source, generation, c.account, c.rules.zone, c.domain)
	if err != nil {
		return err
	}
	jobs := make([]forwardJob, 0, len(known)+len(routes))
	for i := range known {
		item := known[i]
		if item.State == "removed" || item.State == "cancelled" {
			continue
		}
		if current, ok := wanted[item.Destination.Route.Address]; !ok || current != item.Destination.Route {
			jobs = append(jobs, forwardJob{remove: &known[i]})
		}
	}
	sorted := append([]Route(nil), routes...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Address < sorted[j].Address })
	for _, route := range sorted {
		jobs = append(jobs, forwardJob{route: route})
	}
	return s.runJobs(ctx, jobs, func(item context.Context, job forwardJob) error {
		if job.remove != nil {
			return c.RemoveForwardRule(item, s.Ledger, *job.remove)
		}
		_, err := c.EnsureForwardRule(item, s.Ledger, job.route)
		return err
	})
}
