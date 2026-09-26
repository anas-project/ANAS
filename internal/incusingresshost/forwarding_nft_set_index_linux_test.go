//go:build linux

package incusingresshost

import (
	"encoding/binary"
	"reflect"
	"testing"

	"golang.org/x/sys/unix"
)

func TestForwardingSetBindingDoesNotAdoptReusedInterfaceName(t *testing.T) {
	// Shape captured from native r5: nft printed fwout5, not its kernel index.
	body := []byte(`[{"elem":{"val":{"concat":["fwout5","10.83.5.8","198.18.5.2",8080]},"expires":29}}]`)
	for _, index := range []uint32{7, 99} {
		proof := [][]any{{index, "10.83.5.8", "198.18.5.2", uint16(8080)}}
		out, err := bindForwardingSetMembers(body, proof)
		if err != nil {
			t.Fatal(err)
		}
		var v []any
		if decodeObservedJSON(out, &v) != nil {
			t.Fatal("invalid JSON")
		}
		value, _, err := forwardingElementValue(v[0], 30)
		if err != nil {
			t.Fatal(err)
		}
		tuple := value.(map[string]any)["concat"].([]any)
		n, err := nftNumber(tuple[0], 32)
		if err != nil || uint32(n) != index {
			t.Fatal("used name instead of raw key identity")
		}
	}
	for _, proof := range [][][]any{
		nil, {{uint32(7), "10.83.5.9", "198.18.5.2", uint16(8080)}},
		{{uint32(7), "10.83.5.8", "198.18.5.2", uint16(8081)}},
		{{uint32(0), "10.83.5.8", "198.18.5.2", uint16(8080)}},
		{{uint32(7), "10.83.5.8", "198.18.5.2", uint16(8080)}, {uint32(99), "10.83.5.8", "198.18.5.2", uint16(8080)}},
	} {
		if _, err := bindForwardingSetMembers(body, proof); err == nil {
			t.Fatal("missing, extra or substituted set evidence accepted")
		}
	}
	if _, err := bindForwardingSetMembers([]byte(`[{"concat":[99,"10.83.5.8","198.18.5.2",8080]}]`), [][]any{{uint32(7), "10.83.5.8", "198.18.5.2", uint16(8080)}}); err == nil {
		t.Fatal("contradictory literal accepted")
	}
	if _, err := bindForwardingSetMembers([]byte(`[]`), nil); err != nil {
		t.Fatal("empty closed set rejected", err)
	}
}

func TestForwardingRawSetKeyTruncationAndBoundaries(t *testing.T) {
	key := make([]byte, 16)
	binary.NativeEndian.PutUint32(key, 77)
	copy(key[4:], []byte{10, 83, 5, 8, 198, 18, 5, 2})
	binary.BigEndian.PutUint16(key[12:], 8080)
	attr := nftNetlinkAttribute
	element := attr(unix.NFTA_LIST_ELEM|unix.NLA_F_NESTED, attr(unix.NFTA_SET_ELEM_KEY|unix.NLA_F_NESTED, attr(unix.NFTA_DATA_VALUE, key)))
	body := append([]byte{1, 0, 0, 0}, attr(unix.NFTA_SET_ELEM_LIST_TABLE, []byte("scope\x00"))...)
	body = append(body, attr(unix.NFTA_SET_ELEM_LIST_SET, []byte("flows\x00"))...)
	body = append(body, attr(unix.NFTA_SET_ELEM_LIST_ELEMENTS|unix.NLA_F_NESTED, element)...)
	types := forwardingIndexSetTypes("inet", "flows")
	actual, err := forwardingKernelSetKeys(body, 1, "scope", "flows", types)
	if err != nil || !reflect.DeepEqual(actual, [][]any{{uint32(77), "10.83.5.8", "198.18.5.2", uint16(8080)}}) {
		t.Fatal(actual, err)
	}
	for n := 0; n < len(body); n++ {
		// A header with no element list is a valid empty-set observation;
		// truncation inside the actual encoded list must never be accepted.
		if n >= len(body)-len(attr(unix.NFTA_SET_ELEM_LIST_ELEMENTS|unix.NLA_F_NESTED, element))+1 {
			if _, err := forwardingKernelSetKeys(body[:n], 1, "scope", "flows", types); err == nil {
				t.Fatal("truncated native key accepted", n)
			}
		}
	}
	for _, changed := range []struct {
		family     byte
		table, set string
	}{{7, "scope", "flows"}, {1, "other", "flows"}, {1, "scope", "other"}} {
		if _, err := forwardingKernelSetKeys(body, changed.family, changed.table, changed.set, types); err == nil {
			t.Fatal("wrong native scope accepted")
		}
	}
}
