package main

import (
	"context"
	"fmt"
	"net/netip"
	"slices"
	"strconv"
	"strings"

	"github.com/anas-project/ANAS/internal/computenet"
)

// A lease bridge carries one Provider-owned network ACL. It is the only place
// a lease's traffic is allowed or refused (INCUS-R-112--R-134):
//
//   - every egress allow rule names the bridge's own subnets as its source, so
//     a guest forging an off-subnet source -- which IPv6 source filtering would
//     stop, but that needs host br_netfilter -- matches nothing and is dropped
//     by the default egress action before masquerade;
//   - the egress tier is a set of allow rules for destinations, plus drop rules
//     for what a wider allow would otherwise cover (Incus evaluates drop rules
//     before allow rules, whatever their order);
//   - the default ingress action is drop, so nothing reaches a guest unless the
//     ingress tier publishes it; replies, DHCP and DNS are allowed by Incus ahead
//     of the ACL.
//
// Traffic between instances on the same bridge is layer 2 and never reaches
// the ACL; intra_lease is the profile NIC's port isolation instead.

const (
	aclEgressDefault  = "drop"
	aclIngressDefault = "drop"

	// leasesAddressSet holds every lease bridge's subnets on the daemon. Each
	// ensure rewrites it, so a lease created later is excluded from every
	// earlier lease at once, without re-ensuring them.
	leasesAddressSet = "anas-leases"
	// traefikAddressSet is written only by hostd from the Traefik containers it
	// reads from Docker (INCUS-R-118, R-119). The Provider names it, never
	// writes it.
	traefikAddressSet = "anas-traefik"

	leasesSetMarker = "compute-lease-subnets"
)

// reservedIPv4 are the ranges an internet destination never falls in:
// "this network", private, shared (CGNAT), loopback, link-local, multicast and
// reserved. publicIPv4 is their complement, because an ACL rule can only allow;
// "everything but" has to be spelled out as what remains.
var reservedIPv4 = []string{"0.0.0.0/8", "10.0.0.0/8", "100.64.0.0/10", "127.0.0.0/8", "169.254.0.0/16",
	"172.16.0.0/12", "192.168.0.0/16", "224.0.0.0/3"}

var publicIPv4 = complementIPv4(reservedIPv4)

// publicIPv6 is global unicast; ULA, link-local and multicast lie outside it.
var publicIPv6 = []string{"2000::/3"}

// privateRanges are where Docker and other host-local networks live. Only the
// internet_lan_host tier reaches them, and on Docker bridges the host's static
// forwarding rules still pass only connections Docker itself translated to a
// published port (INCUS-R-126).
var privateRanges = []string{"10.0.0.0/8", "100.64.0.0/10", "172.16.0.0/12", "192.168.0.0/16", "fc00::/7"}

type aclRule struct {
	Action          string `json:"action"`
	Source          string `json:"source,omitempty"`
	Destination     string `json:"destination,omitempty"`
	Protocol        string `json:"protocol,omitempty"`
	SourcePort      string `json:"source_port,omitempty"`
	DestinationPort string `json:"destination_port,omitempty"`
	ICMPType        string `json:"icmp_type,omitempty"`
	ICMPCode        string `json:"icmp_code,omitempty"`
	Description     string `json:"description,omitempty"`
	State           string `json:"state"`
}

type networkACL struct {
	Name        string            `json:"name,omitempty"`
	Description string            `json:"description,omitempty"`
	Egress      []aclRule         `json:"egress"`
	Ingress     []aclRule         `json:"ingress"`
	Config      map[string]string `json:"config"`
}

type addressSet struct {
	Name        string            `json:"name,omitempty"`
	Description string            `json:"description,omitempty"`
	Addresses   []string          `json:"addresses"`
	Config      map[string]string `json:"config"`
}

// leaseAddressing is what the bridge actually has: its subnets, gateway and
// the slot addresses reserved on it. ensure reports it back to Core.
type leaseAddressing struct {
	Subnets []string // masked, IPv4 first
	Slots   []slotAddress
}

// leaseSubnets returns the bridge's concrete networks. "auto" is replaced by
// the daemon at creation, so a missing concrete IPv4 network is an error; IPv6
// is absent when the lease has it off.
func leaseSubnets(n network, l lease) ([]string, error) {
	var subnets []string
	for _, family := range []string{"ipv4", "ipv6"} {
		value := strings.TrimSpace(n.Config[family+".address"])
		if value == "" || value == "none" {
			if family == "ipv4" || l.NetworkIPv6 {
				return nil, fmt.Errorf("lease network %s has no concrete %s subnet", n.Name, family)
			}
			continue
		}
		if family == "ipv6" && !l.NetworkIPv6 {
			return nil, fmt.Errorf("lease network %s still carries IPv6 while the lease has it off", n.Name)
		}
		prefix, err := netip.ParsePrefix(value)
		if err != nil {
			return nil, fmt.Errorf("lease network %s %s address is not a subnet", n.Name, family)
		}
		subnets = append(subnets, prefix.Masked().String())
	}
	return subnets, nil
}

func rule(action, description string) aclRule {
	return aclRule{Action: action, Description: description, State: "enabled"}
}

// familyFilter keeps the CIDRs a lease can actually send to: IPv6 destinations
// only when the lease has IPv6.
func familyFilter(cidrs []string, ipv6 bool) []string {
	var out []string
	for _, cidr := range cidrs {
		if strings.Contains(cidr, ":") && !ipv6 {
			continue
		}
		out = append(out, cidr)
	}
	return out
}

func portList(ports []uint16) string {
	parts := make([]string, len(ports))
	for i, port := range ports {
		parts[i] = strconv.Itoa(int(port))
	}
	return strings.Join(parts, ",")
}

// desiredLeaseACL is a pure function of the frozen declaration, the host's
// LAN and addresses as Core computed them for this apply, and the bridge's
// actual addressing.
func desiredLeaseACL(l lease, addressing leaseAddressing) networkACL {
	source := strings.Join(addressing.Subnets, ",")
	n := l.Network
	lan := familyFilter(prefixStrings(l.LAN), l.NetworkIPv6)
	host := familyFilter(prefixStrings(l.HostAddresses), l.NetworkIPv6)
	allow := func(destination []string, description string) []aclRule {
		if len(destination) == 0 {
			return nil
		}
		r := rule("allow", description)
		r.Source, r.Destination = source, strings.Join(destination, ",")
		return []aclRule{r}
	}
	drop := func(destination []string, description string) []aclRule {
		if len(destination) == 0 {
			return nil
		}
		r := rule("drop", description)
		r.Destination = strings.Join(destination, ",")
		return []aclRule{r}
	}
	traefik := func(description string) []aclRule {
		r := rule("allow", description)
		r.Source, r.Destination = source, "$"+traefikAddressSet
		r.Protocol, r.DestinationPort = "tcp", strconv.Itoa(int(n.TraefikPort))
		return []aclRule{r}
	}
	public := familyFilter(append(slices.Clone(publicIPv4), publicIPv6...), l.NetworkIPv6)
	leases := drop([]string{"$" + leasesAddressSet}, "ANAS lease: other leases are never reachable")

	var egress []aclRule
	switch n.Egress {
	case computenet.EgressInternet:
		egress = slices.Concat(allow(public, "ANAS lease egress internet: public addresses"),
			drop(lan, "ANAS lease egress internet: not the LAN"), drop(host, "ANAS lease egress internet: not the host"), leases)
	case computenet.EgressInternetLAN:
		egress = slices.Concat(allow(public, "ANAS lease egress internet_lan: public addresses"),
			allow(lan, "ANAS lease egress internet_lan: the LAN"), drop(host, "ANAS lease egress internet_lan: not the host"), leases)
	case computenet.EgressInternetLANHost:
		egress = slices.Concat(allow(public, "ANAS lease egress internet_lan_host: public addresses"),
			allow(lan, "ANAS lease egress internet_lan_host: the LAN"),
			allow(host, "ANAS lease egress internet_lan_host: host addresses"),
			allow(familyFilter(privateRanges, l.NetworkIPv6), "ANAS lease egress internet_lan_host: published Docker ports"), leases)
	case computenet.EgressModulesOnly:
		egress = traefik("ANAS lease egress modules_only: ANAS Modules through Traefik")
	}
	if n.ModuleAccess && (n.Egress == computenet.EgressInternet || n.Egress == computenet.EgressInternetLAN) {
		egress = append(egress, traefik("ANAS lease module_access: ANAS Modules through Traefik")...)
	}

	ingress := []aclRule{}
	if n.Ingress == computenet.IngressPublished {
		other := rule("drop", "ANAS lease ingress: never from a lease")
		other.Source = "$" + leasesAddressSet
		ingress = append(ingress, other)
		if len(n.HTTPPorts) > 0 {
			r := rule("allow", "ANAS lease ingress: Traefik to HTTP publication ports")
			r.Source, r.Protocol, r.DestinationPort = "$"+traefikAddressSet, "tcp", portList(n.HTTPPorts)
			ingress = append(ingress, r)
		}
		for _, slot := range addressing.Slots {
			for _, protocol := range []string{computenet.ProtocolTCP, computenet.ProtocolUDP} {
				var ports []uint16
				for _, binding := range n.Ports {
					if binding.Slot == slot.Name && binding.Protocol == protocol && !slices.Contains(ports, binding.GuestPort) {
						ports = append(ports, binding.GuestPort)
					}
				}
				if len(ports) == 0 {
					continue
				}
				slices.Sort(ports)
				r := rule("allow", "ANAS lease ingress: port bindings to slot "+slot.Name)
				r.Destination, r.Protocol, r.DestinationPort = slot.destinations(), protocol, portList(ports)
				ingress = append(ingress, r)
			}
		}
	}
	if egress == nil {
		egress = []aclRule{}
	}
	return networkACL{
		Description: "ANAS compute lease network policy for " + l.Consumer,
		Egress:      egress,
		Ingress:     ingress,
		Config: map[string]string{
			"user.anas.consumer": l.Consumer,
			"user.anas.sandbox":  l.Sandbox,
			leaseCredentialKey:   l.Credential,
		},
	}
}

// complementIPv4 returns the smallest list of CIDRs covering every IPv4 address
// outside the given ranges.
func complementIPv4(excluded []string) []string {
	var blocked []netip.Prefix
	for _, cidr := range excluded {
		blocked = append(blocked, netip.MustParsePrefix(cidr))
	}
	var out []string
	var walk func(netip.Prefix)
	walk = func(p netip.Prefix) {
		for _, b := range blocked {
			if b.Bits() <= p.Bits() && b.Contains(p.Addr()) {
				return // p lies entirely inside an excluded range
			}
		}
		overlaps := false
		for _, b := range blocked {
			if p.Overlaps(b) {
				overlaps = true
				break
			}
		}
		if !overlaps {
			out = append(out, p.String())
			return
		}
		lower := netip.PrefixFrom(p.Addr(), p.Bits()+1)
		upperAddr := p.Addr().As4()
		bit := p.Bits()
		upperAddr[bit/8] |= 0x80 >> (bit % 8)
		walk(lower)
		walk(netip.PrefixFrom(netip.AddrFrom4(upperAddr), p.Bits()+1))
	}
	walk(netip.MustParsePrefix("0.0.0.0/0"))
	return out
}

func prefixStrings(prefixes []netip.Prefix) []string {
	out := make([]string, len(prefixes))
	for i, p := range prefixes {
		out[i] = p.String()
	}
	return out
}

// ensureNetworkACL writes the ACL before the bridge references it (the daemon
// refuses an unknown ACL name) and returns its name.
func ensureNetworkACL(ctx context.Context, c *client, l lease, addressing leaseAddressing) (string, error) {
	name := leaseNetworkName(l.Sandbox)
	desired := desiredLeaseACL(l, addressing)
	path := "/1.0/network-acls/" + name + "?project=default"
	var current networkACL
	err := c.do(ctx, "GET", path, nil, &current)
	switch {
	case err == nil:
		// A new object type has no pre-marker history to adopt: an ACL of
		// this name that is not this lease's is someone else's.
		if err := verifyACLOwner(current, l); err != nil {
			return "", err
		}
		if err := c.do(ctx, "PUT", path, desired, nil); err != nil {
			return "", err
		}
	case isNotFound(err):
		desired.Name = name
		if err := c.do(ctx, "POST", "/1.0/network-acls?project=default", desired, nil); err != nil {
			return "", err
		}
	default:
		return "", err
	}
	var actual networkACL
	if err := c.do(ctx, "GET", path, nil, &actual); err != nil {
		return "", fmt.Errorf("read back lease network ACL: %w", err)
	}
	if err := verifyACL(actual, l, addressing); err != nil {
		return "", err
	}
	return name, nil
}

func verifyACLOwner(a networkACL, l lease) error {
	if a.Config["user.anas.consumer"] != l.Consumer || a.Config["user.anas.sandbox"] != l.Sandbox || a.Config[leaseCredentialKey] != l.Credential {
		return fmt.Errorf("network ACL %s is not owned by this lease; refusing to adopt or modify it", leaseNetworkName(l.Sandbox))
	}
	return nil
}

// verifyACL requires exactly the desired rules, in any order: a missing rule
// narrows the lease, an extra or changed one widens it (INCUS-R-125, R-134).
func verifyACL(a networkACL, l lease, addressing leaseAddressing) error {
	if err := verifyACLOwner(a, l); err != nil {
		return err
	}
	desired := desiredLeaseACL(l, addressing)
	if !sameRules(a.Egress, desired.Egress) || !sameRules(a.Ingress, desired.Ingress) {
		return fmt.Errorf("network ACL %s does not match the lease's declared network policy", leaseNetworkName(l.Sandbox))
	}
	return nil
}

func sameRules(got, want []aclRule) bool {
	if len(got) != len(want) {
		return false
	}
	key := func(r aclRule) string {
		return strings.Join([]string{r.Action, normalizeSubjects(r.Source), normalizeSubjects(r.Destination), r.Protocol,
			r.SourcePort, normalizeSubjects(r.DestinationPort), r.ICMPType, r.ICMPCode, r.State}, "|")
	}
	a, b := make([]string, len(got)), make([]string, len(want))
	for i := range got {
		a[i] = key(got[i])
	}
	for i := range want {
		b[i] = key(want[i])
	}
	slices.Sort(a)
	slices.Sort(b)
	return slices.Equal(a, b)
}

func normalizeSubjects(value string) string {
	if value == "" {
		return ""
	}
	parts := strings.Split(value, ",")
	for i := range parts {
		parts[i] = strings.TrimSpace(parts[i])
	}
	slices.Sort(parts)
	return strings.Join(parts, ",")
}

// bridgeACLConfig are the keys that attach the ACL to the bridge.
func bridgeACLConfig(acl string) map[string]string {
	return map[string]string{
		"security.acls":                        acl,
		"security.acls.default.egress.action":  aclEgressDefault,
		"security.acls.default.ingress.action": aclIngressDefault,
	}
}

// ensureLeasesAddressSet rewrites the daemon-wide set of lease subnets from
// every marked lease bridge. Every lease's ACL drops traffic to and from it,
// so writing the union (not just this lease's subnets) is what keeps a newer
// lease out of an older one without touching the older ACL.
func ensureLeasesAddressSet(ctx context.Context, c *client) error {
	want, err := allLeaseSubnets(ctx, c)
	if err != nil {
		return err
	}
	path := "/1.0/network-address-sets/" + leasesAddressSet + "?project=default"
	desired := addressSet{
		Description: "ANAS compute: every lease bridge subnet",
		Addresses:   want,
		Config:      map[string]string{"user.anas.managed": leasesSetMarker},
	}
	var current addressSet
	err = c.do(ctx, "GET", path, nil, &current)
	switch {
	case err == nil:
		if current.Config["user.anas.managed"] != leasesSetMarker {
			return fmt.Errorf("network address set %s is not managed by ANAS; refusing to modify it", leasesAddressSet)
		}
		if err := c.do(ctx, "PUT", path, desired, nil); err != nil {
			return err
		}
	case isNotFound(err):
		desired.Name = leasesAddressSet
		if err := c.do(ctx, "POST", "/1.0/network-address-sets?project=default", desired, nil); err != nil {
			return err
		}
	default:
		return err
	}
	return verifyLeasesAddressSet(ctx, c)
}

// verifyLeasesAddressSet requires the set to cover every current lease
// subnet. Covering more is harmless: a subnet whose bridge is gone has no
// guests to protect or to block.
func verifyLeasesAddressSet(ctx context.Context, c *client) error {
	covers, err := leasesAddressSetCovers(ctx, c)
	if err != nil {
		return err
	}
	if !covers {
		return fmt.Errorf("network address set %s is missing, not managed by ANAS or does not cover every lease subnet", leasesAddressSet)
	}
	return nil
}

// leasesAddressSetCovers is the read-only check inspect uses: drift is a
// false answer, only an unreadable daemon is an error.
func leasesAddressSetCovers(ctx context.Context, c *client) (bool, error) {
	want, err := allLeaseSubnets(ctx, c)
	if err != nil {
		return false, err
	}
	var current addressSet
	err = c.do(ctx, "GET", "/1.0/network-address-sets/"+leasesAddressSet+"?project=default", nil, &current)
	if isNotFound(err) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("read the lease subnet address set: %w", err)
	}
	if current.Config["user.anas.managed"] != leasesSetMarker {
		return false, nil
	}
	have := map[string]bool{}
	for _, address := range current.Addresses {
		if prefix, err := netip.ParsePrefix(strings.TrimSpace(address)); err == nil {
			have[prefix.Masked().String()] = true
		}
	}
	for _, subnet := range want {
		if !have[subnet] {
			return false, nil
		}
	}
	return true, nil
}

func allLeaseSubnets(ctx context.Context, c *client) ([]string, error) {
	var networks []network
	if err := c.do(ctx, "GET", "/1.0/networks?project=default&recursion=1", nil, &networks); err != nil {
		return nil, fmt.Errorf("list lease networks: %w", err)
	}
	var subnets []string
	for _, n := range networks {
		if n.Type != "bridge" || n.Config["user.anas.consumer"] == "" || n.Config["user.anas.sandbox"] == "" {
			continue
		}
		for _, family := range []string{"ipv4", "ipv6"} {
			if prefix, err := netip.ParsePrefix(strings.TrimSpace(n.Config[family+".address"])); err == nil {
				subnets = append(subnets, prefix.Masked().String())
			}
		}
	}
	slices.Sort(subnets)
	return slices.Compact(subnets), nil
}

// traefikAddressSetPresent reports whether hostd has created the Traefik set.
// The Provider never creates it: an empty placeholder written here would look
// like hostd's and silently route nothing (INCUS-R-119).
func traefikAddressSetPresent(ctx context.Context, c *client) (bool, error) {
	var current addressSet
	err := c.do(ctx, "GET", "/1.0/network-address-sets/"+traefikAddressSet+"?project=default", nil, &current)
	if isNotFound(err) {
		return false, nil
	}
	return err == nil, err
}
