package main

import (
	"context"
	"fmt"
	"net/netip"
	"slices"
	"strings"

	"github.com/anas-project/ANAS/internal/computeclient"
	"github.com/anas-project/ANAS/internal/computenet"
)

// Slots pin an instance name to a fixed address outside the bridge's DHCP
// range, so a port binding keeps working when the instance is rebuilt
// (INCUS-R-155). The bridge owns the allocation, in user keys only the
// Provider's administrative certificate can write; the lease profile carries a
// copy the shared client reads when it creates the pinned instance.

const (
	slotKeyPrefix = "user.anas.slot."
	// slotIPv6Offset places slot addresses at <prefix>::ff00 and up, outside
	// the stateful DHCPv6 range <prefix>::1:0-<prefix>::1:ffff.
	slotIPv6Offset = 0xff00
)

type slotAddress struct {
	Name     string `json:"name"`
	Instance string `json:"instance"`
	IPv4     string `json:"ipv4"`
	IPv6     string `json:"ipv6,omitempty"`
}

// destinations is the ACL subject naming this slot's addresses.
func (s slotAddress) destinations() string {
	out := s.IPv4 + "/32"
	if s.IPv6 != "" {
		out += "," + s.IPv6 + "/128"
	}
	return out
}

// networkReport is what ensure hands back to Core for the resource state
// (INCUS-R-159): the bridge, its subnets and gateways, and the slot addresses.
type networkReport struct {
	Bridge      string        `json:"bridge"`
	IPv4Subnet  string        `json:"ipv4_subnet"`
	IPv4Gateway string        `json:"ipv4_gateway"`
	IPv6Subnet  string        `json:"ipv6_subnet,omitempty"`
	IPv6Gateway string        `json:"ipv6_gateway,omitempty"`
	Slots       []slotAddress `json:"slots,omitempty"`
}

func leaseNetworkName(sandbox string) string { return computeclient.NetworkName(sandbox) }

// bridgeAddressing reads the concrete subnets and gateways from a bridge.
func bridgeAddressing(n network, l lease) (v4, v6 netip.Prefix, err error) {
	v4, err = netip.ParsePrefix(strings.TrimSpace(n.Config["ipv4.address"]))
	if err != nil || !v4.Addr().Is4() {
		return v4, v6, fmt.Errorf("lease network %s has no concrete IPv4 subnet", n.Name)
	}
	if l.NetworkIPv6 {
		v6, err = netip.ParsePrefix(strings.TrimSpace(n.Config["ipv6.address"]))
		if err != nil || !v6.Addr().Is6() {
			return v4, v6, fmt.Errorf("lease network %s has no concrete IPv6 subnet", n.Name)
		}
	}
	return v4, v6, nil
}

// lastAddress returns the highest address of a prefix (its IPv4 broadcast).
func lastAddress(p netip.Prefix) netip.Addr {
	bytes := p.Masked().Addr().AsSlice()
	host := len(bytes)*8 - p.Bits()
	for i := len(bytes) - 1; host > 0; i-- {
		if host >= 8 {
			bytes[i] = 0xff
			host -= 8
		} else {
			bytes[i] |= byte(1<<host) - 1
			host = 0
		}
	}
	addr, _ := netip.AddrFromSlice(bytes)
	return addr
}

func addrAdd(a netip.Addr, n int) netip.Addr {
	for ; n > 0; n-- {
		a = a.Next()
	}
	for ; n < 0; n++ {
		a = a.Prev()
	}
	return a
}

// addressingConfig is the bridge configuration derived from the concrete
// subnets: a DHCP range that leaves the slot block free, stateful DHCPv6 when
// a slot needs a fixed IPv6, and one key set per declared slot. Existing slot
// addresses are kept; a slot that is no longer declared loses its keys.
func addressingConfig(current network, l lease) (map[string]string, []string, leaseAddressing, error) {
	v4, v6, err := bridgeAddressing(current, l)
	if err != nil {
		return nil, nil, leaseAddressing{}, err
	}
	addressing := leaseAddressing{Subnets: []string{v4.Masked().String()}}
	if l.NetworkIPv6 {
		addressing.Subnets = append(addressing.Subnets, v6.Masked().String())
	}
	config := map[string]string{}
	var remove []string
	// The slot block is the top MaxSlots addresses below the IPv4 broadcast.
	// Anything narrower than a /26 cannot spare it next to a usable DHCP range.
	reservable := v4.Bits() <= 26
	broadcast := lastAddress(v4)
	if reservable {
		first := addrAdd(v4.Masked().Addr(), 2)
		lastDynamic := addrAdd(broadcast, -computenet.MaxSlots-1)
		config["ipv4.dhcp.ranges"] = first.String() + "-" + lastDynamic.String()
	} else if len(l.Network.Slots) > 0 {
		return nil, nil, leaseAddressing{}, fmt.Errorf("lease network %s is narrower than a /26 and cannot reserve slot addresses", current.Name)
	}
	if l.NetworkIPv6 && len(l.Network.Slots) > 0 {
		base := v6.Masked().Addr().As16()
		start, end := base, base
		start[12], start[13], start[14], start[15] = 0, 1, 0, 0
		end[12], end[13], end[14], end[15] = 0, 1, 0xff, 0xff
		config["ipv6.dhcp.stateful"] = "true"
		config["ipv6.dhcp.ranges"] = netip.AddrFrom16(start).String() + "-" + netip.AddrFrom16(end).String()
	} else {
		remove = append(remove, "ipv6.dhcp.stateful", "ipv6.dhcp.ranges")
	}

	declared := map[string]computenet.Slot{}
	for _, slot := range l.Network.Slots {
		declared[slot.Name] = slot
	}
	taken := map[int]string{}
	existing := map[string]int{}
	for key, value := range current.Config {
		if !strings.HasPrefix(key, slotKeyPrefix) || !strings.HasSuffix(key, ".ipv4") {
			continue
		}
		name := strings.TrimSuffix(strings.TrimPrefix(key, slotKeyPrefix), ".ipv4")
		addr, err := netip.ParseAddr(value)
		index := -1
		for i := 0; i < computenet.MaxSlots; i++ {
			if addrAdd(broadcast, -1-i) == addr {
				index = i
			}
		}
		if _, still := declared[name]; err == nil && index >= 0 && still && reservable && taken[index] == "" {
			taken[index], existing[name] = name, index
		}
	}
	for key := range current.Config {
		if !strings.HasPrefix(key, slotKeyPrefix) {
			continue
		}
		name := strings.TrimPrefix(key, slotKeyPrefix)
		name = name[:max(0, strings.LastIndex(name, "."))]
		if _, kept := existing[name]; !kept {
			remove = append(remove, key)
		}
	}
	for _, slot := range l.Network.Slots {
		index, ok := existing[slot.Name]
		if !ok {
			for i := 0; i < computenet.MaxSlots; i++ {
				if taken[i] == "" {
					index, ok = i, true
					break
				}
			}
			if !ok {
				return nil, nil, leaseAddressing{}, fmt.Errorf("lease network %s has no free slot address", current.Name)
			}
			taken[index] = slot.Name
		}
		address := slotAddress{Name: slot.Name, Instance: slot.Instance, IPv4: addrAdd(broadcast, -1-index).String()}
		if l.NetworkIPv6 {
			address.IPv6 = addrAdd(v6.Masked().Addr(), slotIPv6Offset+index).String()
		}
		prefix := slotKeyPrefix + slot.Name
		config[prefix+".instance"], config[prefix+".ipv4"] = address.Instance, address.IPv4
		if address.IPv6 != "" {
			config[prefix+".ipv6"] = address.IPv6
		} else {
			remove = append(remove, prefix+".ipv6")
		}
		addressing.Slots = append(addressing.Slots, address)
	}
	return config, remove, addressing, nil
}

// slotProfileConfig is the copy of the slot allocation on the lease profile.
func slotProfileConfig(slots []slotAddress) map[string]string {
	config := map[string]string{}
	for _, slot := range slots {
		prefix := slotKeyPrefix + slot.Name
		config[prefix+".instance"], config[prefix+".ipv4"] = slot.Instance, slot.IPv4
		if slot.IPv6 != "" {
			config[prefix+".ipv6"] = slot.IPv6
		}
	}
	return config
}

// readAddressing recomputes the addressing from a bridge as inspect sees it,
// requiring the bridge to already carry exactly the configuration ensure
// would write.
func readAddressing(current network, l lease) (leaseAddressing, bool, error) {
	config, remove, addressing, err := addressingConfig(current, l)
	if err != nil {
		return leaseAddressing{}, false, err
	}
	for key, value := range config {
		if current.Config[key] != value {
			return addressing, false, nil
		}
	}
	for _, key := range remove {
		if current.Config[key] != "" {
			return addressing, false, nil
		}
	}
	return addressing, true, nil
}

func reportFor(name string, n network, addressing leaseAddressing, l lease) networkReport {
	v4, v6, _ := bridgeAddressing(n, l)
	report := networkReport{Bridge: name, IPv4Subnet: v4.Masked().String(), IPv4Gateway: v4.Addr().String(), Slots: addressing.Slots}
	if l.NetworkIPv6 {
		report.IPv6Subnet, report.IPv6Gateway = v6.Masked().String(), v6.Addr().String()
	}
	return report
}

// removeLegacyBridge deletes the bridge and ACL this lease used under the old
// anas* name once nothing uses them. Instances still attached keep it; the
// daemon refuses to delete a network in use, and so does this.
func removeLegacyBridge(ctx context.Context, c *client, l lease) error {
	legacy := computeclient.LegacyNetworkName(l.Sandbox)
	var old network
	err := c.do(ctx, "GET", "/1.0/networks/"+legacy+"?project=default", nil, &old)
	if isNotFound(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if old.Type != "bridge" || old.Config["user.anas.consumer"] != l.Consumer || old.Config["user.anas.sandbox"] != l.Sandbox ||
		old.Config[leaseCredentialKey] != "" && old.Config[leaseCredentialKey] != l.Credential || len(old.UsedBy) > 0 {
		return nil
	}
	if err := c.do(ctx, "DELETE", "/1.0/networks/"+legacy+"?project=default", nil, nil); err != nil && !isNotFound(err) {
		return err
	}
	var acl networkACL
	err = c.do(ctx, "GET", "/1.0/network-acls/"+legacy+"?project=default", nil, &acl)
	if err == nil && verifyACLOwner(acl, l) == nil {
		if err := c.do(ctx, "DELETE", "/1.0/network-acls/"+legacy+"?project=default", nil, nil); err != nil && !isNotFound(err) {
			return err
		}
	}
	return nil
}

// sortedSlots keeps the report and the profile copy in a stable order.
func sortedSlots(slots []slotAddress) []slotAddress {
	out := slices.Clone(slots)
	slices.SortFunc(out, func(a, b slotAddress) int { return strings.Compare(a.Name, b.Name) })
	return out
}
