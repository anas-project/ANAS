package incusprovision

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"syscall"

	"github.com/anas-project/ANAS/internal/computenet"
	"github.com/anas-project/ANAS/internal/consoleconfig"
	"github.com/anas-project/ANAS/internal/deployment"
	"gopkg.in/yaml.v3"
)

// Port bindings (INCUS-R-150--R-161): a Docker-style layer-4 forward from a
// host port to a lease slot. Three host artifacts carry them:
//
//   - a fixed nft table, installed by incus.configure, whose chains rewrite
//     traffic addressed to the host itself (fib daddr type local, loopback
//     excepted) through four port maps;
//   - the port maps' elements, which only the bounded sync action
//     incus.ports.sync replaces, in one nft transaction, from the frozen
//     deployments hostd reads itself;
//   - one systemd socket unit per binding (Accept=no) with a resident holder
//     service, so a host process or a Docker publication on the same port
//     fails instead of being silently shadowed.
//
// At boot the hold units start with sockets.target, before Docker, and
// anas-incus-network.service restores the maps afterwards, leaving out any
// binding whose port another process took first.

const (
	PortTable          = "anas_incus_ports"
	portTableComment   = "anas-port-bindings"
	PortBindingsPath   = "/var/lib/anas/incus-host/ports.json"
	PortBindingsSchema = "anas.incus-port-bindings/v1"
	PortSyncSchema     = "anas.incus-port-sync/v1"

	// PortBindingIssuePrefix starts every port binding runtime issue key.
	PortBindingIssuePrefix = "incus.port-binding/"

	systemdUnitDir = "/etc/systemd/system"
	// PortHoldTemplatePath is installed with the chain; its presence is
	// the configure approval anasd looks for.
	PortHoldTemplatePath = systemdUnitDir + "/anas-port-tcp@.socket"
	socketsWantsDir      = systemdUnitDir + "/sockets.target.wants"
	maxPortBindings      = 1024
	maxPortWorkspaces    = 64
)

var portMaps = []string{"tcp4", "udp4", "tcp6", "udp6"}

// portChainRuleset is the fixed table. Its maps start empty; the rule shape
// is the one the 2026-09-30 probe verified on docker.io 26 and Docker CE 29.
const portChainRuleset = `table inet ` + PortTable + ` {
 comment "` + portTableComment + `"
 map tcp4 { type inet_service : ipv4_addr . inet_service; }
 map udp4 { type inet_service : ipv4_addr . inet_service; }
 map tcp6 { type inet_service : ipv6_addr . inet_service; }
 map udp6 { type inet_service : ipv6_addr . inet_service; }
 chain bind {
  meta nfproto ipv4 meta l4proto tcp dnat ip addr . port to tcp dport map @tcp4
  meta nfproto ipv4 meta l4proto udp dnat ip addr . port to udp dport map @udp4
  meta nfproto ipv6 meta l4proto tcp dnat ip6 addr . port to tcp dport map @tcp6
  meta nfproto ipv6 meta l4proto udp dnat ip6 addr . port to udp dport map @udp6
 }
 chain pre {
  type nat hook prerouting priority dstnat; policy accept;
  fib daddr type local jump bind
 }
 chain out {
  type nat hook output priority -100; policy accept;
  ip daddr 127.0.0.0/8 return
  ip6 daddr ::1 return
  fib daddr type local jump bind
 }
}
`

// portChainRules is how many rules each chain holds, for the readback.
var portChainRules = map[string]int{"bind": 4, "pre": 1, "out": 3}

// portUnitTemplates are the hold units, one template pair per protocol. The
// instance name is the port. The socket binds the port on every address
// (dual-stack); the service only keeps the socket, never reads it: traffic is
// redirected before it would arrive, and loopback connections wait in the
// backlog until the kernel drops them (INCUS-R-160).
var portUnitTemplates = map[string]string{
	"anas-port-tcp@.socket":  portSocketTemplate("tcp", "ListenStream"),
	"anas-port-udp@.socket":  portSocketTemplate("udp", "ListenDatagram"),
	"anas-port-tcp@.service": portServiceTemplate("tcp"),
	"anas-port-udp@.service": portServiceTemplate("udp"),
}

func portSocketTemplate(protocol, listen string) string {
	return `# Managed by ANAS (incus.configure). Holds host port %i/` + protocol + ` for a compute port binding.
[Unit]
Description=ANAS port binding hold %i/` + protocol + `
Before=docker.service

[Socket]
` + listen + `=%i
Accept=no

[Install]
WantedBy=sockets.target
`
}

func portServiceTemplate(protocol string) string {
	return `# Managed by ANAS (incus.configure). Keeps the hold socket of port %i/` + protocol + `.
[Unit]
Description=ANAS port binding holder %i/` + protocol + `
Requires=anas-port-` + protocol + `@%i.socket
After=anas-port-` + protocol + `@%i.socket

[Service]
ExecStart=/usr/bin/sleep infinity
DynamicUser=yes
NoNewPrivileges=yes
ProtectSystem=strict
ProtectHome=yes
PrivateTmp=yes
PrivateDevices=yes
RestrictAddressFamilies=AF_UNIX
MemoryMax=16M
Restart=always

[Install]
WantedBy=multi-user.target
`
}

var holdUnitPattern = regexp.MustCompile(`^anas-port-(tcp|udp)@([0-9]{1,5})\.(socket|service)$`)

func holdUnits(protocol string, port int) (string, string) {
	base := "anas-port-" + protocol + "@" + strconv.Itoa(port)
	return base + ".socket", base + ".service"
}

// PortBinding is one binding as hostd applies it.
type PortBinding struct {
	Workspace string `json:"workspace"`
	Lease     string `json:"lease"`
	Protocol  string `json:"protocol"`
	HostPort  int    `json:"host_port"`
	Bridge    string `json:"bridge"`
	IPv4      string `json:"ipv4"`
	IPv6      string `json:"ipv6,omitempty"`
	GuestPort int    `json:"guest_port"`
}

func (b PortBinding) key() string { return b.Protocol + "/" + strconv.Itoa(b.HostPort) }

// IssueKey is the binding's runtime issue key in its workspace store.
func (b PortBinding) IssueKey() string {
	return PortBindingIssuePrefix + b.Lease + "/" + b.key()
}

// PortRejection is a binding that did not take effect, and why.
type PortRejection struct {
	PortBinding
	Reason string `json:"reason"`
}

type PortSyncResult struct {
	Schema   string          `json:"schema"`
	Approved bool            `json:"approved"`
	Bindings []PortBinding   `json:"bindings"`
	Rejected []PortRejection `json:"rejected"`
}

type portBindingsFile struct {
	Schema   string        `json:"schema"`
	Bindings []PortBinding `json:"bindings"`
}

type portElement struct {
	Map     string
	Port    int
	Address string
	Target  int
}

func elementsFor(bindings []PortBinding) []portElement {
	var out []portElement
	for _, b := range bindings {
		out = append(out, portElement{Map: b.Protocol + "4", Port: b.HostPort, Address: canonicalAddress(b.IPv4), Target: b.GuestPort})
		if b.IPv6 != "" {
			out = append(out, portElement{Map: b.Protocol + "6", Port: b.HostPort, Address: canonicalAddress(b.IPv6), Target: b.GuestPort})
		}
	}
	sortElements(out)
	return out
}

func canonicalAddress(value string) string {
	if addr, err := netip.ParseAddr(value); err == nil {
		return addr.String()
	}
	return value
}

func sortElements(elements []portElement) {
	sort.Slice(elements, func(i, j int) bool {
		if elements[i].Map != elements[j].Map {
			return elements[i].Map < elements[j].Map
		}
		return elements[i].Port < elements[j].Port
	})
}

// portElementsScript replaces every map's elements in one nft transaction:
// it either all applies or leaves the previous elements in place.
func portElementsScript(elements []portElement) string {
	var b strings.Builder
	for _, name := range portMaps {
		fmt.Fprintf(&b, "flush map inet %s %s\n", PortTable, name)
	}
	for _, e := range elements {
		fmt.Fprintf(&b, "add element inet %s %s { %d : %s . %d }\n", PortTable, e.Map, e.Port, e.Address, e.Target)
	}
	return b.String()
}

// portHost is everything a sync, a restore or a check touches on the host.
// Production uses localPortHost; tests replace single functions.
type portHost struct {
	workspaces   func() ([]consoleconfig.Workspace, error)
	readFile     func(path string, limit int64) ([]byte, error)
	bridge       func(name string) ([]netip.Prefix, error)
	enabledHolds func() (map[string]bool, error)
	holdActive   func(ctx context.Context, protocol string, port int) bool
	portFree     func(protocol string, port int) error
	dockerPorts  func(ctx context.Context) (map[string]bool, error)
	hold         func(ctx context.Context, protocol string, port int) error
	release      func(ctx context.Context, protocol string, port int) error
	chainCurrent func(ctx context.Context) (bool, error)
	installChain func(ctx context.Context) error
	loadElements func(ctx context.Context, script string) error
	readElements func(ctx context.Context) ([]portElement, error)
	loadApplied  func() ([]PortBinding, error)
	saveApplied  func([]PortBinding) error
}

func (r *localRuntime) portHost() portHost {
	return portHost{
		workspaces: func() ([]consoleconfig.Workspace, error) {
			config, err := loadImagePruneServiceConfig()
			if err != nil {
				return nil, err
			}
			return config.Workspaces, nil
		},
		readFile:     readRootOwnedPublicFile,
		bridge:       bridgePrefixes,
		enabledHolds: enabledPortHolds,
		holdActive: func(ctx context.Context, protocol string, port int) bool {
			socket, service := holdUnits(protocol, port)
			_, code, err := r.commands.output(ctx, fixedSystemctl, []string{"is-active", "--quiet", socket, service}, nil)
			return err == nil && code == 0
		},
		portFree:    probePortFree,
		dockerPorts: r.docker.publishedPorts,
		hold: func(ctx context.Context, protocol string, port int) error {
			socket, service := holdUnits(protocol, port)
			return r.commands.run(ctx, fixedSystemctl, []string{"enable", "--now", socket, service}, nil)
		},
		release: func(ctx context.Context, protocol string, port int) error {
			socket, service := holdUnits(protocol, port)
			return r.commands.run(ctx, fixedSystemctl, []string{"disable", "--now", socket, service}, nil)
		},
		chainCurrent: r.portChainInstalled,
		installChain: r.installPortTable,
		loadElements: func(ctx context.Context, script string) error {
			return r.commands.runWithInput(ctx, fixedNFT, []string{"-f", "-"}, nil, []byte(script))
		},
		readElements: r.readPortElements,
		loadApplied:  loadAppliedPortBindings,
		saveApplied: func(bindings []PortBinding) error {
			body, err := json.Marshal(portBindingsFile{Schema: PortBindingsSchema, Bindings: bindings})
			if err != nil {
				return err
			}
			if err := ensureTrustedRootDirectory(filepath.Dir(PortBindingsPath), 0700); err != nil {
				return err
			}
			return writeRootOnlyFile(PortBindingsPath, body, 0600)
		},
	}
}

// --- desired bindings: hostd reads the frozen deployments itself ----------

type leaseNetworkRecord struct {
	Actual struct {
		LeaseNetwork *struct {
			Bridge string `yaml:"bridge"`
			Slots  []struct {
				Name string `yaml:"name"`
				IPv4 string `yaml:"ipv4"`
				IPv6 string `yaml:"ipv6"`
			} `yaml:"slots"`
		} `yaml:"lease_network"`
	} `yaml:"actual"`
}

var leaseIdentifier = regexp.MustCompile(`^[a-z][a-z0-9_]{0,62}$`)

// frozenPortBindings lists every binding of every registered workspace's
// active deployment, with the slot addresses Core recorded from the
// Provider. A workspace whose state cannot be trusted contributes nothing;
// one with no active deployment has nothing to bind.
func (h portHost) frozenPortBindings() ([]PortBinding, []PortRejection, error) {
	workspaces, err := h.workspaces()
	if err != nil {
		return nil, nil, ErrBlocked
	}
	if len(workspaces) > maxPortWorkspaces {
		return nil, nil, ErrBlocked
	}
	workspaces = slices.Clone(workspaces)
	sort.Slice(workspaces, func(i, j int) bool { return workspaces[i].ID < workspaces[j].ID })
	var bindings []PortBinding
	var rejected []PortRejection
	for _, workspace := range workspaces {
		if !filepath.IsAbs(workspace.Path) || filepath.Clean(workspace.Path) != workspace.Path {
			return nil, nil, ErrBlocked
		}
		base := filepath.Join(workspace.Path, ".anas")
		var active deployment.ActiveState
		body, err := h.readFile(filepath.Join(base, "state", "active.yml"), 64<<10)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil || yaml.Unmarshal(body, &active) != nil {
			rejected = append(rejected, PortRejection{PortBinding: PortBinding{Workspace: workspace.ID}, Reason: "workspace_state_untrusted"})
			continue
		}
		if active.ActiveDeployment == "" {
			continue
		}
		if deployment.ValidateID(active.ActiveDeployment) != nil {
			rejected = append(rejected, PortRejection{PortBinding: PortBinding{Workspace: workspace.ID}, Reason: "workspace_state_untrusted"})
			continue
		}
		var manifest deployment.Manifest
		body, err = h.readFile(filepath.Join(base, "deployments", active.ActiveDeployment, "deployment.yml"), 4<<20)
		if err != nil || yaml.Unmarshal(body, &manifest) != nil || manifest.ID != active.ActiveDeployment {
			rejected = append(rejected, PortRejection{PortBinding: PortBinding{Workspace: workspace.ID}, Reason: "workspace_state_untrusted"})
			continue
		}
		for _, resource := range manifest.Resources {
			network := resource.ComputeNetwork
			if resource.Contract != "compute" || network == nil || len(network.Ports) == 0 {
				continue
			}
			lease := resource.Consumer + "." + resource.ID
			unresolved := func(reason string) {
				for _, port := range network.Ports {
					rejected = append(rejected, PortRejection{PortBinding: PortBinding{Workspace: workspace.ID, Lease: lease, Protocol: port.Protocol, HostPort: int(port.HostPort), GuestPort: int(port.GuestPort)}, Reason: reason})
				}
			}
			if !leaseIdentifier.MatchString(resource.Consumer) || !leaseIdentifier.MatchString(resource.ID) {
				unresolved("lease_identity_invalid")
				continue
			}
			var record leaseNetworkRecord
			body, err := h.readFile(filepath.Join(base, "state", "resources", lease+".yml"), 1<<20)
			if err != nil || yaml.Unmarshal(body, &record) != nil || record.Actual.LeaseNetwork == nil {
				unresolved("lease_network_unrecorded")
				continue
			}
			recorded := record.Actual.LeaseNetwork
			for _, port := range network.Ports {
				b := PortBinding{Workspace: workspace.ID, Lease: lease, Protocol: port.Protocol, HostPort: int(port.HostPort), Bridge: recorded.Bridge, GuestPort: int(port.GuestPort)}
				for _, slot := range recorded.Slots {
					if slot.Name == port.Slot {
						b.IPv4, b.IPv6 = slot.IPv4, slot.IPv6
					}
				}
				bindings = append(bindings, b)
			}
		}
	}
	if len(bindings) > maxPortBindings {
		return nil, nil, ErrBlocked
	}
	return bindings, rejected, nil
}

var leaseBridgeName = regexp.MustCompile(`^lease[0-9a-f]{10}$`)

// validatePortBinding is INCUS-R-158's per-entry check, short of occupancy:
// the protocol and ports, the approved range, and a slot address inside the
// lease bridge's own subnet that is not the subnet's network, gateway or
// broadcast address.
func (h portHost) validatePortBinding(b PortBinding, first, last int) string {
	if (b.Protocol != computenet.ProtocolTCP && b.Protocol != computenet.ProtocolUDP) || b.GuestPort < 1 || b.GuestPort > 65535 {
		return "binding_invalid"
	}
	if b.HostPort < first || b.HostPort > last {
		return "outside_approved_range"
	}
	if !leaseBridgeName.MatchString(b.Bridge) {
		return "not_a_lease_bridge"
	}
	if b.IPv4 == "" {
		return "slot_address_unknown"
	}
	prefixes, err := h.bridge(b.Bridge)
	if err != nil {
		return "lease_bridge_missing"
	}
	inside := func(value string, v4 bool) bool {
		addr, err := netip.ParseAddr(value)
		if err != nil || addr.Is4() != v4 || addr.Zone() != "" {
			return false
		}
		for _, gateway := range prefixes {
			if gateway.Addr().Is4() != v4 || !gateway.Masked().Contains(addr) || addr == gateway.Addr() || addr == gateway.Masked().Addr() {
				continue
			}
			if v4 && addr == lastAddress(gateway.Masked()) {
				continue
			}
			return true
		}
		return false
	}
	if !inside(b.IPv4, true) || (b.IPv6 != "" && !inside(b.IPv6, false)) {
		return "address_outside_lease"
	}
	return ""
}

func lastAddress(p netip.Prefix) netip.Addr {
	b := p.Addr().As4()
	value := uint32(b[0])<<24 | uint32(b[1])<<16 | uint32(b[2])<<8 | uint32(b[3])
	if bits := 32 - p.Bits(); bits >= 32 {
		value = 0xffffffff
	} else {
		value |= (uint32(1) << bits) - 1
	}
	return netip.AddrFrom4([4]byte{byte(value >> 24), byte(value >> 16), byte(value >> 8), byte(value)})
}

// --- sync -----------------------------------------------------------------

// syncPortBindings converges the host on the frozen deployments. Holds for
// new bindings are taken first, the maps are then replaced in one
// transaction and read back, and only then are holds no binding needs any
// more released (HOSTACT-R-015). A failure before the maps are replaced
// leaves the previous maps in effect.
func (h portHost) syncPortBindings(ctx context.Context, first, last int) (PortSyncResult, error) {
	result := PortSyncResult{Schema: PortSyncSchema, Approved: true, Bindings: []PortBinding{}, Rejected: []PortRejection{}}
	desired, rejected, err := h.frozenPortBindings()
	if err != nil {
		return result, err
	}
	result.Rejected = append(result.Rejected, rejected...)
	held, err := h.enabledHolds()
	if err != nil {
		return result, err
	}
	docker, err := h.dockerPorts(ctx)
	if err != nil {
		return result, errors.Join(ErrExternalEffects, err)
	}
	taken := map[string]bool{}
	var accepted []PortBinding
	for _, b := range desired {
		reason := h.validatePortBinding(b, first, last)
		switch {
		case reason != "":
		case taken[b.key()]:
			reason = "port_taken_by_other_binding"
		case docker[b.key()]:
			reason = "port_published_by_docker"
		case held[b.key()] && h.holdActive(ctx, b.Protocol, b.HostPort):
		case h.portFree(b.Protocol, b.HostPort) != nil:
			reason = "port_in_use"
		}
		if reason != "" {
			result.Rejected = append(result.Rejected, PortRejection{PortBinding: b, Reason: reason})
			continue
		}
		taken[b.key()] = true
		accepted = append(accepted, b)
	}
	// Hold first: a port is never forwarded without its hold.
	var effective []PortBinding
	for _, b := range accepted {
		if !held[b.key()] || !h.holdActive(ctx, b.Protocol, b.HostPort) {
			if err := h.hold(ctx, b.Protocol, b.HostPort); err != nil || !h.holdActive(ctx, b.Protocol, b.HostPort) {
				_ = h.release(ctx, b.Protocol, b.HostPort)
				result.Rejected = append(result.Rejected, PortRejection{PortBinding: b, Reason: "hold_failed"})
				continue
			}
		}
		effective = append(effective, b)
	}
	if current, err := h.chainCurrent(ctx); err != nil {
		return result, err
	} else if !current {
		if err := h.installChain(ctx); err != nil {
			return result, errors.Join(ErrExternalEffects, err)
		}
	}
	want := elementsFor(effective)
	if err := h.loadElements(ctx, portElementsScript(want)); err != nil {
		return result, errors.Join(ErrExternalEffects, err)
	}
	got, err := h.readElements(ctx)
	if err != nil || !slices.Equal(got, want) {
		return result, errors.Join(ErrExternalEffects, err)
	}
	if err := h.saveApplied(effective); err != nil {
		return result, err
	}
	keep := map[string]bool{}
	for _, b := range effective {
		keep[b.key()] = true
	}
	var failures []error
	for key := range held {
		if !keep[key] {
			protocol, port, _ := strings.Cut(key, "/")
			number, _ := strconv.Atoi(port)
			failures = append(failures, h.release(ctx, protocol, number))
		}
	}
	result.Bindings = effective
	sortRejections(result.Rejected)
	return result, errors.Join(failures...)
}

func sortRejections(rejected []PortRejection) {
	sort.SliceStable(rejected, func(i, j int) bool {
		a, b := rejected[i], rejected[j]
		if a.Workspace != b.Workspace {
			return a.Workspace < b.Workspace
		}
		if a.Lease != b.Lease {
			return a.Lease < b.Lease
		}
		return a.key() < b.key()
	})
}

// restorePortBindings runs at boot after the hold units have started: the
// bindings applied before the restart go back into the maps, except those
// whose hold did not come up because another process took the port first
// (INCUS-R-161). The maps are not touched again until the next sync.
func (h portHost) restorePortBindings(ctx context.Context) ([]PortRejection, error) {
	applied, err := h.loadApplied()
	if err != nil {
		return nil, err
	}
	var effective []PortBinding
	var rejected []PortRejection
	for _, b := range applied {
		if h.holdActive(ctx, b.Protocol, b.HostPort) {
			effective = append(effective, b)
		} else {
			rejected = append(rejected, PortRejection{PortBinding: b, Reason: "hold_lost_at_boot"})
		}
	}
	if current, err := h.chainCurrent(ctx); err != nil {
		return rejected, err
	} else if !current {
		if err := h.installChain(ctx); err != nil {
			return rejected, err
		}
	}
	want := elementsFor(effective)
	if err := h.loadElements(ctx, portElementsScript(want)); err != nil {
		return rejected, err
	}
	got, err := h.readElements(ctx)
	if err != nil || !slices.Equal(got, want) {
		return rejected, errors.Join(ErrExternalEffects, err)
	}
	return rejected, nil
}

// PortFinding is one problem anasd's periodic check found with an applied
// binding (INCUS-R-162).
type PortFinding struct {
	Binding PortBinding `json:"binding"`
	Reason  string      `json:"reason"`
}

// checkPortBindings reports, without changing anything, what is wrong with
// the bindings hostd last applied: a lost hold, a Docker publication on the
// same port, or maps that no longer hold what was applied.
func (h portHost) checkPortBindings(ctx context.Context) ([]PortBinding, []PortFinding, error) {
	applied, err := h.loadApplied()
	if err != nil {
		return nil, nil, err
	}
	docker, err := h.dockerPorts(ctx)
	if err != nil {
		return applied, nil, err
	}
	current, err := h.chainCurrent(ctx)
	if err != nil {
		return applied, nil, err
	}
	present := map[portElement]bool{}
	if current {
		elements, err := h.readElements(ctx)
		if err != nil {
			return applied, nil, err
		}
		for _, e := range elements {
			present[e] = true
		}
	}
	var findings []PortFinding
	for _, b := range applied {
		switch {
		case !h.holdActive(ctx, b.Protocol, b.HostPort):
			findings = append(findings, PortFinding{Binding: b, Reason: "hold_lost"})
		case docker[b.key()]:
			findings = append(findings, PortFinding{Binding: b, Reason: "port_published_by_docker"})
		}
		for _, e := range elementsFor([]PortBinding{b}) {
			if !present[e] {
				findings = append(findings, PortFinding{Binding: b, Reason: "rules_drifted"})
				break
			}
		}
	}
	return applied, findings, nil
}

// CheckPortBindings is anasd's read-only check of the applied bindings.
func CheckPortBindings(ctx context.Context) ([]PortBinding, []PortFinding, error) {
	if _, err := os.Lstat(PortBindingsPath); errors.Is(err, os.ErrNotExist) {
		return nil, nil, nil
	}
	return newLocalRuntime().portHost().checkPortBindings(ctx)
}

// --- local implementations --------------------------------------------------

func loadAppliedPortBindings() ([]PortBinding, error) {
	body, err := readRootOnlyFile(PortBindingsPath, 1<<20)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var file portBindingsFile
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&file) != nil || file.Schema != PortBindingsSchema {
		return nil, ErrUnsafeState
	}
	return file.Bindings, nil
}

func bridgePrefixes(name string) ([]netip.Prefix, error) {
	iface, err := net.InterfaceByName(name)
	if err != nil {
		return nil, err
	}
	addrs, err := iface.Addrs()
	if err != nil {
		return nil, err
	}
	var out []netip.Prefix
	for _, addr := range addrs {
		if prefix, err := netip.ParsePrefix(addr.String()); err == nil && !prefix.Addr().IsLinkLocalUnicast() {
			out = append(out, prefix)
		}
	}
	return out, nil
}

// enabledPortHolds lists the hold sockets enabled for boot, by their
// sockets.target links, keyed "tcp/30022".
func enabledPortHolds() (map[string]bool, error) {
	out := map[string]bool{}
	entries, err := os.ReadDir(socketsWantsDir)
	if errors.Is(err, os.ErrNotExist) {
		return out, nil
	}
	if err != nil {
		return nil, err
	}
	for _, entry := range entries {
		match := holdUnitPattern.FindStringSubmatch(entry.Name())
		if match != nil && match[3] == "socket" {
			out[match[1]+"/"+match[2]] = true
		}
	}
	return out, nil
}

func probePortFree(protocol string, port int) error {
	address := ":" + strconv.Itoa(port)
	var closer io.Closer
	var err error
	if protocol == computenet.ProtocolUDP {
		closer, err = net.ListenPacket("udp", address)
	} else {
		closer, err = net.Listen("tcp", address)
	}
	if err != nil {
		if errors.Is(err, syscall.EADDRINUSE) {
			return ErrBlocked
		}
		return err
	}
	return closer.Close()
}

// installPortTable installs the hold unit templates and replaces the table
// with the fixed one, its maps empty. It is configure's step; a sync or a
// restore that finds the table missing or altered runs it as well.
func (r *localRuntime) installPortTable(ctx context.Context) error {
	if err := r.installPortUnitTemplates(ctx); err != nil {
		return err
	}
	// "table" first makes the delete a no-op when the table is absent.
	script := "table inet " + PortTable + "\ndelete table inet " + PortTable + "\n" + portChainRuleset
	if err := r.commands.runWithInput(ctx, fixedNFT, []string{"-c", "-f", "-"}, nil, []byte(portChainRuleset)); err != nil {
		return err
	}
	if err := r.commands.runWithInput(ctx, fixedNFT, []string{"-f", "-"}, nil, []byte(script)); err != nil {
		return err
	}
	installed, err := r.portChainInstalled(ctx)
	if err != nil {
		return err
	}
	if !installed {
		return ErrExternalEffects
	}
	return nil
}

func (r *localRuntime) installPortUnitTemplates(ctx context.Context) error {
	current, err := portUnitTemplatesInstalled()
	if err != nil || current {
		return err
	}
	if err := ensureTrustedRootDirectory(systemdUnitDir, 0755); err != nil {
		return err
	}
	for name, body := range portUnitTemplates {
		if err := writeRootOnlyFile(filepath.Join(systemdUnitDir, name), []byte(body), 0644); err != nil {
			return err
		}
	}
	return r.commands.run(ctx, fixedSystemctl, []string{"daemon-reload"}, nil)
}

func portUnitTemplatesInstalled() (bool, error) {
	for name, body := range portUnitTemplates {
		path := filepath.Join(systemdUnitDir, name)
		if _, err := os.Lstat(path); errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		got, err := readRootOwnedPublicFile(path, 8192)
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		if err != nil {
			return false, err
		}
		if string(got) != body {
			return false, nil
		}
	}
	return true, nil
}

// RemovePortBindings is uninstall's step: every hold, the table, the
// templates and the applied list.
func (r *localRuntime) RemovePortBindings(ctx context.Context) error {
	held, err := enabledPortHolds()
	if err != nil {
		return err
	}
	for key := range held {
		protocol, port, _ := strings.Cut(key, "/")
		number, _ := strconv.Atoi(port)
		socket, service := holdUnits(protocol, number)
		if err := r.commands.run(ctx, fixedSystemctl, []string{"disable", "--now", socket, service}, nil); err != nil {
			return err
		}
	}
	reset := "table inet " + PortTable + "\ndelete table inet " + PortTable + "\n"
	if err := r.commands.runWithInput(ctx, fixedNFT, []string{"-f", "-"}, nil, []byte(reset)); err != nil {
		return err
	}
	for name := range portUnitTemplates {
		if err := os.Remove(filepath.Join(systemdUnitDir, name)); err != nil && !errors.Is(err, os.ErrNotExist) {
			return ErrExternalEffects
		}
	}
	if err := os.Remove(PortBindingsPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return ErrExternalEffects
	}
	return r.commands.run(ctx, fixedSystemctl, []string{"daemon-reload"}, nil)
}

// InstallPortBindings is configure's step.
func (r *localRuntime) InstallPortBindings(ctx context.Context) error {
	return r.installPortTable(ctx)
}

// SyncPortBindings is the sync action's effect within the approved range.
func (r *localRuntime) SyncPortBindings(ctx context.Context, first, last int) (PortSyncResult, error) {
	return r.portHost().syncPortBindings(ctx, first, last)
}

func (r *localRuntime) RestorePortBindings(ctx context.Context) ([]PortRejection, error) {
	return r.portHost().restorePortBindings(ctx)
}

type nftListing struct {
	Nftables []map[string]json.RawMessage `json:"nftables"`
}

// portChainInstalled reads the table back: its marker, the four maps and the
// three chains with the rule counts the fixed ruleset gives them.
func (r *localRuntime) portChainInstalled(ctx context.Context) (bool, error) {
	if current, err := portUnitTemplatesInstalled(); err != nil || !current {
		return false, err
	}
	out, code, err := r.commands.output(ctx, fixedNFT, []string{"-j", "list", "table", "inet", PortTable}, nil)
	if err != nil {
		return false, err
	}
	if code != 0 {
		return false, nil
	}
	return portChainListingCurrent(out), nil
}

func portChainListingCurrent(out []byte) bool {
	var listing nftListing
	if json.Unmarshal(out, &listing) != nil {
		return false
	}
	tableOK, maps, rules := false, map[string]bool{}, map[string]int{}
	chains := map[string]bool{}
	for _, object := range listing.Nftables {
		var item struct {
			Family  string `json:"family"`
			Name    string `json:"name"`
			Table   string `json:"table"`
			Chain   string `json:"chain"`
			Comment string `json:"comment"`
		}
		for kind, raw := range object {
			if json.Unmarshal(raw, &item) != nil {
				continue
			}
			switch kind {
			case "table":
				tableOK = item.Family == "inet" && item.Name == PortTable && item.Comment == portTableComment
			case "map":
				maps[item.Name] = item.Table == PortTable
			case "chain":
				chains[item.Name] = item.Table == PortTable
			case "rule":
				if item.Table == PortTable {
					rules[item.Chain]++
				}
			}
		}
	}
	if !tableOK || len(maps) != len(portMaps) || len(chains) != len(portChainRules) {
		return false
	}
	for _, name := range portMaps {
		if !maps[name] {
			return false
		}
	}
	for name, count := range portChainRules {
		if !chains[name] || rules[name] != count {
			return false
		}
	}
	return true
}

func (r *localRuntime) readPortElements(ctx context.Context) ([]portElement, error) {
	out, code, err := r.commands.output(ctx, fixedNFT, []string{"-j", "list", "table", "inet", PortTable}, nil)
	if err != nil {
		return nil, err
	}
	if code != 0 {
		return nil, ErrExternalEffects
	}
	return parsePortElements(out)
}

// parsePortElements reads the maps' elements from `nft -j list table`. An
// element is [port, {"concat": [address, port]}], or the same pair wrapped
// as {"elem": {"val": ...}} by nft versions that print element options.
func parsePortElements(out []byte) ([]portElement, error) {
	var listing nftListing
	if err := json.Unmarshal(out, &listing); err != nil {
		return nil, ErrExternalEffects
	}
	elements := []portElement{}
	for _, object := range listing.Nftables {
		raw, ok := object["map"]
		if !ok {
			continue
		}
		var m struct {
			Name  string            `json:"name"`
			Table string            `json:"table"`
			Elem  []json.RawMessage `json:"elem"`
		}
		if json.Unmarshal(raw, &m) != nil || m.Table != PortTable || !slices.Contains(portMaps, m.Name) {
			continue
		}
		for _, item := range m.Elem {
			var pair []json.RawMessage
			if json.Unmarshal(item, &pair) != nil || len(pair) != 2 {
				return nil, ErrExternalEffects
			}
			var port int
			if json.Unmarshal(pair[0], &port) != nil {
				var wrapped struct {
					Elem struct {
						Val int `json:"val"`
					} `json:"elem"`
				}
				if json.Unmarshal(pair[0], &wrapped) != nil || wrapped.Elem.Val == 0 {
					return nil, ErrExternalEffects
				}
				port = wrapped.Elem.Val
			}
			var target struct {
				Concat []json.RawMessage `json:"concat"`
			}
			if json.Unmarshal(pair[1], &target) != nil || len(target.Concat) != 2 {
				return nil, ErrExternalEffects
			}
			var address string
			var guest int
			if json.Unmarshal(target.Concat[0], &address) != nil || json.Unmarshal(target.Concat[1], &guest) != nil {
				return nil, ErrExternalEffects
			}
			elements = append(elements, portElement{Map: m.Name, Port: port, Address: canonicalAddress(address), Target: guest})
		}
	}
	sortElements(elements)
	return elements, nil
}

type dockerPortSummary struct {
	Ports []struct {
		PublicPort int    `json:"PublicPort"`
		Type       string `json:"Type"`
	} `json:"Ports"`
}

// dockerContainersPath lists running containers with their published ports.
const dockerContainersPath = "/v1.44/containers/json"

// publishedPorts is every host port a running container publishes, keyed
// "tcp/8080". Docker without its userland proxy holds no socket for them, so
// a bind probe alone would miss them.
func (c *dockerClient) publishedPorts(ctx context.Context) (map[string]bool, error) {
	var raw json.RawMessage
	if err := c.do(ctx, "GET", dockerContainersPath, nil, &raw); err != nil {
		return nil, err
	}
	var containers []dockerPortSummary
	if err := json.Unmarshal(raw, &containers); err != nil {
		return nil, ErrExternalEffects
	}
	out := map[string]bool{}
	for _, container := range containers {
		for _, port := range container.Ports {
			if port.PublicPort > 0 && (port.Type == "tcp" || port.Type == "udp") {
				out[port.Type+"/"+strconv.Itoa(port.PublicPort)] = true
			}
		}
	}
	return out, nil
}
