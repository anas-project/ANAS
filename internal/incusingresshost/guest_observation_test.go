package incusingresshost

import (
	"encoding/json"
	"testing"
)

func TestGuestObservationRequiresActualActiveBridgeVeth(t *testing.T) {
	for _, scenario := range []string{"valid", "tap", "wrong-bridge", "down", "missing-peer", "overflow", "duplicate"} {
		t.Run(scenario, func(t *testing.T) {
			link := map[string]any{"ifname": "vethguest0", "ifindex": 10, "link_index": 77, "master": "anas1234567890", "address": "02:00:00:00:00:10", "link_type": "ether", "flags": []string{"UP", "LOWER_UP"}, "linkinfo": map[string]any{"info_kind": "veth"}}
			switch scenario {
			case "tap":
				link["linkinfo"] = map[string]any{"info_kind": "tun"}
			case "wrong-bridge":
				link["master"] = "other"
			case "down":
				link["flags"] = []string{"UP", "NO-CARRIER"}
			case "missing-peer":
				delete(link, "link_index")
			case "overflow":
				link["ifindex"] = uint64(1) << 40
			}
			links := []any{link}
			if scenario == "duplicate" {
				links = append(links, link)
			}
			body, _ := json.Marshal(links)
			v, err := decodeGuestVeth(body, "vethguest0", "anas1234567890")
			if scenario == "valid" {
				if err != nil || v.IfIndex != 10 || v.PeerIfIndex != 77 {
					t.Fatal(v, err)
				}
			} else if err == nil {
				t.Fatal("invalid native identity accepted")
			}
		})
	}
}
