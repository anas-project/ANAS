//go:build linux

package incusingresshost

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"net"
	"net/netip"
	"slices"
	"strconv"

	"golang.org/x/sys/unix"
)

// As with direct meta comparisons, libnftables prints interface indices in
// concatenated set members as reusable names. Read actual keys from the same
// generation-checked netlink socket; never consult the current name->index map.
func forwardingIndexSetTypes(family, name string) []string {
	if family == "inet" && name == "flows" {
		return []string{"iface_index", "ipv4_addr", "ipv4_addr", "inet_service"}
	}
	if family != "bridge" {
		return nil
	}
	switch name {
	case "sources":
		return []string{"iface_index", "ether_addr", "ipv4_addr"}
	case "arp_sources":
		return []string{"iface_index", "ether_addr", "ether_addr", "ipv4_addr"}
	case "source_ports":
		return []string{"iface_index", "ether_addr"}
	case "replies":
		return []string{"iface_index", "ether_addr", "ipv4_addr", "ipv4_addr", "inet_service"}
	}
	return nil
}

func bindForwardingKernelSetRead(ctx context.Context, r *nftIndexSocket, family, table string, body []byte) ([]byte, error) {
	var doc struct {
		NFTables []map[string]json.RawMessage `json:"nftables"`
	}
	if decodeObservedJSON(body, &doc) != nil {
		return nil, errNFTIndexEvidence
	}
	f := byte(1)
	if family == "bridge" {
		f = 7
	}
	seen := map[string]bool{}
	for _, obj := range doc.NFTables {
		raw, ok := obj["set"]
		if !ok {
			continue
		}
		var fields map[string]json.RawMessage
		if decodeObservedJSON(raw, &fields) != nil {
			return nil, errNFTIndexEvidence
		}
		var name, setFamily, setTable string
		if json.Unmarshal(fields["name"], &name) != nil || json.Unmarshal(fields["family"], &setFamily) != nil || json.Unmarshal(fields["table"], &setTable) != nil || setFamily != family || setTable != table || seen[name] {
			return nil, errNFTIndexEvidence
		}
		seen[name] = true
		types := forwardingIndexSetTypes(family, name)
		if types == nil {
			continue
		}
		var actual []string
		if json.Unmarshal(fields["type"], &actual) != nil || !slices.Equal(actual, types) {
			return nil, errNFTIndexEvidence
		}
		attrs := append(nftNetlinkAttribute(unix.NFTA_SET_ELEM_LIST_TABLE, append([]byte(table), 0)), nftNetlinkAttribute(unix.NFTA_SET_ELEM_LIST_SET, append([]byte(name), 0))...)
		messages, err := r.query(ctx, unix.NFT_MSG_GETSETELEM, unix.NFT_MSG_NEWSETELEM, f, true, attrs)
		if err != nil {
			return nil, err
		}
		keys := [][]any{}
		for _, message := range messages {
			values, err := forwardingKernelSetKeys(message, f, table, name, types)
			if err != nil {
				return nil, err
			}
			keys = append(keys, values...)
			if len(keys) > 8192 {
				return nil, errNFTIndexEvidence
			}
		}
		members := fields["elem"]
		if len(members) == 0 {
			members = []byte(`[]`)
		}
		bound, err := bindForwardingSetMembers(members, keys)
		if err != nil {
			return nil, err
		}
		if len(fields["elem"]) != 0 {
			fields["elem"] = bound
		}
		obj["set"], err = json.Marshal(fields)
		if err != nil {
			return nil, err
		}
	}
	return json.Marshal(doc)
}

func forwardingKernelSetKeys(body []byte, family byte, table, name string, types []string) ([][]any, error) {
	if len(body) < 4 || body[0] != family || body[1] != 0 || len(types) < 2 || types[0] != "iface_index" {
		return nil, errNFTIndexEvidence
	}
	attrs, err := nftUniqueAttributes(body[4:])
	if err != nil || string(attrs[unix.NFTA_SET_ELEM_LIST_TABLE]) != table+"\x00" || string(attrs[unix.NFTA_SET_ELEM_LIST_SET]) != name+"\x00" {
		return nil, errNFTIndexEvidence
	}
	for key := range attrs {
		if key != unix.NFTA_SET_ELEM_LIST_TABLE && key != unix.NFTA_SET_ELEM_LIST_SET && key != unix.NFTA_SET_ELEM_LIST_ELEMENTS && key != unix.NFTA_SET_ELEM_LIST_SET_ID {
			return nil, errNFTIndexEvidence
		}
	}
	list, err := nftAttributes(attrs[unix.NFTA_SET_ELEM_LIST_ELEMENTS])
	if err != nil {
		return nil, err
	}
	result := [][]any{}
	for _, item := range list {
		if item.kind != unix.NFTA_LIST_ELEM {
			return nil, errNFTIndexEvidence
		}
		element, err := nftUniqueAttributes(item.value)
		if err != nil {
			return nil, err
		}
		for key, value := range element {
			switch key {
			case unix.NFTA_SET_ELEM_KEY:
			case unix.NFTA_SET_ELEM_FLAGS:
				if len(value) != 4 || binary.BigEndian.Uint32(value) != 0 {
					return nil, errNFTIndexEvidence
				}
			case unix.NFTA_SET_ELEM_TIMEOUT, unix.NFTA_SET_ELEM_EXPIRATION:
				if len(value) != 8 || binary.BigEndian.Uint64(value) > uint64(ForwardingPermitTTL.Milliseconds()) {
					return nil, errNFTIndexEvidence
				}
			case unix.NFTA_SET_ELEM_PAD:
				if len(value) != 0 {
					return nil, errNFTIndexEvidence
				}
			default:
				return nil, errNFTIndexEvidence
			}
		}
		data, err := nftUniqueAttributes(element[unix.NFTA_SET_ELEM_KEY])
		if err != nil || len(data) != 1 {
			return nil, errNFTIndexEvidence
		}
		key := data[unix.NFTA_DATA_VALUE]
		values := []any{}
		for _, typ := range types {
			length := 4
			if typ == "ether_addr" {
				length = 8
			}
			if len(key) < length {
				return nil, errNFTIndexEvidence
			}
			part := key[:length]
			key = key[length:]
			switch typ {
			case "iface_index":
				index := binary.NativeEndian.Uint32(part)
				if index == 0 {
					return nil, errNFTIndexEvidence
				}
				values = append(values, index)
			case "ipv4_addr":
				values = append(values, netip.AddrFrom4([4]byte{part[0], part[1], part[2], part[3]}).String())
			case "ether_addr":
				if part[6] != 0 || part[7] != 0 {
					return nil, errNFTIndexEvidence
				}
				values = append(values, net.HardwareAddr(part[:6]).String())
			case "inet_service":
				if part[2] != 0 || part[3] != 0 {
					return nil, errNFTIndexEvidence
				}
				values = append(values, binary.BigEndian.Uint16(part[:2]))
			default:
				return nil, errNFTIndexEvidence
			}
		}
		if len(key) != 0 {
			return nil, errNFTIndexEvidence
		}
		result = append(result, values)
	}
	return result, nil
}

func bindForwardingSetMembers(body []byte, keys [][]any) ([]byte, error) {
	var members []any
	if decodeObservedJSON(body, &members) != nil || members == nil || len(members) > 8192 || len(keys) != len(members) {
		return nil, errNFTIndexEvidence
	}
	proofs := map[string][]any{}
	for _, key := range keys {
		if len(key) < 2 {
			return nil, errNFTIndexEvidence
		}
		index, ok := key[0].(uint32)
		if !ok || index == 0 {
			return nil, errNFTIndexEvidence
		}
		suffix, err := json.Marshal(key[1:])
		if err != nil || proofs[string(suffix)] != nil {
			return nil, errNFTIndexEvidence
		}
		proofs[string(suffix)] = key
	}
	seen := map[string]bool{}
	for _, raw := range members {
		value, _, err := forwardingElementValue(raw, uint64(ForwardingPermitTTL.Seconds()))
		if err != nil {
			return nil, err
		}
		object, ok := value.(map[string]any)
		if !ok || len(object) != 1 {
			return nil, errNFTIndexEvidence
		}
		tuple, ok := object["concat"].([]any)
		if !ok || len(tuple) < 2 {
			return nil, errNFTIndexEvidence
		}
		suffix, err := json.Marshal(tuple[1:])
		if err != nil {
			return nil, err
		}
		key := string(suffix)
		proof := proofs[key]
		if proof == nil || seen[key] || len(proof) != len(tuple) {
			return nil, errNFTIndexEvidence
		}
		seen[key] = true
		index := proof[0].(uint32)
		switch v := tuple[0].(type) {
		case json.Number:
			if v.String() != strconv.FormatUint(uint64(index), 10) {
				return nil, errNFTIndexEvidence
			}
		case string:
			if number, err := strconv.ParseUint(v, 10, 32); err == nil {
				if uint32(number) != index {
					return nil, errNFTIndexEvidence
				}
			} else if !ifaceName.MatchString(v) {
				return nil, errNFTIndexEvidence
			}
		default:
			return nil, errNFTIndexEvidence
		}
		tuple[0] = index
	}
	return json.Marshal(members)
}
