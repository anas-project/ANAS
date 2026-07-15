package runner

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/whlsxl/anas/internal/compose"
)

func ensureMacvlan(env map[string]string, base string, _ compose.CLI) error {
	name := env["VLAN_INTERFACE"]
	matches, exists, err := inspectMacvlan(name, env)
	if err != nil {
		return err
	}
	if exists && !matches {
		return fmt.Errorf("docker network %q exists with different macvlan settings; refusing to replace it automatically", name)
	}
	script := filepath.Join(base, "anas_service.sh")
	body := fmt.Sprintf(`#!/usr/bin/env sh
set -eu

BRIDGE=%q
PARENT=%q
ADDR=%q

case "${1:-add}" in
  add)
    if ! ip link show "$BRIDGE" >/dev/null 2>&1; then
      ip link add "$BRIDGE" link "$PARENT" type macvlan mode bridge
    fi
    ip addr replace "$ADDR" dev "$BRIDGE"
    ip link set "$BRIDGE" up
    ;;
  del)
    ip link delete "$BRIDGE" 2>/dev/null || true
    ;;
esac
`, env["VLAN_BRIDGE_INTERFACE"], env["INTERFACE"], env["VLAN_BRIDGE_IP"]+"/"+env["VLAN_SUBNET_MASK"])
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
	err = exec.Command("docker", "network", "create",
		"-d", "macvlan",
		"-o", "parent="+env["INTERFACE"],
		"--subnet", env["HOST_SEGMENT"],
		"--gateway", env["VLAN_GATEWAY_IP"],
		"--ip-range", env["VLAN_SEGMENT"],
		"--aux-address", "bridge="+env["VLAN_BRIDGE_IP"],
		name,
	).Run()
	if err != nil {
		_ = runNetworkScript(env["NETWORK_NAMESPACE_PATH"], script, "del")
		return fmt.Errorf("create docker macvlan network: %w", err)
	}
	return nil
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
