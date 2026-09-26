package incusingresshost

import (
	"encoding/json"
	"fmt"
	"net/netip"
	"slices"
	"strconv"
)

func decodeForwardingLink(body []byte, name string, bridge bool) (uint32, string, error) {
	var links []map[string]json.RawMessage
	if decodeObservedJSON(body, &links) != nil || len(links) != 1 {
		return 0, "", fmt.Errorf("forwarding interface observation is incomplete")
	}
	l := links[0]
	if forwardingRawString(l, "ifname") != name || !ifaceName.MatchString(name) {
		return 0, "", fmt.Errorf("forwarding interface identity changed")
	}
	index, err := strconv.ParseUint(string(l["ifindex"]), 10, 32)
	mac := forwardingRawString(l, "address")
	var flags []string
	if err != nil || index == 0 || !forwardingMAC(mac) || json.Unmarshal(l["flags"], &flags) != nil || !slices.Contains(flags, "UP") {
		return 0, "", fmt.Errorf("forwarding interface is not administratively active")
	}
	// An empty bridge may lack carrier before the first guest exists. Its
	// administrative state, ifindex and MAC still identify the approved bridge.
	var info struct {
		Kind string          `json:"info_kind"`
		Data json.RawMessage `json:"info_data,omitempty"`
	}
	if bridge && (json.Unmarshal(l["linkinfo"], &info) != nil || info.Kind != "bridge") {
		return 0, "", fmt.Errorf("forwarding lease does not use a kernel bridge")
	}
	if forwardingRawString(l, "master") != "" {
		return 0, "", fmt.Errorf("forwarding interface has an unsupported master or VRF")
	}
	return uint32(index), mac, nil
}

func checkForwardingBridgeAddress(body []byte, name, cidr string) error {
	var links []struct {
		Name      string `json:"ifname"`
		Addresses []struct {
			Family string `json:"family"`
			Local  string `json:"local"`
			Bits   int    `json:"prefixlen"`
			Scope  string `json:"scope"`
		} `json:"addr_info"`
	}
	// ip includes interface metadata unrelated to addresses; read all bounded
	// JSON without treating those fields as an authority projection.
	if json.Unmarshal(body, &links) != nil || len(links) != 1 || links[0].Name != name || len(links[0].Addresses) != 1 {
		return fmt.Errorf("forwarding bridge address is absent or ambiguous")
	}
	a := links[0].Addresses[0]
	if a.Family != "inet" || a.Scope != "global" || a.Local+"/"+strconv.Itoa(a.Bits) != cidr {
		return fmt.Errorf("daemon and kernel forwarding bridge addresses differ")
	}
	return nil
}

func decodeForwardingRoute(body []byte, destination string, port uint16) (ForwardingRouteProof, error) {
	var entries []map[string]json.RawMessage
	if decodeObservedJSON(body, &entries) != nil || len(entries) != 1 {
		return ForwardingRouteProof{}, fmt.Errorf("forwarding destination route is ambiguous")
	}
	r := entries[0]
	if forwardingRawString(r, "dst") != destination || (forwardingRawString(r, "type") != "" && forwardingRawString(r, "type") != "unicast") ||
		!ifaceName.MatchString(forwardingRawString(r, "dev")) || forwardingRawString(r, "dev") == "lo" ||
		r["multipath"] != nil || r["nhid"] != nil || r["encap"] != nil || r["via"] != nil {
		return ForwardingRouteProof{}, fmt.Errorf("forwarding destination has a local or unsupported route")
	}
	table := uint64(254)
	if r["table"] != nil && string(r["table"]) != `"main"` {
		var err error
		table, err = strconv.ParseUint(string(r["table"]), 10, 32)
		if err != nil || table == 0 {
			return ForwardingRouteProof{}, fmt.Errorf("forwarding route table is ambiguous")
		}
	}
	source := forwardingRawString(r, "prefsrc")
	if _, ok := forwardingIPv4(source); !ok {
		return ForwardingRouteProof{}, fmt.Errorf("forwarding route has no fixed IPv4 source")
	}
	var flags []string
	if json.Unmarshal(r["flags"], &flags) != nil || len(flags) != 0 {
		return ForwardingRouteProof{}, fmt.Errorf("forwarding route flags are unsupported")
	}
	return ForwardingRouteProof{Destination: destination, Port: port, OutputName: forwardingRawString(r, "dev"),
		SourceIPv4: source, Gateway: forwardingRawString(r, "gateway"), Table: uint32(table)}, nil
}

func checkForwardingIngressRoute(body []byte, source string, scope ForwardingKernelScope, approved ForwardingRouteProof) error {
	var entries []map[string]json.RawMessage
	if decodeObservedJSON(body, &entries) != nil || len(entries) != 1 {
		return fmt.Errorf("guest forwarding route is ambiguous")
	}
	r := entries[0]
	if forwardingRawString(r, "dst") != approved.Destination || forwardingRawString(r, "from") != source ||
		forwardingRawString(r, "dev") != approved.OutputName || forwardingRawString(r, "gateway") != approved.Gateway ||
		(forwardingRawString(r, "type") != "" && forwardingRawString(r, "type") != "unicast") || r["multipath"] != nil || r["nhid"] != nil ||
		r["encap"] != nil || r["via"] != nil {
		return fmt.Errorf("guest forwarding route differs from the host-approved route")
	}
	table := "254"
	if raw := r["table"]; raw != nil && string(raw) != `"main"` {
		table = string(raw)
	}
	if table != strconv.FormatUint(uint64(approved.Table), 10) {
		return fmt.Errorf("guest forwarding policy selected another route table")
	}
	ip, ok := forwardingIPv4(source)
	prefix, err := netip.ParsePrefix(scope.Network.BridgeCIDR)
	if !ok || err != nil || !prefix.Contains(ip) {
		return fmt.Errorf("guest route source is outside the approved bridge")
	}
	return nil
}

func forwardingRawString(object map[string]json.RawMessage, key string) string {
	var value string
	if json.Unmarshal(object[key], &value) != nil {
		return ""
	}
	return value
}
