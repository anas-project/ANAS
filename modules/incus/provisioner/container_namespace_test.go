package main

import (
	"context"
	"testing"

	"github.com/anas-project/ANAS/internal/computeclient"
)

func TestContainerNamespacesAreProviderOwnedAndHostFencesRemain(t *testing.T) {
	l := testLease(t, "container")
	config := projectConfig(l)
	if config["restricted.containers.nesting"] != "allow" || config["restricted.containers.privilege"] != "unprivileged" {
		t.Fatal("system container cannot run inner namespaces while remaining unprivileged")
	}
	for _, key := range []string{"restricted.containers.lowlevel", "restricted.virtual-machines.lowlevel", "restricted.devices.disk", "restricted.devices.pci", "restricted.devices.unix-block", "restricted.devices.unix-char", "restricted.devices.gpu", "restricted.devices.usb"} {
		if config[key] != "block" {
			t.Errorf("host fence changed: %s", key)
		}
	}
	if config["restricted.devices.nic"] != "managed" || config["restricted.networks.access"] != computeclient.NetworkName(l.Sandbox) {
		t.Fatal("network fence changed")
	}
	profile := desiredLeaseProfile(l, computeclient.NetworkName(l.Sandbox), nil)
	if profile.Config["security.nesting"] != "true" || profile.Config["security.privileged"] != "false" {
		t.Fatal("namespace policy missing from provider-owned profile")
	}
	vm := testLease(t, "vm")
	if projectConfig(vm)["restricted.containers.nesting"] != "block" || desiredLeaseProfile(vm, computeclient.NetworkName(vm.Sandbox), nil).Config["security.nesting"] != "" {
		t.Fatal("VM tier changed")
	}
}

func TestContainerNamespaceProfileDriftPreventsReadiness(t *testing.T) {
	d := newFakeDaemon(t)
	l := testLease(t, "container")
	c := d.clientFor(t)
	if _, err := ensure(context.Background(), c, l); err != nil {
		t.Fatal(err)
	}
	key := l.Sandbox + "/" + computeclient.ProfileName
	d.profiles[key].Config["security.nesting"] = "false"
	result, _ := inspect(context.Background(), c, l)
	if result.Ready {
		t.Fatal("drifted provider namespace policy reported ready")
	}
}
