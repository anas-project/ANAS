package incusingresshost

import (
	"context"
	"fmt"
	"slices"
)

// These methods are root-side installation primitives, not new public actions.
// The installed production gate stays closed until allocator/readers/health and
// native acceptance are connected. Normal apply never silently enables them.
func (b *Backend) InstallAddressRouting(ctx context.Context) error {
	return b.withGuard(ctx, func() error {
		if err := b.ensureProductionOpen(); err != nil {
			return err
		}
		if err := b.verifyBaseline(ctx); err != nil {
			return err
		}
		g, err := b.addressRouter()
		if err != nil {
			return err
		}
		if state, err := g.load(); err != nil || state.State != "installed" {
			publications, err := b.listReceipts()
			if err != nil || len(publications) != 0 {
				return fmt.Errorf("address baseline installation requires an empty publication scope")
			}
		}
		return g.installLocked(ctx)
	})
}

func (b *Backend) RemoveAddressRouting(ctx context.Context) error {
	return b.withGuard(ctx, func() error {
		if err := b.ensureProductionOpen(); err != nil {
			return err
		}
		if err := b.checkHTTPArtifactsLocked(ctx, nil); err != nil {
			return err
		}
		g, err := b.addressRouter()
		if err != nil {
			return err
		}
		return g.removeLocked(ctx)
	})
}

func (b *Backend) verifyAddressRouting(ctx context.Context, target Target) error {
	if b.config.AddressRouting == nil {
		if b.config.fixture {
			return nil
		}
		return fmt.Errorf("kernel address routing is not installed")
	}
	g, err := b.addressRouter()
	if err != nil {
		return err
	}
	return g.verifyLocked(ctx, target)
}

func (g *addressRouter) checkLocked(ctx context.Context, candidates []Target, receipts []receipt) error {
	state, err := g.load()
	if err != nil {
		return err
	}
	if state.State == "removed" {
		if len(candidates) != 0 || len(receipts) != 0 {
			return fmt.Errorf("removed address baseline still has publications")
		}
		return g.absent(ctx)
	}
	if state.State != "installed" {
		return fmt.Errorf("address routing has an unresolved installation intent")
	}
	if _, err := g.observe(ctx, state, false); err != nil {
		return err
	}
	byToken := map[string]Target{}
	for _, t := range candidates {
		byToken[t.Reservation] = t
	}
	accounted := map[string]addressRoutingHold{}
	for _, hold := range state.Holds {
		for _, token := range hold.Users {
			target, ok := byToken[token]
			if !ok || !sameAddressAllocation(target, hold.Target) {
				return fmt.Errorf("unaccounted kernel address hold")
			}
			accounted[token] = hold
		}
	}
	for _, r := range receipts {
		if !r.AddressHeld {
			continue
		}
		hold, ok := accounted[r.Target.Reservation]
		if !ok {
			if !r.PermitReady && !r.PermitIntent && !r.RouteReady && !r.RouteIntent && slices.Contains(state.Retired, r.Target.Reservation) {
				continue
			}
			return fmt.Errorf("publication address hold has no independent evidence")
		}
		if r.RouteReady || r.PermitReady {
			if hold.State != "held" {
				return fmt.Errorf("active publication has an unfinished address hold")
			}
			if err := g.verifyLocked(ctx, r.Target); err != nil {
				return err
			}
		}
	}
	return nil
}
