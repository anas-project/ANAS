package runner

import (
	"encoding/binary"
	"fmt"
	"net"
	"os"
	"os/exec"
	"strconv"
	"strings"
)

// Host network discovery and macvlan address planning.
//
// These answers describe the machine anas runs on, not any service it starts,
// and their consumers are spread across the deployment: seven modules read the
// host address or interface, and the macvlan plan is read by the runner's own
// ensureMacvlan. They used to be produced by a hook inside the "core" module,
// which put the computation on the far side of a boundary from the gate that
// decides whether it is needed -- see applyHostNetwork for what that cost.

// applyHostNetwork fills in the deployment's view of its host. It runs after
// applyModuleDefaults, so a configured value always wins over a probed one,
// and before calculate, so every module hook sees a settled host environment.
//
// Every key it publishes is owned globally rather than by a module: these are
// statements about the machine, and a module that reads HOST_IP is not thereby
// depending on whoever discovered it.
func (a *app) applyHostNetwork() error {
	if a.env == nil {
		a.env = map[string]string{}
	}
	// Assigned rather than defaulted: a configured server name is normalized
	// too, because the NetBIOS and Kerberos consumers downstream compare it
	// case-sensitively against an upper-case name.
	if name := serverName(a.env["SERVER_NAME"]); name != "" {
		a.env["SERVER_NAME"] = name
		a.setEnvOwner("SERVER_NAME", globalScope)
	}
	if a.env["HOST_DNS_SERVER"] == "" {
		a.setHostEnv("HOST_DNS_SERVER", defaultString(resolvNameservers(), a.env["DNS_SERVER"]))
	}
	if a.env["HOST_IP"] == "" || a.env["INTERFACE"] == "" || a.env["HOST_SUBNET_MASK"] == "" {
		host, err := detectHostNetwork(a.env["HOST_IP"])
		if err != nil {
			return err
		}
		a.setHostEnv("HOST_IP", host.IP.String())
		a.setHostEnv("INTERFACE", host.Interface)
		a.setHostEnv("DEFAULT_GATEWAY_IP", host.Gateway)
		a.setHostEnv("HOST_SUBNET_MASK", strconv.Itoa(host.Prefix))
	}
	if a.env["INTERFACE"] == "" || a.env["HOST_IP"] == "" || a.env["HOST_SUBNET_MASK"] == "" {
		return fmt.Errorf("could not detect host interface, host ip, or subnet mask")
	}
	// The macvlan plan is only computed when a module actually attaches to the
	// host LAN. Carving an address pool refuses any host prefix narrower than a
	// /28, which used to fail `render` outright on a /30 or /32 host -- an
	// ordinary VPS -- even when no module wanted a bridge. The gate that governs
	// ensureMacvlan now also governs the calculation feeding it, which is only
	// possible with both on the same side of the boundary. A deployment that
	// states its addresses carves no pool at all and so has no width to fail on;
	// see applyMacvlanPlan.
	if a.hostLANRequired() {
		if err := a.applyMacvlanPlan(); err != nil {
			return err
		}
	}
	a.detectHostIPv6()
	a.setHostEnv("LOCAL_DNS_SERVER", a.env["HOST_IP"])
	// A configured host address is still the host's address: ownership has to
	// be recorded whether the value was probed or supplied, or a deployment
	// that pins HOST_IP would render modules that cannot see it.
	for _, key := range hostEnvKeys {
		a.setHostEnv(key, "")
	}
	return nil
}

// hostEnvKeys is every key applyHostNetwork may publish. Detection fills in
// what the config left blank and skips the probe entirely when nothing is
// blank, so the list -- rather than the code path taken -- is what says which
// keys belong to the host.
var hostEnvKeys = []string{
	"SERVER_NAME", "HOST_DNS_SERVER", "HOST_IP", "INTERFACE", "DEFAULT_GATEWAY_IP",
	"HOST_SUBNET_MASK", "LOCAL_DNS_SERVER", "HOST_IPV6", "HOST_IPV6_INTERFACE",
	"HOST_HAS_IPV6", "HOST_SEGMENT", "VLAN_SEGMENT", "VLAN_SUBNET_MASK",
	"VLAN_GATEWAY_IP", "VLAN_BRIDGE_IP", "VLAN_BRIDGE_INTERFACE", "VLAN_INTERFACE",
	"HOST_LAN_IP",
}

// applyMacvlanPlan settles the two addresses a host-LAN attachment puts on the
// local segment: the container's own address and the host-side bridge.
//
// Both may be pinned in the config, and on a LAN with a DHCP server pinning
// them is the correct thing to do. Docker's IPAM allocates from a pool carved
// out of the host segment and never asks anyone for it -- see calcVLAN, whose
// defence against handing out a leased address is to sit at the top of the
// range where leases usually are not. That is a convention, not an agreement:
// a router whose pool reaches the top of the segment will lease an address
// this deployment is already using, and nothing on either side will say so.
// A pinned address can be excluded from the router's pool, and can be probed
// for an existing occupant before it is taken (see checkLANAddressConflicts).
//
// The pool is only carved when something is still unpinned, which is also what
// lets a host whose prefix is narrower than a /28 attach to the host LAN at
// all: the pool it cannot carve is the pool nobody is asking for.
func (a *app) applyMacvlanPlan() error {
	if pinned := strings.TrimSpace(a.env["HOST_LAN_BRIDGE_IP"]); pinned != "" {
		a.setHostEnv("VLAN_BRIDGE_IP", pinned)
	}
	segment, err := hostSegment(a.env["HOST_IP"], a.env["HOST_SUBNET_MASK"])
	if err != nil {
		return a.hostNetworkInputError(err, "HOST_IP", "HOST_SUBNET_MASK")
	}
	a.setHostEnv("HOST_SEGMENT", segment)
	a.setHostEnv("VLAN_INTERFACE", macvlanNetworkName)
	a.setHostEnv("VLAN_BRIDGE_INTERFACE", macvlanBridgeName)
	// Containers on the macvlan are on the LAN, so their default route is the
	// LAN's router. The fall back to the host's own address only applies when
	// no default route was detected at all, where nothing else is a better
	// guess; it used to live inside calcVLAN, which meant the pinned path --
	// where no pool is carved -- would have published no gateway at all.
	a.setHostEnv("VLAN_GATEWAY_IP", defaultString(a.env["DEFAULT_GATEWAY_IP"], a.env["HOST_IP"]))
	addressPinned := a.env["HOST_LAN_IP"] != ""
	if !addressPinned || a.env["VLAN_BRIDGE_IP"] == "" {
		vlan, err := calcVLAN(a.env["HOST_IP"], a.env["HOST_SUBNET_MASK"])
		if err != nil {
			return a.hostNetworkInputError(err, "HOST_IP", "HOST_SUBNET_MASK")
		}
		for key, value := range vlan {
			// A pinned address may sit anywhere in the host segment, and
			// VLAN_SEGMENT becomes docker's --ip-range, which would reject it.
			// The range only constrains addresses this deployment did not
			// choose, so with a pinned one there is nothing left to constrain.
			if key == "VLAN_SEGMENT" && addressPinned {
				continue
			}
			a.setHostEnv(key, value)
		}
		a.setHostEnv("HOST_LAN_IP", poolAddress(vlan["VLAN_SEGMENT"], 2))
	} else {
		// Nothing carved a pool, so the bridge address covers only itself and
		// the container is reached through an explicit route. See ensureMacvlan.
		a.setHostEnv("VLAN_SUBNET_MASK", "32")
	}
	return a.validateMacvlanPlan()
}

const (
	macvlanNetworkName = "anas_macvlan"
	macvlanBridgeName  = "anas_bridge"
)

// hostSegment is the CIDR the host's own address sits in. Unlike calcVLAN it
// imposes no width: a segment is a statement about the host, and every host has
// one, including the /30 and /32 hosts that have no room for an address pool.
func hostSegment(ipStr, prefixStr string) (string, error) {
	ip := net.ParseIP(ipStr).To4()
	if ip == nil {
		return "", fmt.Errorf("invalid HOST_IP %q", ipStr)
	}
	prefix, err := strconv.Atoi(prefixStr)
	if err != nil || prefix <= 0 || prefix > 32 {
		return "", fmt.Errorf("invalid HOST_SUBNET_MASK %q", prefixStr)
	}
	return fmt.Sprintf("%s/%d", ip.Mask(net.CIDRMask(prefix, 32)).String(), prefix), nil
}

// poolAddress is the nth address of a CIDR. The bridge takes the first, so the
// container takes the second, which is also the first address docker's IPAM has
// left to hand out once --aux-address has reserved the bridge.
func poolAddress(segment string, n uint32) string {
	_, network, err := net.ParseCIDR(segment)
	if err != nil {
		return ""
	}
	return uint32ToIP(ipToUint32(network.IP) + n).String()
}

// validateMacvlanPlan rejects an address that cannot work before anything is
// created with it. Each of these produced a failure far from its cause: an
// address outside the segment is unreachable, an address equal to the host's
// or the gateway's takes over an address the LAN already depends on, and the
// network and broadcast addresses are not host addresses at all.
func (a *app) validateMacvlanPlan() error {
	_, network, err := net.ParseCIDR(a.env["HOST_SEGMENT"])
	if err != nil {
		return fmt.Errorf("invalid HOST_SEGMENT %q: %w", a.hostNetworkValueForError("HOST_SEGMENT", a.env["HOST_SEGMENT"]), err)
	}
	prefix, _ := network.Mask.Size()
	broadcast := uint32ToIP(ipToUint32(network.IP) | ^uint32(0)>>uint(prefix))
	for _, addr := range []struct{ parameter, key, sourceKey, value string }{
		{"host_lan_bridge_ip", "VLAN_BRIDGE_IP", "HOST_LAN_BRIDGE_IP", a.env["VLAN_BRIDGE_IP"]},
		{"host_lan_ip", "HOST_LAN_IP", "HOST_LAN_IP", a.env["HOST_LAN_IP"]},
	} {
		displayAddress := a.hostNetworkValueForError(addr.key, addr.value)
		if displayAddress != "<redacted>" {
			displayAddress = a.hostNetworkValueForError(addr.sourceKey, addr.value)
		}
		displaySegment := a.hostNetworkValueForError("HOST_SEGMENT", a.env["HOST_SEGMENT"])
		ip := net.ParseIP(addr.value).To4()
		if ip == nil {
			return fmt.Errorf("%s must be an IPv4 address, got %q", addr.parameter, displayAddress)
		}
		if !network.Contains(ip) {
			return fmt.Errorf("%s %s is outside the host segment %s", addr.parameter, displayAddress, displaySegment)
		}
		for _, taken := range []struct{ name, value string }{
			{"the host", a.env["HOST_IP"]},
			{"the default gateway", a.env["DEFAULT_GATEWAY_IP"]},
		} {
			if taken.value != "" && ip.Equal(net.ParseIP(taken.value).To4()) {
				return fmt.Errorf("%s %s is already %s's address", addr.parameter, displayAddress, taken.name)
			}
		}
		// A /32 host segment has neither: the single address is the host's own,
		// and the loop above has already rejected it.
		if prefix < 31 && (ip.Equal(network.IP.To4()) || ip.Equal(broadcast)) {
			return fmt.Errorf("%s %s is the network or broadcast address of %s", addr.parameter, displayAddress, displaySegment)
		}
	}
	if a.env["VLAN_BRIDGE_IP"] == a.env["HOST_LAN_IP"] {
		display := a.hostNetworkValueForError("HOST_LAN_IP", a.env["HOST_LAN_IP"])
		if display != "<redacted>" {
			display = a.hostNetworkValueForError("HOST_LAN_BRIDGE_IP", a.env["VLAN_BRIDGE_IP"])
		}
		return fmt.Errorf("host_lan_ip and host_lan_bridge_ip are both %s; the bridge and the container need separate addresses", display)
	}
	return nil
}

func (a *app) hostNetworkInputError(err error, keys ...string) error {
	for _, key := range keys {
		if a.resolvedValueIsSensitive(key) {
			return fmt.Errorf("invalid host network input <redacted>")
		}
	}
	return err
}

func (a *app) hostNetworkValueForError(key, value string) string {
	if a.resolvedValueIsSensitive(key) {
		return "<redacted>"
	}
	// HOST_SEGMENT is derived from these two inputs. Preserve their source
	// provenance in diagnostics without changing module scoping for the derived
	// runtime key.
	if key == "HOST_SEGMENT" && (a.resolvedValueIsSensitive("HOST_IP") || a.resolvedValueIsSensitive("HOST_SUBNET_MASK")) {
		return "<redacted>"
	}
	return value
}

// setHostEnv publishes a discovered value without overwriting a configured
// one, and records it as globally owned.
func (a *app) setHostEnv(key, value string) {
	if a.env[key] == "" && value != "" {
		a.env[key] = value
	}
	if a.env[key] != "" {
		// setEnvOwner keeps the first owner, so a value that came from the
		// config keeps the config's ownership rather than acquiring ours.
		a.setEnvOwner(key, "")
	}
}

func defaultString(v, d string) string {
	if v == "" {
		return d
	}
	return v
}

// serverName is the host's short name in upper case, which is what the NetBIOS
// and Kerberos consumers downstream expect. A configured value is normalized
// the same way rather than trusted verbatim.
func serverName(configured string) string {
	if configured != "" {
		return strings.ToUpper(configured)
	}
	h, err := os.Hostname()
	if err != nil {
		return ""
	}
	return strings.ToUpper(strings.Split(h, ".")[0])
}

type hostNetwork struct {
	IP        net.IP
	Prefix    int
	Interface string
	Gateway   string
}

func detectHostNetwork(preferred string) (hostNetwork, error) {
	gateway, ifaceName := defaultRoute()
	ifaces, _ := net.Interfaces()
	for _, iface := range ifaces {
		if ifaceName != "" && iface.Name != ifaceName {
			continue
		}
		addrs, _ := iface.Addrs()
		for _, addr := range addrs {
			ipnet, ok := addr.(*net.IPNet)
			if !ok || ipnet.IP.IsLoopback() {
				continue
			}
			ip := ipnet.IP.To4()
			if ip == nil {
				continue
			}
			if preferred != "" && ip.String() != preferred {
				continue
			}
			ones, _ := ipnet.Mask.Size()
			return hostNetwork{IP: ip, Prefix: ones, Interface: iface.Name, Gateway: gateway}, nil
		}
	}
	if preferred != "" {
		for _, iface := range ifaces {
			addrs, _ := iface.Addrs()
			for _, addr := range addrs {
				ipnet, ok := addr.(*net.IPNet)
				if !ok {
					continue
				}
				ip := ipnet.IP.To4()
				if ip != nil && ip.String() == preferred {
					ones, _ := ipnet.Mask.Size()
					return hostNetwork{IP: ip, Prefix: ones, Interface: iface.Name, Gateway: gateway}, nil
				}
			}
		}
	}
	return hostNetwork{}, fmt.Errorf("no usable IPv4 address detected")
}

func defaultRoute() (gateway, iface string) {
	out, err := exec.Command("ip", "-4", "route", "show", "default").Output()
	if err != nil {
		return "", ""
	}
	fields := strings.Fields(string(out))
	for i, f := range fields {
		if f == "via" && i+1 < len(fields) {
			gateway = fields[i+1]
		}
		if f == "dev" && i+1 < len(fields) {
			iface = fields[i+1]
		}
	}
	return gateway, iface
}

func resolvNameservers() string {
	b, err := os.ReadFile("/etc/resolv.conf")
	if err != nil {
		return ""
	}
	out := []string{}
	for _, line := range strings.Split(string(b), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && fields[0] == "nameserver" {
			out = append(out, fields[1])
		}
	}
	return strings.Join(out, " ")
}

// detectHostIPv6 publishes whether this host holds a routable IPv6 address,
// and which one.
//
// It belongs to the runner rather than to a DDNS module because more than one
// module needs the answer, and because the question is about the host rather
// than about any service. It is also only a statement about this machine: a
// host address says nothing about whether a bridge-network container can reach
// the IPv6 internet, which is a separate question each module answers for
// itself.
//
// The check is deliberately local. Dialling an outside host would measure
// connectivity at one instant and make rendering depend on the internet being
// reachable, whereas the failure this guards against -- "connect: network is
// unreachable" -- is decided before a packet is sent, by the absence of a
// global address to source it from. Link-local and loopback addresses exist on
// every machine and route nowhere, so they do not count.
// HOST_HAS_IPV6 is assigned rather than defaulted: it is derived from whether
// an address exists, so a stale value in the environment must not survive.
func (a *app) detectHostIPv6() {
	a.setEnvOwner("HOST_HAS_IPV6", globalScope)
	if a.env["HOST_IPV6"] != "" {
		a.env["HOST_HAS_IPV6"] = "true"
		return
	}
	ip, iface := probeHostIPv6(a.env["INTERFACE"])
	if ip == "" {
		a.env["HOST_HAS_IPV6"] = "false"
		return
	}
	a.setHostEnv("HOST_IPV6", ip)
	a.setHostEnv("HOST_IPV6_INTERFACE", iface)
	a.env["HOST_HAS_IPV6"] = "true"
}

// probeHostIPv6 returns the host's routable IPv6 address, preferring the one
// on the interface that already carries the IPv4 default route.
func probeHostIPv6(preferred string) (ip, iface string) {
	ifaces, _ := net.Interfaces()
	fallbackIP, fallbackIface := "", ""
	for _, in := range ifaces {
		addrs, _ := in.Addrs()
		for _, addr := range addrs {
			ipnet, ok := addr.(*net.IPNet)
			if !ok {
				continue
			}
			// To4 is non-nil for IPv4-mapped forms, which are not IPv6 routing.
			if ipnet.IP.To4() != nil || !ipnet.IP.IsGlobalUnicast() {
				continue
			}
			// A unique-local address is globally unicast by Go's definition but
			// is not reachable from outside, so publishing it as the host's
			// address would produce an AAAA record nobody can connect to.
			if isUniqueLocalIPv6(ipnet.IP) {
				continue
			}
			if in.Name == preferred {
				return ipnet.IP.String(), in.Name
			}
			if fallbackIP == "" {
				fallbackIP, fallbackIface = ipnet.IP.String(), in.Name
			}
		}
	}
	return fallbackIP, fallbackIface
}

// isUniqueLocalIPv6 reports fc00::/7, the IPv6 counterpart of RFC1918.
func isUniqueLocalIPv6(ip net.IP) bool {
	return len(ip) == net.IPv6len && ip[0]&0xfe == 0xfc
}

// calcVLAN carves the macvlan address pool out of the top of the host segment.
// The pool is a fixed /28 so that containers on it never collide with DHCP
// leases at the bottom of the range, which also means the host segment has to
// be wider than a /28 for there to be anything to carve.
func calcVLAN(ipStr, prefixStr string) (map[string]string, error) {
	ip := net.ParseIP(ipStr).To4()
	if ip == nil {
		return nil, fmt.Errorf("invalid HOST_IP %q", ipStr)
	}
	prefix, err := strconv.Atoi(prefixStr)
	if err != nil || prefix <= 0 || prefix > 28 {
		return nil, fmt.Errorf("invalid HOST_SUBNET_MASK %q for macvlan", prefixStr)
	}
	mask := net.CIDRMask(prefix, 32)
	network := ip.Mask(mask)
	vlanMask := 28
	hostBits := 32 - prefix
	broadcastN := ipToUint32(network) ^ uint32((1<<hostBits)-1)
	vlanNetwork := uint32ToIP(broadcastN & ^uint32((1<<(32-vlanMask))-1))
	bridgeIP := uint32ToIP(ipToUint32(vlanNetwork) + 1)
	// VLAN_GATEWAY_IP is deliberately absent. It was here, set to the host's own
	// address, and it never took effect: applyMacvlanPlan publishes the default
	// route first and setHostEnv keeps the first value. The fallback it was
	// meant to provide now lives at that one call site, where it applies to the
	// pinned path too.
	return map[string]string{
		"HOST_SEGMENT":          fmt.Sprintf("%s/%d", network.String(), prefix),
		"VLAN_SUBNET_MASK":      strconv.Itoa(vlanMask),
		"VLAN_BRIDGE_INTERFACE": macvlanBridgeName,
		"VLAN_BRIDGE_IP":        bridgeIP.String(),
		"VLAN_SEGMENT":          fmt.Sprintf("%s/%d", vlanNetwork.String(), vlanMask),
		"VLAN_INTERFACE":        macvlanNetworkName,
	}, nil
}

func ipToUint32(ip net.IP) uint32 {
	ip = ip.To4()
	return binary.BigEndian.Uint32(ip)
}

func uint32ToIP(v uint32) net.IP {
	var b [4]byte
	binary.BigEndian.PutUint32(b[:], v)
	return net.IPv4(b[0], b[1], b[2], b[3]).To4()
}
