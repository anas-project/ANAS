package incusingresshost

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

func TestNFTBaselineDoesNotDropUnrelatedHostForwarding(t *testing.T) {
	b, _, _ := testBackend(t)
	script := b.baselineInstallScript()
	if strings.Contains(script, "policy drop") {
		t.Fatal("scope-specific ingress installs a host-wide default-drop chain")
	}
	if !strings.Contains(script, "jump http_permits") || !strings.Contains(script, "meta ibrname") {
		t.Fatal("baseline lacks ordered permit dispatch or real ingress bridge provenance")
	}
}

func TestNFTNativeSetUsesNumericTimeoutAndInlineConcatenation(t *testing.T) {
	target := testTarget()
	body := json.RawMessage(`{"family":"inet","table":"anas_ingress_scope_one","name":"` + permitSetName(target) + `","handle":8,"type":["ipv4_addr","inet_service"],"flags":["timeout"],"timeout":30,"elem":[{"elem":{"val":{"concat":["10.42.0.2",7000]},"timeout":30,"expires":28}}]}`)
	set, err := parseNFTSet(body)
	if err != nil || !set.compatible(target) || !set.Elements["10.42.0.2.7000"] {
		t.Fatalf("real nft JSON was not understood: %+v %v", set, err)
	}
}

func TestNFTRuleRejectsExtraVerdictAndNativeCommentIsMetadata(t *testing.T) {
	b, target, _ := testBackend(t)
	spec := b.permitRuleSpecs(target)[permitComment(target)]
	raw := permitRule("anas_ingress_scope_one", "br-ingress", "incusbr0", "saddr", "daddr", "dport", "10.231.2.2", permitSetName(target), []string{"new", "established"}, permitComment(target), 10)
	expressions := raw["expr"].([]any)
	// Native nft stores the comment beside expr, never as a statement.
	filtered := make([]any, 0, len(expressions))
	for _, expression := range expressions {
		if _, comment := expression.(map[string]any)["comment"]; !comment {
			filtered = append(filtered, expression)
		}
	}
	raw["expr"], raw["comment"] = filtered, permitComment(target)
	body, _ := json.Marshal(raw)
	rule, err := parseNFTRule(body)
	if err != nil || !rule.matches(spec) {
		t.Fatalf("native rule comment was rejected: %v", err)
	}
	raw["expr"] = append([]any{map[string]any{"accept": nil}}, filtered...)
	body, _ = json.Marshal(raw)
	rule, err = parseNFTRule(body)
	if err == nil && rule.matches(spec) {
		t.Fatal("early accept plus later checks was treated as a constrained permit")
	}
}

func TestNFTPermitReadbackRequiresLiveTuple(t *testing.T) {
	b, target, state := testBackend(t)
	var document map[string][]map[string]any
	if err := json.Unmarshal([]byte(activeNFTJSON(target)), &document); err != nil {
		t.Fatal(err)
	}
	var objects []map[string]any
	for _, object := range document["nftables"] {
		if _, element := object["element"]; element {
			continue
		}
		if set, ok := object["set"].(map[string]any); ok {
			delete(set, "elem")
		}
		objects = append(objects, object)
	}
	writeFile(t, filepath.Join(state, "nft-inet.json"), nftJSON(objects))
	if err := b.confirmPermit(context.Background(), target, true); err == nil {
		t.Fatal("empty or expired permit set was reported as live")
	}
}

func TestNFTReadbackCannotHideForeignObjects(t *testing.T) {
	b, _, state := testBackend(t)
	var document map[string][]map[string]any
	if err := json.Unmarshal([]byte(baselineInetJSON()), &document); err != nil {
		t.Fatal(err)
	}
	document["nftables"] = append(document["nftables"], map[string]any{"rule": map[string]any{
		"family": "inet", "table": "anas_ingress_scope_one", "chain": "forward", "handle": 999,
		"expr": []any{map[string]any{"accept": nil}},
	}})
	body, _ := json.Marshal(document)
	writeFile(t, filepath.Join(state, "nft-inet.json"), string(body))
	if _, err := b.nftInventory(context.Background()); err == nil {
		t.Fatal("foreign rule without ANAS comment disappeared from inventory")
	}
}

func TestNFTStateNormalizationIsLimitedToEquivalentStateSets(t *testing.T) {
	want := rawNFTExpressions(nftStates([]string{"new", "established"}))
	for _, native := range []string{
		`{"match":{"left":{"ct":{"key":"state"}},"op":"in","right":["established","new"]}}`,
		`{"match":{"left":{"ct":{"key":"state"}},"op":"==","right":{"set":["established","new"]}}}`,
	} {
		if !sameNFTExpressions([]json.RawMessage{json.RawMessage(native)}, want) {
			t.Errorf("equivalent native state set rejected: %s", native)
		}
	}
	for _, native := range []string{
		`{"match":{"left":{"ct":{"key":"state"}},"op":"==","right":["established","new"]}}`,
		`{"match":{"left":{"ct":{"key":"state"}},"op":"!=","right":{"set":["established","new"]}}}`,
		`{"match":{"left":{"ct":{"key":"state"}},"op":"in","right":["established","new","related"]}}`,
		`{"match":{"left":{"ct":{"key":"state"}},"op":"in","right":["new","new","established"]}}`,
		`{"match":{"left":{"ct":{"key":"state"}},"op":"in","right":[],"extra":true}}`,
		`{"match":{"left":{"ct":{"key":"state"}},"op":"in","right":["new","established"],"extra":true}}`,
	} {
		if sameNFTExpressions([]json.RawMessage{json.RawMessage(native)}, want) {
			t.Errorf("changed state expression accepted: %s", native)
		}
	}
	flags := rawNFTExpressions(nftMatch(nftPayload("tcp", "flags"), "==", map[string]any{"set": []string{"syn", "ack"}}))
	if sameNFTExpressions([]json.RawMessage{json.RawMessage(`{"match":{"left":{"payload":{"protocol":"tcp","field":"flags"}},"op":"in","right":["syn","ack"]}}`)}, flags) {
		t.Fatal("state-specific equivalence was extended to independent TCP flag bits")
	}
}

func TestNFTNumberNormalizationKeepsProtocolConstraints(t *testing.T) {
	want := rawNFTExpressions(nftMetaMatch("l4proto", "==", "tcp"))
	if !sameNFTExpressions([]json.RawMessage{json.RawMessage(`{"match":{"left":{"meta":{"key":"l4proto"}},"op":"==","right":6}}`)}, want) {
		t.Fatal("native numeric TCP protocol was rejected")
	}
	for _, rhs := range []string{`17`, `"6"`, `{"set":[6,17]}`, `null`} {
		native := json.RawMessage(`{"match":{"left":{"meta":{"key":"l4proto"}},"op":"==","right":` + rhs + `}}`)
		if sameNFTExpressions([]json.RawMessage{native}, want) {
			t.Errorf("widened or ambiguous protocol accepted: %s", rhs)
		}
	}
}

// nft 1.1.6 on the designated Ubuntu host emits state bits in numeric
// listings. Only individual known ct-state values have a symbolic equivalent;
// compound masks, decimal/exponent aliases and unknown values stay rejected.
func TestNFTNumericStateReadbackIsExact(t *testing.T) {
	want := rawNFTExpressions(nftStates([]string{"established", "related"}))
	for _, rhs := range []string{`{"set":[2,4]}`, `{"set":[4,2]}`, `{"set":[2,"related"]}`} {
		got := []json.RawMessage{json.RawMessage(`{"match":{"left":{"ct":{"key":"state"}},"op":"==","right":` + rhs + `}}`)}
		if !sameNFTExpressions(got, want) {
			t.Errorf("observed exact numeric states rejected: %s", rhs)
		}
	}
	for _, rhs := range []string{`{"set":[2,8]}`, `{"set":[2,4,8]}`, `{"set":[2,2,4]}`, `{"set":[2,"established",4]}`, `{"set":[6]}`, `{"set":[2,4.0]}`, `{"set":[2,4e0]}`, `{"set":[2,"4"]}`, `{"set":[2,0]}`, `{"set":[2,-4]}`, `{"set":[2,128]}`} {
		got := []json.RawMessage{json.RawMessage(`{"match":{"left":{"ct":{"key":"state"}},"op":"==","right":` + rhs + `}}`)}
		if sameNFTExpressions(got, want) {
			t.Errorf("changed or ambiguous numeric state accepted: %s", rhs)
		}
	}
	for _, item := range []struct{ number, state string }{{"1", "invalid"}, {"2", "established"}, {"4", "related"}, {"8", "new"}, {"64", "untracked"}} {
		got := []json.RawMessage{json.RawMessage(`{"match":{"left":{"ct":{"key":"state"}},"op":"==","right":` + item.number + `}}`)}
		if !sameNFTExpressions(got, rawNFTExpressions(nftStates([]string{item.state}))) {
			t.Errorf("single ct state %s rejected", item.state)
		}
	}
}
