package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/netip"
	"os"
	"runtime"
	"strconv"
	"strings"
)

// The port range for port bindings is approved by the operator in
// incus.configure and written by hostd next to the connection bundle. Core
// allocates auto ports from it and refuses a port binding when it is absent
// (INCUS-R-153, R-164); hostd still re-checks every entry when it syncs.

const (
	hostNetworkSchema      = "anas.incus-host-network/v1"
	defaultHostNetworkPath = "/var/lib/anas/incus-host/network.json"
	hostdInstallationPath  = "/etc/anas/hostd.json"
)

var (
	hostNetworkTestPath string
	hostdPolicyTestPath string
)

type hostNetworkPolicy struct {
	Schema    string `json:"schema"`
	PortRange struct {
		First int `json:"first"`
		Last  int `json:"last"`
	} `json:"port_range"`
}

// publishPortBindingRange sets INCUS_PORT_BINDING_RANGE, or leaves it empty
// and names why in INCUS_PORT_BINDING_BLOCKER. Only an Incus daemon this host
// provisioned has a hostd to apply port bindings.
func publishPortBindingRange(e map[string]string, automatic bool) {
	e["INCUS_PORT_BINDING_RANGE"], e["INCUS_PORT_BINDING_BLOCKER"] = "", ""
	if !automatic {
		e["INCUS_PORT_BINDING_BLOCKER"] = "remote_daemon"
		return
	}
	policyPath := hostdInstallationPath
	if hostdPolicyTestPath != "" {
		policyPath = hostdPolicyTestPath
	}
	if info, err := os.Stat(policyPath); err != nil || !info.Mode().IsRegular() || runtime.GOOS != "linux" && hostdPolicyTestPath == "" {
		e["INCUS_PORT_BINDING_BLOCKER"] = "hostd_missing"
		return
	}
	policy, err := loadHostNetworkPolicy()
	if err != nil {
		e["INCUS_PORT_BINDING_BLOCKER"] = "host_not_configured"
		return
	}
	e["INCUS_PORT_BINDING_RANGE"] = strconv.Itoa(policy.PortRange.First) + "-" + strconv.Itoa(policy.PortRange.Last)
}

func loadHostNetworkPolicy() (hostNetworkPolicy, error) {
	path := defaultHostNetworkPath
	if hostNetworkTestPath != "" {
		path = hostNetworkTestPath
	}
	body, err := readSafeHostBundleFile(path, 4096)
	if err != nil {
		return hostNetworkPolicy{}, err
	}
	var policy hostNetworkPolicy
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if validateNoDuplicateJSONFields(body) != nil || decoder.Decode(&policy) != nil || decoder.Decode(&struct{}{}) != io.EOF {
		return hostNetworkPolicy{}, fmt.Errorf("incus host network policy is invalid")
	}
	if policy.Schema != hostNetworkSchema || policy.PortRange.First < 1024 || policy.PortRange.Last > 65535 || policy.PortRange.First > policy.PortRange.Last {
		return hostNetworkPolicy{}, fmt.Errorf("incus host network policy is invalid")
	}
	return policy, nil
}

// validateLANExtraSubnets checks the operator's extra LAN subnets. A default
// route, loopback or link-local range would make every tier that allows the
// LAN allow far more than a LAN.
func validateLANExtraSubnets(value string) error {
	count := 0
	for _, raw := range strings.Split(value, ",") {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		count++
		prefix, err := netip.ParsePrefix(raw)
		if err != nil || prefix.Bits() == 0 || prefix.Addr().IsLoopback() || prefix.Addr().IsLinkLocalUnicast() || prefix.Addr().IsMulticast() || prefix.Addr().IsUnspecified() {
			return fmt.Errorf("incus lan_extra_subnets must list LAN subnets in CIDR form, without default, loopback, link-local or multicast ranges")
		}
	}
	if count > 64 {
		return fmt.Errorf("incus lan_extra_subnets lists more than 64 subnets")
	}
	return nil
}
