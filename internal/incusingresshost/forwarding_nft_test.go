package incusingresshost

import (
	"bytes"
	"encoding/json"
	"reflect"
	"testing"
)

// nft 1.1.6's actual r3 readback omitted these dependencies. Do not accept
// arbitrary missing predicates: the typed payload/set must itself enforce the
// same protocol, and all non-redundant expression ordering remains checked.
func TestForwardingReadbackProtocolDependencies(t *testing.T) {
	cases := []struct {
		name             string
		declared, native []json.RawMessage
	}{
		{"source-ip", rawNFTExpressions(nftPayloadMatch("ether", "type", "==", "ip"), forwardingSetMatch("sources", forwardingMeta("iif"), nftPayload("ether", "saddr"), nftPayload("ip", "saddr"))), rawNFTExpressions(forwardingSetMatch("sources", forwardingMeta("iif"), nftPayload("ether", "saddr"), nftPayload("ip", "saddr")))},
		{"source-arp", rawNFTExpressions(nftPayloadMatch("ether", "type", "==", "arp"), forwardingSetMatch("arp_sources", forwardingMeta("iif"), nftPayload("ether", "saddr"), nftPayload("arp", "saddr ether"), nftPayload("arp", "saddr ip"))), rawNFTExpressions(forwardingSetMatch("arp_sources", forwardingMeta("iif"), nftPayload("ether", "saddr"), nftPayload("arp", "saddr ether"), nftPayload("arp", "saddr ip")))},
		{"tcp-flow", rawNFTExpressions(nftMetaMatch("l4proto", "==", "tcp"), forwardingSetMatch("flows", forwardingMeta("oif"), nftPayload("ip", "saddr"), nftPayload("ip", "daddr"), nftPayload("tcp", "dport"))), rawNFTExpressions(forwardingSetMatch("flows", forwardingMeta("oif"), nftPayload("ip", "saddr"), nftPayload("ip", "daddr"), nftPayload("tcp", "dport")))},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			a, ok := forwardingNormalizeExpr(c.declared)
			b, good := forwardingNormalizeExpr(c.native)
			if !ok || !good || !reflect.DeepEqual(a, b) {
				t.Fatal("native implied protocol dependency was rejected")
			}
		})
	}
}

func TestForwardingProtocolNormalizationDoesNotEraseDistinctGuards(t *testing.T) {
	payload := forwardingSetMatch("sources", forwardingMeta("iif"), nftPayload("ether", "saddr"), nftPayload("ip", "saddr"))
	for _, guard := range []any{nftPayloadMatch("ether", "type", "==", "arp"), nftMetaMatch("l4proto", "==", "tcp")} {
		a, ok := forwardingNormalizeExpr(rawNFTExpressions(guard, payload))
		b, good := forwardingNormalizeExpr(rawNFTExpressions(payload))
		if !ok || !good || reflect.DeepEqual(a, b) {
			t.Fatal("distinct protocol predicate was erased")
		}
	}
	guard := nftPayloadMatch("ether", "type", "==", "ip")
	for _, barrier := range []any{map[string]any{"counter": map[string]any{"packets": 0, "bytes": 0}}, map[string]any{"accept": nil}, map[string]any{"jump": map[string]any{"target": "foreign"}}} {
		a, ok := forwardingNormalizeExpr(rawNFTExpressions(guard, barrier, payload))
		b, good := forwardingNormalizeExpr(rawNFTExpressions(barrier, payload))
		if !ok || !good || reflect.DeepEqual(a, b) {
			t.Fatal("protocol normalization crossed an effect")
		}
	}
}

func TestForwardingNativeInterfaceDatatypeAndExclusiveCreation(t *testing.T) {
	s, _ := forwardingKernelFixture(t)
	definitions, err := forwardingNFTDefinitions(s, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range definitions {
		for _, set := range d.Sets {
			if len(set.Types) > 1 && set.Types[0] != "iface_index" {
				t.Fatalf("native nft interface set %s uses unsupported type %q", set.Name, set.Types[0])
			}
		}
	}
	body, err := forwardingNFTCreate(s)
	if err != nil || !json.Valid(body) || bytes.Contains(body, []byte(`"ifindex"`)) {
		t.Fatal("invalid native creation grammar")
	}
	var doc struct {
		NFTables []map[string]json.RawMessage `json:"nftables"`
	}
	if json.Unmarshal(body, &doc) != nil {
		t.Fatal("invalid grammar")
	}
	creates := 0
	for _, cmd := range doc.NFTables {
		if _, ok := cmd["create"]; ok {
			creates++
		}
	}
	if creates != 2 {
		t.Fatal("owned tables must use exclusive native creation")
	}
	if len(forwardingCompatChain(s)) > 28 {
		t.Fatal("xtables chain name exceeds its native limit")
	}
}
