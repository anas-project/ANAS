package incusprovision

import (
	"path/filepath"
	"strings"
	"testing"
)

// Enrollment probes the gateway listener from the host itself; that exception
// must come before the rule dropping every other source.
func TestLocalEnrollmentProbeRequiresNarrowLoopbackException(t *testing.T) {
	plan := ControlNetworkPlan{OwnershipID: "owner-1", Bridge: "br-anas-ctrl", Subnet: "10.77.0.0/24", Gateway: "10.77.0.1"}
	rules := nftControlRules(plan)
	want := `iifname "lo" ip saddr 10.77.0.1 ip daddr 10.77.0.1 tcp dport 8443 accept comment "anas-local-control-probe"`
	if !strings.Contains(rules, want) {
		t.Fatal("local enrollment probe is blocked by the listener default-deny rule")
	}
	if strings.Index(rules, want) > strings.Index(rules, `comment "anas-control-incus-default-deny"`) {
		t.Fatal("loopback exception appears after the rejecting verdict")
	}
	if strings.Contains(rules, "127.0.0.1") || strings.Contains(rules, "18443") {
		t.Fatal("rules still admit the removed loopback listener or relay port")
	}
}

// A host that was never configured has neither the drop-in nor its directory;
// that is "not ordered yet", not an unsafe state (2026-09-30 native gate).
func TestUnitOrderingAbsentBeforeConfigure(t *testing.T) {
	path := filepath.Join(t.TempDir(), "incus.service.d", "anas-after-docker.conf")
	ordered, err := orderedAfterDockerAt(path)
	if err != nil || ordered {
		t.Fatalf("missing drop-in directory = %v, %v; want not ordered without error", ordered, err)
	}
}
