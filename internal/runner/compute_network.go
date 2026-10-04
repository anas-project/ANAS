package runner

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net"
	"net/netip"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"syscall"

	"github.com/anas-project/ANAS/internal/computenet"
	"github.com/anas-project/ANAS/internal/runtimeissues"
)

// prepareComputeNetwork freezes every compute lease's network declaration:
// the egress and ingress tiers, the slots, and the port bindings with their
// host ports resolved (INCUS-R-112, R-131, R-152, R-153). It runs after
// calculate, where the provider has published whether this host can bind
// ports at all, and before rendering.
func (a *app) prepareComputeNetwork() error {
	previous := a.previousComputeNetworks()
	type owner struct{ lease, binding string }
	taken := map[string]owner{}
	if port := strings.TrimSpace(a.env["TRAEFIK_BASE_PORT"]); port != "" && contains(a.order, "traefik") {
		taken["tcp/"+port] = owner{lease: "traefik", binding: "the Traefik HTTPS entrypoint"}
	}
	var pending []*ResourceRequest
	for i := range a.resourceRequests {
		r := &a.resourceRequests[i]
		if r.Contract != "compute" {
			continue
		}
		network, err := computenet.ParseSpec(r.Spec)
		if err != nil {
			return fmt.Errorf("resource %s.%s: %w", r.Consumer, r.ID, err)
		}
		if network.NeedsTraefik() {
			port, err := strconv.Atoi(strings.TrimSpace(a.env["TRAEFIK_BASE_PORT"]))
			if !contains(a.order, "traefik") || err != nil || port < 1 || port > 65535 {
				return fmt.Errorf("resource %s.%s reaches ANAS Modules through Traefik, but the deployment has no traefik Module", r.Consumer, r.ID)
			}
			network.TraefikPort = uint16(port)
		}
		r.ComputeNetwork = &network
		if len(network.Ports) > 0 {
			if err := a.portBindingAvailable(*r); err != nil {
				return err
			}
		}
		pending = append(pending, r)
	}
	// Explicit ports first, so an auto port can never take a port a later
	// lease names outright.
	for _, r := range pending {
		identity := r.Consumer + "." + r.ID
		old := previous[identity]
		for i := range r.ComputeNetwork.Ports {
			b := &r.ComputeNetwork.Ports[i]
			if b.Auto {
				continue
			}
			key := b.Protocol + "/" + strconv.Itoa(int(b.HostPort))
			if other, used := taken[key]; used {
				return fmt.Errorf("resource %s port binding %s conflicts with %s %s (INCUS-R-138)", identity, key, other.lease, other.binding)
			}
			taken[key] = owner{lease: identity, binding: "port binding"}
			if !old.holds(*b) {
				if err := a.hostPortFree(b.Protocol, b.HostPort); err != nil {
					// A port taken on the host is a runtime condition, not a
					// declaration error: record it (INCUS-R-154).
					a.recordRuntimeIssue(runtimeissues.Finding{Key: portBindingIssueKey(identity, b.Protocol, b.HostPort),
						Message: fmt.Sprintf("explicit host port %s of %s is taken: %v", key, identity, err)})
					return fmt.Errorf("resource %s port binding %s: %w", identity, key, err)
				}
			}
		}
	}
	for _, r := range pending {
		identity := r.Consumer + "." + r.ID
		old := previous[identity]
		for i := range r.ComputeNetwork.Ports {
			b := &r.ComputeNetwork.Ports[i]
			if !b.Auto {
				continue
			}
			if port := old.autoPort(*b); port != 0 {
				key := b.Protocol + "/" + strconv.Itoa(int(port))
				if _, used := taken[key]; !used {
					b.HostPort, taken[key] = port, owner{lease: identity, binding: "port binding"}
					continue
				}
			}
			first, last, err := a.portBindingRange(*r)
			if err != nil {
				return err
			}
			port, err := a.allocateHostPort(b.Protocol, first, last, func(port uint16) bool {
				_, used := taken[b.Protocol+"/"+strconv.Itoa(int(port))]
				return used
			})
			if err != nil {
				return fmt.Errorf("resource %s port binding %s/auto: %w", identity, b.Protocol, err)
			}
			b.HostPort = port
			taken[b.Protocol+"/"+strconv.Itoa(int(port))] = owner{lease: identity, binding: "port binding"}
		}
		if err := r.ComputeNetwork.Validate(stringSpec(r.Spec, "instance_prefix"), quotaMaxInstances(r.Spec)); err != nil {
			return fmt.Errorf("resource %s: %w", identity, err)
		}
	}
	// Every explicit port was free or already held: earlier conflicts are over.
	a.resolveRuntimeIssues(PortBindingIssuePrefix)
	return nil
}

// PortBindingIssuePrefix starts the key of every port binding runtime issue:
// incus.port-binding/<consumer>.<resource>/<protocol>/<host port>.
const PortBindingIssuePrefix = "incus.port-binding/"

func portBindingIssueKey(lease, protocol string, port uint16) string {
	return PortBindingIssuePrefix + lease + "/" + protocol + "/" + strconv.Itoa(int(port))
}

func (a *app) runtimeIssueStore() *runtimeissues.Store {
	if a.base == "" {
		return nil
	}
	return runtimeissues.Open(filepath.Join(a.base, "state", "runtime-issues.json"), func(format string, args ...any) {
		a.warning("runtime_issue", format, args...)
	})
}

// recordRuntimeIssue never fails the command it reports on: the command's
// own error is what the caller acts on.
func (a *app) recordRuntimeIssue(finding runtimeissues.Finding) {
	if store := a.runtimeIssueStore(); store != nil {
		if err := store.Record(runtimeissues.SourceApply, finding); err != nil {
			a.warning("runtime_issue_unrecorded", "could not record runtime issue %s: %v", finding.Key, err)
		}
	}
}

func (a *app) resolveRuntimeIssues(prefix string) {
	if store := a.runtimeIssueStore(); store != nil {
		if err := store.Reconcile(runtimeissues.SourceApply, prefix, nil); err != nil {
			a.warning("runtime_issue_unrecorded", "could not resolve runtime issues %s: %v", prefix, err)
		}
	}
}

// frozenBindings is a lease's previous frozen network, used to keep an auto
// port across applies and to recognize a port this deployment already holds.
type frozenBindings struct{ network *computenet.Network }

func (f frozenBindings) holds(b computenet.PortBinding) bool {
	if f.network == nil {
		return false
	}
	return slices.ContainsFunc(f.network.Ports, func(old computenet.PortBinding) bool {
		return old.Protocol == b.Protocol && old.HostPort == b.HostPort
	})
}

func (f frozenBindings) autoPort(b computenet.PortBinding) uint16 {
	if f.network == nil {
		return 0
	}
	for _, old := range f.network.Ports {
		if old.Auto && old.Protocol == b.Protocol && old.Slot == b.Slot && old.GuestPort == b.GuestPort && old.HostPort != 0 {
			return old.HostPort
		}
	}
	return 0
}

func (a *app) previousComputeNetworks() map[string]frozenBindings {
	out := map[string]frozenBindings{}
	if a.base == "" {
		return out
	}
	active, err := loadActiveState(a.base)
	if err != nil || active == nil || active.ActiveDeployment == "" {
		return out
	}
	manifest, err := loadDeploymentManifest(filepath.Join(a.base, "deployments", active.ActiveDeployment))
	if err != nil {
		return out
	}
	for _, resource := range manifest.Resources {
		if resource.Contract == "compute" && resource.ComputeNetwork != nil {
			out[resource.Consumer+"."+resource.ID] = frozenBindings{network: resource.ComputeNetwork.Clone()}
		}
	}
	return out
}

var portRangePattern = regexp.MustCompile(`^([0-9]{1,5})-([0-9]{1,5})$`)

// portBindingAvailable answers INCUS-R-164: a port binding is applied by
// hostd, so a host without one fails here with the reason rather than
// skipping the binding. The provider publishes the range the operator
// approved in incus.configure, or why there is none.
func (a *app) portBindingAvailable(r ResourceRequest) error {
	if _, _, err := a.portBindingRange(r); err != nil {
		return err
	}
	return nil
}

func (a *app) portBindingRange(r ResourceRequest) (uint16, uint16, error) {
	prefix := defaultEnvPrefix(r.Provider)
	value := strings.TrimSpace(a.env[prefix+"_PORT_BINDING_RANGE"])
	if value == "" {
		reason := map[string]string{
			"hostd_missing":       "anas-hostd is not installed as a system service on this host (installed with --no-service, a non-systemd distribution or a development build)",
			"host_not_configured": "the host has not run incus.configure, which approves the port range",
			"remote_daemon":       "the Incus daemon is not on this host, so its host has no hostd to apply port bindings",
		}[strings.TrimSpace(a.env[prefix+"_PORT_BINDING_BLOCKER"])]
		if reason == "" {
			reason = "the compute provider did not publish an approved host port range"
		}
		return 0, 0, fmt.Errorf("resource %s.%s declares port bindings, but %s (INCUS-R-164)", r.Consumer, r.ID, reason)
	}
	match := portRangePattern.FindStringSubmatch(value)
	if match == nil {
		return 0, 0, fmt.Errorf("provider %s published an invalid port binding range", r.Provider)
	}
	first, _ := strconv.Atoi(match[1])
	last, _ := strconv.Atoi(match[2])
	if first < 1 || last > 65535 || first > last {
		return 0, 0, fmt.Errorf("provider %s published an invalid port binding range", r.Provider)
	}
	for _, b := range r.ComputeNetwork.Ports {
		if !b.Auto && (int(b.HostPort) < first || int(b.HostPort) > last) {
			return 0, 0, fmt.Errorf("resource %s.%s port binding %s/%d is outside the approved range %s", r.Consumer, r.ID, b.Protocol, b.HostPort, value)
		}
	}
	return uint16(first), uint16(last), nil
}

// allocateHostPort picks a random free port in the approved range (R-153).
// Random rather than lowest-free, so two hosts or two workspaces do not walk
// the same sequence and collide on the first port.
func (a *app) allocateHostPort(protocol string, first, last uint16, reserved func(uint16) bool) (uint16, error) {
	span := int64(last) - int64(first) + 1
	for attempt := 0; attempt < 64; attempt++ {
		n, err := rand.Int(rand.Reader, big.NewInt(span))
		if err != nil {
			return 0, err
		}
		port := first + uint16(n.Int64())
		if reserved(port) {
			continue
		}
		if a.hostPortFree(protocol, port) == nil {
			return port, nil
		}
	}
	return 0, fmt.Errorf("no free port found in %d-%d", first, last)
}

// hostPortProbe is replaced in tests. It reports an error when the port is
// held by a host process or by a Docker publication on this host.
var hostPortProbe = probeHostPort

func (a *app) hostPortFree(protocol string, port uint16) error {
	if err := hostPortProbe(protocol, port); err != nil {
		return err
	}
	published, err := a.dockerPublishedPorts()
	if err != nil {
		return fmt.Errorf("read Docker published ports: %w", err)
	}
	if published[protocol+"/"+strconv.Itoa(int(port))] {
		return fmt.Errorf("port is already published by a Docker container")
	}
	return nil
}

func probeHostPort(protocol string, port uint16) error {
	address := ":" + strconv.Itoa(int(port))
	var closer interface{ Close() error }
	var err error
	if protocol == computenet.ProtocolUDP {
		closer, err = net.ListenPacket("udp", address)
	} else {
		closer, err = net.Listen("tcp", address)
	}
	if err != nil {
		if errors.Is(err, syscall.EADDRINUSE) {
			return fmt.Errorf("port is already in use on this host")
		}
		return fmt.Errorf("port cannot be probed on this host: %w", err)
	}
	return closer.Close()
}

// dockerPublishedPortsFunc is replaced in tests.
var dockerPublishedPortsFunc = (*app).readDockerPublishedPorts

func (a *app) dockerPublishedPorts() (map[string]bool, error) {
	return dockerPublishedPortsFunc(a)
}

var dockerPortMapping = regexp.MustCompile(`:([0-9]{1,5})->[0-9]{1,5}/(tcp|udp)`)

func (a *app) readDockerPublishedPorts() (map[string]bool, error) {
	cmd := externalCommandContext(a.subprocessContext(), "docker", "ps", "--format", "{{.Ports}}")
	cmd.Env = a.compose.Environment(a.commandEnvironment(nil), nil)
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	published := map[string]bool{}
	for _, match := range dockerPortMapping.FindAllStringSubmatch(string(out), -1) {
		published[match[2]+"/"+match[1]] = true
	}
	return published, nil
}

func quotaMaxInstances(spec map[string]any) int {
	quota, _ := spec["quota"].(map[string]any)
	n, _ := intFromAny(quota["max_instances"])
	return n
}

// validateFrozenComputeNetwork re-derives the declaration from the frozen
// spec and requires the frozen network to match it, apart from the host
// ports Core chose for auto bindings and the Traefik port it recorded.
func validateFrozenComputeNetwork(r ResourceRequest) error {
	if r.Contract != "compute" {
		if r.ComputeNetwork != nil {
			return fmt.Errorf("compute network is attached to a non-compute resource")
		}
		return nil
	}
	if r.ComputeNetwork == nil {
		// Deployments frozen before network declarations ran with the default
		// policy; replaying them keeps that policy.
		return nil
	}
	declared, err := computenet.ParseSpec(r.Spec)
	if err != nil {
		return err
	}
	frozen := r.ComputeNetwork.Clone()
	if err := frozen.Validate(stringSpec(r.Spec, "instance_prefix"), quotaMaxInstances(r.Spec)); err != nil {
		return err
	}
	declared.TraefikPort = frozen.TraefikPort
	for i := range declared.Ports {
		if i < len(frozen.Ports) && declared.Ports[i].Auto {
			declared.Ports[i].HostPort = frozen.Ports[i].HostPort
		}
	}
	a, _ := declared.Encode()
	b, _ := frozen.Encode()
	if a != b {
		return fmt.Errorf("frozen compute network differs from the resource declaration")
	}
	return nil
}

// frozenComputeNetwork is what the Provider receives: the frozen network, or
// the default policy for a deployment frozen before declarations existed.
func frozenComputeNetwork(r ResourceRequest) computenet.Network {
	if r.ComputeNetwork != nil {
		return *r.ComputeNetwork.Clone()
	}
	return computenet.Default()
}

// hostInterface is one host interface and its addresses with their prefix
// lengths, as the kernel reports them.
type hostInterface struct {
	Name  string
	Addrs []netip.Prefix
}

// hostInterfaceAddrs is replaced in tests.
var hostInterfaceAddrs = func() ([]hostInterface, error) {
	interfaces, err := net.Interfaces()
	if err != nil {
		return nil, err
	}
	out := make([]hostInterface, 0, len(interfaces))
	for _, iface := range interfaces {
		addrs, err := iface.Addrs()
		if err != nil {
			return nil, err
		}
		entry := hostInterface{Name: iface.Name}
		for _, addr := range addrs {
			if prefix, err := netip.ParsePrefix(addr.String()); err == nil {
				entry.Addrs = append(entry.Addrs, prefix)
			}
		}
		out = append(out, entry)
	}
	return out, nil
}

// hostLANAndAddresses computes what this host's LAN and own addresses are at
// the moment of apply (INCUS-R-120, R-121): the subnets directly connected to
// the interface carrying the default route, and every address the host holds.
// Loopback and link-local are never either; Incus and Docker bridges are not
// the default-route interface, so they never become LAN by address range.
func (a *app) hostLANAndAddresses() (lan, addresses []string, err error) {
	routeInterface := strings.TrimSpace(a.env["INTERFACE"])
	if routeInterface == "" {
		_, routeInterface = defaultRoute()
	}
	interfaces, err := hostInterfaceAddrs()
	if err != nil {
		return nil, nil, fmt.Errorf("list host interfaces: %w", err)
	}
	lanSet, addressSet := map[string]bool{}, map[string]bool{}
	for _, iface := range interfaces {
		for _, prefix := range iface.Addrs {
			ip := prefix.Addr().Unmap()
			if ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsMulticast() || ip.IsUnspecified() {
				continue
			}
			addressSet[netip.PrefixFrom(ip, ip.BitLen()).String()] = true
			if iface.Name == routeInterface && prefix.Bits() > 0 {
				lanSet[netip.PrefixFrom(ip, prefix.Bits()).Masked().String()] = true
			}
		}
	}
	for value := range lanSet {
		lan = append(lan, value)
	}
	for value := range addressSet {
		addresses = append(addresses, value)
	}
	slices.Sort(lan)
	slices.Sort(addresses)
	return lan, addresses, nil
}

// computeNetworkState is the Provider's ensure report as Core records it:
// what the console's lease network view and hostd's port table read.
type computeNetworkState struct {
	Bridge      string              `yaml:"bridge" json:"bridge"`
	IPv4Subnet  string              `yaml:"ipv4_subnet" json:"ipv4_subnet"`
	IPv4Gateway string              `yaml:"ipv4_gateway" json:"ipv4_gateway"`
	IPv6Subnet  string              `yaml:"ipv6_subnet,omitempty" json:"ipv6_subnet,omitempty"`
	IPv6Gateway string              `yaml:"ipv6_gateway,omitempty" json:"ipv6_gateway,omitempty"`
	Slots       []computeSlotRecord `yaml:"slots,omitempty" json:"slots,omitempty"`
}

type computeSlotRecord struct {
	Name     string `yaml:"name" json:"name"`
	Instance string `yaml:"instance" json:"instance"`
	IPv4     string `yaml:"ipv4" json:"ipv4"`
	IPv6     string `yaml:"ipv6,omitempty" json:"ipv6,omitempty"`
}

var bridgeNamePattern = regexp.MustCompile(`^[a-z][a-z0-9]{1,14}$`)

// parseComputeEnsureResult reads the last JSON line of the Provider's stdout
// and validates the network it reports. A Provider that reports nothing is an
// error: Core would otherwise record a lease without its subnet and gateway.
func parseComputeEnsureResult(out string) (*computeNetworkState, error) {
	lines := strings.Split(strings.TrimSpace(out), "\n")
	line := strings.TrimSpace(lines[len(lines)-1])
	var result struct {
		Ready   bool                 `json:"ready"`
		Network *computeNetworkState `json:"network"`
	}
	decoder := json.NewDecoder(strings.NewReader(line))
	if line == "" || decoder.Decode(&result) != nil {
		return nil, fmt.Errorf("provider did not report the ensured lease")
	}
	if !result.Ready || result.Network == nil {
		return nil, fmt.Errorf("provider did not report a ready lease network")
	}
	n := result.Network
	v4, err := netip.ParsePrefix(n.IPv4Subnet)
	gateway, gatewayErr := netip.ParseAddr(n.IPv4Gateway)
	if !bridgeNamePattern.MatchString(n.Bridge) || err != nil || !v4.Addr().Is4() || v4 != v4.Masked() || gatewayErr != nil || !v4.Contains(gateway) {
		return nil, fmt.Errorf("provider reported an invalid lease bridge, subnet or gateway")
	}
	var v6 netip.Prefix
	if n.IPv6Subnet != "" || n.IPv6Gateway != "" {
		v6, err = netip.ParsePrefix(n.IPv6Subnet)
		gateway6, gatewayErr := netip.ParseAddr(n.IPv6Gateway)
		if err != nil || !v6.Addr().Is6() || v6 != v6.Masked() || gatewayErr != nil || !v6.Contains(gateway6) {
			return nil, fmt.Errorf("provider reported an invalid lease IPv6 subnet or gateway")
		}
	}
	for _, slot := range n.Slots {
		addr, err := netip.ParseAddr(slot.IPv4)
		if err != nil || !v4.Contains(addr) || slot.Name == "" || slot.Instance == "" {
			return nil, fmt.Errorf("provider reported a slot address outside the lease subnet")
		}
		if slot.IPv6 != "" {
			addr6, err := netip.ParseAddr(slot.IPv6)
			if err != nil || !v6.IsValid() || !v6.Contains(addr6) {
				return nil, fmt.Errorf("provider reported a slot address outside the lease subnet")
			}
		}
	}
	return n, nil
}
