package incusingresshost

import (
	"encoding/json"
	"fmt"
	"net/netip"
	"slices"
	"strconv"
	"strings"
)

type forwardingNFTSet struct {
	Name    string
	Types   []string
	Timeout uint64
	Values  []any
}

type forwardingNFTDefinition struct {
	Family, Name, Comment string
	Chains                []nftChain
	Sets                  []forwardingNFTSet
	Rules                 []nftRule
}

func forwardingNFTName(scope ForwardingKernelScope) string     { return "anas_fwd_" + scope.ID[:20] }
func forwardingCompatChain(scope ForwardingKernelScope) string { return "ANAS-FWD-" + scope.ID[:19] }
func forwardingIPSetName(scope ForwardingKernelScope) string   { return "anas-fwd-" + scope.ID[:20] }
func forwardingKernelComment(scope ForwardingKernelScope) string {
	return "anas-forwarding:" + scope.Owner + ":" + scope.ID
}

func forwardingConcat(values ...any) any { return map[string]any{"concat": values} }
func forwardingMeta(key string) any      { return map[string]any{"meta": map[string]any{"key": key}} }
func forwardingDirection(value string) any {
	return nftMatch(map[string]any{"ct": map[string]any{"key": "direction"}}, "==", value)
}
func forwardingSetMatch(name string, values ...any) any {
	var left any = values[0]
	if len(values) > 1 {
		left = forwardingConcat(values...)
	}
	return nftMatch(left, "==", "@"+name)
}

// Only the lease bridge is fenced. Compatibility ACCEPT rules are appended
// after existing administrator FORWARD rules, never before an explicit DROP.
func forwardingNFTDefinitions(scope ForwardingKernelScope, instances []ForwardingInstanceProof) ([]forwardingNFTDefinition, error) {
	if validateForwardingInstances(scope, instances) != nil {
		return nil, fmt.Errorf("invalid forwarding nft scope")
	}
	name, comment := forwardingNFTName(scope), forwardingKernelComment(scope)
	inet := forwardingNFTDefinition{Family: "inet", Name: name, Comment: comment}
	bridge := forwardingNFTDefinition{Family: "bridge", Name: name, Comment: comment}
	chain := func(family, name, hook string, priority int) nftChain {
		return nftChain{Family: family, Table: forwardingNFTName(scope), Name: name, Type: "filter", Hook: hook, Prio: &priority, Policy: "accept"}
	}
	inet.Chains = []nftChain{chain("inet", "forward", "forward", -190)}
	bridge.Chains = []nftChain{chain("bridge", "source", "prerouting", -310), chain("bridge", "reply", "postrouting", 190)}
	seconds := uint64(ForwardingPermitTTL.Seconds())
	inet.Sets = []forwardingNFTSet{{Name: "flows", Types: []string{"iface_index", "ipv4_addr", "ipv4_addr", "inet_service"}, Timeout: seconds}}
	bridge.Sets = []forwardingNFTSet{
		{Name: "sources", Types: []string{"iface_index", "ether_addr", "ipv4_addr"}, Timeout: seconds},
		{Name: "arp_sources", Types: []string{"iface_index", "ether_addr", "ether_addr", "ipv4_addr"}, Timeout: seconds},
		{Name: "source_ports", Types: []string{"iface_index", "ether_addr"}, Timeout: seconds},
		{Name: "replies", Types: []string{"iface_index", "ether_addr", "ipv4_addr", "ipv4_addr", "inet_service"}, Timeout: seconds},
		{Name: "guests", Types: []string{"ipv4_addr"}}, {Name: "macs", Types: []string{"ether_addr"}},
	}
	add := func(def *forwardingNFTDefinition, chain, label string, expr ...any) {
		def.Rules = append(def.Rules, nftRule{Family: def.Family, Table: def.Name, Chain: chain,
			Comment: comment + ":" + label, Expr: rawNFTExpressions(expr...)})
	}
	accept, drop, ret := map[string]any{"accept": nil}, map[string]any{"drop": nil}, map[string]any{"return": nil}
	// Pin name AND ifindex: reuse of a number by an unrelated interface cannot
	// inherit either an ACCEPT or this lease's static deny fence.
	origin := []any{nftMetaMatch("iif", "==", scope.Network.BridgeID), nftMetaMatch("iifname", "==", scope.Network.BridgeName)}
	returning := []any{nftMetaMatch("oif", "==", scope.Network.BridgeID), nftMetaMatch("oifname", "==", scope.Network.BridgeName)}
	add(&inet, "forward", "original", append(slices.Clone(origin),
		forwardingDirection("original"), nftMetaMatch("l4proto", "==", "tcp"),
		forwardingSetMatch("flows", forwardingMeta("oif"), nftPayload("ip", "saddr"), nftPayload("ip", "daddr"), nftPayload("tcp", "dport")),
		nftStates([]string{"new", "established"}), accept)...)
	add(&inet, "forward", "return", append(slices.Clone(returning),
		forwardingDirection("reply"), nftMetaMatch("l4proto", "==", "tcp"),
		forwardingSetMatch("flows", forwardingMeta("iif"), nftPayload("ip", "daddr"), nftPayload("ip", "saddr"), nftPayload("tcp", "sport")),
		nftStates([]string{"established"}), accept)...)
	add(&inet, "forward", "deny-original", append(slices.Clone(origin), drop)...)
	add(&inet, "forward", "deny-return", append(slices.Clone(returning), drop)...)
	// Before bridge learning: unauthorized physical ports cannot move a
	// protected MAC's FDB entry or announce its ARP identity.
	add(&bridge, "source", "other-bridge", nftMetaMatch("ibrname", "!=", scope.Network.BridgeName), ret)
	add(&bridge, "source", "source-ip", nftPayloadMatch("ether", "type", "==", "ip"),
		forwardingSetMatch("sources", forwardingMeta("iif"), nftPayload("ether", "saddr"), nftPayload("ip", "saddr")), accept)
	add(&bridge, "source", "source-arp", nftPayloadMatch("ether", "type", "==", "arp"),
		forwardingSetMatch("arp_sources", forwardingMeta("iif"), nftPayload("ether", "saddr"), nftPayload("arp", "saddr ether"), nftPayload("arp", "saddr ip")), accept)
	add(&bridge, "source", "source-dhcp", nftPayloadMatch("ether", "type", "==", "ip"),
		forwardingSetMatch("source_ports", forwardingMeta("iif"), nftPayload("ether", "saddr")),
		nftPayloadMatch("ip", "saddr", "==", "0.0.0.0"), nftPayloadMatch("ip", "daddr", "==", "255.255.255.255"),
		nftMetaMatch("l4proto", "==", "udp"), nftPayloadMatch("udp", "sport", "==", 68), nftPayloadMatch("udp", "dport", "==", 67), accept)
	add(&bridge, "source", "deny-mac", forwardingSetMatch("macs", nftPayload("ether", "saddr")), drop)
	add(&bridge, "source", "deny-ip", nftPayloadMatch("ether", "type", "==", "ip"), forwardingSetMatch("guests", nftPayload("ip", "saddr")), drop)
	add(&bridge, "source", "deny-arp-mac", nftPayloadMatch("ether", "type", "==", "arp"), forwardingSetMatch("macs", nftPayload("arp", "saddr ether")), drop)
	add(&bridge, "source", "deny-arp-ip", nftPayloadMatch("ether", "type", "==", "arp"), forwardingSetMatch("guests", nftPayload("arp", "saddr ip")), drop)
	add(&bridge, "reply", "other-bridge", nftMetaMatch("obrname", "!=", scope.Network.BridgeName), ret)
	add(&bridge, "reply", "reply-identity", nftPayloadMatch("ether", "type", "==", "ip"), nftMetaMatch("l4proto", "==", "tcp"),
		forwardingSetMatch("replies", forwardingMeta("oif"), nftPayload("ether", "daddr"), nftPayload("ip", "daddr"), nftPayload("ip", "saddr"), nftPayload("tcp", "sport")),
		forwardingDirection("reply"), nftStates([]string{"established"}), accept)
	// Preserve only host-local DNS/DHCP replies from this bridge's own gateway.
	// They are not external forwarding grants or guest-selected host services.
	gateway := netip.MustParsePrefix(scope.Network.BridgeCIDR).Addr().String()
	for _, protocol := range []string{"udp", "tcp"} {
		add(&bridge, "reply", "local-dns-"+protocol, nftMetaMatch("iif", "==", 0), nftPayloadMatch("ether", "type", "==", "ip"),
			nftPayloadMatch("ip", "saddr", "==", gateway), nftMetaMatch("l4proto", "==", protocol), nftPayloadMatch(protocol, "sport", "==", 53),
			forwardingSetMatch("sources", forwardingMeta("oif"), nftPayload("ether", "daddr"), nftPayload("ip", "daddr")), accept)
	}
	add(&bridge, "reply", "local-dhcp", nftMetaMatch("iif", "==", 0), nftPayloadMatch("ether", "type", "==", "ip"),
		nftPayloadMatch("ip", "saddr", "==", gateway), nftMetaMatch("l4proto", "==", "udp"),
		nftPayloadMatch("udp", "sport", "==", 67), nftPayloadMatch("udp", "dport", "==", 68),
		forwardingSetMatch("sources", forwardingMeta("oif"), nftPayload("ether", "daddr"), nftPayload("ip", "daddr")), accept)
	add(&bridge, "reply", "deny-reply", nftPayloadMatch("ether", "type", "==", "ip"), forwardingSetMatch("guests", nftPayload("ip", "daddr")), drop)
	for _, p := range canonicalForwardingInstances(instances) {
		bridge.Sets[0].Values = append(bridge.Sets[0].Values, forwardingConcat(p.HostVethID, p.GuestMAC, p.GuestIPv4))
		bridge.Sets[1].Values = append(bridge.Sets[1].Values, forwardingConcat(p.HostVethID, p.GuestMAC, p.GuestMAC, p.GuestIPv4))
		bridge.Sets[2].Values = append(bridge.Sets[2].Values, forwardingConcat(p.HostVethID, p.GuestMAC))
		bridge.Sets[4].Values = append(bridge.Sets[4].Values, p.GuestIPv4)
		bridge.Sets[5].Values = append(bridge.Sets[5].Values, p.GuestMAC)
		for _, route := range scope.Routes {
			inet.Sets[0].Values = append(inet.Sets[0].Values, forwardingConcat(route.OutputID, p.GuestIPv4, route.Destination, route.Port))
			bridge.Sets[3].Values = append(bridge.Sets[3].Values, forwardingConcat(p.HostVethID, p.GuestMAC, p.GuestIPv4, route.Destination, route.Port))
		}
	}
	return []forwardingNFTDefinition{inet, bridge}, nil
}

func forwardingNFTCreate(scope ForwardingKernelScope) ([]byte, error) {
	definitions, err := forwardingNFTDefinitions(scope, nil)
	if err != nil {
		return nil, err
	}
	commands := []any{}
	for _, d := range definitions {
		// Exclusive creation prevents a colliding foreign table from being
		// treated as ours in the observation-to-write interval.
		commands = append(commands, map[string]any{"create": map[string]any{"table": map[string]any{"family": d.Family, "name": d.Name, "comment": d.Comment}}})
		for _, c := range d.Chains {
			commands = append(commands, map[string]any{"add": map[string]any{"chain": c}})
		}
		for _, set := range d.Sets {
			var types any = set.Types[0]
			if len(set.Types) > 1 {
				types = set.Types
			}
			value := map[string]any{"family": d.Family, "table": d.Name, "name": set.Name, "type": types, "size": 8192}
			if set.Timeout != 0 {
				value["flags"], value["timeout"], value["gc-interval"] = []string{"timeout"}, set.Timeout, 1
			}
			commands = append(commands, map[string]any{"add": map[string]any{"set": value}})
		}
		for _, rule := range d.Rules {
			commands = append(commands, map[string]any{"add": map[string]any{"rule": rule}})
		}
	}
	return json.Marshal(map[string]any{"nftables": commands})
}

// Closing authority retains the deny fence until connections and ownership
// are independently retired. Deleting rules is not proof of full revocation.
func forwardingNFTMembers(scope ForwardingKernelScope, instances []ForwardingInstanceProof, closeOnly bool) ([]byte, error) {
	definitions, err := forwardingNFTDefinitions(scope, instances)
	if err != nil {
		return nil, err
	}
	commands := []any{}
	for _, d := range definitions {
		for _, set := range d.Sets {
			if closeOnly && set.Timeout == 0 {
				continue
			}
			object := map[string]any{"family": d.Family, "table": d.Name, "name": set.Name}
			commands = append(commands, map[string]any{"flush": map[string]any{"set": object}})
			if !closeOnly && len(set.Values) != 0 {
				elements := map[string]any{"family": d.Family, "table": d.Name, "name": set.Name, "elem": set.Values}
				commands = append(commands, map[string]any{"add": map[string]any{"element": elements}})
			}
		}
	}
	return json.Marshal(map[string]any{"nftables": commands})
}

func forwardingIPSetMember(instance ForwardingInstanceProof, route ForwardingRouteProof) string {
	return strings.Join([]string{instance.GuestIPv4, "tcp:" + strconv.Itoa(int(route.Port)), route.Destination}, ",")
}
