package incusprovision

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"time"
)

// ForwardingObservation reports only a few local routing/filter facts. It is
// not a route simulation, a packet trace, a grant or evidence of reachability.
// In particular an accept in one nft base chain cannot override a later drop.
// No host addresses, firewall text, comments or counters enter public plans.
type ForwardingObservation struct {
	IPv4Routing     string `json:"ipv4_routing"` // enabled, disabled, unobserved
	IPv4Filter      string `json:"ipv4_filter"`  // drop_observed, no_drop_observed, unobserved
	IPv4BaseChains  int    `json:"ipv4_base_chains"`
	DockerUserChain bool   `json:"docker_user_chain_observed"`
}

func unknownForwarding(routing []byte) ForwardingObservation {
	result := ForwardingObservation{IPv4Routing: "unobserved", IPv4Filter: "unobserved"}
	switch string(bytes.TrimSpace(routing)) {
	case "0":
		result.IPv4Routing = "disabled"
	case "1":
		result.IPv4Routing = "enabled"
	}
	return result
}

func parseForwardingObservation(routing, body []byte) (ForwardingObservation, error) {
	unknown := unknownForwarding(routing)
	if len(body) == 0 || len(body) > 1<<20 || validateNoDuplicateJSONFields(body) != nil {
		return unknown, ErrIncomplete
	}
	var document struct {
		NFTables []json.RawMessage `json:"nftables"`
	}
	if decodeNFTObject(body, &document, "nftables") != nil || document.NFTables == nil {
		return unknown, ErrIncomplete
	}
	result := unknown
	result.IPv4Filter = "no_drop_observed"
	seen := map[string]bool{}
	metaSeen := false
	for _, raw := range document.NFTables {
		var object map[string]json.RawMessage
		if json.Unmarshal(raw, &object) != nil || len(object) != 1 {
			return unknown, ErrIncomplete
		}
		for kind, value := range object {
			var fields map[string]json.RawMessage
			if json.Unmarshal(value, &fields) != nil || fields == nil {
				return unknown, ErrIncomplete
			}
			switch kind {
			case "metainfo":
				var meta struct {
					Version     int    `json:"json_schema_version"`
					ToolVersion string `json:"version"`
					ReleaseName string `json:"release_name"`
				}
				if metaSeen || decodeNFTObject(value, &meta, "json_schema_version", "version", "release_name") != nil || meta.Version != 1 {
					return unknown, ErrIncomplete
				}
				metaSeen = true
			case "chain":
				var chain struct{ Family, Table, Name, Type, Hook, Policy string }
				if decodeNFTObject(value, &chain, "family", "table", "name", "handle", "type", "hook", "prio", "policy", "dev", "flags") != nil ||
					chain.Family == "" || chain.Table == "" || chain.Name == "" ||
					len(chain.Family)+len(chain.Table)+len(chain.Name) > 768 {
					return unknown, ErrIncomplete
				}
				switch chain.Family {
				case "ip", "ip6", "inet", "arp", "bridge", "netdev":
				default:
					return unknown, ErrIncomplete
				}
				key := chain.Family + "\x00" + chain.Table + "\x00" + chain.Name
				if seen[key] {
					return unknown, ErrIncomplete
				}
				seen[key] = true
				for _, selected := range []string{"family", "table", "name", "type", "hook", "policy"} {
					if v, present := fields[selected]; present && bytes.Equal(bytes.TrimSpace(v), []byte("null")) {
						return unknown, ErrIncomplete
					}
				}
				if chain.Family == "ip" && chain.Table == "filter" && chain.Name == "DOCKER-USER" && chain.Hook == "" {
					result.DockerUserChain = true
				}
				if chain.Hook != "forward" || (chain.Family != "ip" && chain.Family != "inet") {
					continue
				}
				if chain.Type != "filter" || (chain.Policy != "accept" && chain.Policy != "drop") {
					return unknown, ErrIncomplete
				}
				result.IPv4BaseChains++
				if chain.Policy == "drop" {
					result.IPv4Filter = "drop_observed"
				}
			case "table", "rule", "set", "map", "flowtable", "counter", "quota", "ct helper", "ct timeout", "ct expectation", "limit", "secmark", "synproxy":
				// No attempt to infer the final packet verdict from individual
				// rules or sets. Even no drop policy observed is not acceptance.
			default:
				return unknown, ErrIncomplete
			}
		}
	}
	return result, nil
}

func (r *localRuntime) observeForwarding(ctx context.Context) (ForwardingObservation, error) {
	if ctx == nil {
		return ForwardingObservation{}, ErrInvalid
	}
	if err := ctx.Err(); err != nil {
		return ForwardingObservation{}, err
	}
	var routing []byte
	if file, err := os.Open("/proc/sys/net/ipv4/ip_forward"); err == nil {
		body, err := io.ReadAll(io.LimitReader(file, 17))
		_ = file.Close()
		if err == nil && len(body) <= 16 {
			routing = body
		}
	}
	// A missing binary, unavailable nft backend, version or failed read is
	// diagnostic uncertainty, not permission to alter Docker or stop unrelated
	// control-plane maintenance. Parent cancellation still stops the action.
	readCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	body, code, err := r.commands.output(readCtx, fixedNFT, []string{"-j", "list", "ruleset"}, nil)
	if ctx.Err() != nil {
		return ForwardingObservation{}, ctx.Err()
	}
	if err != nil || code != 0 {
		return unknownForwarding(routing), nil
	}
	result, err := parseForwardingObservation(routing, body)
	if err != nil {
		return unknownForwarding(routing), nil
	}
	return result, nil
}

func forwardingWarnings(observed ForwardingObservation) []string {
	warnings := []string{"guest_egress_unverified"}
	switch observed.IPv4Routing {
	case "enabled":
	case "disabled":
		warnings = append(warnings, "ipv4_forwarding_disabled")
	default:
		warnings = append(warnings, "ipv4_forwarding_unobserved")
	}
	switch observed.IPv4Filter {
	case "no_drop_observed":
	case "drop_observed":
		warnings = append(warnings, "ipv4_forward_filter_drop_observed")
	default:
		warnings = append(warnings, "ipv4_forward_filter_unobserved")
	}
	return warnings
}
