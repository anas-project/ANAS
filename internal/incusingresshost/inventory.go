package incusingresshost

import (
	"context"
	"fmt"
	"strconv"
)

func (b *Backend) CheckHTTPArtifacts(ctx context.Context, candidates []Target) error {
	return b.withGuard(ctx, func() error {
		return b.checkHTTPArtifactsLocked(ctx, candidates)
	})
}

func (b *Backend) checkHTTPArtifactsLocked(ctx context.Context, candidates []Target) error {
	if err := validateDirectory(b.config.ReceiptDir, b.config.fixture); err != nil {
		return err
	}
	if err := b.verifyRouteNamespace(ctx); err != nil {
		return err
	}
	if err := b.verifyBaseline(ctx); err != nil {
		return err
	}
	byReservation := map[string]Target{}
	byRoute := map[string][]Target{}
	for _, target := range candidates {
		if err := b.validateTarget(target); err != nil {
			return err
		}
		if _, exists := byReservation[target.Reservation]; exists {
			return fmt.Errorf("duplicate ingress host inventory candidate")
		}
		byReservation[target.Reservation] = target
		byRoute[target.GuestIP+"/32"] = append(byRoute[target.GuestIP+"/32"], target)
	}
	receipts, err := b.listReceipts()
	if err != nil {
		return err
	}
	receipted := map[string]bool{}
	for _, r := range receipts {
		candidate, ok := byReservation[r.Target.Reservation]
		if !ok || candidate != r.Target {
			return fmt.Errorf("ingress host receipt is not accounted for by the current journal")
		}
		receipted[r.Target.Reservation] = true
	}
	activeReceipts := map[string]receipt{}
	for _, r := range receipts {
		activeReceipts[r.Target.Reservation] = r
	}
	if b.config.AddressRouting != nil {
		guard, err := b.addressRouter()
		if err != nil {
			return err
		}
		if err := guard.checkLocked(ctx, candidates, receipts); err != nil {
			return err
		}
	}
	if err := b.checkNFTArtifacts(ctx, byReservation, activeReceipts); err != nil {
		return err
	}
	if err := b.checkRouteArtifacts(ctx, byRoute, activeReceipts); err != nil {
		return err
	}
	connections, err := b.conntrackScope(ctx)
	if err != nil {
		return err
	}
	for _, connection := range connections {
		target, ok := b.byConnectionCandidate(connection, byReservation)
		if !ok {
			return fmt.Errorf("ingress host inventory found an unaccounted backend connection")
		}
		r := activeReceipts[target.Reservation]
		if !r.PermitReady || !r.RouteReady {
			return fmt.Errorf("ingress host inventory found connection after route or permit removal")
		}
	}
	return nil
}

func (b *Backend) checkNFTArtifacts(ctx context.Context, candidates map[string]Target, receipts map[string]receipt) error {
	inv, err := b.nftInventory(ctx)
	if err != nil {
		return err
	}
	knownSets := map[string]bool{}
	knownComments := map[string]bool{}
	for _, target := range candidates {
		if receipt := receipts[target.Reservation]; receipt.Target != target || !receipt.PermitReady {
			continue
		}
		set, exists := inv.Sets[permitSetName(target)]
		if !exists || !set.live(target) {
			return fmt.Errorf("ready ingress receipt has no live nft permission")
		}
		seen := map[string]bool{}
		for _, rule := range inv.Rules {
			if rule.Comment == permitComment(target) || rule.Comment == permitComment(target)+":reply" {
				seen[rule.Comment] = true
			}
		}
		if len(seen) != 2 {
			return fmt.Errorf("ready ingress receipt is missing its nft rule pair")
		}
		knownSets[permitSetName(target)] = true
		knownComments[permitComment(target)] = true
		knownComments[permitComment(target)+":reply"] = true
	}
	for set := range inv.Sets {
		if !knownSets[set] {
			return fmt.Errorf("ingress host inventory found an unaccounted nft permit set")
		}
	}
	for _, rule := range inv.Rules {
		if !knownComments[rule.Comment] {
			return fmt.Errorf("ingress host inventory found an unaccounted nft permit rule")
		}
	}
	return b.checkReplyOriginArtifacts(ctx, receipts)
}

func (b *Backend) checkRouteArtifacts(ctx context.Context, candidates map[string][]Target, receipts map[string]receipt) error {
	body, err := b.runner.output(ctx, b.config.Binaries.IP, []string{"-j", "-n", b.config.RouteNetNS, "-4", "route", "show", "table", strconv.FormatUint(uint64(b.config.RouteTable), 10)})
	if err != nil {
		return fmt.Errorf("read ingress route table inventory: %w", err)
	}
	routes, err := parseRoutes(body)
	if err != nil {
		return err
	}
	for _, route := range routes {
		group := candidates[route.Dst]
		owned := false
		for _, candidate := range group {
			r := receipts[candidate.Reservation]
			if r.Target == candidate && r.RouteReady {
				owned = true
			}
		}
		if !owned || route.Gateway != b.config.IngressGateway || route.Dev != b.config.TraefikInterface || route.PrefSrc != b.config.TraefikSourceIP || route.Protocol != strconv.FormatUint(uint64(b.config.RouteProtocol), 10) {
			return fmt.Errorf("ingress host inventory found an unaccounted guest route")
		}
	}
	return nil
}

func (b *Backend) byConnectionCandidate(connection conntrackEntry, candidates map[string]Target) (Target, bool) {
	for _, target := range candidates {
		if connection.Src == b.config.TraefikSourceIP && connection.Dst == target.GuestIP && connection.DstPort == target.GuestPort {
			return target, true
		}
	}
	return Target{}, false
}
