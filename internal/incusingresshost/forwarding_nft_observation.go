package incusingresshost

import (
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
)

func forwardingNormalizeExpr(raw []json.RawMessage) ([]any, bool) {
	var values []any
	for _, expression := range raw {
		var value any
		if decodeObservedJSON(expression, &value) != nil {
			return nil, false
		}
		// ARP belongs only to this compiled source-fence grammar. The separate
		// HTTP backend's narrower EtherType normalization remains unchanged.
		arp := reflect.DeepEqual(value, nftPayloadMatch("ether", "type", "==", "arp"))
		if !arp && !normalizeNFTExpression(value) {
			return nil, false
		}
		values = append(values, value)
	}
	// nft elides explicit EtherType/l4proto checks when later typed payload
	// matches already imply them. Cross only side-effect-free conjunctions;
	// never erase a condition across a counter, verdict or unknown expression.
	out := make([]any, 0, len(values))
	for i, value := range values {
		protocol := ""
		for _, candidate := range []string{"ip", "arp"} {
			if reflect.DeepEqual(value, nftPayloadMatch("ether", "type", "==", candidate)) {
				protocol = candidate
			}
		}
		for _, candidate := range []string{"tcp", "udp"} {
			if reflect.DeepEqual(value, nftMetaMatch("l4proto", "==", candidate)) {
				protocol = candidate
			}
		}
		implied := false
		if protocol != "" {
			for _, next := range values[i+1:] {
				object, ok := next.(map[string]any)
				if !ok || len(object) != 1 {
					break
				}
				match, ok := object["match"].(map[string]any)
				if !ok || len(match) != 3 || match["op"] != "==" {
					break
				}
				if forwardingTypedPayload(match["left"], protocol) {
					implied = true
					break
				}
			}
		}
		if !implied {
			out = append(out, value)
		}
	}
	return out, true
}

func forwardingTypedPayload(left any, protocol string) bool {
	object, ok := left.(map[string]any)
	if !ok || len(object) != 1 {
		return false
	}
	if payload, ok := object["payload"].(map[string]any); ok && len(payload) == 2 && payload["protocol"] == protocol {
		field, ok := payload["field"].(string)
		return ok && ((protocol == "ip" && (field == "saddr" || field == "daddr")) ||
			(protocol == "arp" && (field == "saddr ether" || field == "saddr ip")) ||
			((protocol == "tcp" || protocol == "udp") && (field == "sport" || field == "dport")))
	}
	if fields, ok := object["concat"].([]any); ok {
		for _, field := range fields {
			if forwardingTypedPayload(field, protocol) {
				return true
			}
		}
	}
	return false
}

func forwardingValues(set forwardingNFTSet) map[string]bool {
	result := map[string]bool{}
	for _, value := range set.Values {
		body, _ := json.Marshal(value)
		result[string(body)] = true
	}
	return result
}

func forwardingElementValue(raw any, timeout uint64) (any, uint64, error) {
	remaining := timeout
	if wrapper, ok := raw.(map[string]any); ok && wrapper["elem"] != nil {
		if len(wrapper) != 1 {
			return nil, 0, fmt.Errorf("ambiguous forwarding set element")
		}
		item, ok := wrapper["elem"].(map[string]any)
		if !ok {
			return nil, 0, fmt.Errorf("invalid forwarding set element")
		}
		for key := range item {
			if key != "val" && key != "timeout" && key != "expires" {
				return nil, 0, fmt.Errorf("unknown forwarding set element property")
			}
		}
		if value, present := item["timeout"]; present {
			n, err := nftNumber(value, 64)
			if err != nil || n != timeout {
				return nil, 0, fmt.Errorf("forwarding set timeout changed")
			}
		}
		if value, present := item["expires"]; present {
			n, err := nftNumber(value, 64)
			if err != nil || n > timeout {
				return nil, 0, fmt.Errorf("forwarding set lifetime exceeds its grant")
			}
			remaining = n
		}
		raw = item["val"]
	}
	if timeout == 0 && remaining != 0 {
		return nil, 0, fmt.Errorf("unexpected permanent set lifetime")
	}
	return raw, remaining, nil
}

// Validate the complete native AST. Expiration may remove a saved tuple, but
// cannot authorize an extra tuple, widened timeout, chain, rule or side effect.
func validateForwardingNFT(body []byte, expected forwardingNFTDefinition, knownHandle uint64, closed, allowExpired bool) (uint64, error) {
	var document struct {
		NFTables []map[string]json.RawMessage `json:"nftables"`
	}
	bad := func() (uint64, error) {
		return 0, fmt.Errorf("forwarding nft ownership or complete definition is unverified")
	}
	if decodeObservedJSON(body, &document) != nil || len(document.NFTables) == 0 || len(document.NFTables) > 16384 {
		return bad()
	}
	sets := map[string]forwardingNFTSet{}
	for _, set := range expected.Sets {
		sets[set.Name] = set
	}
	chains := map[string]nftChain{}
	for _, chain := range expected.Chains {
		chains[chain.Name] = chain
	}
	seenSets, seenChains := map[string]bool{}, map[string]bool{}
	rules := 0
	var handle uint64
	metaSeen := false
	for _, entry := range document.NFTables {
		if len(entry) != 1 {
			return bad()
		}
		for kind, raw := range entry {
			switch kind {
			case "metainfo":
				var meta struct {
					Version string `json:"version"`
					Release string `json:"release_name"`
					Schema  int    `json:"json_schema_version"`
				}
				if metaSeen || decodeObservedJSON(raw, &meta) != nil || meta.Schema != 1 {
					return bad()
				}
				metaSeen = true
			case "table":
				var table struct {
					Family  string   `json:"family"`
					Name    string   `json:"name"`
					Handle  uint64   `json:"handle"`
					Comment string   `json:"comment"`
					Flags   []string `json:"flags,omitempty"`
				}
				if handle != 0 || decodeObservedJSON(raw, &table) != nil || table.Family != expected.Family || table.Name != expected.Name || table.Comment != expected.Comment || len(table.Flags) != 0 || table.Handle == 0 || knownHandle != 0 && table.Handle != knownHandle {
					return bad()
				}
				handle = table.Handle
			case "chain":
				var chain nftChain
				if decodeObservedJSON(raw, &chain) != nil || chain.Handle == 0 || seenChains[chain.Name] {
					return bad()
				}
				want, exists := chains[chain.Name]
				chain.Handle = 0
				if !exists || !reflect.DeepEqual(want, chain) {
					return bad()
				}
				seenChains[chain.Name] = true
			case "set":
				var set struct {
					Family   string          `json:"family"`
					Table    string          `json:"table"`
					Name     string          `json:"name"`
					Handle   uint64          `json:"handle"`
					Type     json.RawMessage `json:"type"`
					Flags    []string        `json:"flags,omitempty"`
					Timeout  uint64          `json:"timeout,omitempty"`
					GC       uint64          `json:"gc-interval,omitempty"`
					Size     uint64          `json:"size,omitempty"`
					Policy   string          `json:"policy,omitempty"`
					Elements []any           `json:"elem,omitempty"`
				}
				if decodeObservedJSON(raw, &set) != nil || seenSets[set.Name] || set.Family != expected.Family || set.Table != expected.Name || set.Handle == 0 || set.Size != 8192 || (set.Policy != "" && set.Policy != "performance") {
					return bad()
				}
				want, exists := sets[set.Name]
				if !exists || set.Timeout != want.Timeout {
					return bad()
				}
				var types []string
				var single string
				if json.Unmarshal(set.Type, &types) != nil {
					if json.Unmarshal(set.Type, &single) != nil {
						return bad()
					}
					types = []string{single}
				}
				if !slices.Equal(types, want.Types) || (want.Timeout == 0 && (len(set.Flags) != 0 || set.GC != 0)) || (want.Timeout != 0 && (!slices.Equal(set.Flags, []string{"timeout"}) || set.GC != 1)) {
					return bad()
				}
				allowed, seen := forwardingValues(want), map[string]bool{}
				for _, rawValue := range set.Elements {
					value, ttl, err := forwardingElementValue(rawValue, want.Timeout)
					if err != nil {
						return bad()
					}
					encoded, _ := json.Marshal(value)
					key := string(encoded)
					if seen[key] || !allowed[key] || closed && want.Timeout != 0 || (!allowExpired && want.Timeout != 0 && ttl == 0) {
						return bad()
					}
					seen[key] = true
				}
				if !(closed && want.Timeout != 0) && (!allowExpired || want.Timeout == 0) && len(seen) != len(allowed) {
					return bad()
				}
				seenSets[set.Name] = true
			case "rule":
				if rules >= len(expected.Rules) {
					return bad()
				}
				actual, err := parseNFTRule(raw)
				want := expected.Rules[rules]
				if err != nil || actual.Family != want.Family || actual.Table != want.Table || actual.Chain != want.Chain || actual.Comment != want.Comment || actual.Handle == "" {
					return bad()
				}
				a, okA := forwardingNormalizeExpr(actual.Expr)
				b, okB := forwardingNormalizeExpr(want.Expr)
				if !okA || !okB || !reflect.DeepEqual(a, b) {
					return bad()
				}
				rules++
			default:
				return bad()
			}
		}
	}
	if handle == 0 || len(seenSets) != len(sets) || len(seenChains) != len(chains) || rules != len(expected.Rules) {
		return bad()
	}
	return handle, nil
}
