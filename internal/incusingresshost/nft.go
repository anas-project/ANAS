package incusingresshost

import (
	"context"
	"encoding/json"
	"fmt"
	"net/netip"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"time"
)

const permitChain = "http_permits"
const replyChain = "http_reply_origins"

type nftInventory struct {
	Sets  map[string]nftSet
	Rules []nftRule
}
type nftSet struct {
	Family, Table, Name string
	Type, Flags         []string
	Timeout             uint64
	Elements            map[string]bool
	Lifetimes           map[string]uint64
}
type nftRule struct {
	Family  string            `json:"family"`
	Table   string            `json:"table"`
	Chain   string            `json:"chain"`
	Comment string            `json:"comment,omitempty"`
	Handle  string            `json:"-"`
	Expr    []json.RawMessage `json:"expr"`
}
type nftRuleSpec struct {
	Family, Chain, MAC                                            string
	PhysicalIfIndex                                               uint32
	Comment, Table, IIF, OIF, IPField, IP, Set, TCPField, Verdict string
	States                                                        []string
}
type nftChain struct {
	Family string `json:"family"`
	Table  string `json:"table"`
	Name   string `json:"name"`
	Type   string `json:"type,omitempty"`
	Hook   string `json:"hook,omitempty"`
	Prio   *int   `json:"prio,omitempty"`
	Policy string `json:"policy,omitempty"`
	Handle uint64 `json:"handle,omitempty"`
}
type nftTable struct {
	Family, Name string
	Handle       uint64
	Flags        []string
	Chains       []nftChain
	Rules        []nftRule
	Sets         map[string]nftSet
}

// The scope never installs a host-wide default-drop base chain. Only its own
// bridge paths are fenced; dynamic permits are reached BEFORE the fence.
func (b *Backend) baselineInstallScript() string {
	var out strings.Builder
	for _, family := range []string{"inet", "bridge"} {
		out.WriteString(b.baselineFamilyInstallScript(family))
	}
	return out.String()
}

func (b *Backend) baselineFamilyInstallScript(family string) string {
	var out strings.Builder
	table, chains, rules := b.baselineDefinition(family)
	fmt.Fprintf(&out, "create table %s %s\n", family, table)
	for _, chain := range chains {
		if chain.Hook == "" {
			fmt.Fprintf(&out, "add chain %s %s %s\n", family, table, chain.Name)
			continue
		}
		fmt.Fprintf(&out, "add chain %s %s %s { type filter hook %s priority %d; policy accept; }\n", family, table, chain.Name, chain.Hook, *chain.Prio)
	}
	for _, rule := range rules {
		fmt.Fprintf(&out, "add rule %s %s %s %s comment %s\n", family, table, rule.Chain, rule.Text, shellQuote(rule.Comment))
	}
	return out.String()
}
func (b *Backend) baselineRemoveScript() string {
	return fmt.Sprintf("delete table bridge %s\ndelete table inet %s\n", b.config.OriginTable, b.config.PermitTable)
}

type baselineRuleDefinition struct {
	Chain, Comment, Text string
	Expr                 []json.RawMessage
}

func (b *Backend) baselineDefinition(family string) (string, []nftChain, []baselineRuleDefinition) {
	c := b.config
	table, chain, hook, priority := c.PermitTable, "forward", "forward", -5
	if family == "bridge" {
		table, chain, hook, priority = c.OriginTable, "origin", "prerouting", -300
	}
	chains := []nftChain{{Family: family, Table: table, Name: chain, Type: "filter", Hook: hook, Prio: &priority, Policy: "accept"}}
	if family == "inet" {
		chains = append(chains, nftChain{Family: family, Table: table, Name: permitChain})
	} else if c.AddressRouting != nil {
		chains = append(chains, nftChain{Family: family, Table: table, Name: replyChain})
	}
	rules := []baselineRuleDefinition{}
	add := func(label, text string, expr ...any) {
		rules = append(rules, baselineRuleDefinition{Chain: chain, Comment: "anas:v2:baseline:" + c.ScopeName + ":" + label, Text: text, Expr: rawNFTExpressions(expr...)})
	}
	counter := map[string]any{"counter": map[string]any{"packets": 0, "bytes": 0}}
	drop := map[string]any{"drop": nil}
	accept := map[string]any{"accept": nil}
	q := shellQuote
	if family == "inet" {
		for _, pair := range [][3]string{{c.IngressBridge, c.GuestBridge, "request"}, {c.GuestBridge, c.IngressBridge, "reply"}} {
			if c.AddressRouting != nil && pair[2] == "request" {
				prefix := netip.MustParsePrefix(c.GuestSubnet).Masked()
				value := map[string]any{"prefix": map[string]any{"addr": prefix.Addr().String(), "len": prefix.Bits()}}
				add("request-dispatch", "iifname "+q(c.IngressBridge)+" ip daddr "+c.GuestSubnet+" jump "+permitChain, nftMetaMatch("iifname", "==", c.IngressBridge), nftPayloadMatch("ip", "daddr", "==", value), map[string]any{"jump": map[string]any{"target": permitChain}})
				continue
			}
			add(pair[2]+"-dispatch", "iifname "+q(pair[0])+" oifname "+q(pair[1])+" jump "+permitChain, nftMetaMatch("iifname", "==", pair[0]), nftMetaMatch("oifname", "==", pair[1]), map[string]any{"jump": map[string]any{"target": permitChain}})
		}
		for _, pair := range [][3]string{{c.IngressBridge, c.GuestBridge, "request"}, {c.GuestBridge, c.IngressBridge, "reply"}} {
			if c.AddressRouting != nil && pair[2] == "request" {
				prefix := netip.MustParsePrefix(c.GuestSubnet).Masked()
				value := map[string]any{"prefix": map[string]any{"addr": prefix.Addr().String(), "len": prefix.Bits()}}
				add("request-deny", "iifname "+q(c.IngressBridge)+" ip daddr "+c.GuestSubnet+" counter drop", nftMetaMatch("iifname", "==", c.IngressBridge), nftPayloadMatch("ip", "daddr", "==", value), counter, drop)
				continue
			}
			add(pair[2]+"-deny", "iifname "+q(pair[0])+" oifname "+q(pair[1])+" counter drop", nftMetaMatch("iifname", "==", pair[0]), nftMetaMatch("oifname", "==", pair[1]), counter, drop)
		}
		// Preserve replies to guest-initiated NAT egress, not unsolicited LAN or
		// cross-lease ingress. Earlier pair fences still revoke established HTTP.
		add("egress-reply", "oifname "+q(c.GuestBridge)+" ct direction reply ct state == { established, related } accept", nftMetaMatch("oifname", "==", c.GuestBridge), nftMatch(map[string]any{"ct": map[string]any{"key": "direction"}}, "==", "reply"), nftStates([]string{"established", "related"}), accept)
		add("unsolicited-deny", "oifname "+q(c.GuestBridge)+" counter drop", nftMetaMatch("oifname", "==", c.GuestBridge), counter, drop)
	} else {
		if c.AddressRouting != nil {
			// Check the physical guest ingress port before the bridge obscures
			// it behind its master. No conntrack state can bypass this gate.
			add("guest-reply-dispatch", "meta ibrname "+q(c.GuestBridge)+" ether type ip ip daddr "+c.TraefikSourceIP+" jump "+replyChain,
				nftMetaMatch("ibrname", "==", c.GuestBridge), nftPayloadMatch("ether", "type", "==", "ip"), nftPayloadMatch("ip", "daddr", "==", c.TraefikSourceIP), map[string]any{"jump": map[string]any{"target": replyChain}})
			add("guest-reply-deny", "meta ibrname "+q(c.GuestBridge)+" ether type ip ip daddr "+c.TraefikSourceIP+" counter drop",
				nftMetaMatch("ibrname", "==", c.GuestBridge), nftPayloadMatch("ether", "type", "==", "ip"), nftPayloadMatch("ip", "daddr", "==", c.TraefikSourceIP), counter, drop)
		}
		add("source-interface", "meta ibrname "+q(c.IngressBridge)+" iifname != "+q(c.TraefikVeth)+" counter drop", nftMetaMatch("ibrname", "==", c.IngressBridge), nftMetaMatch("iifname", "!=", c.TraefikVeth), counter, drop)
		add("source-mac", "iifname "+q(c.TraefikVeth)+" ether saddr != "+c.Namespace.TraefikMAC+" counter drop", nftMetaMatch("iifname", "==", c.TraefikVeth), nftPayloadMatch("ether", "saddr", "!=", c.Namespace.TraefikMAC), counter, drop)
		add("source-ip", "iifname "+q(c.TraefikVeth)+" ether type ip ip saddr != "+c.TraefikSourceIP+" counter drop", nftMetaMatch("iifname", "==", c.TraefikVeth), nftPayloadMatch("ether", "type", "==", "ip"), nftPayloadMatch("ip", "saddr", "!=", c.TraefikSourceIP), counter, drop)
		add("no-ipv6-backend", "iifname "+q(c.TraefikVeth)+" ether type ip6 counter drop", nftMetaMatch("iifname", "==", c.TraefikVeth), nftPayloadMatch("ether", "type", "==", "ip6"), counter, drop)
	}
	return table, chains, rules
}
func nftMatch(left any, op string, right any) any {
	return map[string]any{"match": map[string]any{"left": left, "op": op, "right": right}}
}
func nftMetaMatch(key, op string, value any) any {
	return nftMatch(map[string]any{"meta": map[string]any{"key": key}}, op, value)
}
func nftPayload(protocol, field string) any {
	return map[string]any{"payload": map[string]any{"protocol": protocol, "field": field}}
}
func nftPayloadMatch(protocol, field, op string, value any) any {
	return nftMatch(nftPayload(protocol, field), op, value)
}
func nftStates(states []string) any {
	var right any = states[0]
	if len(states) > 1 {
		right = map[string]any{"set": states}
	}
	return nftMatch(map[string]any{"ct": map[string]any{"key": "state"}}, "==", right)
}
func rawNFTExpressions(values ...any) []json.RawMessage {
	out := make([]json.RawMessage, 0, len(values))
	for _, v := range values {
		body, _ := json.Marshal(v)
		out = append(out, body)
	}
	return out
}
func (b *Backend) permitRuleSpecs(target Target) map[string]nftRuleSpec {
	comment, set := permitComment(target), permitSetName(target)
	guestOutput := b.config.GuestBridge
	if b.config.AddressRouting != nil {
		guestOutput = target.HostVethName
	}
	return map[string]nftRuleSpec{
		comment:            {Comment: comment, Table: b.config.PermitTable, IIF: b.config.IngressBridge, OIF: guestOutput, IPField: "saddr", IP: b.config.TraefikSourceIP, Set: set, TCPField: "dport", States: []string{"new", "established"}, Verdict: "accept"},
		comment + ":reply": {Comment: comment + ":reply", Table: b.config.PermitTable, IIF: b.config.GuestBridge, OIF: b.config.IngressBridge, IPField: "daddr", IP: b.config.TraefikSourceIP, Set: set, TCPField: "sport", States: []string{"established"}, Verdict: "accept"},
	}
}
func (s nftRuleSpec) expressions() []json.RawMessage {
	if s.Family == "bridge" {
		return rawNFTExpressions(nftMetaMatch("iif", "==", s.PhysicalIfIndex), nftMetaMatch("iifname", "==", s.IIF),
			nftPayloadMatch("ether", "saddr", "==", s.MAC), nftPayloadMatch("ether", "type", "==", "ip"),
			nftMetaMatch("l4proto", "==", "tcp"), nftPayloadMatch("ip", "daddr", "==", s.IP),
			nftMatch(map[string]any{"concat": []any{nftPayload("ip", "saddr"), nftPayload("tcp", "sport")}}, "==", "@"+s.Set), map[string]any{"accept": nil})
	}
	other := "saddr"
	direction := "reply"
	if s.IPField == "saddr" {
		other = "daddr"
		direction = "original"
	}
	return rawNFTExpressions(nftMetaMatch("iifname", "==", s.IIF), nftMetaMatch("oifname", "==", s.OIF), nftMetaMatch("l4proto", "==", "tcp"), nftPayloadMatch("ip", s.IPField, "==", s.IP), nftMatch(map[string]any{"concat": []any{nftPayload("ip", other), nftPayload("tcp", s.TCPField)}}, "==", "@"+s.Set), nftMatch(map[string]any{"ct": map[string]any{"key": "direction"}}, "==", direction), nftStates(s.States), map[string]any{s.Verdict: nil})
}
func (r nftRule) matches(s nftRuleSpec) bool {
	family, chain := "inet", permitChain
	if s.Family == "bridge" {
		family, chain = "bridge", replyChain
	}
	return s.Comment != "" && r.Family == family && r.Table == s.Table && r.Chain == chain && r.Handle != "" && r.Comment == s.Comment && sameNFTExpressions(r.Expr, s.expressions())
}

// Compare the complete ordered AST. Searching for expected fragments permits
// earlier verdicts, extra side effects and contradictory constraints.
func sameNFTExpressions(a, b []json.RawMessage) bool {
	x, ok := normalizedNFTExpressions(a)
	if !ok {
		return false
	}
	y, ok := normalizedNFTExpressions(b)
	return ok && reflect.DeepEqual(x, y)
}

func normalizedNFTExpressions(raw []json.RawMessage) ([]any, bool) {
	values := make([]any, len(raw))
	for i := range raw {
		if decodeObservedJSON(raw[i], &values[i]) != nil || !normalizeNFTExpression(values[i]) {
			return nil, false
		}
	}
	// nft omits the EtherType dependency immediately before an IPv4 payload
	// match (optionally preceded by meta l4proto). Normalize only this exact
	// redundancy. Never cross counters, verdicts, unknown matches or a guard
	// for a different protocol; the remaining AST stays complete and ordered.
	out := make([]any, 0, len(values))
	for i, value := range values {
		if reflect.DeepEqual(value, nftPayloadMatch("ether", "type", "==", "ip")) {
			next := i + 1
			if next < len(values) && reflect.DeepEqual(values[next], nftMetaMatch("l4proto", "==", "tcp")) {
				next++
			}
			if next < len(values) && isNFTIPv4AddressMatch(values[next]) {
				continue
			}
		}
		// An IPv4 address match followed immediately by the typed IPv4/TCP
		// port-set lookup also entails TCP. nft removes that dependency from
		// its JSON; it is not permission to skip arbitrary protocol predicates.
		if reflect.DeepEqual(value, nftMetaMatch("l4proto", "==", "tcp")) && i+2 < len(values) &&
			isNFTIPv4AddressMatch(values[i+1]) && isNFTIPv4TCPSetMatch(values[i+2]) {
			continue
		}
		out = append(out, value)
	}
	return out, true
}

func isNFTIPv4TCPSetMatch(value any) bool {
	object, ok := value.(map[string]any)
	if !ok || len(object) != 1 {
		return false
	}
	match, ok := object["match"].(map[string]any)
	if !ok || len(match) != 3 || match["op"] != "==" {
		return false
	}
	left, ok := match["left"].(map[string]any)
	if !ok || len(left) != 1 {
		return false
	}
	parts, ok := left["concat"].([]any)
	if !ok || len(parts) != 2 || (!reflect.DeepEqual(parts[0], nftPayload("ip", "saddr")) && !reflect.DeepEqual(parts[0], nftPayload("ip", "daddr"))) ||
		(!reflect.DeepEqual(parts[1], nftPayload("tcp", "sport")) && !reflect.DeepEqual(parts[1], nftPayload("tcp", "dport"))) {
		return false
	}
	set, ok := match["right"].(string)
	return ok && strings.HasPrefix(set, "@") && nftName.MatchString(strings.TrimPrefix(set, "@"))
}

func isNFTIPv4AddressMatch(value any) bool {
	object, ok := value.(map[string]any)
	if !ok || len(object) != 1 {
		return false
	}
	match, ok := object["match"].(map[string]any)
	if !ok || len(match) != 3 || (match["op"] != "==" && match["op"] != "!=") {
		return false
	}
	left, ok := match["left"].(map[string]any)
	if !ok || len(left) != 1 {
		return false
	}
	payload, ok := left["payload"].(map[string]any)
	if !ok || len(payload) != 2 || payload["protocol"] != "ip" || (payload["field"] != "saddr" && payload["field"] != "daddr") {
		return false
	}
	address, ok := match["right"].(string)
	ip, err := netip.ParseAddr(address)
	return ok && err == nil && ip.Is4()
}
func normalizeNFTExpression(value any) bool {
	object, ok := value.(map[string]any)
	if !ok || len(object) != 1 {
		return false
	}
	if raw, ok := object["counter"]; ok {
		c, ok := raw.(map[string]any)
		if !ok || len(c) != 2 {
			return false
		}
		for _, k := range []string{"packets", "bytes"} {
			if _, e := nftNumber(c[k], 64); e != nil {
				return false
			}
			c[k] = json.Number("0")
		}
	}
	match, ok := object["match"].(map[string]any)
	if !ok {
		return true
	}
	left, ok := match["left"].(map[string]any)
	if !ok {
		return true
	}
	if meta, ok := left["meta"].(map[string]any); ok && meta["key"] == "l4proto" && match["right"] == json.Number("6") {
		match["right"] = "tcp"
	}
	if payload, ok := left["payload"].(map[string]any); ok && payload["protocol"] == "ether" && payload["field"] == "type" {
		// Numeric nft 1.1.6 output can collide for distinct EtherTypes.
		// Inventory deliberately requests symbolic output; only the two
		// explicit protocol names used by our policy are understood here.
		if match["right"] != "ip" && match["right"] != "ip6" {
			return false
		}
	}
	if ct, ok := left["ct"].(map[string]any); ok && ct["key"] == "direction" && match["right"] == json.Number("1") {
		match["right"] = "reply"
	}
	if ct, ok := left["ct"].(map[string]any); ok && ct["key"] == "direction" && match["right"] == json.Number("0") {
		match["right"] = "original"
	}
	if ct, ok := left["ct"].(map[string]any); ok && ct["key"] == "state" {
		// ct_state bits are mutually exclusive. nft serializes implicit bitmask
		// matches as "in"; explicit anonymous-set equality is equivalent here,
		// but never generalize this normalization to tcp flags or arbitrary masks.
		op := match["op"]
		if op != "==" && op != "in" {
			return false
		}
		var values []any
		switch right := match["right"].(type) {
		case string:
			values = []any{right}
		case json.Number:
			values = []any{right}
		case map[string]any:
			if len(right) != 1 {
				return false
			}
			var ok bool
			values, ok = right["set"].([]any)
			if !ok {
				return false
			}
		case []any:
			if op != "in" {
				return false
			}
			values = right
		default:
			return false
		}
		if len(values) == 0 {
			return false
		}
		states := []string{}
		for _, value := range values {
			state, ok := nftConntrackState(value)
			if !ok || slices.Contains(states, state) {
				return false
			}
			states = append(states, state)
		}
		slices.Sort(states)
		match["op"], match["right"] = "==", map[string]any{"set": states}
	}

	return true
}

// Numeric listings expose the individual kernel ct-state bits. Do not treat
// arbitrary bitmasks or stringified numbers as state names: doing so could
// silently widen a permit or merge distinct predicates.
func nftConntrackState(value any) (string, bool) {
	if number, ok := value.(json.Number); ok {
		switch number.String() {
		case "1":
			return "invalid", true
		case "2":
			return "established", true
		case "4":
			return "related", true
		case "8":
			return "new", true
		case "64":
			return "untracked", true
		}
		return "", false
	}
	state, ok := value.(string)
	return state, ok && slices.Contains([]string{"invalid", "established", "related", "new", "untracked"}, state)
}
func (s nftSet) compatible(target Target) bool {
	key := target.GuestIP + "." + strconv.FormatUint(uint64(target.GuestPort), 10)
	return slices.Equal(s.Type, []string{"ipv4_addr", "inet_service"}) && slices.Equal(s.Flags, []string{"timeout"}) && s.Timeout > 0 && s.Timeout <= 60 && (len(s.Elements) == 0 || len(s.Elements) == 1 && s.Elements[key])
}
func (s nftSet) live(target Target) bool {
	key := target.GuestIP + "." + strconv.FormatUint(uint64(target.GuestPort), 10)
	return s.compatible(target) && len(s.Elements) == 1 && s.Elements[key] && s.Lifetimes[key] > 0 && s.Lifetimes[key] <= s.Timeout
}

func (b *Backend) validateNFTBaseline(table nftTable, family string) error {
	name, chains, rules := b.baselineDefinition(family)
	if table.Family != family || table.Name != name || len(table.Flags) != 0 || len(table.Chains) != len(chains) {
		return fmt.Errorf("owned nft baseline table identity changed")
	}
	for _, want := range chains {
		found := false
		for _, got := range table.Chains {
			got.Handle = 0
			if reflect.DeepEqual(got, want) {
				found = true
			}
		}
		if !found {
			return fmt.Errorf("owned nft chain identity, priority or policy changed")
		}
	}
	observed := []nftRule{}
	for _, r := range table.Rules {
		if family == "inet" && r.Chain == permitChain || family == "bridge" && b.config.AddressRouting != nil && r.Chain == replyChain {
			continue
		}
		observed = append(observed, r)
	}
	if len(observed) != len(rules) {
		return fmt.Errorf("owned nft baseline rule inventory changed")
	}
	for i, want := range rules {
		got := observed[i]
		if got.Chain != want.Chain || got.Comment != want.Comment || !sameNFTExpressions(got.Expr, want.Expr) {
			return fmt.Errorf("owned nft baseline rule identity or order changed")
		}
	}
	if family == "bridge" && b.config.AddressRouting == nil && len(table.Sets) != 0 {
		return fmt.Errorf("unexpected nft origin set")
	}
	return nil
}
func (b *Backend) nftInventory(ctx context.Context) (nftInventory, error) {
	return b.nftFamilyInventory(ctx, "inet")
}

func (b *Backend) nftFamilyInventory(ctx context.Context, family string) (nftInventory, error) {
	name, chain := b.config.PermitTable, permitChain
	if family == "bridge" && b.config.AddressRouting != nil {
		name, chain = b.config.OriginTable, replyChain
	} else if family != "inet" {
		return nftInventory{}, fmt.Errorf("unsupported nft permit family")
	}
	// Keep protocol symbols: fully numeric output is ambiguous for some
	// EtherTypes. Interface names must not substitute for numeric identity.
	read := func() (nftTable, error) {
		body, err := b.runner.output(ctx, b.config.Binaries.NFT, []string{"-j", "-a", "-y", "-T", "list", "table", family, name})
		if err != nil {
			return nftTable{}, fmt.Errorf("read owned nft permit table: %w", err)
		}
		return parseNFTTable(body)
	}
	var table nftTable
	var err error
	if family == "bridge" && !b.config.fixture {
		table, err = readNFTTableWithKernelIndices(ctx, name, read)
	} else {
		table, err = read()
	}
	if err != nil {
		return nftInventory{}, err
	}
	if err = b.validateNFTBaseline(table, family); err != nil {
		return nftInventory{}, err
	}
	owned, err := b.loadBaselineReceipt()
	expectedHandle := owned.InetHandle
	if family == "bridge" {
		expectedHandle = owned.BridgeHandle
	}
	if err != nil || owned.State != "installed" || expectedHandle != table.Handle {
		return nftInventory{}, fmt.Errorf("nft table lacks installed ownership evidence")
	}
	inv := nftInventory{Sets: table.Sets}
	for _, r := range table.Rules {
		if r.Chain == chain {
			inv.Rules = append(inv.Rules, r)
		}
	}
	// No filtering by comments or name prefixes: every object needs independent
	// durable evidence, including partial effects with an outstanding intent.
	receipts, err := b.listReceipts()
	if err != nil {
		return nftInventory{}, err
	}
	knownSets := map[string]Target{}
	specs := map[string]nftRuleSpec{}
	for _, r := range receipts {
		if !r.PermitReady && !r.PermitIntent {
			continue
		}
		knownSets[permitSetName(r.Target)] = r.Target
		if family == "bridge" {
			spec, err := b.replyOriginSpec(r.Target)
			if err != nil {
				return nftInventory{}, err
			}
			specs[spec.Comment] = spec
		} else {
			for k, s := range b.permitRuleSpecs(r.Target) {
				specs[k] = s
			}
		}
	}
	for setName, set := range inv.Sets {
		target, ok := knownSets[setName]
		if !ok || set.Family != family || set.Table != name || !set.compatible(target) || set.Timeout != uint64(b.config.PermitTTL.Round(time.Second)/time.Second) {
			return nftInventory{}, fmt.Errorf("unaccounted or changed nft set")
		}
	}
	seen := map[string]bool{}
	for _, r := range inv.Rules {
		spec, ok := specs[r.Comment]
		if !ok || seen[r.Comment] || !r.matches(spec) {
			return nftInventory{}, fmt.Errorf("unaccounted, duplicate or changed nft permit rule")
		}
		seen[r.Comment] = true
	}
	return inv, nil
}

func parseNFTTable(body []byte) (nftTable, error) {
	var raw struct {
		NFTables []map[string]json.RawMessage `json:"nftables"`
	}
	if decodeObservedJSON(body, &raw) != nil || len(raw.NFTables) == 0 || len(raw.NFTables) > 8192 {
		return nftTable{}, fmt.Errorf("incomplete nft JSON observation")
	}
	out := nftTable{Sets: map[string]nftSet{}}
	chainNames := map[string]bool{}
	handles := map[string]bool{}
	tableSeen := false
	metaSeen := false
	for _, entry := range raw.NFTables {
		if len(entry) != 1 {
			return nftTable{}, fmt.Errorf("ambiguous nft object")
		}
		for kind, body := range entry {
			switch kind {
			case "metainfo":
				var meta struct {
					Version string `json:"version"`
					Release string `json:"release_name"`
					Schema  int    `json:"json_schema_version"`
				}
				if metaSeen || decodeObservedJSON(body, &meta) != nil || meta.Schema != 1 {
					return nftTable{}, fmt.Errorf("unsupported nft JSON schema")
				}
				metaSeen = true
			case "table":
				var t struct {
					Family  string   `json:"family"`
					Name    string   `json:"name"`
					Handle  uint64   `json:"handle,omitempty"`
					Flags   []string `json:"flags,omitempty"`
					Comment string   `json:"comment,omitempty"`
				}
				if tableSeen || decodeObservedJSON(body, &t) != nil || (t.Family != "inet" && t.Family != "bridge") || !nftName.MatchString(t.Name) {
					return nftTable{}, fmt.Errorf("invalid nft table identity")
				}
				out.Family, out.Name, out.Flags, out.Handle = t.Family, t.Name, t.Flags, t.Handle
				tableSeen = true
			case "chain":
				var c nftChain
				if decodeObservedJSON(body, &c) != nil || c.Family != out.Family || c.Table != out.Name || !nftName.MatchString(c.Name) || chainNames[c.Name] {
					return nftTable{}, fmt.Errorf("invalid or duplicate nft chain")
				}
				chainNames[c.Name] = true
				out.Chains = append(out.Chains, c)
			case "set":
				set, err := parseNFTSet(body)
				if err != nil || set.Family != out.Family || set.Table != out.Name {
					return nftTable{}, fmt.Errorf("invalid nft set scope")
				}
				if _, ok := out.Sets[set.Name]; ok {
					return nftTable{}, fmt.Errorf("duplicate nft set")
				}
				out.Sets[set.Name] = set
			case "element":
				var e struct {
					Family string          `json:"family"`
					Table  string          `json:"table"`
					Name   string          `json:"name"`
					Elem   json.RawMessage `json:"elem"`
				}
				if decodeObservedJSON(body, &e) != nil || e.Family != out.Family || e.Table != out.Name {
					return nftTable{}, fmt.Errorf("invalid nft element scope")
				}
				set, ok := out.Sets[e.Name]
				if !ok || len(set.Elements) != 0 {
					return nftTable{}, fmt.Errorf("ambiguous nft element inventory")
				}
				if err := set.readElements(e.Elem); err != nil {
					return nftTable{}, err
				}
				out.Sets[e.Name] = set
			case "rule":
				r, err := parseNFTRule(body)
				if err != nil || r.Family != out.Family || r.Table != out.Name || !chainNames[r.Chain] {
					return nftTable{}, fmt.Errorf("invalid nft rule scope")
				}
				key := r.Chain + ":" + r.Handle
				if handles[key] {
					return nftTable{}, fmt.Errorf("duplicate nft rule handle")
				}
				handles[key] = true
				out.Rules = append(out.Rules, r)
			default:
				return nftTable{}, fmt.Errorf("unexpected nft object kind")
			}
		}
	}
	if !tableSeen {
		return nftTable{}, fmt.Errorf("nft table is missing")
	}
	return out, nil
}
func parseNFTSet(body json.RawMessage) (nftSet, error) {
	var raw struct {
		Family   string          `json:"family"`
		Table    string          `json:"table"`
		Name     string          `json:"name"`
		Handle   uint64          `json:"handle,omitempty"`
		Type     []string        `json:"type"`
		Flags    []string        `json:"flags"`
		Timeout  uint64          `json:"timeout"`
		Elements json.RawMessage `json:"elem,omitempty"`
		GC       uint64          `json:"gc-interval,omitempty"`
		Size     uint64          `json:"size,omitempty"`
		Policy   string          `json:"policy,omitempty"`
	}
	if decodeObservedJSON(body, &raw) != nil || !nftName.MatchString(raw.Name) || raw.Timeout == 0 || raw.Timeout > 60 || raw.Policy != "" && raw.Policy != "performance" {
		return nftSet{}, fmt.Errorf("invalid nft set record")
	}
	out := nftSet{Family: raw.Family, Table: raw.Table, Name: raw.Name, Type: raw.Type, Flags: raw.Flags, Timeout: raw.Timeout, Elements: map[string]bool{}, Lifetimes: map[string]uint64{}}
	if len(raw.Elements) > 0 {
		if err := out.readElements(raw.Elements); err != nil {
			return nftSet{}, err
		}
	}
	return out, nil
}
func (s *nftSet) readElements(body json.RawMessage) error {
	var raw any
	if decodeObservedJSON(body, &raw) != nil {
		return fmt.Errorf("invalid nft set elements")
	}
	items, ok := raw.([]any)
	if !ok {
		items = []any{raw}
	}
	if len(items) > 1 {
		return fmt.Errorf("per-publication nft set contains extra tuples")
	}
	for _, item := range items {
		wrapper, ok := item.(map[string]any)
		if !ok || len(wrapper) != 1 {
			return fmt.Errorf("invalid nft element encoding")
		}
		value, expires := any(wrapper), uint64(0)
		if e, ok := wrapper["elem"].(map[string]any); ok {
			for k := range e {
				if k != "val" && k != "timeout" && k != "expires" {
					return fmt.Errorf("unexpected nft element property")
				}
			}
			value = e["val"]
			if timeout, ok := e["timeout"]; ok {
				n, err := nftNumber(timeout, 64)
				if err != nil || n != s.Timeout {
					return fmt.Errorf("nft element timeout changed")
				}
			}
			if v, ok := e["expires"]; ok {
				n, err := nftNumber(v, 64)
				if err != nil || n > s.Timeout {
					return fmt.Errorf("nft element expiry is invalid")
				}
				expires = n
			}
		}
		concat, ok := value.(map[string]any)
		if !ok || len(concat) != 1 {
			return fmt.Errorf("invalid nft tuple")
		}
		fields, ok := concat["concat"].([]any)
		if !ok || len(fields) != 2 {
			return fmt.Errorf("invalid nft concatenation")
		}
		ip, ok := fields[0].(string)
		address, err := netip.ParseAddr(ip)
		if !ok || err != nil || !address.Is4() || !address.IsPrivate() || address.String() != ip {
			return fmt.Errorf("invalid nft tuple address")
		}
		port, err := nftNumber(fields[1], 16)
		if err != nil || port == 0 {
			return fmt.Errorf("invalid nft tuple port")
		}
		key := ip + "." + strconv.FormatUint(port, 10)
		if s.Elements[key] {
			return fmt.Errorf("duplicate nft tuple")
		}
		s.Elements[key] = true
		s.Lifetimes[key] = expires
	}
	return nil
}
func parseNFTRule(body json.RawMessage) (nftRule, error) {
	var raw struct {
		Family  string            `json:"family"`
		Table   string            `json:"table"`
		Chain   string            `json:"chain"`
		Handle  uint64            `json:"handle"`
		Comment string            `json:"comment,omitempty"`
		Expr    []json.RawMessage `json:"expr"`
	}
	if decodeObservedJSON(body, &raw) != nil || raw.Handle == 0 || len(raw.Expr) == 0 || len(raw.Expr) > 32 {
		return nftRule{}, fmt.Errorf("invalid nft rule")
	}
	return nftRule{Family: raw.Family, Table: raw.Table, Chain: raw.Chain, Handle: strconv.FormatUint(raw.Handle, 10), Comment: raw.Comment, Expr: raw.Expr}, nil
}
func nftNumber(value any, bits int) (uint64, error) {
	number, ok := value.(json.Number)
	if !ok {
		return 0, fmt.Errorf("expected native nft integer")
	}
	return strconv.ParseUint(number.String(), 10, bits)
}
