//go:build linux

package incusingresshost

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"
)

func forwardingCommandRunner() commandRunner {
	return commandRunner{config: backendConfig{CommandTimeout: 5 * time.Second, Binaries: trustedBinaries{IP: trustedIPBinary}}}
}

// A daemon's empty instance list is not proof that the physical bridge is
// unused. Foreign, stopped or detached-from-daemon ports must block removal of
// its deny fence. Recheck the exact original bridge before and after the dump.
func CheckForwardingBridgeDrained(ctx context.Context, expected ForwardingNetworkProof) error {
	if ctx == nil || os.Geteuid() != 0 || expected.Validate() != nil {
		return fmt.Errorf("invalid forwarding retirement identity")
	}
	for pass := 0; pass < 2; pass++ {
		actual, err := ObserveForwardingBridge(ctx, expected.BridgeName, expected.BridgeCIDR)
		if err != nil || actual != expected {
			return fmt.Errorf("forwarding retirement bridge or boot identity changed")
		}
		if pass == 0 {
			body, err := forwardingCommandRunner().output(ctx, trustedIPBinary, []string{"-j", "link", "show", "master", expected.BridgeName})
			if err != nil || forwardingBridgePortsAbsent(body) != nil {
				return fmt.Errorf("forwarding retirement requires an empty physical bridge")
			}
		}
	}
	return ctx.Err()
}

// ObserveForwardingBridge uses only the compiled local-kernel reader. The
// name/CIDR must first have passed the pinned Incus lease observer.
func ObserveForwardingBridge(ctx context.Context, name, cidr string) (ForwardingNetworkProof, error) {
	empty := ForwardingNetworkProof{}
	if ctx == nil || os.Geteuid() != 0 || !ifaceName.MatchString(name) {
		return empty, fmt.Errorf("invalid forwarding bridge observation")
	}
	boot, err := os.ReadFile("/proc/sys/kernel/random/boot_id")
	if err != nil || len(boot) > 64 {
		return empty, fmt.Errorf("kernel boot identity is unavailable")
	}
	runner := forwardingCommandRunner()
	body, err := runner.output(ctx, trustedIPBinary, []string{"-j", "-d", "link", "show", "dev", name})
	if err != nil {
		return empty, err
	}
	index, mac, err := decodeForwardingLink(body, name, true)
	if err != nil {
		return empty, err
	}
	proof := ForwardingNetworkProof{BootID: strings.TrimSpace(string(boot)), BridgeName: name, BridgeCIDR: cidr, BridgeID: index, BridgeMAC: mac}
	if err := proof.Validate(); err != nil {
		return empty, err
	}
	body, err = runner.output(ctx, trustedIPBinary, []string{"-j", "-4", "address", "show", "dev", name})
	if err != nil || checkForwardingBridgeAddress(body, name, cidr) != nil {
		return empty, fmt.Errorf("forwarding bridge IPv4 identity changed")
	}
	return proof, nil
}

func ObserveForwardingRoute(ctx context.Context, network ForwardingNetworkProof, destination string, port uint16) (ForwardingRouteProof, error) {
	empty := ForwardingRouteProof{}
	_, valid := forwardingIPv4(destination)
	if ctx == nil || os.Geteuid() != 0 || network.Validate() != nil || !valid || port == 0 {
		return empty, fmt.Errorf("invalid forwarding route observation")
	}
	runner := forwardingCommandRunner()
	body, err := runner.output(ctx, trustedIPBinary, []string{"-j", "-4", "route", "get", destination})
	if err != nil {
		return empty, err
	}
	route, err := decodeForwardingRoute(body, destination, port)
	if err != nil {
		return empty, err
	}
	body, err = runner.output(ctx, trustedIPBinary, []string{"-j", "-d", "link", "show", "dev", route.OutputName})
	if err != nil {
		return empty, err
	}
	route.OutputID, route.OutputMAC, err = decodeForwardingLink(body, route.OutputName, false)
	if err != nil || route.ValidateFor(network) != nil {
		return empty, fmt.Errorf("forwarding output interface identity is unavailable")
	}
	return route, nil
}

// CheckForwardingIdentities never mutates a route or interface. Every renewal
// rechecks both the host route and the actual ingress routing decision.
func CheckForwardingIdentities(ctx context.Context, scope ForwardingKernelScope, instances []ForwardingInstanceProof) error {
	if ctx == nil || os.Geteuid() != 0 || validateForwardingInstances(scope, instances) != nil {
		return fmt.Errorf("invalid forwarding identity recheck")
	}
	observed, err := ObserveForwardingBridge(ctx, scope.Network.BridgeName, scope.Network.BridgeCIDR)
	if err != nil || observed != scope.Network {
		return fmt.Errorf("forwarding bridge or boot identity changed")
	}
	for _, route := range scope.Routes {
		actual, err := ObserveForwardingRoute(ctx, scope.Network, route.Destination, route.Port)
		if err != nil || actual != route {
			return fmt.Errorf("forwarding destination route or interface changed")
		}
	}
	runner := forwardingCommandRunner()
	for _, instance := range instances {
		veth, err := ObserveLocalGuestVeth(ctx, instance.HostVethName, scope.Network.BridgeName)
		if err != nil || veth.Name != instance.HostVethName || veth.MAC != instance.HostVethMAC ||
			veth.IfIndex != instance.HostVethID || veth.PeerIfIndex != instance.PeerVethID {
			return fmt.Errorf("forwarding instance physical interface changed")
		}
		for _, route := range scope.Routes {
			body, err := runner.output(ctx, trustedIPBinary, []string{"-j", "-4", "route", "get", route.Destination,
				"from", instance.GuestIPv4, "iif", scope.Network.BridgeName})
			if err != nil || checkForwardingIngressRoute(body, instance.GuestIPv4, scope, route) != nil {
				return fmt.Errorf("forwarding instance ingress route changed")
			}
		}
	}
	return ctx.Err()
}
