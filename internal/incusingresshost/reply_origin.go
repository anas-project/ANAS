package incusingresshost

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"
)

// The physical ifindex comes from the independent address hold, not the
// caller, a reused interface name, or a fresh lookup during cleanup. Withdrawal
// must still be possible after the original guest link has disappeared.
func (b *Backend) replyOriginSpec(target Target) (nftRuleSpec, error) {
	g, err := b.addressRouter()
	if err != nil {
		return nftRuleSpec{}, err
	}
	state, err := g.load()
	if err != nil || state.State != "installed" {
		return nftRuleSpec{}, fmt.Errorf("reply origin lacks installed address evidence")
	}
	for _, hold := range state.Holds {
		if sameAddressAllocation(hold.Target, target) && slices.Contains(hold.Users, target.Reservation) && hold.IfIndex != 0 {
			return b.replyOriginSpecAtIndex(target, hold.IfIndex), nil
		}
	}
	return nftRuleSpec{}, fmt.Errorf("reply origin lacks independent device identity")
}

func (b *Backend) replyOriginSpecAtIndex(target Target, index uint32) nftRuleSpec {
	return nftRuleSpec{Family: "bridge", Chain: replyChain, Table: b.config.OriginTable,
		Comment: permitComment(target) + ":origin", IIF: target.HostVethName, PhysicalIfIndex: index,
		MAC: target.NICMAC, IP: b.config.TraefikSourceIP, Set: permitSetName(target), Verdict: "accept"}
}

func (s nftRuleSpec) replyText() string {
	return fmt.Sprintf("meta iif %d iifname %s ether saddr %s ether type ip meta l4proto tcp ip daddr %s ip saddr . tcp sport @%s accept",
		s.PhysicalIfIndex, shellQuote(s.IIF), s.MAC, s.IP, s.Set)
}

// The two family fragments are submitted in ONE nft transaction by the caller.
// No receipt is ready until both the inet permission and this expiring physical
// origin gate have been read back. Never install a broad ESTABLISHED exception.
func (b *Backend) replyOriginScript(ctx context.Context, target Target, remove bool) (string, error) {
	if b.config.AddressRouting == nil {
		return "", nil
	}
	inv, err := b.nftFamilyInventory(ctx, "bridge")
	if err != nil {
		return "", err
	}
	spec, err := b.replyOriginSpec(target)
	if err != nil {
		return "", err
	}
	return b.replyOriginCommands(inv, spec, target, remove)
}

func (b *Backend) replyOriginCommands(inv nftInventory, spec nftRuleSpec, target Target, remove bool) (string, error) {
	if spec.PhysicalIfIndex == 0 || spec.Family != "bridge" || spec.Table != b.config.OriginTable || spec.Comment != permitComment(target)+":origin" || spec.Set != permitSetName(target) {
		return "", fmt.Errorf("invalid independent reply origin specification")
	}
	var out strings.Builder
	exists := false
	for _, rule := range inv.Rules {
		if rule.Comment != spec.Comment {
			continue
		}
		if exists || !rule.matches(spec) {
			return "", fmt.Errorf("reply origin rule identity changed")
		}
		exists = true
		if remove {
			fmt.Fprintf(&out, "delete rule bridge %s %s handle %s\n", spec.Table, replyChain, rule.Handle)
		}
	}
	if remove {
		if _, ok := inv.Sets[spec.Set]; ok {
			fmt.Fprintf(&out, "delete set bridge %s %s\n", spec.Table, spec.Set)
		}
		return out.String(), nil
	}
	if _, ok := inv.Sets[spec.Set]; !ok {
		fmt.Fprintf(&out, "add set bridge %s %s { type ipv4_addr . inet_service; flags timeout; timeout %s; }\n", spec.Table, spec.Set, b.ttlSeconds())
	}
	fmt.Fprintf(&out, "flush set bridge %s %s\n", spec.Table, spec.Set)
	fmt.Fprintf(&out, "add element bridge %s %s { %s . %s timeout %s }\n", spec.Table, spec.Set, target.GuestIP, strconv.Itoa(int(target.GuestPort)), b.ttlSeconds())
	if !exists {
		fmt.Fprintf(&out, "add rule bridge %s %s %s comment %s\n", spec.Table, replyChain, spec.replyText(), shellQuote(spec.Comment))
	}
	return out.String(), nil
}

func (b *Backend) confirmReplyOrigin(ctx context.Context, target Target, present bool) error {
	if b.config.AddressRouting == nil {
		return nil
	}
	inv, err := b.nftFamilyInventory(ctx, "bridge")
	if err != nil {
		return err
	}
	return confirmReplyOriginInventory(inv, target, present)
}

func confirmReplyOriginInventory(inv nftInventory, target Target, present bool) error {
	set, exists := inv.Sets[permitSetName(target)]
	rules := 0
	for _, rule := range inv.Rules {
		if rule.Comment == permitComment(target)+":origin" {
			rules++
		}
	}
	if present && (!exists || !set.live(target) || rules != 1) {
		return fmt.Errorf("reply origin is missing, duplicated or expired")
	}
	if !present && (exists || rules != 0) {
		return fmt.Errorf("reply origin permission remains after revocation")
	}
	return nil
}

func (b *Backend) checkReplyOriginArtifacts(ctx context.Context, receipts map[string]receipt) error {
	if b.config.AddressRouting == nil {
		return nil
	}
	inv, err := b.nftFamilyInventory(ctx, "bridge")
	if err != nil {
		return err
	}
	for _, r := range receipts {
		if err := confirmReplyOriginInventory(inv, r.Target, r.PermitReady && !r.PermitIntent); err != nil {
			return err
		}
	}
	return nil
}
