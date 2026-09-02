package runner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/anas-project/ANAS/internal/compose"
)

func ensureMacvlan(env map[string]string, _ compose.CLI) error {
	name := env["VLAN_INTERFACE"]
	matches, exists, err := inspectMacvlan(name, env)
	if err != nil {
		return err
	}
	if exists && !matches {
		return fmt.Errorf("docker network %q exists with different macvlan settings; refusing to replace it automatically. Stop the deployment first (anas stop), which removes the network, then apply the new addressing", name)
	}
	// Only on the way to creating the network. Once it exists our own container
	// is the thing answering on these addresses, and a probe would report the
	// deployment as its own conflict. That leaves one gap -- an address leased
	// away while the container was down -- which no probe can close from here
	// and which the router's pool exclusion is the real answer to.
	if !exists {
		if err := checkLANAddressConflicts(env); err != nil {
			return err
		}
	}
	if err := runHostNetHelper(env, bridgeUpArgs(env)...); err != nil {
		return fmt.Errorf("create macvlan bridge: %w", err)
	}
	if exists {
		return nil
	}
	if err := exec.Command("docker", networkCreateArgs(name, env)...).Run(); err != nil {
		_ = runHostNetHelper(env, bridgeDownArgs(env)...)
		return fmt.Errorf("create docker macvlan network: %w", err)
	}
	return nil
}

func (a *app) ensureMacvlan() error {
	if a == nil {
		return errors.New("network application is unavailable")
	}
	if !a.restrictedProcessEnvironment {
		return ensureMacvlan(a.env, a.compose)
	}
	ctx := a.subprocessContext()
	environment := a.commandEnvironment(nil)
	name := a.env["VLAN_INTERFACE"]
	matches, exists, err := inspectMacvlanContext(ctx, environment, name, a.env)
	if err != nil {
		return err
	}
	if exists && !matches {
		return fmt.Errorf("docker network %q exists with different macvlan settings; refusing to replace it automatically. Stop the deployment first (anas stop), which removes the network, then apply the new addressing", name)
	}
	if !exists {
		if err := checkLANAddressConflictsContext(ctx, environment, a.env); err != nil {
			return err
		}
	}
	if err := runHostNetHelperContext(ctx, environment, a.env, bridgeUpArgs(a.env)...); err != nil {
		return fmt.Errorf("create macvlan bridge: %w", err)
	}
	if exists {
		return nil
	}
	cmd := exec.CommandContext(ctx, "docker", networkCreateArgs(name, a.env)...)
	cmd.Env = environment
	cmd.Stdout, cmd.Stderr = io.Discard, io.Discard
	if err := cmd.Run(); err != nil {
		cleanupContext, cancelCleanup := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancelCleanup()
		_ = runHostNetHelperContext(cleanupContext, environment, a.env, bridgeDownArgs(a.env)...)
		return fmt.Errorf("create docker macvlan network: %w", err)
	}
	return nil
}

// bridgeUpArgs and bridgeDownArgs are the helper invocations that replace the
// shell script this used to generate into the runtime base directory and run
// through sudo. The script was the weak part of that arrangement: a sudoers
// rule authorising root to execute a file in a directory the invoking user can
// write is, for that user, indistinguishable from root. The helper is
// root-owned, takes named operations, and validates them itself.
func bridgeUpArgs(env map[string]string) []string {
	args := []string{"bridge", "up",
		"--name", env["VLAN_BRIDGE_INTERFACE"],
		"--parent", env["INTERFACE"],
		"--address", env["VLAN_BRIDGE_IP"] + "/" + env["VLAN_SUBNET_MASK"],
	}
	for _, route := range hostLANRoutes(env) {
		args = append(args, "--route", route)
	}
	return args
}

func bridgeDownArgs(env map[string]string) []string {
	return []string{"bridge", "down", "--name", env["VLAN_BRIDGE_INTERFACE"]}
}

// networkCreateArgs builds the docker macvlan network. --ip-range is omitted
// when the deployment pins its container address: the range exists to keep
// IPAM's own choices inside a corner of the segment, and a pinned address is
// not IPAM's choice. Passing both would make docker reject any pinned address
// outside the range.
func networkCreateArgs(name string, env map[string]string) []string {
	args := []string{"network", "create",
		"-d", "macvlan",
		"-o", "parent=" + env["INTERFACE"],
		"--subnet", env["HOST_SEGMENT"],
		"--gateway", env["VLAN_GATEWAY_IP"],
	}
	if segment := env["VLAN_SEGMENT"]; segment != "" {
		args = append(args, "--ip-range", segment)
	}
	return append(args, "--aux-address", "bridge="+env["VLAN_BRIDGE_IP"], name)
}

// hostLANRoutes is what the host needs routed through the bridge: every
// address this deployment puts on the segment except the bridge's own, which
// is on the bridge already and would be routing to itself.
func hostLANRoutes(env map[string]string) []string {
	routes := []string{}
	for _, addr := range hostLANAddresses(env) {
		if addr != env["VLAN_BRIDGE_IP"] {
			routes = append(routes, addr+"/32")
		}
	}
	return routes
}

// hostLANAddresses is every address this deployment puts on the host segment.
func hostLANAddresses(env map[string]string) []string {
	out := []string{}
	for _, addr := range []string{env["VLAN_BRIDGE_IP"], env["HOST_LAN_IP"]} {
		if strings.TrimSpace(addr) != "" {
			out = append(out, addr)
		}
	}
	return out
}

// checkLANAddressConflicts refuses to take an address something on the segment
// is already answering for.
//
// This is the one thing that turns a silent address collision into a stopped
// deployment. Docker's IPAM does not detect duplicates, and neither side of a
// collision reports one: both hosts simply answer, and the symptom surfaces
// later as intermittent connection failures that look like anything but a file
// server's address.
func checkLANAddressConflicts(env map[string]string) error {
	if strings.EqualFold(strings.TrimSpace(env["HOST_LAN_ARP_CHECK"]), "false") {
		return nil
	}
	for _, addr := range hostLANAddresses(env) {
		mac, err := arpProbe(env["NETWORK_NAMESPACE_PATH"], addr)
		if err != nil {
			// The probe is a safety net, not a dependency: a host without the
			// tools it needs must still be able to deploy.
			continue
		}
		if mac != "" {
			return fmt.Errorf("%s is already in use on the local segment by %s. "+
				"Choose a free address with `anas config set global.host_lan_ip <address>` "+
				"(and global.host_lan_bridge_ip for the bridge), or exclude this address from "+
				"the router's DHCP pool. Set global.host_lan_arp_check to false to skip this check",
				addr, mac)
		}
	}
	return nil
}

func checkLANAddressConflictsContext(ctx context.Context, environment []string, env map[string]string) error {
	if strings.EqualFold(strings.TrimSpace(env["HOST_LAN_ARP_CHECK"]), "false") {
		return nil
	}
	for _, addr := range hostLANAddresses(env) {
		mac, err := arpProbeContext(ctx, environment, env["NETWORK_NAMESPACE_PATH"], addr)
		if err != nil {
			if contextErr := ctx.Err(); contextErr != nil {
				return contextErr
			}
			continue
		}
		if mac != "" {
			return fmt.Errorf("%s is already in use on the local segment by %s. "+
				"Choose a free address with `anas config set global.host_lan_ip <address>` "+
				"(and global.host_lan_bridge_ip for the bridge), or exclude this address from "+
				"the router's DHCP pool. Set global.host_lan_arp_check to false to skip this check",
				addr, mac)
		}
	}
	return nil
}

// arpProbe returns the hardware address answering for addr, or "" when nothing
// does.
//
// It asks at the ARP layer rather than with a ping alone, because a host that
// drops ICMP still has to answer ARP to exist on the segment at all. The ping
// is only what provokes the kernel into resolving the address; the answer is
// read out of the neighbour table afterwards.
func arpProbe(namespacePath, addr string) (string, error) {
	if strings.TrimSpace(addr) == "" {
		return "", nil
	}
	ping, err := probeCommand(namespacePath, "ping", "-c", "1", "-W", "1", addr)
	if err != nil {
		return "", err
	}
	// A silent address is the expected case, so the ping's own failure says
	// nothing; only the neighbour table does.
	_ = ping.Run()
	neigh, err := probeCommand(namespacePath, "ip", "-4", "neigh", "show", addr)
	if err != nil {
		return "", err
	}
	out, err := neigh.Output()
	if err != nil {
		return "", err
	}
	return reachableNeighbour(string(out)), nil
}

func arpProbeContext(ctx context.Context, environment []string, namespacePath, addr string) (string, error) {
	if strings.TrimSpace(addr) == "" {
		return "", nil
	}
	ping, err := probeCommandContext(ctx, environment, namespacePath, "ping", "-c", "1", "-W", "1", addr)
	if err != nil {
		return "", err
	}
	_ = ping.Run()
	if err := ctx.Err(); err != nil {
		return "", err
	}
	neigh, err := probeCommandContext(ctx, environment, namespacePath, "ip", "-4", "neigh", "show", addr)
	if err != nil {
		return "", err
	}
	out, err := neigh.Output()
	if err != nil {
		if contextErr := ctx.Err(); contextErr != nil {
			return "", contextErr
		}
		return "", err
	}
	return reachableNeighbour(string(out)), nil
}

// reachableNeighbour reads `ip neigh show` output and returns the hardware
// address only for a confirmed-reachable entry.
//
// The state matters. An entry can carry a hardware address from an earlier
// deployment of our own and sit there STALE long after that container is gone;
// treating it as an occupant would make the runner refuse to start over an
// address nobody holds. REACHABLE means the kernel got an answer just now,
// which is exactly the question being asked -- and the direction of the
// remaining error is the safe one: a probe that resolves too slowly reports no
// conflict rather than a false one.
func reachableNeighbour(out string) string {
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(line)
		mac, reachable := "", false
		for i, field := range fields {
			if field == "lladdr" && i+1 < len(fields) {
				mac = fields[i+1]
			}
			if field == "REACHABLE" {
				reachable = true
			}
		}
		if mac != "" && reachable {
			return mac
		}
	}
	return ""
}

// probeCommand runs a read-only network query where the deployment's addresses
// live. Without a namespace that is this process's own network, and the query
// needs no privilege at all -- which is the point, since the alternative would
// have been to widen the one sudo boundary this program has for a check that
// only looks. A configured namespace is a test or isolation environment where
// entering it is itself privileged, so there the query goes through the same
// door as everything else.
func probeCommand(namespacePath, name string, args ...string) (*exec.Cmd, error) {
	if namespacePath == "" {
		return exec.Command(name, args...), nil
	}
	if !filepath.IsAbs(namespacePath) {
		return nil, fmt.Errorf("NETWORK_NAMESPACE_PATH must be absolute: %q", namespacePath)
	}
	full := append([]string{"nsenter", "--net=" + filepath.Clean(namespacePath), name}, args...)
	return exec.Command("sudo", full...), nil
}

func probeCommandContext(ctx context.Context, environment []string, namespacePath, name string, args ...string) (*exec.Cmd, error) {
	var cmd *exec.Cmd
	if namespacePath == "" {
		cmd = exec.CommandContext(ctx, name, args...)
	} else {
		if !filepath.IsAbs(namespacePath) {
			return nil, fmt.Errorf("NETWORK_NAMESPACE_PATH must be absolute: %q", namespacePath)
		}
		full := append([]string{"nsenter", "--net=" + filepath.Clean(namespacePath), name}, args...)
		cmd = exec.CommandContext(ctx, "sudo", full...)
	}
	cmd.Env = append([]string(nil), environment...)
	return cmd, nil
}

// hostNetHelperArgs is the command that applies one bridge operation.
//
// The helper is looked up next to the anas binary first, then in the location
// the installer uses, and only then on PATH. That order matters because the
// helper is the thing holding CAP_NET_ADMIN: PATH belongs to whoever invoked
// anas, so finding it there is a development convenience rather than how a
// deployment is meant to resolve it.
//
// A configured namespace is an isolation or test environment. Entering one is
// itself privileged, so that path keeps the sudo it always had -- and it is
// the only path that has any.
func hostNetHelperArgs(namespacePath string, args ...string) ([]string, error) {
	helper, err := findHostNetHelper()
	if err != nil {
		return nil, err
	}
	if namespacePath == "" {
		return append([]string{helper}, args...), nil
	}
	if !filepath.IsAbs(namespacePath) {
		return nil, fmt.Errorf("NETWORK_NAMESPACE_PATH must be absolute: %q", namespacePath)
	}
	full := []string{"sudo", "nsenter", "--net=" + filepath.Clean(namespacePath), helper}
	return append(full, args...), nil
}

// hostNetHelperName is the binary that performs the privileged part of host
// LAN setup. It is overridable only from tests in this package.
var hostNetHelperName = "anas-helper"

// hostNetHelperInstallPath is where the installer puts it: a root-owned
// directory, because a helper that root executes must not live anywhere the
// invoking user can write. A variable only so that tests can point the lookup
// somewhere they can write.
var hostNetHelperInstallPath = "/usr/local/lib/anas"

func findHostNetHelper() (string, error) {
	candidates := []string{}
	if self, err := os.Executable(); err == nil {
		candidates = append(candidates, filepath.Join(filepath.Dir(self), hostNetHelperName))
	}
	candidates = append(candidates, filepath.Join(hostNetHelperInstallPath, hostNetHelperName))
	for _, candidate := range candidates {
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate, nil
		}
	}
	found, err := exec.LookPath(hostNetHelperName)
	if err != nil {
		return "", fmt.Errorf("%s is not installed. It performs the host network setup this "+
			"deployment needs and holds the one capability anas requires; install it beside the "+
			"anas binary or in %s. Looked in: %s, and PATH",
			hostNetHelperName, hostNetHelperInstallPath, strings.Join(candidates, ", "))
	}
	return found, nil
}

func runHostNetHelper(env map[string]string, args ...string) error {
	commandArgs, err := hostNetHelperArgs(env["NETWORK_NAMESPACE_PATH"], args...)
	if err != nil {
		return err
	}
	cmd := exec.Command(commandArgs[0], commandArgs[1:]...)
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func runHostNetHelperContext(ctx context.Context, environment []string, env map[string]string, args ...string) error {
	commandArgs, err := hostNetHelperArgs(env["NETWORK_NAMESPACE_PATH"], args...)
	if err != nil {
		return err
	}
	cmd := exec.CommandContext(ctx, commandArgs[0], commandArgs[1:]...)
	cmd.Env = append([]string(nil), environment...)
	cmd.Stdout, cmd.Stderr = io.Discard, io.Discard
	if err := cmd.Run(); err != nil {
		if contextErr := ctx.Err(); contextErr != nil {
			return contextErr
		}
		return err
	}
	return nil
}

func removeMacvlan(env map[string]string) error {
	bridge := env["VLAN_BRIDGE_INTERFACE"]
	if bridge == "" {
		// Nothing was ever planned, so nothing was ever created. This used to
		// be answered by the presence of a generated script; the bridge name is
		// the same answer without a file to keep in sync.
		return nil
	}
	name := env["VLAN_INTERFACE"]
	if name == "" {
		name = macvlanNetworkName
	}
	if err := exec.Command("docker", "network", "inspect", name).Run(); err == nil {
		if err := exec.Command("docker", "network", "rm", name).Run(); err != nil {
			return fmt.Errorf("remove docker macvlan network %q: %w", name, err)
		}
	} else if exitErr, ok := err.(*exec.ExitError); !ok || exitErr.ExitCode() != 1 {
		return fmt.Errorf("inspect docker macvlan network %q: %w", name, err)
	}
	if err := runHostNetHelper(env, bridgeDownArgs(env)...); err != nil {
		return fmt.Errorf("remove macvlan bridge: %w", err)
	}
	return nil
}

func (a *app) removeMacvlan() error {
	if a == nil {
		return errors.New("network application is unavailable")
	}
	if !a.restrictedProcessEnvironment {
		return removeMacvlan(a.env)
	}
	ctx := a.subprocessContext()
	environment := a.commandEnvironment(nil)
	bridge := a.env["VLAN_BRIDGE_INTERFACE"]
	if bridge == "" {
		return nil
	}
	name := a.env["VLAN_INTERFACE"]
	if name == "" {
		name = macvlanNetworkName
	}
	inspect := exec.CommandContext(ctx, "docker", "network", "inspect", name)
	inspect.Env = environment
	inspect.Stdout, inspect.Stderr = io.Discard, io.Discard
	if err := inspect.Run(); err == nil {
		remove := exec.CommandContext(ctx, "docker", "network", "rm", name)
		remove.Env = environment
		remove.Stdout, remove.Stderr = io.Discard, io.Discard
		if err := remove.Run(); err != nil {
			return fmt.Errorf("remove docker macvlan network %q: %w", name, err)
		}
	} else if contextErr := ctx.Err(); contextErr != nil {
		return contextErr
	} else if exitErr, ok := err.(*exec.ExitError); !ok || exitErr.ExitCode() != 1 {
		return fmt.Errorf("inspect docker macvlan network %q: %w", name, err)
	}
	if err := runHostNetHelperContext(ctx, environment, a.env, bridgeDownArgs(a.env)...); err != nil {
		return fmt.Errorf("remove macvlan bridge: %w", err)
	}
	return nil
}

type dockerNetworkInspect struct {
	Driver  string            `json:"Driver"`
	Options map[string]string `json:"Options"`
	IPAM    struct {
		Config []struct {
			Subnet     string            `json:"Subnet"`
			IPRange    string            `json:"IPRange"`
			Gateway    string            `json:"Gateway"`
			AuxAddress map[string]string `json:"AuxiliaryAddresses"`
		} `json:"Config"`
	} `json:"IPAM"`
}

func inspectMacvlan(name string, env map[string]string) (matches, exists bool, err error) {
	out, cmdErr := exec.Command("docker", "network", "inspect", name).Output()
	if cmdErr != nil {
		if exitErr, ok := cmdErr.(*exec.ExitError); ok && exitErr.ExitCode() == 1 {
			return false, false, nil
		}
		return false, false, fmt.Errorf("inspect docker network %q: %w", name, cmdErr)
	}
	var networks []dockerNetworkInspect
	if err := json.Unmarshal(out, &networks); err != nil || len(networks) != 1 {
		return false, true, fmt.Errorf("inspect docker network %q returned invalid data", name)
	}
	n := networks[0]
	if n.Driver != "macvlan" || n.Options["parent"] != env["INTERFACE"] || len(n.IPAM.Config) == 0 {
		return false, true, nil
	}
	ipam := n.IPAM.Config[0]
	return ipam.Subnet == env["HOST_SEGMENT"] &&
		ipam.IPRange == env["VLAN_SEGMENT"] &&
		ipam.Gateway == env["VLAN_GATEWAY_IP"] &&
		ipam.AuxAddress["bridge"] == env["VLAN_BRIDGE_IP"], true, nil
}

func inspectMacvlanContext(ctx context.Context, environment []string, name string, env map[string]string) (matches, exists bool, err error) {
	cmd := exec.CommandContext(ctx, "docker", "network", "inspect", name)
	cmd.Env = append([]string(nil), environment...)
	out, cmdErr := cmd.Output()
	if cmdErr != nil {
		if contextErr := ctx.Err(); contextErr != nil {
			return false, false, contextErr
		}
		if exitErr, ok := cmdErr.(*exec.ExitError); ok && exitErr.ExitCode() == 1 {
			return false, false, nil
		}
		return false, false, fmt.Errorf("inspect docker network %q: %w", name, cmdErr)
	}
	return inspectMacvlanOutput(name, env, out)
}

func inspectMacvlanOutput(name string, env map[string]string, out []byte) (matches, exists bool, err error) {
	var networks []dockerNetworkInspect
	if err := json.Unmarshal(out, &networks); err != nil || len(networks) != 1 {
		return false, true, fmt.Errorf("inspect docker network %q returned invalid data", name)
	}
	n := networks[0]
	if n.Driver != "macvlan" || n.Options["parent"] != env["INTERFACE"] || len(n.IPAM.Config) == 0 {
		return false, true, nil
	}
	ipam := n.IPAM.Config[0]
	return ipam.Subnet == env["HOST_SEGMENT"] &&
		ipam.IPRange == env["VLAN_SEGMENT"] &&
		ipam.Gateway == env["VLAN_GATEWAY_IP"] &&
		ipam.AuxAddress["bridge"] == env["VLAN_BRIDGE_IP"], true, nil
}
