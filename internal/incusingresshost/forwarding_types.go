package incusingresshost

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/netip"
	"regexp"
	"slices"
	"strings"
	"time"
)

const ForwardingKernelSchema = "anas.incus-forwarding-kernel/v1"
const ForwardingPermitTTL = 30 * time.Second

var forwardingUUID = regexp.MustCompile(`^[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}$`)
var forwardingOwner = regexp.MustCompile(`^[a-f0-9]{32}$`)
var forwardingDigest = regexp.MustCompile(`^[a-f0-9]{64}$`)

// ForwardingNetworkProof is read from Incus and the local kernel by the host
// authority. Interface names alone are not identities, and a reboot never
// carries a numeric interface identity into a new kernel.
type ForwardingNetworkProof struct {
	BootID     string `json:"boot_id"`
	BridgeName string `json:"bridge_name"`
	BridgeMAC  string `json:"bridge_mac"`
	BridgeID   uint32 `json:"bridge_ifindex"`
	BridgeCIDR string `json:"bridge_cidr"`
}

type ForwardingRouteProof struct {
	Destination string `json:"destination_ipv4"`
	Port        uint16 `json:"tcp_port"`
	OutputName  string `json:"output_name"`
	OutputMAC   string `json:"output_mac"`
	OutputID    uint32 `json:"output_ifindex"`
	SourceIPv4  string `json:"source_ipv4"`
	Gateway     string `json:"gateway,omitempty"`
	Table       uint32 `json:"route_table"`
}

// ForwardingKernelScope contains only already-approved, independently
// observed facts. Constructing it confers no privilege: it is used by the
// compiled host action after its active-lease and confirmation checks.
type ForwardingKernelScope struct {
	Schema       string                 `json:"schema"`
	Owner        string                 `json:"owner"`
	ID           string                 `json:"id"`
	GrantDigest  string                 `json:"grant_digest"`
	Network      ForwardingNetworkProof `json:"network"`
	Routes       []ForwardingRouteProof `json:"routes"`
	MaxInstances int                    `json:"max_instances"`
}

type ForwardingInstanceProof struct {
	InstanceID   string `json:"instance_id"`
	WorkloadID   string `json:"workload_id"`
	UUID         string `json:"instance_uuid"`
	Incarnation  string `json:"incarnation"`
	GuestIPv4    string `json:"guest_ipv4"`
	GuestMAC     string `json:"guest_mac"`
	HostVethName string `json:"host_veth_name"`
	HostVethMAC  string `json:"host_veth_mac"`
	HostVethID   uint32 `json:"host_veth_ifindex"`
	PeerVethID   uint32 `json:"peer_veth_ifindex"`
}

func forwardingIPv4(value string) (netip.Addr, bool) {
	ip, err := netip.ParseAddr(value)
	return ip, err == nil && ip.Is4() && ip.String() == value && ip.IsGlobalUnicast() && !ip.IsLoopback() && !ip.IsLinkLocalUnicast()
}

func forwardingMAC(value string) bool {
	mac, err := net.ParseMAC(value)
	return err == nil && len(mac) == 6 && mac[0]&1 == 0 && mac.String() == value && value != "00:00:00:00:00:00"
}

func (n ForwardingNetworkProof) Validate() error {
	p, err := netip.ParsePrefix(n.BridgeCIDR)
	if !forwardingUUID.MatchString(n.BootID) || !ifaceName.MatchString(n.BridgeName) || !forwardingMAC(n.BridgeMAC) ||
		n.BridgeID == 0 || err != nil || !p.Addr().Is4() || !p.Addr().IsPrivate() || p.Bits() < 8 || p.Bits() > 30 ||
		p.String() != n.BridgeCIDR || p.Addr() == p.Masked().Addr() || p.Addr() == forwardingBroadcast(p) {
		return fmt.Errorf("invalid observed forwarding bridge identity")
	}
	return nil
}

func forwardingBroadcast(prefix netip.Prefix) netip.Addr {
	ip := prefix.Masked().Addr().As4()
	for bit := prefix.Bits(); bit < 32; bit++ {
		ip[bit/8] |= byte(1 << (7 - bit%8))
	}
	return netip.AddrFrom4(ip)
}

func (r ForwardingRouteProof) ValidateFor(n ForwardingNetworkProof) error {
	destination, validDestination := forwardingIPv4(r.Destination)
	_, validSource := forwardingIPv4(r.SourceIPv4)
	prefix, prefixErr := netip.ParsePrefix(n.BridgeCIDR)
	if n.Validate() != nil || !validDestination || !validSource || prefixErr != nil || prefix.Contains(destination) ||
		r.Port == 0 || !ifaceName.MatchString(r.OutputName) || r.OutputName == n.BridgeName || r.OutputName == "lo" ||
		r.OutputID == 0 || r.OutputID == n.BridgeID || !forwardingMAC(r.OutputMAC) || r.Table == 0 {
		return fmt.Errorf("invalid observed forwarding destination route")
	}
	if r.Gateway != "" {
		if _, ok := forwardingIPv4(r.Gateway); !ok {
			return fmt.Errorf("invalid observed forwarding gateway")
		}
	}
	return nil
}

func (s ForwardingKernelScope) Validate() error {
	if s.Schema != ForwardingKernelSchema || !forwardingOwner.MatchString(s.Owner) || !forwardingOwner.MatchString(s.ID) || !forwardingDigest.MatchString(s.GrantDigest) ||
		s.Network.Validate() != nil || len(s.Routes) == 0 || len(s.Routes) > 32 || s.MaxInstances < 1 || s.MaxInstances > 256 {
		return fmt.Errorf("invalid approved forwarding kernel scope")
	}
	seen := map[string]bool{}
	for _, r := range s.Routes {
		key := fmt.Sprintf("%s:%d", r.Destination, r.Port)
		if r.ValidateFor(s.Network) != nil || seen[key] {
			return fmt.Errorf("ambiguous approved forwarding destination")
		}
		seen[key] = true
	}
	return nil
}

func (p ForwardingInstanceProof) ValidateFor(s ForwardingKernelScope) error {
	ip, ok := forwardingIPv4(p.GuestIPv4)
	prefix, err := netip.ParsePrefix(s.Network.BridgeCIDR)
	if s.Validate() != nil || !ok || err != nil || !prefix.Contains(ip) || ip == prefix.Addr() ||
		ip == prefix.Masked().Addr() || ip == forwardingBroadcast(prefix) ||
		!forwardingUUID.MatchString(p.UUID) || !forwardingDigest.MatchString(p.Incarnation) ||
		!forwardingMAC(p.GuestMAC) || !forwardingMAC(p.HostVethMAC) || !ifaceName.MatchString(p.HostVethName) ||
		p.HostVethName == s.Network.BridgeName || p.HostVethID == 0 || p.HostVethID == s.Network.BridgeID || p.PeerVethID == 0 ||
		p.InstanceID == "" || len(p.InstanceID) > 128 || p.WorkloadID == "" || len(p.WorkloadID) > 128 ||
		strings.ContainsAny(p.InstanceID+p.WorkloadID, "\x00\r\n") {
		return fmt.Errorf("invalid observed forwarding instance identity")
	}
	return nil
}

func validateForwardingInstances(scope ForwardingKernelScope, instances []ForwardingInstanceProof) error {
	if scope.Validate() != nil || len(instances) > scope.MaxInstances {
		return fmt.Errorf("forwarding instance set exceeds the approved lease")
	}
	names, uuids, ips, macs, indices := map[string]bool{}, map[string]bool{}, map[string]bool{}, map[string]bool{}, map[uint32]bool{}
	for _, p := range instances {
		if p.ValidateFor(scope) != nil || names[p.InstanceID] || uuids[p.UUID] || ips[p.GuestIPv4] || macs[p.GuestMAC] || indices[p.HostVethID] {
			return fmt.Errorf("forwarding instances do not have unique physical and allocation identities")
		}
		names[p.InstanceID], uuids[p.UUID], ips[p.GuestIPv4], macs[p.GuestMAC], indices[p.HostVethID] = true, true, true, true, true
	}
	return nil
}

func forwardingHash(value any) string {
	body, _ := json.Marshal(value)
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}

func canonicalForwardingInstances(instances []ForwardingInstanceProof) []ForwardingInstanceProof {
	result := slices.Clone(instances)
	if result == nil {
		result = []ForwardingInstanceProof{}
	}
	slices.SortFunc(result, func(a, b ForwardingInstanceProof) int { return strings.Compare(a.InstanceID, b.InstanceID) })
	return result
}
