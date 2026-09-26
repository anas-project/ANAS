//go:build linux

package incusingresshost

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"reflect"
	"runtime"
	"strconv"
	"time"

	"golang.org/x/sys/unix"
)

type forwardingIndexLiteral struct {
	Key   string
	Index uint32
}

// Reuse the existing GETGEN/GETRULE transport. Formatter names are never
// resolved through today's interface map: that could adopt a replacement.
func readForwardingNFTWithKernelIndices(ctx context.Context, family, table string, read func() ([]byte, error)) (_ []byte, result error) {
	if ctx == nil || read == nil || !nftName.MatchString(table) || (family != "inet" && family != "bridge") {
		return nil, errNFTIndexEvidence
	}
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	fd, err := unix.Socket(unix.AF_NETLINK, unix.SOCK_RAW|unix.SOCK_NONBLOCK|unix.SOCK_CLOEXEC, unix.NETLINK_NETFILTER)
	if err != nil {
		return nil, errNFTIndexEvidence
	}
	defer func() {
		if unix.Close(fd) != nil {
			result = errors.Join(result, errNFTIndexEvidence)
		}
	}()
	if unix.Bind(fd, &unix.SockaddrNetlink{Family: unix.AF_NETLINK}) != nil {
		return nil, errNFTIndexEvidence
	}
	address, err := unix.Getsockname(fd)
	local, ok := address.(*unix.SockaddrNetlink)
	if err != nil || !ok || local.Pid == 0 {
		return nil, errNFTIndexEvidence
	}
	r := nftIndexSocket{fd: fd, port: local.Pid}
	before, err := r.generation(ctx)
	if err != nil {
		return nil, err
	}
	f := byte(1)
	if family == "bridge" {
		f = 7
	}
	rules, err := r.query(ctx, unix.NFT_MSG_GETRULE, unix.NFT_MSG_NEWRULE, f, true, nftNetlinkAttribute(unix.NFTA_RULE_TABLE, append([]byte(table), 0)))
	if err != nil {
		return nil, err
	}
	indices := map[string][]forwardingIndexLiteral{}
	for _, body := range rules {
		key, values, err := forwardingKernelRuleIndices(body, f, table)
		if err != nil {
			return nil, err
		}
		if _, duplicate := indices[key]; duplicate {
			return nil, errNFTIndexEvidence
		}
		indices[key] = values
	}
	body, err := read()
	if err != nil {
		return nil, err
	}
	body, err = bindForwardingKernelIndices(body, family, table, indices)
	if err != nil {
		return nil, err
	}
	body, err = bindForwardingKernelSetRead(ctx, &r, family, table, body)
	if err != nil {
		return nil, err
	}
	after, err := r.generation(ctx)
	if err != nil || before != after || ctx.Err() != nil {
		return nil, errNFTIndexEvidence
	}
	return body, nil
}

func forwardingKernelRuleIndices(body []byte, family byte, table string) (string, []forwardingIndexLiteral, error) {
	bad := func() (string, []forwardingIndexLiteral, error) { return "", nil, errNFTIndexEvidence }
	if len(body) < 4 || body[0] != family || body[1] != 0 {
		return bad()
	}
	attrs, err := nftUniqueAttributes(body[4:])
	if err != nil || string(attrs[unix.NFTA_RULE_TABLE]) != table+"\x00" || len(attrs[unix.NFTA_RULE_HANDLE]) != 8 {
		return bad()
	}
	chain := attrs[unix.NFTA_RULE_CHAIN]
	if len(chain) < 2 || chain[len(chain)-1] != 0 || !nftName.MatchString(string(chain[:len(chain)-1])) {
		return bad()
	}
	handle := binary.BigEndian.Uint64(attrs[unix.NFTA_RULE_HANDLE])
	if handle == 0 {
		return bad()
	}
	list, err := nftAttributes(attrs[unix.NFTA_RULE_EXPRESSIONS])
	if err != nil || len(list) == 0 || len(list) > 128 {
		return bad()
	}
	parts := make([]map[uint16][]byte, len(list))
	for i, item := range list {
		if item.kind != unix.NFTA_LIST_ELEM {
			return bad()
		}
		parts[i], err = nftUniqueAttributes(item.value)
		if err != nil || len(parts[i]) != 2 {
			return bad()
		}
	}
	u32 := func(v []byte) uint32 {
		if len(v) != 4 {
			return ^uint32(0)
		}
		return binary.BigEndian.Uint32(v)
	}
	values := []forwardingIndexLiteral{}
	for i := 0; i+1 < len(parts); i++ {
		if string(parts[i][unix.NFTA_EXPR_NAME]) != "meta\x00" || string(parts[i+1][unix.NFTA_EXPR_NAME]) != "cmp\x00" {
			continue
		}
		meta, err := nftUniqueAttributes(parts[i][unix.NFTA_EXPR_DATA])
		if err != nil {
			return bad()
		}
		key := ""
		switch u32(meta[unix.NFTA_META_KEY]) {
		case unix.NFT_META_IIF:
			key = "iif"
		case unix.NFT_META_OIF:
			key = "oif"
		default:
			continue
		}
		cmp, err := nftUniqueAttributes(parts[i+1][unix.NFTA_EXPR_DATA])
		if err != nil {
			return bad()
		}
		reg := u32(meta[unix.NFTA_META_DREG])
		if len(meta) != 2 || (reg != unix.NFT_REG_1 && reg != unix.NFT_REG32_00) || len(cmp) != 3 || u32(cmp[unix.NFTA_CMP_SREG]) != reg || u32(cmp[unix.NFTA_CMP_OP]) != unix.NFT_CMP_EQ {
			return bad()
		}
		data, err := nftUniqueAttributes(cmp[unix.NFTA_CMP_DATA])
		if err != nil || len(data) != 1 || len(data[unix.NFTA_DATA_VALUE]) != 4 {
			return bad()
		}
		values = append(values, forwardingIndexLiteral{Key: key, Index: binary.NativeEndian.Uint32(data[unix.NFTA_DATA_VALUE])})
	}
	return string(chain[:len(chain)-1]) + "\x00" + strconv.FormatUint(handle, 10), values, nil
}

func bindForwardingKernelIndices(body []byte, family, table string, indices map[string][]forwardingIndexLiteral) ([]byte, error) {
	var doc struct {
		NFTables []map[string]json.RawMessage `json:"nftables"`
	}
	if len(body) > 1<<20 || decodeObservedJSON(body, &doc) != nil || len(doc.NFTables) == 0 {
		return nil, errNFTIndexEvidence
	}
	seen := map[string]bool{}
	for _, object := range doc.NFTables {
		if len(object) != 1 {
			return nil, errNFTIndexEvidence
		}
		raw, ok := object["rule"]
		if !ok {
			continue
		}
		rule, err := parseNFTRule(raw)
		if err != nil || rule.Family != family || rule.Table != table {
			return nil, errNFTIndexEvidence
		}
		key := rule.Chain + "\x00" + rule.Handle
		proof, exists := indices[key]
		if !exists || seen[key] {
			return nil, errNFTIndexEvidence
		}
		seen[key] = true
		n := 0
		for i, expr := range rule.Expr {
			var value map[string]any
			if decodeObservedJSON(expr, &value) != nil {
				return nil, errNFTIndexEvidence
			}
			match, ok := value["match"].(map[string]any)
			if !ok {
				continue
			}
			selected := ""
			for _, k := range []string{"iif", "oif"} {
				if reflect.DeepEqual(match["left"], forwardingMeta(k)) {
					selected = k
				}
			}
			if selected == "" {
				continue
			}
			if len(value) != 1 || len(match) != 3 || match["op"] != "==" || n >= len(proof) || proof[n].Key != selected {
				return nil, errNFTIndexEvidence
			}
			index := proof[n].Index
			n++
			switch v := match["right"].(type) {
			case json.Number:
				if v.String() != strconv.FormatUint(uint64(index), 10) {
					return nil, errNFTIndexEvidence
				}
			case string:
				if number, err := strconv.ParseUint(v, 10, 32); err == nil {
					if uint32(number) != index {
						return nil, errNFTIndexEvidence
					}
				} else if !ifaceName.MatchString(v) || index == 0 {
					return nil, errNFTIndexEvidence
				}
			default:
				return nil, errNFTIndexEvidence
			}
			match["right"] = index
			rule.Expr[i], err = json.Marshal(value)
			if err != nil {
				return nil, errNFTIndexEvidence
			}
		}
		if n != len(proof) {
			return nil, errNFTIndexEvidence
		}
		var fields map[string]json.RawMessage
		if decodeObservedJSON(raw, &fields) != nil {
			return nil, errNFTIndexEvidence
		}
		fields["expr"], err = json.Marshal(rule.Expr)
		if err != nil {
			return nil, errNFTIndexEvidence
		}
		object["rule"], err = json.Marshal(fields)
		if err != nil {
			return nil, errNFTIndexEvidence
		}
	}
	if len(seen) != len(indices) {
		return nil, errNFTIndexEvidence
	}
	return json.Marshal(doc)
}
