//go:build linux

package incusingresshost

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"
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
	namespace, cookie := isolatedReplyTestNamespace(t)
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
	// A real container's peer lives in another netns. In the old one-netns
	// fixture iproute2 resolves IFLA_LINK into a reusable "link" name instead
	// of reporting link_index. Keep the production numeric identity check and
	// model the actual boundary, rather than learning an index from that name.
	guestNamespace, guestCookie := isolatedReplyTestNamespace(t)
	var guest *exec.Cmd
	if err := inOpenedNetworkNamespace(ctx, guestNamespace, guestCookie, func() error {
		guest = exec.CommandContext(ctx, "/usr/bin/sleep", "90")
		return guest.Start()
	}); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = guest.Process.Kill(); _ = guest.Wait() }()
	err = inOpenedNetworkNamespace(ctx, namespace, cookie, func() (result error) {
		defer func() {
			if result != nil {
				rules, ruleErr := g.json(ctx, "-j", "-N", "-4", "rule", "show")
				routes, routeErr := g.table(ctx)
				t.Logf("isolated routing policy: %+v (error %v); table: %+v (error %v)", rules, ruleErr, routes, routeErr)
			}
		}()
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
				{"link", "set", target.HostVethName, "up"},
				{"link", "set", "peer0", "netns", strconv.Itoa(guest.Process.Pid)},
			} {
				if err := run(args...); err != nil {
					return err
				}
			}
			return inOpenedNetworkNamespace(ctx, guestNamespace, guestCookie, func() error {
				return run("link", "set", "peer0", "up")
			})
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
