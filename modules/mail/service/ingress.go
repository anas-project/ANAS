package main

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/anas-project/ANAS/modules/mail/dms"
	"github.com/anas-project/ANAS/modules/mail/ingress"
	"github.com/anas-project/ANAS/modules/mail/store"
)

const ingressDirectory = "/var/lib/anas-mail-ingress"

// Direct VPS publication requires an explicit, independently verified MX
// cutover. Preparing its routes alone is not evidence that mail reaches it.
var errDirectCutoverPending = errors.New("direct-vps-mx-cutover-unconfirmed")

type ingressEntry struct {
	client   *ingress.SourceClient
	consumer *ingress.Consumer
	wake     chan struct{}
	spec     ingress.SourceSpec
	routes   *ingress.RouteSync
	public   *ingress.CloudflareRuleSync
	forward  interface {
		Synchronize(context.Context, []ingress.Route) error
	}
	forwardReader *ingress.CloudflareForwardRules
}

// forwardCutover is implemented by the production forward synchronizer. The
// ordinary synchronize interface is also used by independent source tests.
type forwardCutover interface {
	PrepareDestinations(context.Context, []ingress.Route) error
	PreflightCutoverWithForwards(context.Context, []ingress.Route, []*ingress.CloudflareRuleSync, []*ingress.ForwardSync) error
}

type replacementSources struct {
	workers  []*ingress.CloudflareRuleSync
	forwards []*ingress.ForwardSync
}

func (p replacementSources) present() bool { return len(p.workers)+len(p.forwards) > 0 }

type ingressRuntime struct {
	entries      map[string]ingressEntry
	notification http.Handler
	cancel       context.CancelFunc
	workers      sync.WaitGroup
	store        store.Store
	syncMu       sync.Mutex
	cutoverReady bool
}

// forwardPublicPending is returned only after every source's local route has
// been read back and all other public checks succeeded. It separates a locally
// usable mailbox from unverified public delivery; it is never public readiness.
type forwardPublicPending struct{ cause error }

func (e forwardPublicPending) Error() string { return "mail-forward-public-pending" }
func (e forwardPublicPending) Unwrap() error { return e.cause }

func onlyForwardPending(err error) bool {
	if err == ingress.ErrDestinationPending || err == ingress.ErrForwardBatchPending {
		return true
	}
	if err == nil {
		return false
	}
	if multiple, ok := err.(interface{ Unwrap() []error }); ok {
		children := multiple.Unwrap()
		if len(children) == 0 {
			return false
		}
		for _, child := range children {
			if !onlyForwardPending(child) {
				return false
			}
		}
		return true
	}
	if single, ok := err.(interface{ Unwrap() error }); ok {
		return onlyForwardPending(single.Unwrap())
	}
	return false
}

func prepareIngress(cfg loginConfig, data store.Store, native *dms.Client) (*ingressRuntime, error) {
	if ingress.ValidateCapacity(cfg.Sources, cfg.IngressCapacityBytes) != nil {
		return nil, errors.New("mail source total capacity invalid")
	}
	if len(cfg.Sources) == 0 {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if e := data.ReserveIngressSources(ctx, cfg.IngressCapacityBytes, nil); e != nil {
			return nil, errors.New("mail source reservation unconfirmed")
		}
		return nil, nil
	}
	if native == nil || ingress.ValidateSources(cfg.Sources, nil) != nil {
		return nil, errors.New("mail source binding invalid")
	}
	ca, e := os.ReadFile(cfg.CAFile)
	if e != nil || len(ca) > 4<<20 {
		return nil, errors.New("mail source trust unavailable")
	}
	if e = prepareIngressStorage(); e != nil {
		return nil, e
	}
	runtime := &ingressRuntime{entries: map[string]ingressEntry{}, store: data}
	keys := map[string]ingress.NotificationSource{}
	for _, source := range cfg.Sources {
		client, e := ingress.NewSourceClient(source.ClientConfig(ca))
		if e != nil {
			runtime.close()
			return nil, errors.New("mail source binding invalid")
		}
		entry := ingressEntry{client: client, wake: make(chan struct{}, 1), consumer: &ingress.Consumer{Ledger: data, Source: client, Native: native, Directory: filepath.Join(ingressDirectory, source.ID), SourceID: source.ID, Generation: source.Generation}}
		entry.spec = source.SourceSpec
		entry.routes = &ingress.RouteSync{Ledger: data, Source: client, ID: source.ID, Generation: source.Generation}
		if source.Cloudflare != nil {
			rules, e := ingress.NewCloudflareRules(client, source.Cloudflare.ZoneID, source.Cloudflare.Worker, source.Credentials.Routing)
			if e != nil {
				client.Close()
				runtime.close()
				return nil, errors.New("mail public routing binding invalid")
			}
			entry.public = &ingress.CloudflareRuleSync{Ledger: data, Client: rules}
		}
		if source.CloudflareForward != nil {
			rules, e := ingress.NewCloudflareForwardRules(client, source.CloudflareForward.AccountID, source.CloudflareForward.ZoneID, source.CloudflareForward.Domain, source.Credentials.Destination, source.Credentials.Routing)
			if e != nil {
				client.Close()
				runtime.close()
				return nil, errors.New("mail forwarding binding invalid")
			}
			entry.forward = &ingress.ForwardSync{Ledger: data, Client: rules}
			entry.forwardReader = rules
		}
		runtime.entries[source.ID] = entry
		if source.Notifications {
			keys[source.ID] = ingress.NotificationSource{Generation: source.Generation, Secret: source.Credentials.Notification}
		}
	}
	reservations := make([]store.IngressReservation, 0, len(cfg.Sources))
	for _, source := range cfg.Sources {
		reservations = append(reservations, source.SourceSpec.Reservation())
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	err := data.ReserveIngressSources(ctx, cfg.IngressCapacityBytes, reservations)
	cancel()
	if err != nil {
		runtime.close()
		return nil, errors.New("mail source reservation unconfirmed")
	}
	if len(keys) > 0 {
		runtime.notification = ingress.NotificationHandler{Store: data, Sources: keys, Wake: func(source string) {
			if entry, ok := runtime.entries[source]; ok {
				select {
				case entry.wake <- struct{}{}:
				default:
				}
			}
		}}
	}
	return runtime, nil
}

func (r *ingressRuntime) Synchronize(ctx context.Context, ready map[string]string) error {
	if r == nil {
		return nil
	}
	r.syncMu.Lock()
	defer r.syncMu.Unlock()
	ordered := make([]ingressEntry, 0, len(r.entries))
	for _, entry := range r.entries {
		ordered = append(ordered, entry)
	}
	sort.Slice(ordered, func(i, j int) bool {
		if ordered[i].spec.Mode != ordered[j].spec.Mode {
			return ordered[i].spec.Mode == "draining"
		}
		return ordered[i].spec.ID < ordered[j].spec.ID
	})
	// Configuration is immutable for this runtime. On first use (and after a
	// restart), verify every replacement before closing any old source. Mark
	// this complete only after all public changes have been read back; partial
	// failures must repeat the fresh checks on the next attempt.
	replacements := map[string]replacementSources{}
	protected := map[string]bool{}
	directPreparation := map[string]bool{}
	if !r.cutoverReady {
		for _, next := range ordered {
			if next.spec.Mode != "active" {
				continue
			}
			for _, old := range ordered {
				if old.spec.Mode != "draining" || !sharedIngressDomain(next.spec, old.spec) {
					continue
				}
				_, forwardOK := next.forward.(forwardCutover)
				direct := next.spec.Provider == "cloud-relay" && next.spec.Cloudflare == nil && next.spec.CloudflareForward == nil && next.public == nil && next.forward == nil
				if next.client == nil || next.routes == nil || (!direct && next.public == nil && (!forwardOK || next.spec.CloudflareForward == nil)) {
					return errors.New("mail replacement routing binding missing")
				}
				peers := replacements[next.spec.ID]
				if old.public != nil {
					peers.workers = append(peers.workers, old.public)
				} else if previous, ok := old.forward.(*ingress.ForwardSync); ok && old.spec.CloudflareForward != nil {
					peers.forwards = append(peers.forwards, previous)
				} else {
					return errors.New("mail replacement routing binding missing")
				}
				replacements[next.spec.ID] = peers
				protected[old.spec.ID] = true
				if direct {
					directPreparation[next.spec.ID] = true
				}
			}
		}
		for _, entry := range ordered {
			if replacements[entry.spec.ID].present() {
				if e := entry.client.CheckReady(ctx, entry.spec); e != nil {
					return e
				}
			}
		}
	}
	desired := map[string][]ingress.Route{}
	for _, entry := range ordered {
		routes := []ingress.Route{}
		if entry.spec.Mode == "active" {
			snapshot, e := r.store.IngressRouteSnapshot(ctx, entry.spec.ID, entry.spec.Generation, ready)
			if e != nil {
				return e
			}
			for _, route := range snapshot {
				for _, domain := range entry.spec.Domains {
					if strings.HasSuffix(route.Address, "@"+domain) {
						routes = append(routes, route)
						break
					}
				}
			}
		}
		desired[entry.spec.ID] = routes
	}
	// A temporarily unconfirmed mailbox must not disappear merely because its
	// old source is being replaced. The old snapshot can retain a previously
	// published claim; the new source must independently be allowed to receive
	// that exact current claim before withdrawal can proceed.
	for _, next := range ordered {
		if !replacements[next.spec.ID].present() {
			continue
		}
		wanted := map[ingress.Route]bool{}
		for _, route := range desired[next.spec.ID] {
			wanted[route] = true
		}
		for _, old := range ordered {
			if old.spec.Mode != "draining" || !sharedIngressDomain(next.spec, old.spec) {
				continue
			}
			previous, e := r.store.IngressRouteSnapshot(ctx, old.spec.ID, old.spec.Generation, ready)
			if e != nil {
				return e
			}
			for _, route := range previous {
				for _, domain := range next.spec.Domains {
					if strings.HasSuffix(route.Address, "@"+domain) && !wanted[route] {
						return errors.New("mail replacement mailbox configuration unconfirmed")
					}
				}
			}
		}
	}
	for _, entry := range ordered {
		if replacements[entry.spec.ID].present() {
			if e := entry.routes.Prepare(ctx, desired[entry.spec.ID], ready); e != nil {
				return e
			}
		}
	}
	var preparationPending []error
	for _, entry := range ordered {
		if peers := replacements[entry.spec.ID]; peers.present() {
			if directPreparation[entry.spec.ID] {
				// Keep the old public route until a separate MX cutover has
				// been implemented and verified. Do not invent that proof here.
				preparationPending = append(preparationPending, errDirectCutoverPending)
			} else if entry.public != nil {
				if e := entry.public.PreflightWithForwards(ctx, desired[entry.spec.ID], peers.workers, peers.forwards); e != nil {
					return e
				}
			} else {
				forward := entry.forward.(forwardCutover)
				if e := forward.PrepareDestinations(ctx, desired[entry.spec.ID]); e != nil {
					if !onlyForwardPending(e) {
						return e
					}
					preparationPending = append(preparationPending, e)
				}
				if e := forward.PreflightCutoverWithForwards(ctx, desired[entry.spec.ID], peers.workers, peers.forwards); e != nil {
					return e
				}
			}
		}
	}
	// While any destination awaits verification, keep each old source's
	// *currently open* valid claims. A directory snapshot alone may include a
	// claim closed during an earlier partial attempt and must never reopen it.
	if len(preparationPending) > 0 {
		for _, entry := range ordered {
			if !protected[entry.spec.ID] {
				continue
			}
			if entry.client == nil || entry.routes == nil {
				return errors.New("mail draining source binding missing")
			}
			valid, e := r.store.IngressRouteSnapshot(ctx, entry.spec.ID, entry.spec.Generation, ready)
			if e != nil {
				return e
			}
			current, e := entry.client.ReadRoutes(ctx)
			if e != nil {
				return e
			}
			open := make(map[ingress.Route]bool, len(current.Bindings))
			for _, binding := range current.Bindings {
				if binding.ClosedSequence == nil {
					open[binding.Route] = true
				}
			}
			for _, route := range valid {
				if open[route] {
					desired[entry.spec.ID] = append(desired[entry.spec.ID], route)
				}
			}
		}
	}
	// Prove every local source permission before considering a pending public
	// target. A failure here never grants local access.
	for _, entry := range ordered {
		routes := desired[entry.spec.ID]
		if entry.spec.Provider == "cloud-relay" {
			if entry.client == nil {
				return errors.New("mail relay identity binding missing")
			}
			if e := entry.client.CheckRelayMode(ctx, entry.spec); e != nil {
				return e
			}
		}
		var e error
		if len(preparationPending) > 0 && protected[entry.spec.ID] {
			e = entry.routes.Prepare(ctx, routes, ready)
		} else {
			e = entry.routes.Synchronize(ctx, routes, ready)
		}
		if e != nil {
			return e
		}
	}
	var pending, failures []error
	pending = append(pending, preparationPending...)
	for _, entry := range ordered {
		routes := desired[entry.spec.ID]
		if len(preparationPending) > 0 && replacements[entry.spec.ID].present() {
			// The old public rule still occupies this address. Do not attempt to
			// publish a replacement until every target is independently verified.
			continue
		}
		if entry.forward != nil {
			if entry.spec.CloudflareForward == nil {
				failures = append(failures, errors.New("mail forwarding binding missing"))
				continue
			}
			if e := entry.forward.Synchronize(ctx, routes); e != nil {
				if onlyForwardPending(e) {
					pending = append(pending, e)
				} else {
					failures = append(failures, e)
				}
			}
			continue
		}
		if entry.public == nil {
			if entry.spec.Provider == "cloud-relay" && entry.spec.Cloudflare == nil && entry.spec.CloudflareForward == nil {
				// Direct VPS receiving has no Cloudflare rule. Clear only stale
				// Cloudflare errors from older attempts after the VPS routes have
				// been verified; this does not establish public SMTP readiness.
				if e := r.store.CloudflareRuleFailure(ctx, entry.spec.ID, entry.spec.Generation, ""); e != nil {
					failures = append(failures, e)
				}
				continue
			}
			record, done := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
			_ = r.store.CloudflareRuleFailure(record, entry.spec.ID, entry.spec.Generation, "cloudflare-not-configured")
			done()
			failures = append(failures, errors.New("mail public routing binding missing"))
			continue
		}
		if e := entry.public.Synchronize(ctx, routes); e != nil {
			failures = append(failures, e)
		}
	}
	if ctx.Err() != nil {
		failures = append(failures, ctx.Err())
	}
	if len(preparationPending) > 0 && len(failures) == 0 {
		// The preserved old rules may have changed while processing other
		// sources. Prove public ownership again before allowing local access.
		for _, entry := range ordered {
			if peers := replacements[entry.spec.ID]; peers.present() {
				if directPreparation[entry.spec.ID] {
					// No new public access is granted while direct MX is unknown;
					// old routes were reconciled by their own synchronizers above.
					continue
				}
				var e error
				if entry.public != nil {
					e = entry.public.PreflightWithForwards(ctx, desired[entry.spec.ID], peers.workers, peers.forwards)
				} else {
					e = entry.forward.(forwardCutover).PreflightCutoverWithForwards(ctx, desired[entry.spec.ID], peers.workers, peers.forwards)
				}
				if e != nil {
					failures = append(failures, e)
				}
			}
		}
	}
	if len(failures) > 0 {
		return errors.Join(append(failures, pending...)...)
	}
	if len(pending) > 0 {
		if len(directPreparation) > 0 {
			return errors.Join(pending...)
		}
		return forwardPublicPending{cause: errors.Join(pending...)}
	}
	r.cutoverReady = true
	return nil
}

func sharedIngressDomain(a, b ingress.SourceSpec) bool {
	for _, domain := range a.Domains {
		for _, other := range b.Domains {
			if domain == other {
				return true
			}
		}
	}
	return false
}
func (r *ingressRuntime) start(parent context.Context) {
	if r == nil {
		return
	}
	ctx, cancel := context.WithCancel(parent)
	r.cancel = cancel
	for _, entry := range r.entries {
		r.workers.Add(1)
		go func(entry ingressEntry) { defer r.workers.Done(); _ = entry.consumer.Run(ctx, entry.wake) }(entry)
	}
}
func (r *ingressRuntime) close() {
	if r == nil {
		return
	}
	if r.cancel != nil {
		r.cancel()
	}
	done := make(chan struct{})
	go func() { r.workers.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
	}
	for _, entry := range r.entries {
		entry.client.Close()
	}
}
func withIngressNotifications(base http.Handler, runtime *ingressRuntime) http.Handler {
	if runtime == nil || runtime.notification == nil {
		return base
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/ingress/notify" {
			runtime.notification.ServeHTTP(w, r)
			return
		}
		base.ServeHTTP(w, r)
	})
}
