package main

import (
	"reflect"
	"strings"
	"testing"
)

// The name check is the whole blast radius of this program. Everything it can
// do, it does to an interface named here, so a name it must refuse is a host
// interface it can never damage however the arguments arrive.
func TestBridgeRefusesInterfacesItDoesNotManage(t *testing.T) {
	for _, name := range []string{
		"eth0", "enp3s0", "lo", "docker0", "wlan0",
		"", "an", "ANAS_BRIDGE", "not_anas_bridge", "../anas", "anas bridge", "anas-bridge",
	} {
		_, err := parseBridge([]string{"up", "--name", name, "--parent", "eth0", "--address", "192.168.1.50/32"})
		if err == nil {
			t.Errorf("bridge up accepted interface %q", name)
			continue
		}
		if !strings.Contains(err.Error(), "only manages interfaces named anas") {
			t.Errorf("%q was refused for the wrong reason: %v", name, err)
		}
		if _, err := parseBridge([]string{"down", "--name", name}); err == nil {
			t.Errorf("bridge down accepted interface %q", name)
		}
	}

	for _, name := range []string{"anas_bridge", "anas", "anas0", "anas_lan_2"} {
		if _, err := parseBridge([]string{"up", "--name", name, "--parent", "eth0", "--address", "192.168.1.50/32"}); err != nil {
			t.Errorf("bridge up refused its own interface %q: %v", name, err)
		}
	}
}

func TestBridgeRejectsUnusableArguments(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{
			"address without a prefix length",
			[]string{"up", "--name", "anas_bridge", "--parent", "eth0", "--address", "192.168.1.50"},
			"must include a prefix length",
		},
		{
			"address that is not an address",
			[]string{"up", "--name", "anas_bridge", "--parent", "eth0", "--address", "192.168.1.999/32"},
			"not a valid address",
		},
		{
			"IPv6 address",
			[]string{"up", "--name", "anas_bridge", "--parent", "eth0", "--address", "2001:db8::1/128"},
			"not IPv4",
		},
		{
			"route without a prefix length",
			[]string{"up", "--name", "anas_bridge", "--parent", "eth0", "--address", "192.168.1.50/32", "--route", "192.168.1.51"},
			"must include a prefix length",
		},
		{
			"parent that is not an interface name",
			[]string{"up", "--name", "anas_bridge", "--parent", "eth0; rm -rf /", "--address", "192.168.1.50/32"},
			"not a valid interface name",
		},
		{
			"parent longer than IFNAMSIZ",
			[]string{"up", "--name", "anas_bridge", "--parent", "0123456789abcdef", "--address", "192.168.1.50/32"},
			"not a valid interface name",
		},
		{
			"the bridge as its own parent",
			[]string{"up", "--name", "anas_bridge", "--parent", "anas_bridge", "--address", "192.168.1.50/32"},
			"cannot be its own parent",
		},
		{"missing address", []string{"up", "--name", "anas_bridge", "--parent", "eth0"}, "--address is required"},
		{"unknown flag", []string{"up", "--name", "anas_bridge", "--nuke", "yes"}, "unknown flag"},
		{"flag without a value", []string{"up", "--name"}, "needs a value"},
		{"bare argument", []string{"up", "anas_bridge"}, "unexpected argument"},
		{"neither up nor down", []string{"sideways", "--name", "anas_bridge"}, "is not up or down"},
		{
			"down with more than a name",
			[]string{"down", "--name", "anas_bridge", "--address", "192.168.1.50/32"},
			"takes only --name",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := parseBridge(tc.args)
			if err == nil {
				t.Fatalf("%s was accepted", tc.name)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %q, want it to mention %q", err.Error(), tc.want)
			}
		})
	}
}

// This is the code that runs with CAP_NET_ADMIN. What it will do to a host
// should be assertable without a host.
func TestBridgeCommandsForAFreshInterface(t *testing.T) {
	request, err := parseBridge([]string{
		"up", "--name", "anas_bridge", "--parent", "eth0",
		"--address", "192.168.1.50/32", "--route", "192.168.1.51/32",
	})
	if err != nil {
		t.Fatal(err)
	}
	got := bridgeCommands(request, hostState{})
	want := [][]string{
		{"link", "add", "anas_bridge", "link", "eth0", "type", "macvlan", "mode", "bridge"},
		{"addr", "replace", "192.168.1.50/32", "dev", "anas_bridge"},
		{"link", "set", "anas_bridge", "up"},
		{"route", "replace", "192.168.1.51/32", "dev", "anas_bridge"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("commands =\n%v\nwant\n%v", got, want)
	}
}

// An address from an earlier plan keeps the host answering for a range this
// deployment no longer owns. `addr replace` does not remove it: a different
// prefix length makes it a different address as far as replace is concerned.
func TestBridgeCommandsRemoveAddressesFromAnEarlierPlan(t *testing.T) {
	request, err := parseBridge([]string{
		"up", "--name", "anas_bridge", "--parent", "eth0", "--address", "192.168.1.50/32",
	})
	if err != nil {
		t.Fatal(err)
	}
	got := bridgeCommands(request, hostState{
		InterfaceExists: true,
		Addresses:       []string{"192.168.1.241/28", "192.168.1.50/32"},
	})
	want := [][]string{
		{"addr", "replace", "192.168.1.50/32", "dev", "anas_bridge"},
		{"addr", "del", "192.168.1.241/28", "dev", "anas_bridge"},
		{"link", "set", "anas_bridge", "up"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("commands =\n%v\nwant\n%v", got, want)
	}
	// The interface already exists, so nothing recreates it -- recreating would
	// drop the containers attached to its parent's macvlan siblings.
	for _, command := range got {
		if command[0] == "link" && command[1] == "add" {
			t.Fatal("an existing interface was recreated")
		}
	}
}

func TestBridgeDownDeletesOnlyTheNamedInterface(t *testing.T) {
	request, err := parseBridge([]string{"down", "--name", "anas_bridge"})
	if err != nil {
		t.Fatal(err)
	}
	got := bridgeCommands(request, hostState{InterfaceExists: true})
	want := [][]string{{"link", "delete", "anas_bridge"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("commands = %v, want %v", got, want)
	}
}

func TestRunRejectsUnknownOperations(t *testing.T) {
	if err := run(nil); err == nil {
		t.Fatal("running with no operation was accepted")
	}
	if err := run([]string{"rm"}); err == nil {
		t.Fatal("an unknown operation was accepted")
	}
	if err := run([]string{"help"}); err != nil {
		t.Fatalf("help failed: %v", err)
	}
}
