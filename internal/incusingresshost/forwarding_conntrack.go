package incusingresshost

import (
	"fmt"
	"strings"
)

func parseForwardingConntrack(body []byte, scope ForwardingKernelScope, instance ForwardingInstanceProof, route ForwardingRouteProof) ([]conntrackEntry, error) {
	if instance.ValidateFor(scope) != nil || route.ValidateFor(scope.Network) != nil || len(body) > 1<<20 {
		return nil, fmt.Errorf("invalid forwarding conntrack scope")
	}
	result := []conntrackEntry{}
	seen := map[conntrackEntry]bool{}
	for _, line := range strings.Split(string(body), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		entry, err := parseConntrackTupleLine(line)
		// Only SNAT to the observed output address is supported. DNAT, a
		// foreign source/address/port/zone, or an unobserved reply is not ours.
		if err != nil || entry.Src != instance.GuestIPv4 || entry.Dst != route.Destination || entry.DstPort != route.Port ||
			entry.ReplySrc != route.Destination || entry.ReplySrcPort != route.Port ||
			(entry.ReplyDst != route.SourceIPv4 && entry.ReplyDst != instance.GuestIPv4) || seen[entry] || len(result) >= 256 {
			return nil, fmt.Errorf("forwarding conntrack query returned an out-of-scope or ambiguous NAT flow")
		}
		if entry.ReplyDst == instance.GuestIPv4 && entry.ReplyDstPort != entry.SrcPort {
			return nil, fmt.Errorf("unsupported routed port translation")
		}
		seen[entry] = true
		result = append(result, entry)
	}
	return result, nil
}
