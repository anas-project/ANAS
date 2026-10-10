//go:build !linux

package main

import (
	"encoding/json"
	"net"
	"strconv"
)

// serveUDP is the portable fallback for local unit tests; the acceptance VM
// runs the Linux version, which answers from the address it was sent to.
func serveUDP(tag string, port int) {
	pc, err := net.ListenPacket("udp", ":"+strconv.Itoa(port))
	if err != nil {
		fail(err.Error())
	}
	go func() {
		buffer := make([]byte, 512)
		for {
			_, peer, err := pc.ReadFrom(buffer)
			if err != nil {
				return
			}
			body, _ := json.Marshal(reply{Tag: tag, Peer: peerOf(peer)})
			_, _ = pc.WriteTo(append(body, '\n'), peer)
		}
	}()
}
