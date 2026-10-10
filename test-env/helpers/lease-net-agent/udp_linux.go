//go:build linux

package main

import (
	"encoding/json"
	"net"
	"strconv"
	"syscall"
	"unsafe"
)

// serveUDP answers datagrams on one port. A host with many addresses must
// answer from the address the datagram was sent to, or a connected client
// discards the reply; the kernel reports that address (IP_PKTINFO,
// IPV6_PKTINFO) and the reply names it as its source. IPv4 and IPv6 use
// separate sockets so each family carries its own control message.
func serveUDP(tag string, port int) {
	started := false
	for _, family := range []string{"udp4", "udp6"} {
		host := "0.0.0.0"
		if family == "udp6" {
			host = "::"
		}
		pc, err := net.ListenPacket(family, net.JoinHostPort(host, strconv.Itoa(port)))
		if err != nil {
			continue
		}
		conn := pc.(*net.UDPConn)
		raw, err := conn.SyscallConn()
		if err != nil {
			fail(err.Error())
		}
		v6 := family == "udp6"
		_ = raw.Control(func(fd uintptr) {
			if v6 {
				err = syscall.SetsockoptInt(int(fd), syscall.IPPROTO_IPV6, syscall.IPV6_RECVPKTINFO, 1)
			} else {
				err = syscall.SetsockoptInt(int(fd), syscall.IPPROTO_IP, syscall.IP_PKTINFO, 1)
			}
		})
		if err != nil {
			fail(err.Error())
		}
		started = true
		go func() {
			buffer, oob := make([]byte, 512), make([]byte, 256)
			for {
				_, oobn, _, peer, err := conn.ReadMsgUDP(buffer, oob)
				if err != nil {
					return
				}
				body, _ := json.Marshal(reply{Tag: tag, Peer: peerOf(peer)})
				_, _, _ = conn.WriteMsgUDP(append(body, '\n'), replySource(oob[:oobn], v6), peer)
			}
		}()
	}
	if !started {
		fail("no UDP socket on port " + strconv.Itoa(port))
	}
}

// replySource turns the received destination into the control message that
// makes it the reply's source address.
func replySource(oob []byte, v6 bool) []byte {
	messages, err := syscall.ParseSocketControlMessage(oob)
	if err != nil {
		return nil
	}
	for _, m := range messages {
		switch {
		case !v6 && m.Header.Level == syscall.IPPROTO_IP && m.Header.Type == syscall.IP_PKTINFO && len(m.Data) >= syscall.SizeofInet4Pktinfo:
			received := (*syscall.Inet4Pktinfo)(unsafe.Pointer(&m.Data[0]))
			info := syscall.Inet4Pktinfo{Spec_dst: received.Addr}
			return controlMessage(syscall.IPPROTO_IP, syscall.IP_PKTINFO, unsafe.Slice((*byte)(unsafe.Pointer(&info)), syscall.SizeofInet4Pktinfo))
		case v6 && m.Header.Level == syscall.IPPROTO_IPV6 && m.Header.Type == syscall.IPV6_PKTINFO && len(m.Data) >= syscall.SizeofInet6Pktinfo:
			received := (*syscall.Inet6Pktinfo)(unsafe.Pointer(&m.Data[0]))
			info := syscall.Inet6Pktinfo{Addr: received.Addr}
			return controlMessage(syscall.IPPROTO_IPV6, syscall.IPV6_PKTINFO, unsafe.Slice((*byte)(unsafe.Pointer(&info)), syscall.SizeofInet6Pktinfo))
		}
	}
	return nil
}

func controlMessage(level, kind int, data []byte) []byte {
	oob := make([]byte, syscall.CmsgSpace(len(data)))
	header := (*syscall.Cmsghdr)(unsafe.Pointer(&oob[0]))
	header.Level, header.Type = int32(level), int32(kind)
	header.SetLen(syscall.CmsgLen(len(data)))
	copy(oob[syscall.CmsgLen(0):], data)
	return oob
}
