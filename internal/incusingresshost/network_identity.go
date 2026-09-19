package incusingresshost

import (
	"context"
	"fmt"
	"net/netip"
	"strconv"
)

func (b *Backend) verifyNativeRouteNamespace(ctx context.Context) error {
	if err := validateInstalledNamespacePin(b.config.Namespace); err != nil {
		return err
	}
	// The command runner enters the opened, kernel-pinned namespace for -n;
	// the latter never reaches the production ip command as a name or path.
	inside, err := b.runner.output(ctx, b.config.Binaries.IP, []string{"-j", "-d", "-n", b.config.RouteNetNS, "address", "show", "dev", b.config.TraefikInterface})
	if err != nil {
		return err
	}
	peer, err := b.runner.output(ctx, b.config.Binaries.IP, []string{"-j", "-d", "link", "show", "dev", b.config.TraefikVeth})
	if err != nil {
		return err
	}
	bridge, err := b.runner.output(ctx, b.config.Binaries.IP, []string{"-j", "-d", "address", "show", "dev", b.config.IngressBridge})
	if err != nil {
		return err
	}
	return verifyNativeNetworkIdentity(b.config, inside, peer, bridge)
}

func verifyNativeNetworkIdentity(config backendConfig, inside, peer, bridge []byte) error {
	containerLink, err := nativeLink(inside, config.TraefikInterface, "veth")
	if err != nil {
		return err
	}
	hostLink, err := nativeLink(peer, config.TraefikVeth, "veth")
	if err != nil {
		return err
	}
	bridgeLink, err := nativeLink(bridge, config.IngressBridge, "bridge")
	if err != nil {
		return err
	}
	pin := config.Namespace
	if numberField(containerLink, "ifindex") != strconv.FormatUint(uint64(pin.TraefikIfIndex), 10) ||
		numberField(containerLink, "link_index") != strconv.FormatUint(uint64(pin.HostVethIfIndex), 10) ||
		stringField(containerLink, "address") != pin.TraefikMAC ||
		numberField(hostLink, "ifindex") != strconv.FormatUint(uint64(pin.HostVethIfIndex), 10) ||
		numberField(hostLink, "link_index") != strconv.FormatUint(uint64(pin.TraefikIfIndex), 10) ||
		pin.HostVethPeerIfIndex != pin.TraefikIfIndex || stringField(hostLink, "address") != pin.HostVethMAC ||
		stringField(hostLink, "master") != config.IngressBridge {
		return fmt.Errorf("installed Traefik veth pair identity changed")
	}
	sourcePrefix, err := nativeIPv4Prefix(containerLink, config.TraefikSourceIP, config.IngressGateway)
	if err != nil {
		return err
	}
	gatewayPrefix, err := nativeIPv4Prefix(bridgeLink, config.IngressGateway, config.TraefikSourceIP)
	if err != nil {
		return err
	}
	if sourcePrefix.Masked() != gatewayPrefix.Masked() || !sourcePrefix.Contains(gatewayPrefix.Addr()) {
		return fmt.Errorf("Traefik source and host gateway do not share the installed ingress subnet")
	}
	return nil
}

func nativeLink(body []byte, name, kind string) (map[string]any, error) {
	var links []map[string]any
	if err := decodeObservedJSON(body, &links); err != nil || len(links) != 1 {
		return nil, fmt.Errorf("native interface observation is incomplete")
	}
	link := links[0]
	info, ok := link["linkinfo"].(map[string]any)
	if !ok || stringField(info, "info_kind") != kind || stringField(link, "ifname") != name ||
		stringField(link, "link_type") != "ether" || numberField(link, "ifindex") == "" || numberField(link, "ifindex") == "0" {
		return nil, fmt.Errorf("native interface identity is not verified")
	}
	flags, ok := link["flags"].([]any)
	if !ok {
		return nil, fmt.Errorf("native interface flags are missing")
	}
	seen := map[string]bool{}
	for _, value := range flags {
		flag, ok := value.(string)
		if !ok || seen[flag] {
			return nil, fmt.Errorf("native interface flags are invalid")
		}
		seen[flag] = true
	}
	if !seen["UP"] || !seen["LOWER_UP"] || seen["NO-CARRIER"] {
		return nil, fmt.Errorf("installed ingress interface is not up")
	}
	return link, nil
}

func nativeIPv4Prefix(link map[string]any, expected, forbidden string) (netip.Prefix, error) {
	addresses, ok := link["addr_info"].([]any)
	if !ok || len(addresses) == 0 || len(addresses) > 64 {
		return netip.Prefix{}, fmt.Errorf("native interface addresses are incomplete")
	}
	var result netip.Prefix
	for _, value := range addresses {
		address, ok := value.(map[string]any)
		if !ok {
			return netip.Prefix{}, fmt.Errorf("native interface address is invalid")
		}
		if stringField(address, "family") != "inet" {
			continue
		}
		local := stringField(address, "local")
		if local == forbidden {
			return netip.Prefix{}, fmt.Errorf("ingress source and gateway are on the wrong interfaces")
		}
		if local != expected {
			continue
		}
		prefix, err := netip.ParsePrefix(local + "/" + numberField(address, "prefixlen"))
		if err != nil || !prefix.Addr().Is4() || prefix.Bits() < 1 || prefix.Bits() > 30 || result.IsValid() {
			return netip.Prefix{}, fmt.Errorf("native ingress IPv4 prefix is ambiguous")
		}
		result = prefix
	}
	if !result.IsValid() {
		return netip.Prefix{}, fmt.Errorf("installed ingress IPv4 address is absent")
	}
	return result, nil
}
