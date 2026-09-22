package computeingressruntime

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"net/netip"
	"regexp"
	"slices"
	"sort"

	"github.com/anas-project/ANAS/internal/computeingress"
)

// PublicationTarget is the complete trusted input to one HTTP publication.
// Consumer request files never populate this value directly: Publication is
// produced by computeingress.Planner and Epoch comes from Core's active frozen
// authorization snapshot.
type PublicationTarget struct {
	Incarnation string                     `json:"incarnation"`
	Epoch       string                     `json:"epoch"`
	NICMAC      string                     `json:"nic_mac"`
	Publication computeingress.Publication `json:"publication"`
}

// AppliedPublication is a durable receipt for steps that the executor has
// completed and read back. It is recovery input, not authorization: every
// reconciliation intersects it with the current Core epoch and a fresh target
// observation before allowing a route to remain published.
type AppliedPublication struct {
	Target          PublicationTarget `json:"target"`
	Retiring        bool              `json:"retiring,omitempty"`
	AddressHeld     bool              `json:"address_held"`
	GuestRouteReady bool              `json:"guest_route_ready"`
	PermitReady     bool              `json:"permit_ready"`
	RoutePublished  bool              `json:"route_published"`
}

// ExecutorState is intentionally small and contains no secret, raw firewall
// text, command, socket path or consumer-controlled URL.
type ExecutorState struct {
	Schema       string               `json:"schema"`
	Publications []AppliedPublication `json:"publications"`
	// Retired reservations are never accepted again, even after restart. A
	// fresh authorized request needs a new Planner reservation. No TTL pruning.
	Retired []PublicationTarget `json:"retired,omitempty"`
}

const executorStateSchema = "anas.compute-http-executor-state/v1"

// Observer revalidates the current managed instance, NIC allocation and
// restricted lease mapping. Implementations use a read-only Incus identity.
// They must reject stopped, paused, deleted, moved or reallocated instances.
type Observer interface {
	ValidateTarget(context.Context, PublicationTarget) error
}

// HostActions is the typed host boundary. Its implementation independently
// resolves and validates the lease/instance allocation. It accepts neither
// argv nor firewall text and must read back every completed mutation.
type HostActions interface {
	HTTPArtifactInventory
	HoldAddress(context.Context, PublicationTarget) error
	EnsureGuestRoute(context.Context, PublicationTarget) error
	EnsureHTTPPermit(context.Context, PublicationTarget) error
	RemoveHTTPPermit(context.Context, PublicationTarget) error
	CloseHTTPConnections(context.Context, PublicationTarget) error
	RemoveGuestRoute(context.Context, PublicationTarget) error
	ReleaseAddress(context.Context, PublicationTarget) error
}

// BackendProbe runs from Traefik's ingress namespace after the narrow route and
// permit exist. It must verify the expected managed fixture/application rather
// than treating a successful TCP connect as target identity.
type BackendProbe interface {
	ProbeHTTP(context.Context, PublicationTarget) error
}

// RouteRenderer owns the trusted Traefik file-provider directory. PublishHTTP
// must atomically replace only this reservation's route and RemoveHTTP must
// confirm it is absent before returning.
type RouteRenderer interface {
	HTTPArtifactInventory
	PublishHTTP(context.Context, PublicationTarget) error
	RemoveHTTP(context.Context, PublicationTarget) error
}

// Journal is usable only inside StateStore.WithExclusive. Check verifies the
// pinned directory/lock identities before every external operation.
type Journal interface {
	Load(context.Context) (ExecutorState, error)
	Save(context.Context, ExecutorState) error
	Check(context.Context) error
}

// StateStore holds one exclusive lock through observation, mutation and Save.
// All publishers for a Traefik ingress scope must use the same state directory.
type StateStore interface {
	WithExclusive(context.Context, func(Journal) error) error
}

// AuthorizationSource rechecks current Core authority, including the frozen
// domain/auth and request binding. Observer checks the independent instance.
type AuthorizationSource interface {
	ValidateAuthorization(context.Context, PublicationTarget) error
}

type Executor struct {
	Observer  Observer
	Host      HostActions
	Probe     BackendProbe
	Renderer  RouteRenderer
	Store     StateStore
	Authority AuthorizationSource
}

type execution struct {
	Executor
	journal Journal
}

// Reconcile converges durable receipts on the current desired intersection.
// Unknown/stale receipts are withdrawn before any desired publication opens.
// A failed cleanup remains recorded and blocks address reuse and new routes.
func (e Executor) Reconcile(ctx context.Context, epoch string, desired []PublicationTarget) error {
	return e.withSession(ctx, func(s execution) error { return s.reconcile(ctx, epoch, desired) })
}

func (e Executor) withSession(ctx context.Context, run func(execution) error) error {
	if e.Observer == nil || e.Host == nil || e.Probe == nil || e.Renderer == nil || e.Store == nil || e.Authority == nil {
		return fmt.Errorf("HTTP executor requires authority, observer, typed host actions, probe, renderer and exclusive state store")
	}
	return e.Store.WithExclusive(ctx, func(j Journal) error { return run(execution{Executor: e, journal: j}) })
}

func (e execution) reconcile(ctx context.Context, epoch string, desired []PublicationTarget) error {
	if !validEpoch(epoch) {
		return fmt.Errorf("HTTP executor requires the current authorization epoch")
	}
	wanted := make(map[string]PublicationTarget, len(desired))
	wantedHosts := make(map[string]string, len(desired))
	wantedPorts := make(map[instancePort]string, len(desired))
	for _, target := range desired {
		if err := validateTarget(epoch, target); err != nil {
			return err
		}
		key := target.Publication.Reservation
		if old, exists := wanted[key]; exists && old != target {
			return fmt.Errorf("HTTP executor has conflicting desired reservation %q", key)
		}
		if owner, exists := wantedHosts[target.Publication.Host]; exists && owner != key {
			return fmt.Errorf("HTTP executor has conflicting desired hostname %q", target.Publication.Host)
		}
		if owner, exists := wantedPorts[targetPort(target)]; exists && owner != key {
			return fmt.Errorf("HTTP executor has conflicting desired instance ports")
		}
		wanted[key] = target
		wantedHosts[target.Publication.Host] = key
		wantedPorts[targetPort(target)] = key
	}
	state, err := e.journal.Load(ctx)
	if err != nil {
		return fmt.Errorf("load HTTP executor state: %w", err)
	}
	if state.Schema != executorStateSchema {
		return fmt.Errorf("unsupported HTTP executor state schema")
	}
	if err := validateState(state); err != nil {
		return err
	}
	for _, retired := range state.Retired {
		delete(wanted, retired.Publication.Reservation)
	}

	// Cleanup first. A persisted route from another epoch or target must never
	// coexist with a newly opened route after restart or authorization change.
	var invalidTargets []error
	for i := len(state.Publications) - 1; i >= 0; i-- {
		applied := &state.Publications[i]
		current, keep := wanted[applied.Target.Publication.Reservation]
		if !applied.Retiring && keep && current == applied.Target {
			if err := e.validate(ctx, current); err == nil {
				continue
			} else {
				invalidTargets = append(invalidTargets, err)
			}
		}
		reservation := applied.Target.Publication.Reservation
		if err := e.withdraw(ctx, &state, i); err != nil {
			return err
		}
		if keep {
			delete(wanted, reservation)
		}
	}
	if len(invalidTargets) != 0 {
		return fmt.Errorf("HTTP targets invalidated during reconciliation: %w", errors.Join(invalidTargets...))
	}
	// A missing journal is not evidence of an empty external installation.
	// Account for the complete installed scope before renewing or opening any
	// publication. Unaccounted artifacts never become new authority.
	if err := e.checkArtifacts(ctx, state); err != nil {
		return errors.Join(err, e.withdrawAll(ctx))
	}

	keys := make([]string, 0, len(wanted))
	for key := range wanted {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		target := wanted[key]
		index := findApplied(state, key)
		if index < 0 {
			state.Publications = append(state.Publications, AppliedPublication{Target: target})
			index = len(state.Publications) - 1
			if err := e.save(ctx, state); err != nil {
				return err
			}
		}
		if err := e.publish(ctx, &state, index); err != nil {
			return err
		}
	}
	if err := e.checkArtifacts(ctx, state); err != nil {
		return errors.Join(err, e.withdrawAll(ctx))
	}
	return nil
}

func (e execution) publish(ctx context.Context, state *ExecutorState, index int) error {
	a := &state.Publications[index]
	if a.Retiring {
		return fmt.Errorf("HTTP publication is retiring")
	}
	fail := func(cause error) error {
		return errors.Join(cause, e.withdraw(ctx, state, index))
	}
	if err := e.validate(ctx, a.Target); err != nil {
		return fail(fmt.Errorf("HTTP target is no longer publishable: %w", err))
	}
	steps := []struct {
		done *bool
		run  func(context.Context, PublicationTarget) error
		name string
	}{
		{&a.AddressHeld, e.Host.HoldAddress, "hold guest address"},
		{&a.GuestRouteReady, e.Host.EnsureGuestRoute, "ensure guest route"},
		{&a.PermitReady, e.Host.EnsureHTTPPermit, "ensure HTTP permit"},
	}
	for _, step := range steps {
		if err := e.validate(ctx, a.Target); err != nil {
			return fail(err)
		}
		if err := step.run(ctx, a.Target); err != nil {
			return fail(fmt.Errorf("%s: %w", step.name, err))
		}
		if !*step.done {
			*step.done = true
			if err := e.save(ctx, *state); err != nil {
				return err
			}
		}
	}
	if err := e.validate(ctx, a.Target); err != nil {
		return fail(fmt.Errorf("HTTP target changed before probe: %w", err))
	}
	if err := e.Probe.ProbeHTTP(ctx, a.Target); err != nil {
		cleanupErr := e.withdraw(ctx, state, index)
		return errors.Join(fmt.Errorf("probe HTTP backend: %w", err), cleanupErr)
	}
	if err := e.validate(ctx, a.Target); err != nil {
		cleanupErr := e.withdraw(ctx, state, index)
		return errors.Join(fmt.Errorf("HTTP target changed before route publication: %w", err), cleanupErr)
	}
	if err := e.Renderer.PublishHTTP(ctx, a.Target); err != nil {
		cleanupErr := e.withdraw(ctx, state, index)
		return errors.Join(fmt.Errorf("publish HTTP route: %w", err), cleanupErr)
	}
	// Route consumption can span multiple API snapshots. Recheck current
	// authority and independent instance facts after it, before recording ready.
	// A stopped/replaced guest must retire even when the renderer succeeded.
	if err := e.validate(ctx, a.Target); err != nil {
		return fail(fmt.Errorf("HTTP target changed during route publication: %w", err))
	}
	if !a.RoutePublished {
		a.RoutePublished = true
		return e.save(ctx, *state)
	}
	return nil
}

func (e execution) withdraw(ctx context.Context, state *ExecutorState, index int) error {
	a := &state.Publications[index]
	// Persist intent before touching external state. A retry must finish
	// retirement even if the desired request and observed identity return.
	if !a.Retiring {
		a.Retiring = true
		if err := e.save(ctx, *state); err != nil {
			return err
		}
	}
	// Run every closing operation that could have become visible after its last
	// durable receipt. Each implementation must be idempotent and confirm
	// absence; this closes the process-crash window between mutation and Save.
	if err := e.closeStep(ctx, a.Target, e.Renderer.RemoveHTTP); err != nil {
		return fmt.Errorf("remove HTTP route: %w", err)
	}
	if a.RoutePublished {
		a.RoutePublished = false
		if err := e.save(ctx, *state); err != nil {
			return err
		}
	}
	if err := e.closeStep(ctx, a.Target, e.Host.RemoveHTTPPermit); err != nil {
		return fmt.Errorf("remove HTTP permit: %w", err)
	}
	if a.PermitReady {
		a.PermitReady = false
		if err := e.save(ctx, *state); err != nil {
			return err
		}
	}
	// Connection cleanup is required even when a previous crash happened after
	// deleting the permit but before recording/finishing conntrack cleanup.
	if err := e.closeStep(ctx, a.Target, e.Host.CloseHTTPConnections); err != nil {
		return fmt.Errorf("close HTTP backend connections: %w", err)
	}
	if err := e.closeStep(ctx, a.Target, e.Host.RemoveGuestRoute); err != nil {
		return fmt.Errorf("remove guest route: %w", err)
	}
	if a.GuestRouteReady {
		a.GuestRouteReady = false
		if err := e.save(ctx, *state); err != nil {
			return err
		}
	}
	// Unknown external artifacts can still refer to this address. Close known
	// routes/permits first, but do not permit address reuse until the complete
	// installed scope is accounted for. Other retiring targets remain cleanup
	// candidates; inventory does not grant permission to reopen them.
	if err := e.checkArtifactScope(ctx, *state); err != nil {
		return fmt.Errorf("retain guest address while external recovery is incomplete: %w", err)
	}
	if err := e.closeStep(ctx, a.Target, e.Host.ReleaseAddress); err != nil {
		return fmt.Errorf("release guest address: %w", err)
	}
	if a.AddressHeld {
		a.AddressHeld = false
		if err := e.save(ctx, *state); err != nil {
			return err
		}
	}
	state.Retired = append(state.Retired, a.Target)
	state.Publications = slices.Delete(state.Publications, index, index+1)
	return e.save(ctx, *state)
}

func (e execution) save(ctx context.Context, state ExecutorState) error {
	if err := validateState(state); err != nil {
		return err
	}
	if err := e.journal.Save(ctx, state); err != nil {
		return fmt.Errorf("persist HTTP executor receipt: %w", err)
	}
	return nil
}

func validateTarget(epoch string, target PublicationTarget) error {
	if !reservationID.MatchString(target.Publication.Reservation) {
		return fmt.Errorf("invalid HTTP executor reservation")
	}
	return validateTargetIdentity(epoch, target)
}

// Stable identity is also used by the private fixture registry. A registry
// contains no reservation; only a freshly authorized Planner target can supply
// one to a probe. Executor state continues to require validateTarget above.
func validateTargetIdentity(epoch string, target PublicationTarget) error {
	p := target.Publication
	if !validEpoch(epoch) || target.Epoch != epoch || p.Deployment == "" || p.Lease.Consumer == "" || p.Lease.Resource == "" || p.InstanceID == "" || p.InstanceUUID == "" || p.Host == "" || p.GuestIP == "" || p.GuestPort == 0 {
		return fmt.Errorf("invalid HTTP executor target")
	}
	if p.Auth != "none" && p.Auth != "forward_auth" || p.Auth == "forward_auth" && !computeingress.ValidMiddleware(p.Middleware) || p.Auth == "none" && p.Middleware != "" {
		return fmt.Errorf("invalid frozen HTTP route authentication")
	}
	if !leaseID.MatchString(p.Lease.Consumer) || !leaseID.MatchString(p.Lease.Resource) || !nicMAC.MatchString(target.NICMAC) || !validEpoch(target.Incarnation) || len(p.Deployment) > 128 || len(p.InstanceUUID) > 128 || !validHTTPHost(p.Host) {
		return fmt.Errorf("invalid HTTP executor target identity")
	}
	if err := (computeingress.Request{Action: "publish", InstanceID: p.InstanceID, WorkloadID: p.WorkloadID, GuestPort: p.GuestPort, Label: p.Label}).Validate(); err != nil {
		return err
	}
	ip, err := netip.ParseAddr(p.GuestIP)
	if err != nil || !ip.Is4() || !ip.IsPrivate() || ip.String() != p.GuestIP {
		return fmt.Errorf("HTTP executor target requires canonical private IPv4")
	}
	return nil
}

var reservationID = regexp.MustCompile(`^[a-f0-9]{32}:[1-9][0-9]{0,19}$`)
var leaseID = regexp.MustCompile(`^[a-z][a-z0-9_]{0,62}$`)
var nicMAC = regexp.MustCompile(`^(?:[a-f0-9]{2}:){5}[a-f0-9]{2}$`)

func validateState(state ExecutorState) error {
	if state.Schema != executorStateSchema {
		return fmt.Errorf("unsupported HTTP executor state schema")
	}
	seenReservation := map[string]struct{}{}
	seenHost := map[string]struct{}{}
	seenPort := map[instancePort]struct{}{}
	for _, applied := range state.Publications {
		p := applied.Target.Publication
		if err := validateTarget(applied.Target.Epoch, applied.Target); err != nil {
			return fmt.Errorf("invalid HTTP executor receipt")
		}
		if applied.RoutePublished && (!applied.PermitReady || !applied.GuestRouteReady || !applied.AddressHeld) || applied.PermitReady && (!applied.GuestRouteReady || !applied.AddressHeld) || applied.GuestRouteReady && !applied.AddressHeld {
			return fmt.Errorf("HTTP executor receipt has an impossible step order")
		}
		if _, exists := seenReservation[p.Reservation]; exists {
			return fmt.Errorf("duplicate HTTP executor reservation")
		}
		if _, exists := seenHost[p.Host]; exists {
			return fmt.Errorf("duplicate HTTP executor hostname")
		}
		seenReservation[p.Reservation] = struct{}{}
		seenHost[p.Host] = struct{}{}
		port := targetPort(applied.Target)
		if _, exists := seenPort[port]; exists {
			return fmt.Errorf("duplicate HTTP executor instance port")
		}
		seenPort[port] = struct{}{}
	}
	for _, retired := range state.Retired {
		if err := validateTarget(retired.Epoch, retired); err != nil {
			return fmt.Errorf("invalid retired HTTP reservation")
		}
		key := retired.Publication.Reservation
		if _, exists := seenReservation[key]; exists {
			return fmt.Errorf("duplicate or active retired HTTP reservation")
		}
		seenReservation[key] = struct{}{}
	}
	return nil
}

type instancePort struct {
	Lease    computeingress.Lease
	Instance string
	Port     uint16
}

func targetPort(target PublicationTarget) instancePort {
	p := target.Publication
	return instancePort{p.Lease, p.InstanceID, p.GuestPort}
}

func (e execution) validate(ctx context.Context, target PublicationTarget) error {
	if err := e.journal.Check(ctx); err != nil {
		return err
	}
	if err := e.Authority.ValidateAuthorization(ctx, target); err != nil {
		return err
	}
	if err := e.Observer.ValidateTarget(ctx, target); err != nil {
		return err
	}
	if err := e.Authority.ValidateAuthorization(ctx, target); err != nil {
		return err
	}
	return e.journal.Check(ctx)
}

func (e execution) closeStep(ctx context.Context, target PublicationTarget, run func(context.Context, PublicationTarget) error) error {
	if err := e.journal.Check(ctx); err != nil {
		return err
	}
	return run(ctx, target)
}

// Withdrawal does not require Core to be readable or the guest to be Running.
// Try every recorded publication, retaining failures, so one broken lease does
// not prevent other revoked routes from being closed.
func (e execution) withdrawAll(ctx context.Context) error {
	state, err := e.journal.Load(ctx)
	if err != nil {
		return err
	}
	if err := validateState(state); err != nil {
		return err
	}
	var failures []error
	// Iterate stable identities: disk canonicalization can reorder records
	// after an uncertain Save and reload.
	targets := slices.Clone(state.Publications)
	for _, target := range targets {
		index := findApplied(state, target.Target.Publication.Reservation)
		if index < 0 {
			continue
		}
		if err := e.withdraw(ctx, &state, index); err != nil {
			failures = append(failures, err)
			// Reload after an uncertain Save; never carry speculative receipt
			// changes into another lease's successful state replacement.
			state, err = e.journal.Load(ctx)
			if err != nil {
				return errors.Join(append(failures, err)...)
			}
		}
	}
	return errors.Join(failures...)
}

func validEpoch(value string) bool {
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == 32 && hex.EncodeToString(decoded) == value
}

func findApplied(state ExecutorState, reservation string) int {
	for i := range state.Publications {
		if state.Publications[i].Target.Publication.Reservation == reservation {
			return i
		}
	}
	return -1
}
