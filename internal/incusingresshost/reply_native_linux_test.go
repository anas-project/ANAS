//go:build linux

package incusingresshost

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func isolatedReplyTestNamespace(t *testing.T) (*os.File, uint64) {
	t.Helper()
	type result struct {
		file   *os.File
		cookie uint64
		err    error
	}
	ready := make(chan result, 1)
	go func() {
		file, cookie, err := captureIsolatedTestNamespace()
		ready <- result{file: file, cookie: cookie, err: err}
	}()
	r := <-ready
	if r.file != nil {
		t.Cleanup(func() { _ = r.file.Close() })
	}
	if r.err != nil {
		if os.Getenv("ANAS_REQUIRE_INGRESS_NATIVE") == "1" || (!errors.Is(r.err, unix.EPERM) && !errors.Is(r.err, unix.EACCES)) {
			t.Fatal(r.err)
		}
		t.Skip("requires an isolated Linux namespace; native gate forbids skip")
	}
	return r.file, r.cookie
}

// Restore before reporting the fixture ready. Exiting a locked goroutine is
// insufficient when it used the process leader: /proc/PID/ns/net can retain
// that leader's changed namespace while other runtime threads remain outside.
// On an unverified restore this goroutine stays locked and the test fails.
func captureIsolatedTestNamespace() (file *os.File, cookie uint64, result error) {
	runtime.LockOSThread()
	mayUnlock := true
	defer func() {
		if result != nil && file != nil {
			result = errors.Join(result, file.Close())
			file = nil
		}
		if mayUnlock {
			runtime.UnlockOSThread()
		}
	}()
	original, err := openKernelNetworkNamespace("thread-self")
	if err != nil {
		return nil, 0, err
	}
	defer func() { result = errors.Join(result, original.Close()) }()
	before, err := original.Stat()
	if err != nil {
		return nil, 0, err
	}
	if err := unix.Unshare(unix.CLONE_NEWNET); err != nil {
		return nil, 0, err
	}
	mayUnlock = false
	defer func() {
		if err := unix.Setns(int(original.Fd()), unix.CLONE_NEWNET); err != nil {
			result = errors.Join(result, fmt.Errorf("restore test creator namespace: %w", err))
			return
		}
		current, err := openKernelNetworkNamespace("thread-self")
		if err != nil {
			result = errors.Join(result, err)
			return
		}
		after, statErr := current.Stat()
		closeErr := current.Close()
		mayUnlock = statErr == nil && os.SameFile(before, after)
		result = errors.Join(result, statErr, closeErr)
		if !mayUnlock {
			result = errors.Join(result, errors.New("test creator namespace restore was not verified"))
		}
	}()
	file, err = openKernelNetworkNamespace("thread-self")
	if err != nil {
		return nil, 0, err
	}
	cookie, err = networkNamespaceCookie()
	return file, cookie, err
}

func replyTestBinary(t *testing.T, path string) string {
	t.Helper()
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		if os.Getenv("ANAS_REQUIRE_INGRESS_NATIVE") == "1" {
			t.Fatal(err)
		}
		t.Skip("native executable is unavailable: " + path)
	}
	return resolved
}

// This sends actual Ethernet/IP/TCP frames through real veth/bridge/nft hooks
// in a new namespace. It is a physical reply-origin test, not an Incus, full
// TCP-session or Docker-coexistence acceptance test. No host namespace is edited.
func TestNativeReplyOriginRejectsSpoofAndDeviceReuse(t *testing.T) {
	ip, nft := replyTestBinary(t, "/usr/sbin/ip"), replyTestBinary(t, "/usr/sbin/nft")
	ns, cookie := isolatedReplyTestNamespace(t)
	b, target, _ := testBackend(t)
	b.config.AddressRouting = &AddressRouting{Table: 31000, Priority: 10000}
	b.config.Binaries.IP, b.config.Binaries.NFT = ip, nft
	b.config.PermitTTL = 30 * time.Second
	b.runner.config = b.config
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	err := inOpenedNetworkNamespace(ctx, ns, cookie, func() error {
		run := func(args ...string) error { return b.runner.run(ctx, ip, args, nil) }
		index := func(name string) (int, error) {
			body, err := b.runner.output(ctx, ip, []string{"-j", "link", "show", "dev", name})
			if err != nil {
				return 0, err
			}
			var links []map[string]any
			if decodeObservedJSON(body, &links) != nil || len(links) != 1 {
				return 0, fmt.Errorf("invalid native test interface")
			}
			return strconv.Atoi(numberField(links[0], "ifindex"))
		}
		createPair := func(host, peer string) error {
			for _, args := range [][]string{
				{"link", "add", host, "type", "veth", "peer", "name", peer},
				{"link", "set", host, "master", b.config.GuestBridge},
				{"link", "set", host, "up"}, {"link", "set", peer, "up"},
			} {
				if err := run(args...); err != nil {
					return err
				}
			}
			return nil
		}
		if err := run("link", "add", b.config.GuestBridge, "type", "bridge"); err != nil {
			return err
		}
		if err := run("link", "set", b.config.GuestBridge, "up"); err != nil {
			return err
		}
		for _, pair := range [][2]string{{target.HostVethName, "goodpeer"}, {"badhost", "badpeer"}, {"sinkhost", "sinkpeer"}} {
			if err := createPair(pair[0], pair[1]); err != nil {
				return err
			}
		}
		good, err := index("goodpeer")
		if err != nil {
			return err
		}
		bad, err := index("badpeer")
		if err != nil {
			return err
		}
		sink, err := index("sinkpeer")
		if err != nil {
			return err
		}
		physical, err := index(target.HostVethName)
		if err != nil {
			return err
		}
		const sinkMAC = "02:00:00:00:00:30"
		if err := run("link", "set", "sinkpeer", "address", sinkMAC); err != nil {
			return err
		}
		receive, err := unix.Socket(unix.AF_PACKET, unix.SOCK_RAW|unix.SOCK_NONBLOCK|unix.SOCK_CLOEXEC, int(htonsReply(unix.ETH_P_IP)))
		if err != nil {
			return err
		}
		defer unix.Close(receive)
		if err := unix.Bind(receive, &unix.SockaddrLinklayer{Protocol: htonsReply(unix.ETH_P_IP), Ifindex: sink}); err != nil {
			return err
		}
		send, err := unix.Socket(unix.AF_PACKET, unix.SOCK_RAW|unix.SOCK_CLOEXEC, int(htonsReply(unix.ETH_P_IP)))
		if err != nil {
			return err
		}
		defer unix.Close(send)
		sequence := 0
		probe := func(label string, peer int, mac string, port uint16, allowed bool) error {
			sequence++
			marker := []byte(fmt.Sprintf("anas-native-reply-%d", sequence))
			frame := nativeReplyFrame(mac, sinkMAC, target.GuestIP, b.config.TraefikSourceIP, port, marker)
			if err := unix.Sendto(send, frame, 0, &unix.SockaddrLinklayer{Ifindex: peer, Protocol: htonsReply(unix.ETH_P_IP)}); err != nil {
				return err
			}
			deadline := time.Now().Add(350 * time.Millisecond)
			for time.Now().Before(deadline) {
				if ctx.Err() != nil {
					return ctx.Err()
				}
				poll := []unix.PollFd{{Fd: int32(receive), Events: unix.POLLIN}}
				if _, err := unix.Poll(poll, 25); err != nil && !errors.Is(err, unix.EINTR) {
					return err
				}
				buffer := make([]byte, 4096)
				n, _, err := unix.Recvfrom(receive, buffer, 0)
				if errors.Is(err, unix.EAGAIN) || errors.Is(err, unix.EWOULDBLOCK) || errors.Is(err, unix.EINTR) {
					continue
				}
				if err != nil {
					return err
				}
				if !bytes.Contains(buffer[:n], marker) {
					continue
				}
				if !allowed {
					return fmt.Errorf("%s: unauthorized frame crossed reply-origin gate", label)
				}
				return nil
			}
			if allowed {
				return fmt.Errorf("%s: positive packet control failed", label)
			}
			return nil
		}
		// Both physical paths must pass before installing the tested deny.
		if err := probe("good path control", good, target.NICMAC, target.GuestPort, true); err != nil {
			return err
		}
		if err := probe("spoof path control", bad, target.NICMAC, target.GuestPort, true); err != nil {
			return err
		}
		if err := b.applyNFTScript(ctx, b.baselineFamilyInstallScript("bridge")); err != nil {
			return err
		}
		if err := probe("unapproved", good, target.NICMAC, target.GuestPort, false); err != nil {
			return err
		}
		spec := b.replyOriginSpecAtIndex(target, uint32(physical))
		inventory := func() (nftInventory, error) {
			table, err := readNFTTableWithKernelIndices(ctx, b.config.OriginTable, func() (nftTable, error) {
				body, err := b.runner.output(ctx, nft, []string{"-j", "-a", "-y", "-T", "list", "table", "bridge", b.config.OriginTable})
				if err != nil {
					return nftTable{}, err
				}
				return parseNFTTable(body)
			})
			if err != nil {
				return nftInventory{}, err
			}
			if err := b.validateNFTBaseline(table, "bridge"); err != nil {
				return nftInventory{}, err
			}
			inv := nftInventory{Sets: table.Sets}
			for _, r := range table.Rules {
				if r.Chain == replyChain {
					if !r.matches(spec) {
						t.Logf("isolated reply rule: %s; expected: %s", r.Expr, spec.expressions())
						text, textErr := b.runner.output(ctx, nft, []string{"-a", "-n", "list", "chain", "bridge", b.config.OriginTable, replyChain})
						t.Logf("isolated numeric reply chain: %s (error %v)", text, textErr)
						text, textErr = b.runner.output(ctx, nft, []string{"-a", "-nnn", "list", "chain", "bridge", b.config.OriginTable, replyChain})
						t.Logf("isolated triple numeric reply chain: %s (error %v)", text, textErr)
						text, textErr = b.runner.output(ctx, nft, []string{"-a", "-n", "--debug=netlink", "list", "chain", "bridge", b.config.OriginTable, replyChain})
						t.Logf("isolated netlink reply chain: %s (error %v)", text, textErr)
					}
					inv.Rules = append(inv.Rules, r)
				}
			}
			return inv, nil
		}
		apply := func(remove bool) error {
			inv, err := inventory()
			if err != nil {
				return err
			}
			script, err := b.replyOriginCommands(inv, spec, target, remove)
			if err != nil {
				return err
			}
			return b.applyNFTScript(ctx, script)
		}
		if err := apply(false); err != nil {
			return err
		}
		// A successful JSON read cannot be combined with raw rule evidence
		// from an earlier generation. Change only this disposable table after
		// reading JSON and require the joint observation to fail closed.
		changedDuringRead := false
		_, changedErr := readNFTTableWithKernelIndices(ctx, b.config.OriginTable, func() (nftTable, error) {
			body, err := b.runner.output(ctx, nft, []string{"-j", "-a", "-y", "-T", "list", "table", "bridge", b.config.OriginTable})
			if err != nil {
				return nftTable{}, err
			}
			snapshot, err := parseNFTTable(body)
			if err != nil {
				return nftTable{}, err
			}
			if err := b.applyNFTScript(ctx, "add chain bridge "+b.config.OriginTable+" generation_probe\n"); err != nil {
				return nftTable{}, err
			}
			changedDuringRead = true
			return snapshot, nil
		})
		if !changedDuringRead || !errors.Is(changedErr, errNFTIndexEvidence) {
			return fmt.Errorf("changed kernel ruleset generation did not invalidate joint evidence")
		}
		if err := b.applyNFTScript(ctx, "delete chain bridge "+b.config.OriginTable+" generation_probe\n"); err != nil {
			return err
		}
		if err := probe("approved", good, target.NICMAC, target.GuestPort, true); err != nil {
			return err
		}
		if err := probe("wrong physical port", bad, target.NICMAC, target.GuestPort, false); err != nil {
			return err
		}
		if err := probe("wrong MAC", good, "02:00:00:00:00:99", target.GuestPort, false); err != nil {
			return err
		}
		if err := probe("wrong service port", good, target.NICMAC, target.GuestPort+1, false); err != nil {
			return err
		}
		if err := apply(true); err != nil {
			return err
		}
		if err := probe("revoked", good, target.NICMAC, target.GuestPort, false); err != nil {
			return err
		}
		if err := apply(false); err != nil {
			return err
		}
		if err := run("link", "del", target.HostVethName); err != nil {
			return err
		}
		if err := createPair(target.HostVethName, "goodpeer"); err != nil {
			return err
		}
		replacement, err := index(target.HostVethName)
		if err != nil || replacement == physical {
			return fmt.Errorf("replacement control did not allocate a new device")
		}
		good, err = index("goodpeer")
		if err != nil {
			return err
		}
		inv, err := inventory()
		if err != nil {
			return err
		}
		if !inv.Sets[spec.Set].live(target) {
			return fmt.Errorf("reuse test lost live old permission before the negative control")
		}
		if err := probe("same-name replacement", good, target.NICMAC, target.GuestPort, false); err != nil {
			return err
		}
		if err := apply(true); err != nil {
			return err
		}
		spec = b.replyOriginSpecAtIndex(target, uint32(replacement))
		b.config.PermitTTL = time.Second
		if err := apply(false); err != nil {
			return err
		}
		if err := probe("expiry positive control", good, target.NICMAC, target.GuestPort, true); err != nil {
			return err
		}
		time.Sleep(1200 * time.Millisecond)
		if err := probe("expired", good, target.NICMAC, target.GuestPort, false); err != nil {
			return err
		}
		return apply(true)
	})
	if err != nil {
		t.Fatal(err)
	}
}

func htonsReply(v uint16) uint16 { return v<<8 | v>>8 }

func replyChecksum(data []byte) uint16 {
	var sum uint32
	for len(data) >= 2 {
		sum += uint32(binary.BigEndian.Uint16(data))
		data = data[2:]
	}
	if len(data) != 0 {
		sum += uint32(data[0]) << 8
	}
	for sum>>16 != 0 {
		sum = (sum & 65535) + (sum >> 16)
	}
	return ^uint16(sum)
}

func nativeReplyFrame(sourceMAC, destMAC, sourceIP, destIP string, port uint16, payload []byte) []byte {
	frame := make([]byte, 14+20+20+len(payload))
	dm, _ := net.ParseMAC(destMAC)
	sm, _ := net.ParseMAC(sourceMAC)
	copy(frame[:6], dm)
	copy(frame[6:12], sm)
	binary.BigEndian.PutUint16(frame[12:14], unix.ETH_P_IP)
	ip := frame[14:34]
	ip[0], ip[8], ip[9] = 0x45, 64, unix.IPPROTO_TCP
	binary.BigEndian.PutUint16(ip[2:4], uint16(len(frame)-14))
	sa, da := netip.MustParseAddr(sourceIP).As4(), netip.MustParseAddr(destIP).As4()
	copy(ip[12:16], sa[:])
	copy(ip[16:20], da[:])
	binary.BigEndian.PutUint16(ip[10:12], replyChecksum(ip))
	tcp := frame[34:]
	binary.BigEndian.PutUint16(tcp[:2], port)
	binary.BigEndian.PutUint16(tcp[2:4], 50000)
	binary.BigEndian.PutUint32(tcp[4:8], 1)
	binary.BigEndian.PutUint32(tcp[8:12], 2)
	tcp[12], tcp[13] = 0x50, 0x18
	binary.BigEndian.PutUint16(tcp[14:16], 4096)
	copy(tcp[20:], payload)
	pseudo := make([]byte, 12+len(tcp))
	copy(pseudo[:8], ip[12:20])
	pseudo[9] = unix.IPPROTO_TCP
	binary.BigEndian.PutUint16(pseudo[10:12], uint16(len(tcp)))
	copy(pseudo[12:], tcp)
	binary.BigEndian.PutUint16(tcp[16:18], replyChecksum(pseudo))
	return frame
}
