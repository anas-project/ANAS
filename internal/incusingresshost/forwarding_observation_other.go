//go:build !linux

package incusingresshost

import (
	"context"
	"fmt"
)

func CheckForwardingBridgeDrained(context.Context, ForwardingNetworkProof) error {
	return fmt.Errorf("forwarding retirement requires Linux")
}

func ObserveForwardingBridge(context.Context, string, string) (ForwardingNetworkProof, error) {
	return ForwardingNetworkProof{}, fmt.Errorf("forwarding kernel observations require Linux")
}

func ObserveForwardingRoute(context.Context, ForwardingNetworkProof, string, uint16) (ForwardingRouteProof, error) {
	return ForwardingRouteProof{}, fmt.Errorf("forwarding kernel observations require Linux")
}

func CheckForwardingIdentities(context.Context, ForwardingKernelScope, []ForwardingInstanceProof) error {
	return fmt.Errorf("forwarding kernel observations require Linux")
}
