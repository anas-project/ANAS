package incusingresshost

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestConntrackRequiresBothCompleteTuples(t *testing.T) {
	valid := strings.TrimSpace(conntrackLine(testTarget()))
	if _, err := parseConntrackLine(valid); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct{ name, line string }{
		{"missing_reply", strings.Split(valid, " packets=1")[0]},
		{"missing_original_port", strings.Replace(valid, "sport=50000 ", "", 1)},
		{"reply_address", strings.Replace(valid, "src=10.42.0.2 dst=10.231.2.2", "src=10.42.0.3 dst=10.231.2.2", 1)},
		{"reply_port", strings.Replace(valid, "sport=7000 dport=50000", "sport=7001 dport=50000", 1)},
		{"third_tuple", valid + " src=10.42.0.2 dst=10.231.2.2 sport=7000 dport=50000"},
		{"duplicate_original", strings.Replace(valid, "src=10.231.2.2", "src=10.231.2.2 src=10.231.2.2", 1)},
		{"wrong_protocol_number", strings.Replace(valid, "tcp 6", "tcp 17", 1)},
		{"wrong_family_number", strings.Replace(valid, "ipv4 2", "ipv4 10", 1)},
		{"nonzero_zone", valid + " zone=1"},
		{"duplicate_zone", valid + " zone=0 zone=0"},
		{"offloaded", valid + " [OFFLOAD]"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := parseConntrackLine(test.line); err == nil {
				t.Fatal("incomplete, translated or ambiguous conntrack entry accepted")
			}
		})
	}
}

func TestConntrackCleanupRequiresPermitRevocation(t *testing.T) {
	b, target, state := testBackend(t)
	ctx := context.Background()
	for _, action := range []func(context.Context, Target) error{b.HoldAddress, b.EnsureGuestRoute, b.EnsureHTTPPermit} {
		if err := action(ctx, target); err != nil {
			t.Fatal(err)
		}
	}
	writeFile(t, filepath.Join(state, "conntrack"), conntrackLine(target))
	if err := b.CloseHTTPConnections(ctx, target); err == nil {
		t.Fatal("deleted connections while their permit could still admit traffic")
	}
	if _, err := os.Stat(filepath.Join(state, "conntrack")); err != nil {
		t.Fatal("active connection was removed without prior permit revocation")
	}
}

func TestConntrackCleanupDoesNotAcceptOriginalOnlyForgery(t *testing.T) {
	b, target, state := testBackend(t)
	ctx := context.Background()
	if err := b.HoldAddress(ctx, target); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(state, "conntrack"), strings.Split(conntrackLine(target), " packets=1")[0]+"\n")
	if err := b.CloseHTTPConnections(ctx, target); err == nil {
		t.Fatal("malformed original-only inventory authorized a destructive deletion")
	}
	if _, err := os.Stat(filepath.Join(state, "conntrack")); err != nil {
		t.Fatal("invalid inventory triggered deletion")
	}
}

func TestConntrackDeletionPinsBothDirectionsAndZeroZone(t *testing.T) {
	e, err := parseConntrackLine(strings.TrimSpace(conntrackLine(testTarget())))
	if err != nil {
		t.Fatal(err)
	}
	args := conntrackDeleteEntryArgv(e)
	for key, expected := range map[string]string{
		"--orig-src": "10.231.2.2", "--orig-dst": "10.42.0.2", "--sport": "50000", "--dport": "7000",
		"--reply-src": "10.42.0.2", "--reply-dst": "10.231.2.2", "--reply-port-src": "7000", "--reply-port-dst": "50000", "--zone": "0",
	} {
		if argValue(args, key) != expected {
			t.Fatalf("destructive command does not bind %s", key)
		}
	}
	if _, err := parseConntrackExtended([]byte(conntrackLine(testTarget()) + conntrackLine(testTarget()))); err == nil {
		t.Fatal("duplicate inventory identity accepted")
	}
}
