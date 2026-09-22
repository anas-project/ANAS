//go:build linux

package incusingresshost

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"strings"
	"testing"

	"golang.org/x/sys/unix"
)

func rawNFTIndexFixture(table string, index uint32, metaKey, op, sourceReg uint32) []byte {
	be := func(value uint32) []byte {
		data := make([]byte, 4)
		binary.BigEndian.PutUint32(data, value)
		return data
	}
	join := func(parts ...[]byte) []byte {
		var out []byte
		for _, part := range parts {
			out = append(out, part...)
		}
		return out
	}
	attr := nftNetlinkAttribute
	expression := func(name string, parts ...[]byte) []byte {
		return attr(unix.NFTA_LIST_ELEM|unix.NLA_F_NESTED, join(attr(unix.NFTA_EXPR_NAME, append([]byte(name), 0)), attr(unix.NFTA_EXPR_DATA|unix.NLA_F_NESTED, join(parts...))))
	}
	literal := make([]byte, 4)
	binary.NativeEndian.PutUint32(literal, index)
	handle := make([]byte, 8)
	binary.BigEndian.PutUint64(handle, 12)
	return join([]byte{7, 0, 0, 0}, attr(unix.NFTA_RULE_TABLE, append([]byte(table), 0)), attr(unix.NFTA_RULE_CHAIN, append([]byte(replyChain), 0)), attr(unix.NFTA_RULE_HANDLE, handle),
		attr(unix.NFTA_RULE_EXPRESSIONS|unix.NLA_F_NESTED, join(
			expression("meta", attr(unix.NFTA_META_DREG, be(unix.NFT_REG_1)), attr(unix.NFTA_META_KEY, be(metaKey))),
			expression("cmp", attr(unix.NFTA_CMP_SREG, be(sourceReg)), attr(unix.NFTA_CMP_OP, be(op)), attr(unix.NFTA_CMP_DATA|unix.NLA_F_NESTED, attr(unix.NFTA_DATA_VALUE, literal))),
		)))
}

func TestNFTKernelIndexParserRejectsChangedAndTruncatedProof(t *testing.T) {
	valid := rawNFTIndexFixture("scope", 77, unix.NFT_META_IIF, unix.NFT_CMP_EQ, unix.NFT_REG_1)
	if handle, index, err := nftKernelRuleIndex(valid, "scope"); err != nil || handle != "12" || index != 77 {
		t.Fatalf("valid literal: %s/%d %v", handle, index, err)
	}
	for length := 0; length < len(valid); length++ {
		if _, _, err := nftKernelRuleIndex(valid[:length], "scope"); err == nil {
			t.Fatalf("accepted truncation at %d", length)
		}
	}
	for _, body := range [][]byte{
		rawNFTIndexFixture("other", 77, unix.NFT_META_IIF, unix.NFT_CMP_EQ, unix.NFT_REG_1),
		rawNFTIndexFixture("scope", 0, unix.NFT_META_IIF, unix.NFT_CMP_EQ, unix.NFT_REG_1),
		rawNFTIndexFixture("scope", 77, unix.NFT_META_IIFNAME, unix.NFT_CMP_EQ, unix.NFT_REG_1),
		rawNFTIndexFixture("scope", 77, unix.NFT_META_IIF, unix.NFT_CMP_NEQ, unix.NFT_REG_1),
		rawNFTIndexFixture("scope", 77, unix.NFT_META_IIF, unix.NFT_CMP_EQ, unix.NFT_REG_2),
		append(append([]byte(nil), valid...), nftNetlinkAttribute(unix.NFTA_RULE_TABLE, []byte("scope\x00"))...),
		append(append([]byte(nil), valid...), 0),
	} {
		if _, _, err := nftKernelRuleIndex(body, "scope"); err == nil {
			t.Fatal("changed numeric proof accepted")
		}
	}
}

func TestNFTKernelIndexBindingDoesNotLearnReplacementDevice(t *testing.T) {
	b, target, _ := testBackend(t)
	spec := b.replyOriginSpecAtIndex(target, 77)
	name := []json.RawMessage{json.RawMessage(`{"match":{"left":{"meta":{"key":"iif"}},"op":"==","right":"` + target.HostVethName + `"}}`)}
	rule := nftRule{Family: "bridge", Table: spec.Table, Chain: replyChain, Handle: "12", Comment: spec.Comment, Expr: append(name, spec.expressions()[1:]...)}
	table := nftTable{Family: "bridge", Name: spec.Table, Rules: []nftRule{rule}}
	for _, index := range []uint32{77, 78} {
		bound, err := bindNFTKernelIndices(table, spec.Table, map[string]uint32{"12": index})
		if err != nil || bound.Rules[0].matches(spec) != (index == 77) {
			t.Fatalf("kernel literal %d: %v", index, err)
		}
	}
	if !strings.Contains(string(table.Rules[0].Expr[0]), target.HostVethName) {
		t.Fatal("binding mutated caller JSON")
	}
	for _, proof := range []map[string]uint32{nil, {"13": 77}, {"12": 77, "13": 77}, {"12": 0}} {
		if _, err := bindNFTKernelIndices(table, spec.Table, proof); err == nil {
			t.Fatal("unknown or missing raw rule accepted")
		}
	}
	table.Rules[0].Expr = spec.expressions()
	if _, err := bindNFTKernelIndices(table, spec.Table, map[string]uint32{"12": 78}); err == nil {
		t.Fatal("contradictory literal JSON accepted")
	}
	if _, err := readNFTTableWithKernelIndices(nil, "scope", nil); err == nil {
		t.Fatal("nil read accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	called := false
	if _, err := readNFTTableWithKernelIndices(ctx, "scope", func() (nftTable, error) { called = true; return table, nil }); err == nil || called {
		t.Fatal("canceled evidence read invoked callback")
	}
}
