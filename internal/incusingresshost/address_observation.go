package incusingresshost

import (
	"context"
	"encoding/json"
	"fmt"
	"net/netip"
	"slices"
	"strconv"
	"strings"
)

func (g *addressRouter) json(ctx context.Context, args ...string) ([]map[string]any, error) {
	body, err := g.commands.output(ctx, g.b.config.Binaries.IP, args)
	if err != nil {
		return nil, err
	}
	var values []map[string]any
	if decodeObservedJSON(body, &values) != nil || values == nil || len(values) > 4096 {
		return nil, fmt.Errorf("address routing observation is incomplete")
	}
	return values, nil
}

func allowedAddressKeys(value map[string]any, allowed ...string) bool {
	for key := range value {
		if !slices.Contains(allowed, key) {
			return false
		}
	}
	return true
}

func emptyAddressFlags(value map[string]any) bool {
	v, ok := value["flags"]
	if !ok {
		return true
	}
	flags, ok := v.([]any)
	return ok && len(flags) == 0
}

func addressTable(value map[string]any) string {
	if _, ok := value["table"]; !ok {
		return "254"
	} // ip omits the main table.
	switch stringField(value, "table") {
	case "local":
		return "255"
	case "main":
		return "254"
	case "default":
		return "253"
	}
	return numberField(value, "table")
}

// iproute2 can emit CIDR text or an address plus src/dstlen. Both must
// describe the exact canonical IPv4 prefix. Never ignore a separate length,
// accept two conflicting spellings, or silently mask a noncanonical address.
func addressRulePrefix(rule map[string]any, field string) (netip.Prefix, error) {
	invalid := func() (netip.Prefix, error) { return netip.Prefix{}, fmt.Errorf("invalid address routing prefix") }
	text, ok := rule[field].(string)
	if !ok || (field != "src" && field != "dst") {
		return invalid()
	}
	length, separate := rule[field+"len"]
	if strings.Contains(text, "/") {
		prefix, err := netip.ParsePrefix(text)
		if separate || err != nil || !prefix.Addr().Is4() || prefix != prefix.Masked() {
			return invalid()
		}
		return prefix, nil
	}
	address, err := netip.ParseAddr(text)
	if err != nil || !address.Is4() {
		return invalid()
	}
	bits := 32
	if separate {
		number, ok := length.(json.Number)
		if !ok {
			return invalid()
		}
		bits, err = strconv.Atoi(number.String())
		if err != nil || bits < 0 || bits > 32 || strconv.Itoa(bits) != number.String() {
			return invalid()
		}
	}
	prefix := netip.PrefixFrom(address, bits)
	if prefix != prefix.Masked() {
		return invalid()
	}
	return prefix, nil
}

func (g *addressRouter) rules(ctx context.Context, expect bool) error {
	rules, err := g.json(ctx, "-j", "-N", "-4", "rule", "show")
	if err != nil {
		return err
	}
	c := g.b.config
	want := c.AddressRouting
	lookup, deny, local := 0, 0, 0
	for _, rule := range rules {
		priority, err := strconv.ParseUint(numberField(rule, "priority"), 10, 32)
		if err != nil {
			return fmt.Errorf("unbounded or missing routing policy priority")
		}
		// First iteration is deliberately conservative about policy routing:
		// unknown earlier rules could bypass the terminal fence. Do not delete
		// them or silently choose a stronger priority.
		if priority < uint64(want.Priority) {
			if priority != 0 || addressTable(rule) != "255" || stringField(rule, "src") != "all" || !allowedAddressKeys(rule, "priority", "src", "table", "protocol") {
				return fmt.Errorf("earlier routing policy requires independent compatibility validation")
			}
			local++
			continue
		}
		if priority != uint64(want.Priority) && priority != uint64(want.Priority+1) {
			if addressTable(rule) == strconv.FormatUint(uint64(want.Table), 10) {
				return fmt.Errorf("foreign policy references isolated address table")
			}
			continue
		}
		if !expect {
			return fmt.Errorf("address routing priority is already in use")
		}
		src, sourceErr := addressRulePrefix(rule, "src")
		dst, destinationErr := addressRulePrefix(rule, "dst")
		if !allowedAddressKeys(rule, "priority", "src", "srclen", "dst", "dstlen", "iif", "table", "action", "protocol") ||
			sourceErr != nil || destinationErr != nil || src.String() != c.TraefikSourceIP+"/32" || dst.String() != c.GuestSubnet ||
			stringField(rule, "iif") != c.IngressBridge || numberField(rule, "protocol") != strconv.Itoa(int(c.RouteProtocol)) {
			return fmt.Errorf("address routing selector or ownership changed")
		}
		if priority == uint64(want.Priority) {
			if addressTable(rule) != strconv.FormatUint(uint64(want.Table), 10) || rule["action"] != nil {
				return fmt.Errorf("address lookup action changed")
			}
			lookup++
		} else {
			if _, exists := rule["table"]; exists {
				return fmt.Errorf("terminal address rule gained a lookup table")
			}
			action := stringField(rule, "action")
			if action != "unreachable" && action != "7" {
				return fmt.Errorf("address terminal rule is not unreachable")
			}
			deny++
		}
	}
	if local != 1 || expect && (lookup != 1 || deny != 1) {
		return fmt.Errorf("address routing policy is missing or ambiguous")
	}
	return nil
}

func (g *addressRouter) table(ctx context.Context) ([]map[string]any, error) {
	all, err := g.json(ctx, "-j", "-N", "-4", "route", "show", "table", "all")
	if err != nil {
		return nil, err
	}
	table := strconv.FormatUint(uint64(g.b.config.AddressRouting.Table), 10)
	selected := []map[string]any{}
	for _, route := range all {
		if addressTable(route) == "" {
			return nil, fmt.Errorf("route table identity is ambiguous")
		}
		if addressTable(route) == table {
			selected = append(selected, route)
		}
	}
	return selected, nil
}

func (g *addressRouter) absent(ctx context.Context) error {
	if err := g.rules(ctx, false); err != nil {
		return err
	}
	routes, err := g.table(ctx)
	if err != nil {
		return err
	}
	if len(routes) != 0 {
		return fmt.Errorf("isolated address table is not empty")
	}
	return nil
}

func (g *addressRouter) observe(ctx context.Context, state addressRoutingState, requireHolds bool) (map[string]bool, error) {
	if err := g.rules(ctx, true); err != nil {
		return nil, err
	}
	routes, err := g.table(ctx)
	if err != nil {
		return nil, err
	}
	holds := map[string]addressRoutingHold{}
	for _, hold := range state.Holds {
		if requireHolds && hold.State != "held" {
			return nil, fmt.Errorf("address routing has an unfinished hold")
		}
		holds[hold.Target.GuestIP] = hold
	}
	fallback := 0
	found := map[string]bool{}
	for _, route := range routes {
		if !allowedAddressKeys(route, "type", "dst", "dev", "table", "protocol", "scope", "metric", "flags") || !emptyAddressFlags(route) || numberField(route, "protocol") != strconv.Itoa(int(g.b.config.RouteProtocol)) {
			return nil, fmt.Errorf("isolated address table contains an unknown route or modifier")
		}
		dst, kind, scope := stringField(route, "dst"), stringField(route, "type"), stringField(route, "scope")
		if dst == "default" {
			if (kind != "unreachable" && kind != "7") || numberField(route, "metric") != "42760" || route["dev"] != nil || (scope != "" && scope != "global" && scope != "0") {
				return nil, fmt.Errorf("address table lost its terminal unreachable route")
			}
			fallback++
			continue
		}
		canonical, err := canonicalRouteDestination(dst)
		if err != nil {
			return nil, err
		}
		prefix, _ := netip.ParsePrefix(canonical)
		ip := prefix.Addr().String()
		hold, ok := holds[ip]
		if !ok || found[ip] || (kind != "" && kind != "unicast" && kind != "1") || stringField(route, "dev") != hold.Target.HostVethName || (scope != "link" && scope != "253") || route["metric"] != nil {
			return nil, fmt.Errorf("address route lacks exact device-bound ownership")
		}
		found[ip] = true
	}
	if fallback != 1 {
		return nil, fmt.Errorf("address fallback is absent or duplicated")
	}
	if requireHolds && len(found) != len(holds) {
		return nil, fmt.Errorf("a held device route disappeared; it must not be recreated")
	}
	return found, nil
}

func (g *addressRouter) link(ctx context.Context, target Target) (uint32, error) {
	links, err := g.json(ctx, "-j", "-d", "link", "show", "dev", target.HostVethName)
	if err != nil || len(links) != 1 {
		return 0, fmt.Errorf("guest device identity is unavailable")
	}
	body, _ := json.Marshal(links)
	link, err := nativeLink(body, target.HostVethName, "veth")
	if err != nil {
		return 0, err
	}
	if stringField(link, "address") != target.HostVethMAC || stringField(link, "master") != g.b.config.GuestBridge || numberField(link, "link_index") != strconv.FormatUint(uint64(target.HostVethPeerIfIndex), 10) {
		return 0, fmt.Errorf("guest device no longer matches the independent Incus observation")
	}
	index, err := strconv.ParseUint(numberField(link, "ifindex"), 10, 32)
	if err != nil || index == 0 {
		return 0, fmt.Errorf("invalid guest device index")
	}
	return uint32(index), nil
}

func (g *addressRouter) findLink(ctx context.Context, name string) (uint32, bool, error) {
	links, err := g.json(ctx, "-j", "link", "show")
	if err != nil {
		return 0, false, err
	}
	var result uint32
	for _, link := range links {
		if stringField(link, "ifname") != name {
			continue
		}
		index, err := strconv.ParseUint(numberField(link, "ifindex"), 10, 32)
		if err != nil || index == 0 || result != 0 {
			return 0, false, fmt.Errorf("ambiguous guest device inventory")
		}
		result = uint32(index)
	}
	return result, result != 0, nil
}

func (g *addressRouter) neighbour(ctx context.Context, hold addressRoutingHold) (bool, error) {
	all, err := g.json(ctx, "-j", "-4", "neigh", "show")
	if err != nil {
		return false, err
	}
	values := []map[string]any{}
	for _, value := range all {
		if stringField(value, "dev") == "" || stringField(value, "dst") == "" {
			return false, fmt.Errorf("neighbour inventory lacks interface identity")
		}
		if stringField(value, "dev") == hold.Target.HostVethName && stringField(value, "dst") == hold.Target.GuestIP {
			values = append(values, value)
		}
	}
	if len(values) == 0 {
		return false, nil
	}
	if len(values) != 1 {
		return false, fmt.Errorf("ambiguous address neighbour")
	}
	n := values[0]
	states, ok := n["state"].([]any)
	if !allowedAddressKeys(n, "dst", "dev", "lladdr", "state", "flags") || stringField(n, "dst") != hold.Target.GuestIP || stringField(n, "dev") != hold.Target.HostVethName || stringField(n, "lladdr") != hold.Target.NICMAC || !ok || len(states) != 1 || states[0] != "PERMANENT" || !emptyAddressFlags(n) {
		return false, fmt.Errorf("address neighbour identity or permanence changed")
	}
	return true, nil
}

func (g *addressRouter) verifyHold(ctx context.Context, hold addressRoutingHold) error {
	if err := g.rejectLocalDestination(ctx, hold.Target.GuestIP); err != nil {
		return err
	}
	index, err := g.link(ctx, hold.Target)
	if err != nil || index != hold.IfIndex {
		return fmt.Errorf("held device incarnation changed")
	}
	found, err := g.neighbour(ctx, hold)
	if err != nil || !found {
		return fmt.Errorf("held neighbour is unavailable")
	}
	return nil
}

func (g *addressRouter) rejectLocalDestination(ctx context.Context, ip string) error {
	routes, err := g.json(ctx, "-j", "-N", "-4", "route", "show", "table", "all")
	if err != nil {
		return err
	}
	address := netip.MustParseAddr(ip)
	for _, route := range routes {
		if addressTable(route) != "255" {
			continue
		}
		dst := stringField(route, "dst")
		if single, err := netip.ParseAddr(dst); err == nil {
			if single == address {
				return fmt.Errorf("guest destination is a host-local route")
			}
			continue
		}
		prefix, err := netip.ParsePrefix(dst)
		if err != nil || prefix.Contains(address) {
			return fmt.Errorf("guest destination may be handled by the host local routing table")
		}
	}
	return nil
}
