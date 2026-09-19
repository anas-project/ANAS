package incusingresshost

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/netip"
	"strconv"
	"strings"
)

func (b *Backend) routeAddArgv(target Target) []string {
	return []string{"-n", b.config.RouteNetNS, "-4", "route", "add", "table", strconv.FormatUint(uint64(b.config.RouteTable), 10), target.GuestIP + "/32", "via", b.config.IngressGateway, "dev", b.config.TraefikInterface, "src", b.config.TraefikSourceIP, "proto", strconv.FormatUint(uint64(b.config.RouteProtocol), 10)}
}

func (b *Backend) routeDeleteArgv(target Target) []string {
	return []string{"-n", b.config.RouteNetNS, "-4", "route", "del", "table", strconv.FormatUint(uint64(b.config.RouteTable), 10), target.GuestIP + "/32", "via", b.config.IngressGateway, "dev", b.config.TraefikInterface, "proto", strconv.FormatUint(uint64(b.config.RouteProtocol), 10)}
}

func (b *Backend) conntrackListArgv(target Target) []string {
	return []string{"-L", "-f", "ipv4", "-o", "extended", "-p", "tcp", "--orig-src", b.config.TraefikSourceIP, "--orig-dst", target.GuestIP, "--dport", strconv.FormatUint(uint64(target.GuestPort), 10)}
}

func (b *Backend) conntrackScopeListArgv() []string {
	return []string{"-L", "-f", "ipv4", "-o", "extended", "-p", "tcp", "--orig-src", b.config.TraefikSourceIP, "--orig-dst", b.config.GuestSubnet}
}

func (b *Backend) verifyRouteNamespace(ctx context.Context) error {
	if !b.config.fixture {
		return b.verifyNativeRouteNamespace(ctx)
	}
	return b.verifyFixtureRouteNamespace(ctx)
}

// These extended fields belong exclusively to the private fault harness.
// Real iproute2 output is verified by verifyNativeRouteNamespace instead.
func (b *Backend) verifyFixtureRouteNamespace(ctx context.Context) error {
	body, err := b.runner.output(ctx, b.config.Binaries.IP, []string{"-j", "-n", b.config.RouteNetNS, "link", "show", "dev", b.config.TraefikInterface})
	if err != nil {
		return fmt.Errorf("verify Traefik route namespace: %w", err)
	}
	var links []struct {
		IfName            string `json:"ifname"`
		IfIndex           uint32 `json:"ifindex"`
		Address           string `json:"address"`
		NetNSCookie       uint64 `json:"netns_cookie"`
		NetNSDevice       uint64 `json:"netns_device"`
		NetNSInode        uint64 `json:"netns_inode"`
		DockerContainerID string `json:"docker_container_id"`
		DockerStartedAt   string `json:"docker_started_at"`
		PeerIfIndex       uint32 `json:"peer_ifindex"`
		PeerMAC           string `json:"peer_mac"`
		AddrInfo          []struct {
			Family    string `json:"family"`
			Local     string `json:"local"`
			PrefixLen int    `json:"prefixlen"`
		} `json:"addr_info"`
	}
	if err := decodeObservedJSON(body, &links); err != nil || len(links) != 1 {
		return fmt.Errorf("Traefik route namespace/interface identity is not verified")
	}
	link := links[0]
	pin := b.config.Namespace
	if link.IfName != b.config.TraefikInterface || link.IfIndex != pin.TraefikIfIndex || link.Address != pin.TraefikMAC || link.NetNSCookie != pin.NetNSCookie || link.NetNSDevice != pin.NetNSDevice || link.NetNSInode != pin.NetNSInode || link.DockerContainerID != pin.DockerContainerID || link.DockerStartedAt != pin.DockerStartedAt || link.PeerIfIndex != pin.HostVethPeerIfIndex || link.PeerMAC != pin.HostVethMAC {
		return fmt.Errorf("Traefik route namespace/interface identity is not verified")
	}
	sourceOK := false
	gatewayOK := false
	for _, address := range link.AddrInfo {
		if address.Family != "inet" {
			continue
		}
		if address.Local == b.config.TraefikSourceIP && address.PrefixLen > 0 && address.PrefixLen <= 32 {
			sourceOK = true
		}
		if address.Local == b.config.IngressGateway && address.PrefixLen > 0 && address.PrefixLen <= 32 {
			gatewayOK = true
		}
	}
	if !sourceOK || !gatewayOK {
		return fmt.Errorf("Traefik route namespace source/gateway addresses are not pinned")
	}
	return ctx.Err()
}

func (b *Backend) withVerifiedRouteNamespace(ctx context.Context, effect func() error) error {
	if err := b.verifyRouteNamespace(ctx); err != nil {
		return err
	}
	if err := effect(); err != nil {
		return err
	}
	return b.verifyRouteNamespace(ctx)
}

func (b *Backend) ensureGuestRouteEffect(ctx context.Context, target Target) error {
	if err := b.verifyRouteNamespace(ctx); err != nil {
		return err
	}
	routes, err := b.routesForTarget(ctx, target)
	if err != nil {
		return err
	}
	switch len(routes) {
	case 0:
		return b.withVerifiedRouteNamespace(ctx, func() error {
			return b.runner.run(ctx, b.config.Binaries.IP, b.routeAddArgv(target), nil)
		})
	case 1:
		if routes[0].ownedBy(b, target) {
			return b.verifyRouteNamespace(ctx)
		}
		return fmt.Errorf("refuse to overwrite a foreign guest route")
	default:
		return fmt.Errorf("multiple guest routes exist for one publication target")
	}
}

func (b *Backend) confirmRoute(ctx context.Context, target Target, present bool) error {
	matches, err := b.routeMatchCount(ctx, target)
	if err != nil {
		return err
	}
	if present && matches != 1 {
		return fmt.Errorf("owned guest /32 route was not confirmed")
	}
	if !present && matches != 0 {
		return fmt.Errorf("owned guest /32 route remains after removal")
	}
	return nil
}

func (b *Backend) routeMatchCount(ctx context.Context, target Target) (int, error) {
	routes, err := b.routesForTarget(ctx, target)
	if err != nil {
		return 0, err
	}
	matches := 0
	for _, route := range routes {
		if route.ownedBy(b, target) {
			matches++
		}
	}
	return matches, nil
}

func (b *Backend) routesForTarget(ctx context.Context, target Target) ([]routeRecord, error) {
	body, err := b.runner.output(ctx, b.config.Binaries.IP, []string{"-j", "-n", b.config.RouteNetNS, "-4", "route", "show", "table", strconv.FormatUint(uint64(b.config.RouteTable), 10), target.GuestIP + "/32"})
	if err != nil {
		return nil, fmt.Errorf("read back owned guest /32 route: %w", err)
	}
	routes, err := parseRoutes(body)
	if err != nil {
		return nil, err
	}
	return routes, nil
}

func (b *Backend) permitCreateScript(ctx context.Context, target Target) (string, error) {
	inv, err := b.nftInventory(ctx)
	if err != nil {
		return "", err
	}
	set := permitSetName(target)
	tuple := target.GuestIP + " . " + strconv.FormatUint(uint64(target.GuestPort), 10)
	comment := permitComment(target)
	specs := b.permitRuleSpecs(target)
	if existing, ok := inv.Sets[set]; ok && !existing.compatible(target) {
		return "", fmt.Errorf("owned HTTP permit set exists with unexpected type or tuple")
	}
	seenRules := map[string]bool{}
	for _, rule := range inv.Rules {
		if rule.Comment != comment && rule.Comment != comment+":reply" {
			continue
		}
		if !rule.matches(specs[rule.Comment]) {
			return "", fmt.Errorf("owned HTTP permit rule identity mismatch")
		}
		if seenRules[rule.Comment] {
			return "", fmt.Errorf("duplicate owned HTTP permit rule")
		}
		seenRules[rule.Comment] = true
	}
	var builder strings.Builder
	if _, ok := inv.Sets[set]; !ok {
		fmt.Fprintf(&builder, "add set inet %s %s { type ipv4_addr . inet_service; flags timeout; timeout %s; }\n", b.config.PermitTable, set, b.ttlSeconds())
	}
	fmt.Fprintf(&builder, "flush set inet %s %s\n", b.config.PermitTable, set)
	fmt.Fprintf(&builder, "add element inet %s %s { %s timeout %s }\n", b.config.PermitTable, set, tuple, b.ttlSeconds())
	if !seenRules[comment] {
		fmt.Fprintf(&builder, "add rule inet %s http_permits iifname %s oifname %s meta l4proto tcp ip saddr %s ip daddr . tcp dport @%s ct direction original ct state == { new, established } accept comment %s\n", b.config.PermitTable, shellQuote(b.config.IngressBridge), shellQuote(specs[comment].OIF), b.config.TraefikSourceIP, set, shellQuote(comment))
	}
	if !seenRules[comment+":reply"] {
		fmt.Fprintf(&builder, "add rule inet %s http_permits iifname %s oifname %s meta l4proto tcp ip daddr %s ip saddr . tcp sport @%s ct direction reply ct state == established accept comment %s\n", b.config.PermitTable, shellQuote(b.config.GuestBridge), shellQuote(b.config.IngressBridge), b.config.TraefikSourceIP, set, shellQuote(comment+":reply"))
	}
	reply, err := b.replyOriginScript(ctx, target, false)
	if err != nil {
		return "", err
	}
	return reply + builder.String(), nil
}

func (b *Backend) permitRemoveScript(ctx context.Context, target Target) (string, error) {
	inv, err := b.nftInventory(ctx)
	if err != nil {
		return "", err
	}
	comment := permitComment(target)
	specs := b.permitRuleSpecs(target)
	var builder strings.Builder
	for _, rule := range inv.Rules {
		if rule.Comment == comment || rule.Comment == comment+":reply" {
			if rule.Handle == "" || !rule.matches(specs[rule.Comment]) {
				return "", fmt.Errorf("owned HTTP permit rule identity mismatch before removal")
			}
			fmt.Fprintf(&builder, "delete rule inet %s http_permits handle %s\n", b.config.PermitTable, rule.Handle)
		}
	}
	if set, ok := inv.Sets[permitSetName(target)]; ok {
		if !set.compatible(target) {
			return "", fmt.Errorf("owned HTTP permit set identity mismatch before removal")
		}
		fmt.Fprintf(&builder, "delete set inet %s %s\n", b.config.PermitTable, permitSetName(target))
	}
	reply, err := b.replyOriginScript(ctx, target, true)
	if err != nil {
		return "", err
	}
	if builder.Len() == 0 && reply == "" {
		return "# owned HTTP permit already absent\n", nil
	}
	return reply + builder.String(), nil
}

func (b *Backend) verifyBaseline(ctx context.Context) error {
	owned, err := b.loadBaselineReceipt()
	if err != nil || owned.State != "installed" {
		return fmt.Errorf("nft baseline lacks installed ownership evidence")
	}

	inetBody, err := b.runner.output(ctx, b.config.Binaries.NFT, []string{"-j", "list", "table", "inet", b.config.PermitTable})
	if err != nil {
		return fmt.Errorf("verify nft inet baseline: %w", err)
	}
	bridgeBody, err := b.runner.output(ctx, b.config.Binaries.NFT, []string{"-j", "list", "table", "bridge", b.config.OriginTable})
	if err != nil {
		return fmt.Errorf("verify nft bridge baseline: %w", err)
	}
	inet, err := parseNFTTable(inetBody)
	if err != nil {
		return err
	}
	bridge, err := parseNFTTable(bridgeBody)
	if err != nil {
		return err
	}
	if inet.Handle != owned.InetHandle || bridge.Handle != owned.BridgeHandle {
		return fmt.Errorf("owned nft table was replaced")
	}
	if err := b.validateNFTBaseline(inet, "inet"); err != nil {
		return err
	}
	if err := b.validateNFTBaseline(bridge, "bridge"); err != nil {
		return err
	}
	return nil
}

func (b *Backend) confirmPermit(ctx context.Context, target Target, present bool) error {
	inv, err := b.nftInventory(ctx)
	if err != nil {
		return err
	}
	set := permitSetName(target)
	comment := permitComment(target)
	setPresent := false
	if ownedSet, ok := inv.Sets[set]; ok && ownedSet.compatible(target) {
		setPresent = true
		if present && !ownedSet.live(target) {
			return fmt.Errorf("owned HTTP permit tuple is absent or expired")
		}
	} else if ok {
		return fmt.Errorf("owned HTTP permit set identity mismatch")
	}
	rules := 0
	specs := b.permitRuleSpecs(target)
	for _, rule := range inv.Rules {
		if rule.Comment == comment || rule.Comment == comment+":reply" {
			if !rule.matches(specs[rule.Comment]) {
				return fmt.Errorf("owned HTTP permit rule identity mismatch")
			}
			rules++
		}
	}
	if present && (!setPresent || rules != 2) {
		return fmt.Errorf("owned HTTP permit was not confirmed")
	}
	if !present && (setPresent || rules != 0) {
		return fmt.Errorf("owned HTTP permit remains after removal")
	}
	return b.confirmReplyOrigin(ctx, target, present)
}

type routeRecord struct {
	Dst      string
	Gateway  string
	Dev      string
	PrefSrc  string
	Protocol string
}

func parseRoutes(body []byte) ([]routeRecord, error) {
	var raw []map[string]any
	if err := decodeObservedJSON(body, &raw); err != nil {
		return nil, fmt.Errorf("route readback is not JSON")
	}
	result := make([]routeRecord, 0, len(raw))
	for _, item := range raw {
		dst, err := canonicalRouteDestination(stringField(item, "dst"))
		if err != nil {
			return nil, err
		}
		proto, err := canonicalRouteProtocol(item["protocol"])
		if err != nil {
			return nil, err
		}
		result = append(result, routeRecord{Dst: dst, Gateway: stringField(item, "gateway"), Dev: stringField(item, "dev"), PrefSrc: stringField(item, "prefsrc"), Protocol: proto})
	}
	return result, nil
}

func (r routeRecord) ownedBy(b *Backend, target Target) bool {
	return r.Dst == target.GuestIP+"/32" && r.Gateway == b.config.IngressGateway && r.Dev == b.config.TraefikInterface && r.PrefSrc == b.config.TraefikSourceIP && r.Protocol == strconv.FormatUint(uint64(b.config.RouteProtocol), 10)
}

func canonicalRouteDestination(value string) (string, error) {
	if value == "" || value == "default" {
		return "", fmt.Errorf("route readback has no concrete IPv4 destination")
	}
	if address, err := netip.ParseAddr(value); err == nil {
		if !address.Is4() {
			return "", fmt.Errorf("route readback destination is not IPv4")
		}
		return address.String() + "/32", nil
	}
	prefix, err := netip.ParsePrefix(value)
	if err != nil || !prefix.Addr().Is4() || prefix.String() != value {
		return "", fmt.Errorf("route readback destination is not canonical IPv4")
	}
	if prefix.Bits() != 32 {
		return "", fmt.Errorf("route readback destination is not a host route")
	}
	return prefix.String(), nil
}

func canonicalRouteProtocol(value any) (string, error) {
	switch protocol := value.(type) {
	case nil:
		return "", nil
	case json.Number:
		number, err := strconv.ParseUint(protocol.String(), 10, 8)
		if err != nil {
			return "", fmt.Errorf("route readback protocol is out of range")
		}
		return strconv.FormatUint(number, 10), nil
	case string:
		if protocol == "" {
			return "", nil
		}
		number, err := strconv.ParseUint(protocol, 10, 8)
		if err != nil {
			return "", fmt.Errorf("route readback protocol must be numeric")
		}
		return strconv.FormatUint(number, 10), nil
	default:
		return "", fmt.Errorf("route readback protocol has unexpected type")
	}
}

func numberField(m map[string]any, key string) string {
	switch value := m[key].(type) {
	case json.Number:
		number, err := strconv.ParseUint(value.String(), 10, 64)
		if err != nil {
			return ""
		}
		return strconv.FormatUint(number, 10)
	case string:
		if _, err := strconv.ParseUint(value, 10, 64); err != nil {
			return ""
		}
		return value
	default:
		return ""
	}
}

func stringField(m map[string]any, key string) string {
	value, _ := m[key].(string)
	return value
}

func decodeObservedJSON(body []byte, out any) error {
	if len(body) == 0 || len(body) > 2<<20 || jsonDepth(body) > 64 || jsonElements(body) > 16384 {
		return fmt.Errorf("observed JSON is outside bounded schema")
	}
	if err := validateObservedJSONKeys(body, out); err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(out); err != nil {
		return err
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		return fmt.Errorf("observed JSON has trailing data")
	}
	return nil
}

func jsonDepth(body []byte) int {
	depth, maxDepth := 0, 0
	inString, escape := false, false
	for _, c := range body {
		if inString {
			if escape {
				escape = false
				continue
			}
			if c == '\\' {
				escape = true
			} else if c == '"' {
				inString = false
			}
			continue
		}
		switch c {
		case '"':
			inString = true
		case '{', '[':
			depth++
			if depth > maxDepth {
				maxDepth = depth
			}
		case '}', ']':
			depth--
		}
	}
	return maxDepth
}

func jsonElements(body []byte) int {
	count := 0
	inString, escape := false, false
	for _, c := range body {
		if inString {
			if escape {
				escape = false
				continue
			}
			if c == '\\' {
				escape = true
			} else if c == '"' {
				inString = false
			}
			continue
		}
		switch c {
		case '"':
			inString = true
		case ':', ',':
			count++
		}
	}
	return count
}
