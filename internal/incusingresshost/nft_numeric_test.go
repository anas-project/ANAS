package incusingresshost

import (
	"encoding/json"
	"testing"
)

func TestNFTSymbolicEtherTypeRejectsAmbiguousNumericAliases(t *testing.T) {
	for _, raw := range []string{`34525`, `56710`, `2048`, `8`, `"56710"`, `null`} {
		observed := []json.RawMessage{json.RawMessage(`{"match":{"left":{"payload":{"protocol":"ether","field":"type"}},"op":"==","right":` + raw + `}}`)}
		for _, protocol := range []string{"ip", "ip6"} {
			if sameNFTExpressions(observed, rawNFTExpressions(nftPayloadMatch("ether", "type", "==", protocol))) {
				t.Fatalf("ambiguous EtherType %s accepted as %s", raw, protocol)
			}
		}
	}
}

func TestNFTOmittedEtherTypeRequiresAdjacentIPv4Proof(t *testing.T) {
	guard := nftPayloadMatch("ether", "type", "==", "ip")
	ip := nftPayloadMatch("ip", "daddr", "==", "10.42.0.2")
	tcp := nftMetaMatch("l4proto", "==", "tcp")
	accept := map[string]any{"accept": nil}
	for _, pair := range [][2][]json.RawMessage{
		{rawNFTExpressions(guard, ip, accept), rawNFTExpressions(ip, accept)},
		{rawNFTExpressions(guard, tcp, ip, accept), rawNFTExpressions(tcp, ip, accept)},
	} {
		if !sameNFTExpressions(pair[0], pair[1]) || !sameNFTExpressions(pair[1], pair[0]) {
			t.Fatal("exact redundant IPv4 dependency rejected")
		}
	}
	for _, pair := range [][2][]json.RawMessage{
		{rawNFTExpressions(guard, accept), rawNFTExpressions(accept)},
		{rawNFTExpressions(guard, tcp, accept), rawNFTExpressions(tcp, accept)},
		{rawNFTExpressions(guard, accept, ip), rawNFTExpressions(accept, ip)},
		{rawNFTExpressions(guard, nftPayloadMatch("ip6", "daddr", "==", "fd00::2"), accept), rawNFTExpressions(nftPayloadMatch("ip6", "daddr", "==", "fd00::2"), accept)},
		{rawNFTExpressions(nftPayloadMatch("ether", "type", "==", "ip6"), ip, accept), rawNFTExpressions(ip, accept)},
		{rawNFTExpressions(guard, ip, accept), rawNFTExpressions(nftPayloadMatch("ip", "daddr", "==", "10.42.0.3"), accept)},
		{rawNFTExpressions(guard, map[string]any{"counter": map[string]any{"packets": 0, "bytes": 0}}, ip, accept), rawNFTExpressions(map[string]any{"counter": map[string]any{"packets": 0, "bytes": 0}}, ip, accept)},
	} {
		if sameNFTExpressions(pair[0], pair[1]) || sameNFTExpressions(pair[1], pair[0]) {
			t.Fatal("missing, contradictory or non-adjacent protocol constraint accepted")
		}
	}
}

func TestNFTOmittedTCPDependencyRequiresExactPortLookup(t *testing.T) {
	guard := nftMetaMatch("l4proto", "==", "tcp")
	ip := nftPayloadMatch("ip", "saddr", "==", "10.231.2.2")
	lookup := nftMatch(map[string]any{"concat": []any{nftPayload("ip", "daddr"), nftPayload("tcp", "dport")}}, "==", "@approved")
	accept := map[string]any{"accept": nil}
	want := rawNFTExpressions(guard, ip, lookup, accept)
	if !sameNFTExpressions(want, rawNFTExpressions(ip, lookup, accept)) {
		t.Fatal("native TCP dependency elision rejected")
	}
	for _, changed := range [][]json.RawMessage{
		rawNFTExpressions(ip, accept),
		rawNFTExpressions(ip, lookup, map[string]any{"drop": nil}),
		rawNFTExpressions(nftMetaMatch("l4proto", "==", "udp"), ip, lookup, accept),
		rawNFTExpressions(ip, nftMatch(map[string]any{"concat": []any{nftPayload("ip", "daddr"), nftPayload("udp", "dport")}}, "==", "@approved"), accept),
		rawNFTExpressions(ip, nftMatch(map[string]any{"concat": []any{nftPayload("ip", "daddr"), nftPayload("tcp", "dport")}}, "!=", "@approved"), accept),
		rawNFTExpressions(accept, ip, lookup),
	} {
		if sameNFTExpressions(want, changed) {
			t.Fatal("changed protocol, lookup or verdict accepted")
		}
	}
	if sameNFTExpressions(rawNFTExpressions(guard, accept, ip, lookup), rawNFTExpressions(accept, ip, lookup)) {
		t.Fatal("protocol dependency moved across an early verdict")
	}
}
