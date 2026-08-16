package runner

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/anas-project/ANAS/internal/compose"
)

func ensureMacvlan(env map[string]string, base string, _ compose.CLI) error {
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
	script := filepath.Join(base, "anas_service.sh")
	body := fmt.Sprintf(`#!/usr/bin/env sh
set -eu

BRIDGE=%q
PARENT=%q
ADDR=%q
ROUTES=%q

case "${1:-add}" in
  add)
    if ! ip link show "$BRIDGE" >/dev/null 2>&1; then
      ip link add "$BRIDGE" link "$PARENT" type macvlan mode bridge
    fi
    ip addr replace "$ADDR" dev "$BRIDGE"
    # An address left over from an earlier plan would keep the host answering
    # for a range this deployment no longer owns, and would keep a stale
    # connected route pointing at the bridge.
    ip -4 -o addr show dev "$BRIDGE" | awk '{print $4}' | while read -r existing; do
      [ "$existing" = "$ADDR" ] || ip addr del "$existing" dev "$BRIDGE"
    done
    ip link set "$BRIDGE" up
    # A pinned container address may sit outside the bridge address's own
    # prefix, so the host's route to it is stated rather than inherited from
    # the bridge's connected route.
    for route in $ROUTES; do
      ip route replace "$route" dev "$BRIDGE"
    done
    ;;
  del)
    ip link delete "$BRIDGE" 2>/dev/null || true
    ;;
esac
`, env["VLAN_BRIDGE_INTERFACE"], env["INTERFACE"], env["VLAN_BRIDGE_IP"]+"/"+env["VLAN_SUBNET_MASK"], hostLANRoutes(env))
	if err := os.MkdirAll(base, 0700); err != nil {
		return err
	}
	if err := os.WriteFile(script, []byte(body), 0755); err != nil {
		return err
	}
	if err := runNetworkScript(env["NETWORK_NAMESPACE_PATH"], script); err != nil {
		return fmt.Errorf("create macvlan bridge: %w", err)
	}
	if exists {
		return nil
	}
	err = exec.Command("docker", networkCreateArgs(name, env)...).Run()
	if err != nil {
		_ = runNetworkScript(env["NETWORK_NAMESPACE_PATH"], script, "del")
		return fmt.Errorf("create docker macvlan network: %w", err)
	}
	return nil
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

// hostLANRoutes is the space-separated route list the bridge script installs.
func hostLANRoutes(env map[string]string) string {
	routes := []string{}
	for _, addr := range hostLANAddresses(env) {
		if addr != env["VLAN_BRIDGE_IP"] {
			routes = append(routes, addr+"/32")
		}
	}
	return strings.Join(routes, " ")
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

func networkScriptArgs(namespacePath, script string, args ...string) ([]string, error) {
	commandArgs := []string{}
	if namespacePath != "" {
		if !filepath.IsAbs(namespacePath) {
			return nil, fmt.Errorf("NETWORK_NAMESPACE_PATH must be absolute: %q", namespacePath)
		}
		commandArgs = append(commandArgs, "nsenter", "--net="+filepath.Clean(namespacePath))
	}
	commandArgs = append(commandArgs, "sh", script)
	commandArgs = append(commandArgs, args...)
	return commandArgs, nil
}

func runNetworkScript(namespacePath, script string, args ...string) error {
	commandArgs, err := networkScriptArgs(namespacePath, script, args...)
	if err != nil {
		return err
	}
	return exec.Command("sudo", commandArgs...).Run()
}

func removeMacvlan(env map[string]string, base string) error {
	script := filepath.Join(base, "anas_service.sh")
	if _, err := os.Stat(script); err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	name := env["VLAN_INTERFACE"]
	if name == "" {
		name = "anas_macvlan"
	}
	if err := exec.Command("docker", "network", "inspect", name).Run(); err == nil {
		if err := exec.Command("docker", "network", "rm", name).Run(); err != nil {
			return fmt.Errorf("remove docker macvlan network %q: %w", name, err)
		}
	} else if exitErr, ok := err.(*exec.ExitError); !ok || exitErr.ExitCode() != 1 {
		return fmt.Errorf("inspect docker macvlan network %q: %w", name, err)
	}
	if err := runNetworkScript(env["NETWORK_NAMESPACE_PATH"], script, "del"); err != nil {
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
