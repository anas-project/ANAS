package incusingresshost

import (
	"fmt"
	"strconv"
)

// GuestVethObservation is a point-in-time kernel observation, never a hold.
type GuestVethObservation struct {
	Name        string
	MAC         string
	IfIndex     uint32
	PeerIfIndex uint32
}

func decodeGuestVeth(body []byte, name, bridge string) (GuestVethObservation, error) {
	link, err := nativeLink(body, name, "veth")
	if err != nil || stringField(link, "master") != bridge {
		return GuestVethObservation{}, fmt.Errorf("guest interface is not an active veth on the managed bridge")
	}
	index, e1 := strconv.ParseUint(numberField(link, "ifindex"), 10, 32)
	peer, e2 := strconv.ParseUint(numberField(link, "link_index"), 10, 32)
	mac := stringField(link, "address")
	if e1 != nil || e2 != nil || index == 0 || peer == 0 || !macAddress.MatchString(mac) {
		return GuestVethObservation{}, fmt.Errorf("guest veth identity is incomplete")
	}
	return GuestVethObservation{Name: name, MAC: mac, IfIndex: uint32(index), PeerIfIndex: uint32(peer)}, nil
}
