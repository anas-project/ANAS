//go:build linux

package incusingresshost

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"golang.org/x/sys/unix"
)

func TestForwardingKernelRawIndices(t *testing.T) {
	for _, index := range []uint32{0, 77, 78} {
		for _, key := range []uint32{unix.NFT_META_IIF, unix.NFT_META_OIF} {
			body := rawNFTIndexFixture("scope", index, key, unix.NFT_CMP_EQ, unix.NFT_REG_1)
			handle, values, err := forwardingKernelRuleIndices(body, 7, "scope")
			want := "iif"
			if key == unix.NFT_META_OIF {
				want = "oif"
			}
			if err != nil || handle != replyChain+"\x0012" || len(values) != 1 || values[0] != (forwardingIndexLiteral{Key: want, Index: index}) {
				t.Fatalf("raw identity: %q %+v %v", handle, values, err)
			}
			for n := 0; n < len(body); n++ {
				if _, _, err := forwardingKernelRuleIndices(body[:n], 7, "scope"); err == nil {
					t.Fatalf("truncation %d", n)
				}
			}
		}
	}
	for _, body := range [][]byte{
		rawNFTIndexFixture("other", 77, unix.NFT_META_IIF, unix.NFT_CMP_EQ, unix.NFT_REG_1),
		rawNFTIndexFixture("scope", 77, unix.NFT_META_IIF, unix.NFT_CMP_NEQ, unix.NFT_REG_1),
		rawNFTIndexFixture("scope", 77, unix.NFT_META_IIF, unix.NFT_CMP_EQ, unix.NFT_REG_2),
	} {
		if _, _, err := forwardingKernelRuleIndices(body, 7, "scope"); err == nil {
			t.Fatal("invalid raw scope/comparison accepted")
		}
	}
}

func TestForwardingKernelIndexBindingRequiresExactRawRule(t *testing.T) {
	body := []byte(`{"nftables":[{"rule":{"family":"inet","table":"scope","chain":"forward","handle":12,"expr":[{"match":{"op":"==","left":{"meta":{"key":"iif"}},"right":"reused-veth"}},{"accept":null}]}}]}`)
	for _, index := range []uint32{77, 78} {
		proof := map[string][]forwardingIndexLiteral{"forward\x0012": {{Key: "iif", Index: index}}}
		out, err := bindForwardingKernelIndices(body, "inet", "scope", proof)
		if err != nil {
			t.Fatal(err)
		}
		var doc struct {
			NFTables []map[string]json.RawMessage `json:"nftables"`
		}
		if json.Unmarshal(out, &doc) != nil {
			t.Fatal("invalid bound JSON")
		}
		rule, err := parseNFTRule(doc.NFTables[0]["rule"])
		if err != nil {
			t.Fatal(err)
		}
		if !sameNFTExpressions(rule.Expr, rawNFTExpressions(nftMetaMatch("iif", "==", index), map[string]any{"accept": nil})) {
			t.Fatal("used interface name instead of kernel index")
		}
	}
	for _, proof := range []map[string][]forwardingIndexLiteral{
		nil, {"forward\x0013": {{Key: "iif", Index: 77}}}, {"forward\x0012": {{Key: "oif", Index: 77}}},
		{"forward\x0012": {{Key: "iif", Index: 77}}, "forward\x0013": {}},
	} {
		if _, err := bindForwardingKernelIndices(body, "inet", "scope", proof); err == nil {
			t.Fatal("unbound raw evidence accepted")
		}
	}
	numeric := []byte(strings.Replace(string(body), `"reused-veth"`, `78`, 1))
	if _, err := bindForwardingKernelIndices(numeric, "inet", "scope", map[string][]forwardingIndexLiteral{"forward\x0012": {{Key: "iif", Index: 77}}}); err == nil {
		t.Fatal("contradictory numeric JSON accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := readForwardingNFTWithKernelIndices(ctx, "inet", "scope", func() ([]byte, error) { t.Fatal("canceled query executed"); return nil, nil }); err == nil {
		t.Fatal("cancellation ignored")
	}
}
