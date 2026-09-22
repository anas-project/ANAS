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
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

var errNFTIndexEvidence = errors.New("nft numeric interface evidence is unavailable or changed")

// nft's JSON formatter can turn meta iif into a reusable device name even
// with -n. Read the actual first meta/cmp pair from the kernel, not a current
// name-to-index lookup. GETGEN brackets BOTH the raw dump and the complete
// symbolic JSON inventory, so rule replacement cannot mix their identities.
// The socket only sends GETGEN and GETRULE, never a mutation or reset request.
func readNFTTableWithKernelIndices(ctx context.Context, table string, read func() (nftTable, error)) (_ nftTable, result error) {
	if ctx == nil || read == nil || !nftName.MatchString(table) {
		return nftTable{}, errNFTIndexEvidence
	}
	if err := ctx.Err(); err != nil {
		return nftTable{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	// Namespace identity is per thread. Pin socket creation and the fixed
	// command callback to the caller's original namespace for this entire read.
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	fd, err := unix.Socket(unix.AF_NETLINK, unix.SOCK_RAW|unix.SOCK_NONBLOCK|unix.SOCK_CLOEXEC, unix.NETLINK_NETFILTER)
	if err != nil {
		return nftTable{}, errNFTIndexEvidence
	}
	defer func() {
		if unix.Close(fd) != nil {
			result = errors.Join(result, errNFTIndexEvidence)
		}
	}()
	if unix.Bind(fd, &unix.SockaddrNetlink{Family: unix.AF_NETLINK}) != nil {
		return nftTable{}, errNFTIndexEvidence
	}
	address, err := unix.Getsockname(fd)
	local, ok := address.(*unix.SockaddrNetlink)
	if err != nil || !ok || local.Pid == 0 {
		return nftTable{}, errNFTIndexEvidence
	}
	reader := nftIndexSocket{fd: fd, port: local.Pid}
	before, err := reader.generation(ctx)
	if err != nil {
		return nftTable{}, err
	}
	attributes := append(nftNetlinkAttribute(unix.NFTA_RULE_TABLE, append([]byte(table), 0)), nftNetlinkAttribute(unix.NFTA_RULE_CHAIN, append([]byte(replyChain), 0))...)
	rules, err := reader.query(ctx, unix.NFT_MSG_GETRULE, unix.NFT_MSG_NEWRULE, 7, true, attributes)
	if err != nil {
		return nftTable{}, err
	}
	indices := make(map[string]uint32, len(rules))
	for _, body := range rules {
		handle, index, err := nftKernelRuleIndex(body, table)
		if err != nil || indices[handle] != 0 {
			return nftTable{}, errNFTIndexEvidence
		}
		indices[handle] = index
	}
	observed, err := read()
	if err != nil {
		return nftTable{}, err
	}
	after, err := reader.generation(ctx)
	if err != nil || before != after || ctx.Err() != nil {
		return nftTable{}, errNFTIndexEvidence
	}
	return bindNFTKernelIndices(observed, table, indices)
}

// All rule expressions still undergo the original complete AST comparison.
// Only the exact first iif equality gains an independently observed numeric
// value. Missing/extra handles, changed shape and contradictory numbers fail.
func bindNFTKernelIndices(observed nftTable, table string, indices map[string]uint32) (nftTable, error) {
	if observed.Family != "bridge" || observed.Name != table {
		return nftTable{}, errNFTIndexEvidence
	}
	seen := map[string]bool{}
	observed.Rules = append([]nftRule(nil), observed.Rules...)
	for i := range observed.Rules {
		rule := &observed.Rules[i]
		if rule.Chain != replyChain {
			continue
		}
		index, exists := indices[rule.Handle]
		if !exists || index == 0 || seen[rule.Handle] || len(rule.Expr) < 2 {
			return nftTable{}, errNFTIndexEvidence
		}
		seen[rule.Handle] = true
		var first map[string]any
		if decodeObservedJSON(rule.Expr[0], &first) != nil || len(first) != 1 {
			return nftTable{}, errNFTIndexEvidence
		}
		match, ok := first["match"].(map[string]any)
		if !ok || len(match) != 3 || match["op"] != "==" || !reflect.DeepEqual(match["left"], map[string]any{"meta": map[string]any{"key": "iif"}}) {
			return nftTable{}, errNFTIndexEvidence
		}
		switch value := match["right"].(type) {
		case string:
			if !ifaceName.MatchString(value) {
				return nftTable{}, errNFTIndexEvidence
			}
		case json.Number:
			if value.String() != strconv.FormatUint(uint64(index), 10) {
				return nftTable{}, errNFTIndexEvidence
			}
		default:
			return nftTable{}, errNFTIndexEvidence
		}
		match["right"] = index
		encoded, err := json.Marshal(first)
		if err != nil {
			return nftTable{}, errNFTIndexEvidence
		}
		rule.Expr = append([]json.RawMessage(nil), rule.Expr...)
		rule.Expr[0] = encoded
	}
	if len(seen) != len(indices) {
		return nftTable{}, errNFTIndexEvidence
	}
	return observed, nil
}

type nftIndexSocket struct {
	fd             int
	port, sequence uint32
}

func (r *nftIndexSocket) generation(ctx context.Context) (uint32, error) {
	messages, err := r.query(ctx, unix.NFT_MSG_GETGEN, unix.NFT_MSG_NEWGEN, 0, false, nil)
	if err != nil || len(messages) != 1 || len(messages[0]) < 4 {
		return 0, errNFTIndexEvidence
	}
	attrs, err := nftUniqueAttributes(messages[0][4:])
	if err != nil || len(attrs[unix.NFTA_GEN_ID]) != 4 {
		return 0, errNFTIndexEvidence
	}
	return binary.BigEndian.Uint32(attrs[unix.NFTA_GEN_ID]), nil
}

func (r *nftIndexSocket) query(ctx context.Context, request, response uint16, family byte, dump bool, attrs []byte) ([][]byte, error) {
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	r.sequence++
	message := make([]byte, 20+len(attrs))
	binary.NativeEndian.PutUint32(message, uint32(len(message)))
	binary.NativeEndian.PutUint16(message[4:], uint16(unix.NFNL_SUBSYS_NFTABLES<<8)|request)
	flags := uint16(unix.NLM_F_REQUEST)
	if dump {
		flags |= unix.NLM_F_DUMP
	}
	binary.NativeEndian.PutUint16(message[6:], flags)
	binary.NativeEndian.PutUint32(message[8:], r.sequence)
	binary.NativeEndian.PutUint32(message[12:], r.port)
	message[16] = family
	copy(message[20:], attrs)
	if unix.Sendto(r.fd, message, 0, &unix.SockaddrNetlink{Family: unix.AF_NETLINK}) != nil {
		return nil, errNFTIndexEvidence
	}
	var result [][]byte
	total := 0
	buffer := make([]byte, 256<<10)
	for {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		poll := []unix.PollFd{{Fd: int32(r.fd), Events: unix.POLLIN}}
		if _, err := unix.Poll(poll, 50); err != nil {
			if errors.Is(err, unix.EINTR) {
				continue
			}
			return nil, errNFTIndexEvidence
		}
		if poll[0].Revents == 0 {
			continue
		}
		if poll[0].Revents != unix.POLLIN {
			return nil, errNFTIndexEvidence
		}
		n, _, recvFlags, sender, err := unix.Recvmsg(r.fd, buffer, nil, 0)
		if errors.Is(err, unix.EAGAIN) || errors.Is(err, unix.EINTR) {
			continue
		}
		peer, ok := sender.(*unix.SockaddrNetlink)
		if err != nil || !ok || peer.Pid != 0 || peer.Groups != 0 || recvFlags&unix.MSG_TRUNC != 0 || n == 0 {
			return nil, errNFTIndexEvidence
		}
		total += n
		if total > 4<<20 {
			return nil, errNFTIndexEvidence
		}
		messages, err := syscall.ParseNetlinkMessage(buffer[:n])
		if err != nil {
			return nil, errNFTIndexEvidence
		}
		for i, msg := range messages {
			if msg.Header.Seq != r.sequence || msg.Header.Pid != r.port || msg.Header.Flags&unix.NLM_F_DUMP_INTR != 0 {
				return nil, errNFTIndexEvidence
			}
			if msg.Header.Type == unix.NLMSG_DONE {
				if !dump || i != len(messages)-1 || len(msg.Data) != 4 || binary.NativeEndian.Uint32(msg.Data) != 0 {
					return nil, errNFTIndexEvidence
				}
				return result, nil
			}
			if msg.Header.Type != uint16(unix.NFNL_SUBSYS_NFTABLES<<8)|response || len(msg.Data) < 4 || msg.Data[1] != 0 {
				return nil, errNFTIndexEvidence
			}
			if request == unix.NFT_MSG_GETRULE && msg.Data[0] != family {
				return nil, errNFTIndexEvidence
			}
			result = append(result, append([]byte(nil), msg.Data...))
			if len(result) > 1024 {
				return nil, errNFTIndexEvidence
			}
			if !dump {
				if len(messages) != 1 {
					return nil, errNFTIndexEvidence
				}
				return result, nil
			}
		}
	}
}

type nftAttribute struct {
	kind  uint16
	value []byte
}

func nftNetlinkAttribute(kind uint16, value []byte) []byte {
	length := len(value) + 4
	data := make([]byte, (length+3)&^3)
	binary.NativeEndian.PutUint16(data, uint16(length))
	binary.NativeEndian.PutUint16(data[2:], kind)
	copy(data[4:], value)
	return data
}

func nftAttributes(body []byte) ([]nftAttribute, error) {
	var out []nftAttribute
	for len(body) != 0 {
		if len(body) < 4 {
			return nil, errNFTIndexEvidence
		}
		length := int(binary.NativeEndian.Uint16(body))
		aligned := (length + 3) &^ 3
		if length < 4 || aligned > len(body) || len(out) >= 256 {
			return nil, errNFTIndexEvidence
		}
		kind := binary.NativeEndian.Uint16(body[2:]) &^ uint16(unix.NLA_F_NESTED|unix.NLA_F_NET_BYTEORDER)
		out = append(out, nftAttribute{kind: kind, value: body[4:length]})
		body = body[aligned:]
	}
	return out, nil
}

func nftUniqueAttributes(body []byte) (map[uint16][]byte, error) {
	attributes, err := nftAttributes(body)
	if err != nil {
		return nil, err
	}
	values := map[uint16][]byte{}
	for _, attr := range attributes {
		if _, exists := values[attr.kind]; exists {
			return nil, errNFTIndexEvidence
		}
		values[attr.kind] = attr.value
	}
	return values, nil
}

func nftKernelRuleIndex(body []byte, table string) (string, uint32, error) {
	if len(body) < 4 || body[0] != 7 || body[1] != 0 {
		return "", 0, errNFTIndexEvidence
	}
	attrs, err := nftUniqueAttributes(body[4:])
	if err != nil || string(attrs[unix.NFTA_RULE_TABLE]) != table+"\x00" || string(attrs[unix.NFTA_RULE_CHAIN]) != replyChain+"\x00" || len(attrs[unix.NFTA_RULE_HANDLE]) != 8 {
		return "", 0, errNFTIndexEvidence
	}
	handle := binary.BigEndian.Uint64(attrs[unix.NFTA_RULE_HANDLE])
	list, err := nftAttributes(attrs[unix.NFTA_RULE_EXPRESSIONS])
	if err != nil || handle == 0 || len(list) < 2 || len(list) > 64 {
		return "", 0, errNFTIndexEvidence
	}
	parts := make([]map[uint16][]byte, 2)
	for i, name := range []string{"meta", "cmp"} {
		if list[i].kind != unix.NFTA_LIST_ELEM {
			return "", 0, errNFTIndexEvidence
		}
		expression, err := nftUniqueAttributes(list[i].value)
		if err != nil || len(expression) != 2 || string(expression[unix.NFTA_EXPR_NAME]) != name+"\x00" {
			return "", 0, errNFTIndexEvidence
		}
		parts[i], err = nftUniqueAttributes(expression[unix.NFTA_EXPR_DATA])
		if err != nil {
			return "", 0, errNFTIndexEvidence
		}
	}
	u32 := func(v []byte) uint32 {
		if len(v) != 4 {
			return ^uint32(0)
		}
		return binary.BigEndian.Uint32(v)
	}
	meta, compare := parts[0], parts[1]
	reg := u32(meta[unix.NFTA_META_DREG])
	if len(meta) != 2 || u32(meta[unix.NFTA_META_KEY]) != unix.NFT_META_IIF || (reg != unix.NFT_REG_1 && reg != unix.NFT_REG32_00) ||
		len(compare) != 3 || u32(compare[unix.NFTA_CMP_SREG]) != reg || u32(compare[unix.NFTA_CMP_OP]) != unix.NFT_CMP_EQ {
		return "", 0, errNFTIndexEvidence
	}
	data, err := nftUniqueAttributes(compare[unix.NFTA_CMP_DATA])
	if err != nil || len(data) != 1 || len(data[unix.NFTA_DATA_VALUE]) != 4 {
		return "", 0, errNFTIndexEvidence
	}
	index := binary.NativeEndian.Uint32(data[unix.NFTA_DATA_VALUE])
	if index == 0 {
		return "", 0, errNFTIndexEvidence
	}
	return strconv.FormatUint(handle, 10), index, nil
}
