//go:build linux

package incusingresshost

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

// This is a kernel FIB/neighbor lifecycle test in a newly created netns. It is
// NOT a real Incus guest, packet-flow, reverse-traffic or Docker firewall test.
func TestNativeAddressRoutingCannotFollowDeviceReuse(t *testing.T) {
	ip, err := filepath.EvalSymlinks("/usr/sbin/ip")
	if err != nil {
		if os.Getenv("ANAS_REQUIRE_INGRESS_NATIVE") == "1" {
			t.Fatal(err)
		}
		t.Skip("native iproute2 is required")
	}
	type created struct {
		file   *os.File
		cookie uint64
		err    error
	}
	ready := make(chan created, 1)
	go func() {
		runtime.LockOSThread()
		if err := unix.Unshare(unix.CLONE_NEWNET); err != nil {
			runtime.UnlockOSThread()
			ready <- created{err: err}
			return
		}
		file, err := openKernelNetworkNamespace("thread-self")
		if err != nil {
			ready <- created{err: err}
			return
		}
		cookie, err := networkNamespaceCookie()
		ready <- created{file: file, cookie: cookie, err: err}
		// Exit locked: the isolated thread is destroyed, not pooled.
	}()
	ns := <-ready
	if ns.file != nil {
		defer ns.file.Close()
	}
	if ns.err != nil {
		if os.Getenv("ANAS_REQUIRE_INGRESS_NATIVE") == "1" || (!errors.Is(ns.err, unix.EPERM) && !errors.Is(ns.err, unix.EACCES)) {
			t.Fatal(ns.err)
		}
		t.Skip("requires CAP_SYS_ADMIN in a disposable Linux namespace")
	}
	b, target, _ := testBackend(t)
	b.config.AddressRouting = &AddressRouting{Table: 31000, Priority: 10000}
	b.config.Binaries.IP = ip
	b.runner.config = b.config
	g, err := b.addressRouter()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	err = inOpenedNetworkNamespace(ctx, ns.file, ns.cookie, func() error {
		// ip route get with a foreign source and iif exercises the forwarding
		// lookup. Enable it only inside this newly created, pinned namespace;
		// otherwise a disabled-forwarding negative control can mask the fence.
		if err := os.WriteFile("/proc/sys/net/ipv4/ip_forward", []byte("1\n"), 0600); err != nil {
			return fmt.Errorf("enable isolated fixture forwarding: %w", err)
		}
		run := func(args ...string) error { return g.run(ctx, args) }
		for _, args := range [][]string{
			{"link", "set", "lo", "up"},
			{"link", "add", b.config.IngressBridge, "type", "bridge"},
			{"addr", "add", "10.231.2.1/24", "dev", b.config.IngressBridge},
			{"link", "set", b.config.IngressBridge, "up"},
			{"link", "add", b.config.GuestBridge, "type", "bridge"},
			{"addr", "add", "10.42.0.1/24", "dev", b.config.GuestBridge},
			{"link", "set", b.config.GuestBridge, "up"},
		} {
			if err := run(args...); err != nil {
				return fmt.Errorf("prepare isolated topology: %w", err)
			}
		}
		createLink := func() error {
			for _, args := range [][]string{
				{"link", "add", target.HostVethName, "type", "veth", "peer", "name", "peer0"},
				{"link", "set", target.HostVethName, "address", target.HostVethMAC},
				{"link", "set", "peer0", "address", target.NICMAC},
				{"link", "set", target.HostVethName, "master", b.config.GuestBridge},
				{"link", "set", target.HostVethName, "up"}, {"link", "set", "peer0", "up"},
			} {
				if err := run(args...); err != nil {
					return err
				}
			}
			return nil
		}
		if err := createLink(); err != nil {
			return err
		}
		links, err := g.json(ctx, "-j", "-d", "link", "show", "dev", target.HostVethName)
		if err != nil || len(links) != 1 {
			return fmt.Errorf("cannot observe isolated guest link")
		}
		peer, err := strconv.ParseUint(numberField(links[0], "link_index"), 10, 32)
		if err != nil || peer == 0 {
			return fmt.Errorf("isolated veth lacks a peer index")
		}
		target.HostVethPeerIfIndex = uint32(peer)
		b.config.Resolver = fakeResolver{identity: identityFor(target)}
		query := []string{"-j", "-N", "-4", "route", "get", "fibmatch", target.GuestIP, "from", b.config.TraefikSourceIP, "iif", b.config.IngressBridge}
		lookup := func(device string) error {
			values, err := g.json(ctx, query...)
			if err != nil || len(values) != 1 || stringField(values[0], "dev") != device {
				return fmt.Errorf("FIB did not select expected device %s", device)
			}
			return nil
		}
		blocked := func() error {
			if _, err := g.json(ctx, query...); err == nil {
				return fmt.Errorf("FIB fell through to an unapproved device")
			}
			return nil
		}
		if err := lookup(b.config.GuestBridge); err != nil {
			return fmt.Errorf("fallback control: %w", err)
		}
		if err := g.installLocked(ctx); err != nil {
			return fmt.Errorf("install terminal fence: %w", err)
		}
		if err := blocked(); err != nil {
			return err
		}
		if err := g.holdLocked(ctx, target); err != nil {
			return fmt.Errorf("bind native address: %w", err)
		}
		if err := lookup(target.HostVethName); err != nil {
			return err
		}
		if err := run("link", "del", target.HostVethName); err != nil {
			return err
		}
		if err := blocked(); err != nil {
			return fmt.Errorf("deleted device: %w", err)
		}
		if err := createLink(); err != nil {
			return err
		}
		if err := blocked(); err != nil {
			return fmt.Errorf("replacement device: %w", err)
		}
		if err := g.holdLocked(ctx, target); err == nil {
			return fmt.Errorf("recreated deleted device route for old reservation")
		}
		if err := g.releaseLocked(ctx, target); err != nil {
			return fmt.Errorf("release deleted device: %w", err)
		}
		if err := blocked(); err != nil {
			return err
		}
		if err := g.removeLocked(ctx); err != nil {
			return fmt.Errorf("remove terminal fence: %w", err)
		}
		return lookup(b.config.GuestBridge) // Unrelated original route survived.
	})
	if err != nil {
		t.Fatal(err)
	}
}
