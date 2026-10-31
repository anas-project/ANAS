package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"sort"
	"time"
)

// Read the existing local Docker network rather than maintain a second copy of
// deployment addressing. Never honor DOCKER_HOST or caller-selected endpoints.
func restoreMacvlan() error {
	if os.Geteuid() != 0 {
		return fmt.Errorf("restore-macvlan requires root to read the local Docker socket")
	}
	transport := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", "/var/run/docker.sock")
	}}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 15 * time.Second}
	response, err := client.Get("http://localhost/networks/anas_macvlan")
	if err != nil {
		return fmt.Errorf("inspect local macvlan: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusNotFound {
		return nil
	}
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("inspect local macvlan: HTTP %d", response.StatusCode)
	}
	var network restoreNetwork
	if err := json.NewDecoder(response.Body).Decode(&network); err != nil {
		return err
	}
	request, err := restoredBridge(network)
	if err != nil {
		return err
	}
	return applyBridge(request)
}

type restoreNetwork struct {
	Driver  string
	Options map[string]string
	Labels  map[string]string
	IPAM    struct {
		Config []struct {
			Subnet             string
			IPRange            string
			AuxiliaryAddresses map[string]string
		}
	}
	Containers map[string]struct{ IPv4Address string }
}

func restoredBridge(network restoreNetwork) (bridgeRequest, error) {
	request := bridgeRequest{Name: "anas_bridge", Parent: network.Options["parent"]}
	if network.Driver != "macvlan" || len(network.IPAM.Config) != 1 {
		return request, fmt.Errorf("anas_macvlan must have driver macvlan and one IPv4 subnet")
	}
	config := network.IPAM.Config[0]
	_, subnet, err := net.ParseCIDR(config.Subnet)
	if err != nil {
		return request, fmt.Errorf("invalid macvlan subnet %q", config.Subnet)
	}
	bridge := net.ParseIP(config.AuxiliaryAddresses["bridge"])
	if bridge == nil || bridge.To4() == nil || !subnet.Contains(bridge) {
		return request, fmt.Errorf("missing or invalid macvlan bridge reservation")
	}
	// A /32 avoids redirecting the host's whole LAN through the child interface.
	request.Address = bridge.String() + "/32"
	routes := map[string]bool{}
	add := func(cidr string) error {
		ip, block, err := net.ParseCIDR(cidr)
		if err != nil || ip.To4() == nil || !subnet.Contains(ip) {
			return fmt.Errorf("invalid macvlan route %q", cidr)
		}
		ones, bits := block.Mask.Size()
		last := append(net.IP(nil), block.IP...)
		for i := range last {
			last[i] |= ^block.Mask[i]
		}
		if bits != 32 || !subnet.Contains(last) {
			return fmt.Errorf("macvlan route %q exceeds subnet", cidr)
		}
		if ones == 32 && ip.Equal(bridge) {
			return nil
		}
		routes[block.String()] = true
		return nil
	}
	// Legacy networks reserve an IPAM pool. New pinned networks also record the
	// member address, so recovery does not depend on container startup timing.
	if config.IPRange != "" {
		if err := add(config.IPRange); err != nil {
			return request, err
		}
	}
	if address := network.Labels["dev.anas.host-lan-ip"]; address != "" {
		if err := add(address + "/32"); err != nil {
			return request, err
		}
	}
	for _, container := range network.Containers {
		ip, _, err := net.ParseCIDR(container.IPv4Address)
		if err != nil {
			return request, fmt.Errorf("invalid macvlan container address")
		}
		if err := add(ip.String() + "/32"); err != nil {
			return request, err
		}
	}
	for route := range routes {
		request.Routes = append(request.Routes, route)
	}
	sort.Strings(request.Routes)
	return request, validateBridge(request)
}
