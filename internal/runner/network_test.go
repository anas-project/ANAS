package runner

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// The helper is what holds CAP_NET_ADMIN, so where it is found matters as much
// as what it does: PATH belongs to whoever invoked anas, which is the party
// this design is trying not to grant more to.
func TestHostNetHelperIsFoundBesideTheBinaryBeforePATH(t *testing.T) {
	dir := t.TempDir()
	self, err := os.Executable()
	if err != nil {
		t.Skip("no executable path on this platform")
	}
	beside := filepath.Join(filepath.Dir(self), hostNetHelperName)
	if err := os.WriteFile(beside, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Skipf("cannot write beside the test binary: %v", err)
	}
	defer os.Remove(beside)

	shadow := filepath.Join(dir, hostNetHelperName)
	if err := os.WriteFile(shadow, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	found, err := findHostNetHelper()
	if err != nil {
		t.Fatal(err)
	}
	if found != beside {
		t.Fatalf("resolved %q, want the copy beside the binary at %q", found, beside)
	}
}

// Not being installed has to say so. The failure it replaces -- a sudoers rule
// that was never added -- used to surface as a bare non-zero exit from sudo.
func TestHostNetHelperReportsItsAbsence(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	previous := hostNetHelperName
	hostNetHelperName = "anas-helper-that-is-not-installed"
	defer func() { hostNetHelperName = previous }()

	_, err := hostNetHelperArgs("", "bridge", "down", "--name", "anas_bridge")
	if err == nil {
		t.Fatal("a missing helper was not reported")
	}
	if !strings.Contains(err.Error(), "is not installed") {
		t.Fatalf("error = %q, want it to say the helper is missing", err.Error())
	}
}

// Entering a network namespace is privileged in itself, so the isolation
// environments that use one keep the sudo they always had. Nothing else does.
func TestHostNetHelperEscalatesOnlyForANamespace(t *testing.T) {
	dir := t.TempDir()
	helper := filepath.Join(dir, hostNetHelperName)
	if err := os.WriteFile(helper, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	previous := hostNetHelperInstallPath
	hostNetHelperInstallPath = dir
	defer func() { hostNetHelperInstallPath = previous }()

	plain, err := hostNetHelperArgs("", "bridge", "down", "--name", "anas_bridge")
	if err != nil {
		t.Fatal(err)
	}
	if plain[0] == "sudo" {
		t.Fatalf("the ordinary path escalated: %v", plain)
	}
	if filepath.Base(plain[0]) != hostNetHelperName {
		t.Fatalf("args[0] = %q, want the helper itself", plain[0])
	}

	namespaced, err := hostNetHelperArgs("/run/netns/anas-test", "bridge", "down", "--name", "anas_bridge")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"sudo", "nsenter", "--net=/run/netns/anas-test", helper, "bridge", "down", "--name", "anas_bridge"}
	if !reflect.DeepEqual(namespaced, want) {
		t.Fatalf("args = %#v, want %#v", namespaced, want)
	}

	if _, err := hostNetHelperArgs("run/netns/anas-test", "bridge", "down"); err == nil {
		t.Fatal("expected a relative namespace path to be rejected")
	}
}

// The bridge address carries the prefix the plan settled on, and every address
// this deployment puts on the segment other than the bridge gets a route.
func TestBridgeArgsCarryThePlan(t *testing.T) {
	env := map[string]string{
		"VLAN_BRIDGE_INTERFACE": "anas_bridge",
		"INTERFACE":             "eth0",
		"VLAN_BRIDGE_IP":        "192.168.1.50",
		"VLAN_SUBNET_MASK":      "32",
		"HOST_LAN_IP":           "192.168.1.51",
	}
	want := []string{
		"bridge", "up", "--name", "anas_bridge", "--parent", "eth0",
		"--address", "192.168.1.50/32", "--route", "192.168.1.51/32",
	}
	if got := bridgeUpArgs(env); !reflect.DeepEqual(got, want) {
		t.Fatalf("bridgeUpArgs = %#v, want %#v", got, want)
	}
	wantDown := []string{"bridge", "down", "--name", "anas_bridge"}
	if got := bridgeDownArgs(env); !reflect.DeepEqual(got, wantDown) {
		t.Fatalf("bridgeDownArgs = %#v, want %#v", got, wantDown)
	}
}

func TestHostLANRequired(t *testing.T) {
	a := &app{
		order: []string{"core", "storage"},
		reg: map[string]Module{
			"core":    {Name: "core"},
			"storage": {Name: "storage", UseHostLAN: "required"},
		},
	}
	if !a.hostLANRequired() {
		t.Fatal("expected required host LAN to be detected")
	}
}

// The pool is docker's --ip-range. It has to be passed when the runner chose
// the address and omitted when the deployment pinned one, because docker
// rejects a static address outside the range it was given.
func TestNetworkCreateArgsOmitsRangeWhenAddressIsPinned(t *testing.T) {
	pooled := map[string]string{
		"INTERFACE": "eth0", "HOST_SEGMENT": "192.168.1.0/24",
		"VLAN_GATEWAY_IP": "192.168.1.1", "VLAN_SEGMENT": "192.168.1.240/28",
		"VLAN_BRIDGE_IP": "192.168.1.241", "HOST_LAN_IP": "192.168.1.242",
	}
	args := strings.Join(networkCreateArgs("anas_macvlan", pooled), " ")
	if !strings.Contains(args, "--ip-range 192.168.1.240/28") {
		t.Errorf("pooled network lost its range: %s", args)
	}
	if !strings.Contains(args, "--aux-address bridge=192.168.1.241") {
		t.Errorf("bridge address was not reserved from IPAM: %s", args)
	}

	pinned := map[string]string{
		"INTERFACE": "eth0", "HOST_SEGMENT": "192.168.1.0/24",
		"VLAN_GATEWAY_IP": "192.168.1.1",
		"VLAN_BRIDGE_IP":  "192.168.1.50", "HOST_LAN_IP": "192.168.1.51",
	}
	args = strings.Join(networkCreateArgs("anas_macvlan", pinned), " ")
	if strings.Contains(args, "--ip-range") {
		t.Errorf("pinned address must not be constrained by a range: %s", args)
	}
	if !strings.HasSuffix(args, " anas_macvlan") {
		t.Errorf("network name must stay last: %s", args)
	}
}

// A pinned container address can sit outside the bridge address's own prefix,
// where the bridge's connected route does not reach it.
func TestHostLANRoutesCoverTheContainerButNotTheBridge(t *testing.T) {
	routes := hostLANRoutes(map[string]string{
		"VLAN_BRIDGE_IP": "192.168.1.50", "HOST_LAN_IP": "192.168.1.51",
	})
	// The bridge address is on the bridge already; routing to it through the
	// bridge would be a loop.
	if !reflect.DeepEqual(routes, []string{"192.168.1.51/32"}) {
		t.Fatalf("routes = %#v, want only the container address", routes)
	}
	if got := hostLANRoutes(map[string]string{"VLAN_BRIDGE_IP": "192.168.1.241"}); len(got) != 0 {
		t.Errorf("routes = %#v, want none when no container address is planned", got)
	}
}

// The neighbour table answers "is somebody there now", and only a REACHABLE
// entry says that. A STALE entry is a memory -- often of this deployment's own
// previous container -- and treating it as an occupant would refuse to start
// over an address nobody holds.
func TestReachableNeighbourOnlyReportsConfirmedOccupants(t *testing.T) {
	for _, tc := range []struct {
		name string
		out  string
		want string
	}{
		{"reachable", "192.168.1.51 dev eth0 lladdr aa:bb:cc:dd:ee:ff REACHABLE\n", "aa:bb:cc:dd:ee:ff"},
		{"stale", "192.168.1.51 dev eth0 lladdr aa:bb:cc:dd:ee:ff STALE\n", ""},
		{"failed", "192.168.1.51 dev eth0  FAILED\n", ""},
		{"incomplete", "192.168.1.51 dev eth0  INCOMPLETE\n", ""},
		{"nothing at all", "", ""},
		{"second line reachable", "192.168.1.51 dev eth0 lladdr 11:22:33:44:55:66 STALE\n192.168.1.51 dev eth1 lladdr aa:bb:cc:dd:ee:ff REACHABLE\n", "aa:bb:cc:dd:ee:ff"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := reachableNeighbour(tc.out); got != tc.want {
				t.Fatalf("reachableNeighbour(%q) = %q, want %q", tc.out, got, tc.want)
			}
		})
	}
}

// Probing is a read-only question, and asking it must not widen the one sudo
// boundary this program has. Only a configured namespace -- an isolation
// environment where entering it is privileged in itself -- goes through sudo.
func TestProbeCommandEscalatesOnlyForANamespace(t *testing.T) {
	plain, err := probeCommand("", "ip", "-4", "neigh", "show", "192.168.1.51")
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(plain.Path) != "ip" {
		t.Fatalf("probe ran %q, want a direct ip invocation", plain.Path)
	}
	if plain.Args[0] == "sudo" {
		t.Fatal("probing the host's own namespace must not escalate")
	}

	namespaced, err := probeCommand("/run/netns/anas-test", "ping", "-c", "1", "192.168.1.51")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"sudo", "nsenter", "--net=/run/netns/anas-test", "ping", "-c", "1", "192.168.1.51"}
	if !reflect.DeepEqual(namespaced.Args, want) {
		t.Fatalf("probe args = %#v, want %#v", namespaced.Args, want)
	}

	if _, err := probeCommand("run/netns/anas-test", "ping", "192.168.1.51"); err == nil {
		t.Fatal("expected a relative namespace path to be rejected")
	}
}

// The check is a safety net, not a dependency: an operator who has a reason to
// take a contested address, or a host missing the tools to ask, must still be
// able to deploy.
func TestCheckLANAddressConflictsHonorsTheOptOut(t *testing.T) {
	env := map[string]string{
		"VLAN_BRIDGE_IP": "192.168.1.50", "HOST_LAN_IP": "192.168.1.51",
		"HOST_LAN_ARP_CHECK": "false",
	}
	if err := checkLANAddressConflicts(env); err != nil {
		t.Fatalf("opted-out check still ran: %v", err)
	}
}
