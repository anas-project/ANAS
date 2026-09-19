//go:build linux

package hostaction

import (
	"context"
	"os"
	"testing"
)

func TestSystemdPrivateConnectionRejectsOrdinarySocketPeer(t *testing.T) {
	if os.Getpid() == 1 {
		t.Fatal("fixture must not be the service manager")
	}
	client, _ := brokerTestPair(t)
	if verifySystemdManagerConnection(client) == nil {
		t.Fatal("ordinary process was accepted as PID 1")
	}
	if verifySystemdManagerConnection(nil) == nil {
		t.Fatal("absent manager connection was accepted")
	}
}

func TestSystemdUnitLookupRejectsInvalidContextAndPID(t *testing.T) {
	for _, pid := range []uint32{0, 1} {
		if _, err := openSystemdUnitByPID(context.Background(), pid); err == nil {
			t.Fatal("invalid unit process accepted")
		}
	}
	if _, err := openSystemdUnitByPID(nil, 42); err == nil {
		t.Fatal("nil context accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := openSystemdUnitByPID(ctx, 42); err == nil {
		t.Fatal("cancelled manager lookup accepted")
	}
}
