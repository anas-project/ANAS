package incusingresshost

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func activeReplyNFTJSON(b *Backend, target Target, index uint32) string {
	spec := b.replyOriginSpecAtIndex(target, index)
	objects := baselineObjectsFor(b, "bridge")
	objects = append(objects,
		map[string]any{"set": map[string]any{"family": "bridge", "table": b.config.OriginTable, "name": spec.Set,
			"type": []string{"ipv4_addr", "inet_service"}, "flags": []string{"timeout"}, "timeout": 30,
			"elem": []any{map[string]any{"elem": map[string]any{"val": map[string]any{"concat": []any{target.GuestIP, target.GuestPort}}, "timeout": 30, "expires": 29}}}}},
		map[string]any{"rule": map[string]any{"family": "bridge", "table": b.config.OriginTable, "chain": replyChain, "handle": 501, "comment": spec.Comment, "expr": spec.expressions()}},
	)
	return nftJSON(objects)
}

func readyReplyFixture(t *testing.T) (*Backend, *addressRouter, *addressKernelFixture, Target, string) {
	t.Helper()
	b, g, kernel, target := newAddressFixture(t)
	ctx := context.Background()
	if err := b.InstallAddressRouting(ctx); err != nil {
		t.Fatal(err)
	}
	for _, action := range []func(context.Context, Target) error{b.HoldAddress, b.EnsureGuestRoute, b.EnsureHTTPPermit} {
		if err := action(ctx, target); err != nil {
			t.Fatal(err)
		}
	}
	return b, g, kernel, target, filepath.Join(filepath.Dir(b.config.ReceiptDir), "state")
}

func TestReplyOriginRequiresIndependentIndexAndAtomicPermit(t *testing.T) {
	b, _, _, target, state := readyReplyFixture(t)
	spec, err := b.replyOriginSpec(target)
	if err != nil || spec.PhysicalIfIndex != 101 || spec.MAC != target.NICMAC {
		t.Fatalf("missing independent binding: %+v %v", spec, err)
	}
	body, err := os.ReadFile(filepath.Join(state, "nft-check-input"))
	if err != nil {
		t.Fatal(err)
	}
	for _, fragment := range []string{"add element bridge", "add element inet", "meta iif 101", "iifname " + shellQuote(target.HostVethName), "ether saddr " + target.NICMAC, "ct direction original", "ct direction reply"} {
		if !strings.Contains(string(body), fragment) {
			t.Fatalf("single transaction lacks %q", fragment)
		}
	}
	if err := b.CheckHTTPArtifacts(context.Background(), []Target{target}); err != nil {
		t.Fatal(err)
	}
}

func TestReplyOriginReadbackRejectsMissingExpiredAndForeignGate(t *testing.T) {
	for _, test := range []struct{ name, from, to string }{
		{"expired", `"expires":29`, `"expires":0`},
		{"wrong-index", `"right":101`, `"right":102`},
		{"name-is-not-index", `"right":101`, `"right":"vethguest0"`},
		{"wrong-mac", testTarget().NICMAC, "00:16:3e:99:99:99"},
	} {
		t.Run(test.name, func(t *testing.T) {
			b, _, _, target, state := readyReplyFixture(t)
			body := activeReplyNFTJSON(b, target, 101)
			changed := strings.ReplaceAll(body, test.from, test.to)
			if changed == body {
				t.Fatal("fixture mutation missed")
			}
			writeFile(t, filepath.Join(state, "nft-bridge.json"), changed)
			if err := b.CheckHTTPArtifacts(context.Background(), []Target{target}); err == nil {
				t.Fatal("unsafe reply gate accepted")
			}
		})
	}
	b, _, _, target, state := readyReplyFixture(t)
	writeFile(t, filepath.Join(state, "nft-bridge.json"), nftJSON(baselineObjectsFor(b, "bridge")))
	if err := b.confirmPermit(context.Background(), target, true); err == nil {
		t.Fatal("forward-only grant reported as ready")
	}
}

func TestReplyOriginWithdrawalUsesOldEvidenceAfterDeviceReplacement(t *testing.T) {
	b, _, kernel, target, state := readyReplyFixture(t)
	kernel.links[0]["ifindex"] = 202
	// Do not acquire the replacement identity when deleting the old permit.
	if err := b.RemoveHTTPPermit(context.Background(), target); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(filepath.Join(state, "nft-check-input"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "delete rule bridge") || !strings.Contains(string(body), "delete rule inet") || strings.Contains(string(body), "meta iif 202") {
		t.Fatal("revocation is incomplete or followed replacement identity")
	}
	if err := b.confirmPermit(context.Background(), target, false); err != nil {
		t.Fatal(err)
	}
}

func TestReplyOriginDefaultDenyAndCTDirectionAreExact(t *testing.T) {
	b, _, _, target := newAddressFixture(t)
	_, _, rules := b.baselineDefinition("bridge")
	if len(rules) < 2 || !strings.HasSuffix(rules[0].Comment, ":guest-reply-dispatch") || !strings.HasSuffix(rules[1].Comment, ":guest-reply-deny") {
		t.Fatal("reply dispatch must precede terminal deny")
	}
	for _, rule := range b.permitRuleSpecs(target) {
		body, _ := json.Marshal(rule.expressions())
		direction := "original"
		if rule.TCPField == "sport" {
			direction = "reply"
		}
		if !strings.Contains(string(body), `"right":"`+direction+`"`) {
			t.Fatal("permit has no explicit conntrack direction")
		}
		changed := strings.Replace(string(body), `"right":"`+direction+`"`, `"right":"unknown"`, 1)
		var expressions []json.RawMessage
		if err := json.Unmarshal([]byte(changed), &expressions); err != nil {
			t.Fatal(err)
		}
		if sameNFTExpressions(expressions, rule.expressions()) {
			t.Fatal("changed conntrack direction was normalized away")
		}
	}
}

func TestReplyOriginRemovalCannotTouchAnotherPort(t *testing.T) {
	b, _, _, first, state := readyReplyFixture(t)
	second := first
	second.Reservation = strings.Repeat("e", 32) + ":2"
	second.GuestPort++
	b.config.Resolver = multiResolver{first.Reservation: identityFor(first), second.Reservation: identityFor(second)}
	ctx := context.Background()
	if err := b.HoldAddress(ctx, second); err != nil {
		t.Fatal(err)
	}
	if err := b.EnsureGuestRoute(ctx, second); err != nil {
		t.Fatal(err)
	}
	r, err := b.loadReceipt(second)
	if err != nil {
		t.Fatal(err)
	}
	r.PermitIntent = true
	if err := b.saveReceipt(ctx, r); err != nil {
		t.Fatal(err)
	}
	// Inventory contains independently receipted objects for both ports.
	// Inspect the generated delete transaction rather than the simple shell
	// fixture (which cannot faithfully simulate per-rule nft transactions).
	inet, bridge := baselineObjectsFor(b, "inet"), baselineObjectsFor(b, "bridge")
	for i, target := range []Target{first, second} {
		var document struct {
			NFTables []map[string]any `json:"nftables"`
		}
		if err := json.Unmarshal([]byte(activeReplyNFTJSON(b, target, 101)), &document); err != nil {
			t.Fatal(err)
		}
		for _, item := range document.NFTables {
			if value, ok := item["set"].(map[string]any); ok {
				bridge = append(bridge, item)
				copy := make(map[string]any, len(value))
				for key, v := range value {
					copy[key] = v
				}
				copy["family"], copy["table"] = "inet", b.config.PermitTable
				inet = append(inet, map[string]any{"set": copy})
			}
			if rule, ok := item["rule"].(map[string]any); ok && rule["chain"] == replyChain {
				rule["handle"] = 501 + i
				bridge = append(bridge, item)
			}
		}
		j := 0
		for _, spec := range b.permitRuleSpecs(target) {
			j++
			inet = append(inet, map[string]any{"rule": map[string]any{"family": "inet", "table": b.config.PermitTable,
				"chain": permitChain, "handle": 100 + i*10 + j, "comment": spec.Comment, "expr": spec.expressions()}})
		}
	}
	writeFile(t, filepath.Join(state, "nft-inet.json"), nftJSON(inet))
	writeFile(t, filepath.Join(state, "nft-bridge.json"), nftJSON(bridge))
	script, err := b.permitRemoveScript(ctx, first)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{permitSetName(second), "handle 502", "handle 111", "handle 112", "flush", "delete table", "delete chain"} {
		if strings.Contains(script, forbidden) {
			t.Fatalf("one-port withdrawal touches unrelated permission: %s", forbidden)
		}
	}
	for _, required := range []string{permitSetName(first), "handle 501", "handle 101", "handle 102"} {
		if !strings.Contains(script, required) {
			t.Fatalf("incomplete withdrawal: %s", required)
		}
	}
}

func TestReplyOriginFailureRetainsIntentUntilBothFamiliesAreGone(t *testing.T) {
	b, _, _, target, state := readyReplyFixture(t)
	fail(t, state, "nft-apply")
	if err := b.RemoveHTTPPermit(context.Background(), target); err == nil {
		t.Fatal("failed withdrawal was reported as success")
	}
	r, err := b.loadReceipt(target)
	if err != nil || !r.PermitIntent {
		t.Fatalf("failed withdrawal lost its intent: %+v %v", r, err)
	}
	if err := b.CloseHTTPConnections(context.Background(), target); err == nil {
		t.Fatal("unfinished firewall withdrawal allowed connection deletion")
	}
	if err := os.Remove(filepath.Join(state, "fail")); err != nil {
		t.Fatal(err)
	}
	if err := b.RemoveHTTPPermit(context.Background(), target); err != nil {
		t.Fatal(err)
	}
	if err := b.confirmReplyOrigin(context.Background(), target, false); err != nil {
		t.Fatal(err)
	}
	if err := b.CloseHTTPConnections(context.Background(), target); err != nil {
		t.Fatal(err)
	}
}
