package main

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestRestoreMacvlanAfterReboot(t *testing.T) {
	// Docker keeps this configuration while the kernel loses the bridge. No
	// running endpoints are needed to restore a pinned member outside the pool.
	const fixture = `{"Driver":"macvlan","Options":{"parent":"eth0"},"Labels":{"dev.anas.host-lan-ip":"192.0.2.42"},"IPAM":{"Config":[{"Subnet":"192.0.2.0/24","IPRange":"192.0.2.240/28","AuxiliaryAddresses":{"bridge":"192.0.2.241"}}]}}`
	var network restoreNetwork
	if err := json.Unmarshal([]byte(fixture), &network); err != nil {
		t.Fatal(err)
	}
	request, err := restoredBridge(network)
	if err != nil {
		t.Fatal(err)
	}
	want := bridgeRequest{Name: "anas_bridge", Parent: "eth0", Address: "192.0.2.241/32", Routes: []string{"192.0.2.240/28", "192.0.2.42/32"}}
	if !reflect.DeepEqual(request, want) {
		t.Fatalf("got %+v, want %+v", request, want)
	}
	commands := bridgeCommands(request, hostState{})
	if !reflect.DeepEqual(commands[0], []string{"link", "add", "anas_bridge", "link", "eth0", "type", "macvlan", "mode", "bridge"}) {
		t.Fatalf("bridge was not recreated: %v", commands)
	}
	if commands := bridgeCommands(request, hostState{InterfaceExists: true, Addresses: []string{request.Address}}); commands[0][0] == "link" {
		t.Fatalf("existing bridge recreated: %v", commands)
	}

	// Old networks have a reserved pool and may also have running members.
	delete(network.Labels, "dev.anas.host-lan-ip")
	network.Containers = map[string]struct{ IPv4Address string }{"fs": {IPv4Address: "192.0.2.242/24"}}
	request, err = restoredBridge(network)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(request.Routes, []string{"192.0.2.240/28", "192.0.2.242/32"}) {
		t.Fatal(request.Routes)
	}
}

func TestRestoreMacvlanRejectsUnsafeNetwork(t *testing.T) {
	for _, fixture := range []string{
		`{"Driver":"bridge"}`,
		`{"Driver":"macvlan","Options":{"parent":"eth0"},"IPAM":{"Config":[{"Subnet":"192.0.2.0/24"}]}}`,
		`{"Driver":"macvlan","Options":{"parent":"eth0"},"IPAM":{"Config":[{"Subnet":"192.0.2.0/24","IPRange":"0.0.0.0/0","AuxiliaryAddresses":{"bridge":"192.0.2.241"}}]}}`,
		`{"Driver":"macvlan","Options":{"parent":"eth0"},"Labels":{"dev.anas.host-lan-ip":"198.51.100.1"},"IPAM":{"Config":[{"Subnet":"192.0.2.0/24","AuxiliaryAddresses":{"bridge":"192.0.2.241"}}]}}`,
		`{"Driver":"macvlan","Options":{"parent":"../eth0"},"IPAM":{"Config":[{"Subnet":"192.0.2.0/24","AuxiliaryAddresses":{"bridge":"192.0.2.241"}}]}}`,
	} {
		var network restoreNetwork
		if err := json.Unmarshal([]byte(fixture), &network); err != nil {
			t.Fatal(err)
		}
		if _, err := restoredBridge(network); err == nil {
			t.Fatalf("unsafe network accepted: %s", fixture)
		}
	}
}
