package incusprovision

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"
)

// The host side of the compute lease network policy (INCUS-R-118, R-126):
//
//   - a fixed set of FORWARD rules that hand forwarding to and from lease
//     bridges to each lease's Incus ACL, after the administrator's and
//     Docker's own rules, and that let a lease reach a Docker bridge only on a
//     connection Docker itself translated to a published port;
//   - the anas-traefik address set the lease ACLs name, which only hostd
//     writes, from the Traefik containers it reads from Docker;
//   - the approved port range for port bindings, which Core reads back;
//   - a boot unit that restores the rules ANAS owns after a host restart.
//
// None of these is per lease or per instance: incus.configure installs them
// once and incus.uninstall removes them.

const (
	// LeaseBridgeMatch is the interface glob for every lease bridge; the
	// Provider names them "lease" + 10 hex digits (INCUS-R-127).
	LeaseBridgeMatch = "lease+"

	NetworkPolicySchema = "anas.incus-host-network/v1"
	NetworkPolicyPath   = "/var/lib/anas/incus-host/network.json"

	TraefikAddressSet = "anas-traefik"
	traefikSetMarker  = "traefik"
	// TraefikInstanceLabel is the label the Traefik Module puts on its
	// container; hostd reads the addresses of containers carrying it.
	TraefikInstanceLabel = "anas.traefik.instance"

	NetworkUnitName = "anas-incus-network.service"
	NetworkUnitPath = "/etc/systemd/system/" + NetworkUnitName
	hostdExecutable = "/usr/local/lib/anas/anas-hostd"

	DefaultPortRangeFirst = 30000
	DefaultPortRangeLast  = 32767

	iptablesPath  = "/usr/sbin/iptables"
	ip6tablesPath = "/usr/sbin/ip6tables"
)

// leaseForwardRules are appended to FORWARD in this order. Each carries its
// own comment so a readback can find it and check the order without parsing
// the rest of an administrator's chain.
var leaseForwardRules = [][]string{
	{"-i", LeaseBridgeMatch, "-o", "docker0", "-m", "conntrack", "--ctstate", "DNAT", "-m", "comment", "--comment", "anas-lease-forward-1", "-j", "ACCEPT"},
	{"-i", LeaseBridgeMatch, "-o", "br-+", "-m", "conntrack", "--ctstate", "DNAT", "-m", "comment", "--comment", "anas-lease-forward-2", "-j", "ACCEPT"},
	{"-i", LeaseBridgeMatch, "-o", "docker0", "-m", "comment", "--comment", "anas-lease-forward-3", "-j", "DROP"},
	{"-i", LeaseBridgeMatch, "-o", "br-+", "-m", "comment", "--comment", "anas-lease-forward-4", "-j", "DROP"},
	{"-i", LeaseBridgeMatch, "-m", "comment", "--comment", "anas-lease-forward-5", "-j", "ACCEPT"},
	{"-o", LeaseBridgeMatch, "-m", "comment", "--comment", "anas-lease-forward-6", "-j", "ACCEPT"},
}

// networkUnitBody restores the rules ANAS owns after a restart, once Docker
// has rebuilt its own chains, and before Incus starts its bridges. It runs
// the installed hostd binary; it takes no arguments from anywhere else.
const networkUnitBody = `# Managed by ANAS (incus.configure). Restores the host network rules ANAS owns.
[Unit]
Description=ANAS Incus host network rules
After=docker.service network-online.target
Wants=network-online.target
Before=incus.service
PartOf=docker.service

[Service]
Type=oneshot
RemainAfterExit=yes
ExecStart=` + hostdExecutable + ` --restore-network
NoNewPrivileges=true
ProtectHome=true
PrivateTmp=true

[Install]
WantedBy=multi-user.target
`

type networkPolicyFile struct {
	Schema    string    `json:"schema"`
	PortRange portRange `json:"port_range"`
}

type portRange struct {
	First int `json:"first"`
	Last  int `json:"last"`
}

func networkPolicyBody(first, last int) ([]byte, error) {
	if first < 1024 || last > 65535 || first > last {
		return nil, ErrInvalid
	}
	return json.Marshal(networkPolicyFile{Schema: NetworkPolicySchema, PortRange: portRange{First: first, Last: last}})
}

// xtables runs iptables or ip6tables. Both are alternatives symlinks to a
// multi-call binary; the link is resolved and checked like every other fixed
// executable, then run under the applet name the binary dispatches on.
func (c fixedCommands) xtables(ctx context.Context, tool string, args []string) ([]byte, int, error) {
	link := map[string]string{"iptables": iptablesPath, "ip6tables": ip6tablesPath}[tool]
	if link == "" || ctx == nil {
		return nil, -1, ErrInvalid
	}
	for _, arg := range args {
		if arg == "" || strings.ContainsAny(arg, "\x00\r\n") {
			return nil, -1, ErrInvalid
		}
	}
	resolved, err := filepath.EvalSymlinks(link)
	if err != nil {
		return nil, -1, ErrUnsupported
	}
	base := filepath.Base(resolved)
	if (base != "xtables-nft-multi" && base != "xtables-legacy-multi" && base != tool) || executableUsable(resolved) != nil {
		return nil, -1, ErrUnsupported
	}
	runCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(runCtx, resolved, args...)
	cmd.Args[0] = tool
	cmd.Env = []string{"PATH=/usr/sbin:/usr/bin:/sbin:/bin", "LANG=C", "LC_ALL=C"}
	var stdout, stderr limitedBuffer
	stdout.limit, stderr.limit = 1<<20, 16<<10
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	cmd.WaitDelay = 5 * time.Second
	err = cmd.Run()
	if runCtx.Err() != nil {
		return nil, -1, runCtx.Err()
	}
	if stdout.Truncated() {
		return nil, -1, ErrExternalEffects
	}
	if err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			return stdout.Bytes(), exit.ExitCode(), nil
		}
		return nil, -1, ErrExternalEffects
	}
	return stdout.Bytes(), 0, nil
}

// ApplyLeaseForwarding removes any copy of each rule and appends the set
// again, so the rules sit after whatever Docker or an administrator inserted
// and there is never more than one copy (INCUS-R-126).
func (r *localRuntime) ApplyLeaseForwarding(ctx context.Context) error {
	for _, tool := range []string{"iptables", "ip6tables"} {
		if err := r.removeLeaseForwarding(ctx, tool); err != nil {
			return err
		}
		for _, rule := range leaseForwardRules {
			_, code, err := r.commands.xtables(ctx, tool, append([]string{"-w", "5", "-A", "FORWARD"}, rule...))
			if err != nil {
				return err
			}
			if code != 0 {
				return ErrExternalEffects
			}
		}
	}
	installed, err := r.leaseForwardingInstalled(ctx)
	if err != nil {
		return err
	}
	if !installed {
		return ErrExternalEffects
	}
	return nil
}

func (r *localRuntime) RemoveLeaseForwarding(ctx context.Context) error {
	for _, tool := range []string{"iptables", "ip6tables"} {
		if err := r.removeLeaseForwarding(ctx, tool); err != nil {
			return err
		}
	}
	return nil
}

func (r *localRuntime) removeLeaseForwarding(ctx context.Context, tool string) error {
	for _, rule := range leaseForwardRules {
		// -D removes one copy; repeat until -C reports none, bounded.
		for attempt := 0; attempt < 16; attempt++ {
			_, code, err := r.commands.xtables(ctx, tool, append([]string{"-w", "5", "-C", "FORWARD"}, rule...))
			if err != nil {
				return err
			}
			if code != 0 {
				break
			}
			if _, code, err = r.commands.xtables(ctx, tool, append([]string{"-w", "5", "-D", "FORWARD"}, rule...)); err != nil || code != 0 {
				return errors.Join(ErrExternalEffects, err)
			}
			if attempt == 15 {
				return ErrExternalEffects
			}
		}
	}
	return nil
}

// leaseForwardingInstalled reads FORWARD back in both families: every rule
// present once, in order, and after Docker's own jumps.
func (r *localRuntime) leaseForwardingInstalled(ctx context.Context) (bool, error) {
	for _, tool := range []string{"iptables", "ip6tables"} {
		out, code, err := r.commands.xtables(ctx, tool, []string{"-w", "5", "-S", "FORWARD"})
		if err != nil {
			return false, err
		}
		if code != 0 {
			return false, ErrExternalEffects
		}
		if !leaseForwardingOrdered(string(out)) {
			return false, nil
		}
	}
	return true, nil
}

func leaseForwardingOrdered(listing string) bool {
	lastDocker, next := -1, 1
	positions := map[int]int{}
	for i, line := range strings.Split(listing, "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 4 && fields[0] == "-A" && fields[1] == "FORWARD" && fields[len(fields)-2] == "-j" &&
			strings.HasPrefix(fields[len(fields)-1], "DOCKER") {
			lastDocker = i
		}
		for n := 1; n <= len(leaseForwardRules); n++ {
			if strings.Contains(line, "anas-lease-forward-"+strconv.Itoa(n)+" ") || strings.HasSuffix(line, "anas-lease-forward-"+strconv.Itoa(n)) ||
				strings.Contains(line, `"anas-lease-forward-`+strconv.Itoa(n)+`"`) {
				if _, seen := positions[n]; seen {
					return false
				}
				positions[n] = i
			}
		}
	}
	previous := lastDocker
	for ; next <= len(leaseForwardRules); next++ {
		position, ok := positions[next]
		if !ok || position <= previous {
			return false
		}
		previous = position
	}
	return true
}

// InstallNetworkUnit writes the boot unit and enables it; the rules it
// restores are already in place from this configure.
func (r *localRuntime) InstallNetworkUnit(ctx context.Context) error {
	if err := ensureTrustedRootDirectory(filepath.Dir(NetworkUnitPath), 0755); err != nil {
		return err
	}
	if err := writeRootOnlyFile(NetworkUnitPath, []byte(networkUnitBody), 0644); err != nil {
		return err
	}
	if err := r.commands.run(ctx, fixedSystemctl, []string{"daemon-reload"}, nil); err != nil {
		return err
	}
	return r.commands.run(ctx, fixedSystemctl, []string{"enable", NetworkUnitName}, nil)
}

func (r *localRuntime) RemoveNetworkUnit(ctx context.Context) error {
	if installed, _ := networkUnitInstalledAt(NetworkUnitPath); installed {
		if err := r.commands.run(ctx, fixedSystemctl, []string{"disable", NetworkUnitName}, nil); err != nil {
			return err
		}
	}
	if err := os.Remove(NetworkUnitPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return ErrExternalEffects
	}
	return r.commands.run(ctx, fixedSystemctl, []string{"daemon-reload"}, nil)
}

func networkUnitInstalledAt(path string) (bool, error) {
	if _, err := os.Lstat(path); errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	body, err := readRootOwnedPublicFile(path, 8192)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return string(body) == networkUnitBody, nil
}

// WriteNetworkPolicy records the port range the operator approved.
func (r *localRuntime) WriteNetworkPolicy(_ context.Context, first, last int) error {
	body, err := networkPolicyBody(first, last)
	if err != nil {
		return err
	}
	if err := ensureTrustedRootDirectory(filepath.Dir(NetworkPolicyPath), 0700); err != nil {
		return err
	}
	return writeRootOnlyFile(NetworkPolicyPath, body, 0600)
}

func (r *localRuntime) RemoveNetworkPolicy(context.Context) error {
	if err := os.Remove(NetworkPolicyPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return ErrExternalEffects
	}
	return nil
}

func networkPolicyCurrentAt(path string, first, last int) (bool, error) {
	want, err := networkPolicyBody(first, last)
	if err != nil {
		return false, err
	}
	if _, err := os.Lstat(path); errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	body, err := readRootOnlyFile(path, 4096)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return bytes.Equal(body, want), nil
}

type incusAddressSet struct {
	Name        string            `json:"name,omitempty"`
	Description string            `json:"description,omitempty"`
	Addresses   []string          `json:"addresses"`
	Config      map[string]string `json:"config"`
}

// EnsureTraefikAddressSet creates the set the lease ACLs name, then fills it.
// A set of that name ANAS did not create is refused, never adopted.
func (r *localRuntime) EnsureTraefikAddressSet(ctx context.Context) error {
	var current incusAddressSet
	err := r.incus.doSyncOnly(ctx, "GET", "/1.0/network-address-sets/"+TraefikAddressSet+"?project=default", nil, &current)
	switch {
	case err == nil:
		if current.Config["user.anas.managed"] != traefikSetMarker {
			return ErrBlocked
		}
	case errors.Is(err, errIncusNotFound):
		if err := r.incus.doSyncOnly(ctx, "POST", "/1.0/network-address-sets?project=default", incusAddressSet{
			Name: TraefikAddressSet, Description: "ANAS: the Traefik containers compute leases may reach",
			Addresses: []string{}, Config: map[string]string{"user.anas.managed": traefikSetMarker},
		}, nil); err != nil {
			return err
		}
	default:
		return err
	}
	_, err = r.SyncTraefikAddressSet(ctx)
	return err
}

// SyncTraefikAddressSet replaces the set with the addresses of the running
// Traefik containers hostd itself reads from Docker. Nothing in a request
// names an address (INCUS-R-119); with no Traefik running the set is empty.
func (r *localRuntime) SyncTraefikAddressSet(ctx context.Context) ([]string, error) {
	var current incusAddressSet
	if err := r.incus.doSyncOnly(ctx, "GET", "/1.0/network-address-sets/"+TraefikAddressSet+"?project=default", nil, &current); err != nil {
		return nil, err
	}
	if current.Config["user.anas.managed"] != traefikSetMarker {
		return nil, ErrBlocked
	}
	addresses, err := r.docker.traefikAddresses(ctx)
	if err != nil {
		return nil, err
	}
	desired := incusAddressSet{Description: current.Description, Addresses: addresses, Config: current.Config}
	if err := r.incus.doSyncOnly(ctx, "PUT", "/1.0/network-address-sets/"+TraefikAddressSet+"?project=default", desired, nil); err != nil {
		return nil, err
	}
	var actual incusAddressSet
	if err := r.incus.doSyncOnly(ctx, "GET", "/1.0/network-address-sets/"+TraefikAddressSet+"?project=default", nil, &actual); err != nil {
		return nil, err
	}
	got := normalizeAddresses(actual.Addresses)
	if !slices.Equal(got, addresses) {
		return nil, ErrExternalEffects
	}
	return addresses, nil
}

func (r *localRuntime) RemoveTraefikAddressSet(ctx context.Context) error {
	var current incusAddressSet
	err := r.incus.doSyncOnly(ctx, "GET", "/1.0/network-address-sets/"+TraefikAddressSet+"?project=default", nil, &current)
	if errors.Is(err, errIncusNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if current.Config["user.anas.managed"] != traefikSetMarker {
		return ErrBlocked
	}
	err = r.incus.doSyncOnly(ctx, "DELETE", "/1.0/network-address-sets/"+TraefikAddressSet+"?project=default", nil, nil)
	if errors.Is(err, errIncusNotFound) {
		return nil
	}
	return err
}

func (r *localRuntime) traefikAddressSetExists(ctx context.Context) (bool, error) {
	var current incusAddressSet
	err := r.incus.doSyncOnly(ctx, "GET", "/1.0/network-address-sets/"+TraefikAddressSet+"?project=default", nil, &current)
	if errors.Is(err, errIncusNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return current.Config["user.anas.managed"] == traefikSetMarker, nil
}

func normalizeAddresses(in []string) []string {
	out := []string{}
	for _, value := range in {
		value = strings.TrimSpace(value)
		if prefix, err := netip.ParsePrefix(value); err == nil {
			out = append(out, prefix.Masked().String())
		} else if addr, err := netip.ParseAddr(value); err == nil {
			out = append(out, netip.PrefixFrom(addr, addr.BitLen()).String())
		}
	}
	slices.Sort(out)
	return slices.Compact(out)
}

type dockerContainerSummary struct {
	ID              string            `json:"Id"`
	State           string            `json:"State"`
	Labels          map[string]string `json:"Labels"`
	NetworkSettings struct {
		Networks map[string]struct {
			IPAddress         string `json:"IPAddress"`
			GlobalIPv6Address string `json:"GlobalIPv6Address"`
		} `json:"Networks"`
	} `json:"NetworkSettings"`
}

// traefikContainersPath is the one container listing hostd may make: running
// containers that carry the Traefik Module's instance label.
var traefikContainersPath = "/v1.44/containers/json?filters=" +
	strings.ReplaceAll(strings.ReplaceAll(`{"label":["`+TraefikInstanceLabel+`"],"status":["running"]}`, `"`, "%22"), " ", "")

func (c *dockerClient) traefikAddresses(ctx context.Context) ([]string, error) {
	var raw json.RawMessage
	if err := c.do(ctx, "GET", traefikContainersPath, nil, &raw); err != nil {
		return nil, err
	}
	var containers []dockerContainerSummary
	decoder := json.NewDecoder(bytes.NewReader(raw))
	if err := decoder.Decode(&containers); err != nil || decoder.Decode(&struct{}{}) != io.EOF {
		return nil, ErrExternalEffects
	}
	var addresses []string
	for _, container := range containers {
		if container.State != "running" || container.Labels[TraefikInstanceLabel] == "" {
			continue
		}
		for _, network := range container.NetworkSettings.Networks {
			for _, value := range []string{network.IPAddress, network.GlobalIPv6Address} {
				if addr, err := netip.ParseAddr(value); err == nil && !addr.IsLoopback() && !addr.IsUnspecified() {
					addresses = append(addresses, value)
				}
			}
		}
	}
	return normalizeAddresses(addresses), nil
}
