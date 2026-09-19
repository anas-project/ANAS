package incusprovision

import (
	"strings"
	"testing"
)

func TestLocalEnrollmentProbeRequiresNarrowLoopbackRelayException(t *testing.T) {
	plan := ControlNetworkPlan{OwnershipID: "owner-1", Bridge: "br-anas-ctrl", Subnet: "10.77.0.0/24", Gateway: "10.77.0.1"}
	rules := nftControlRules(plan)
	want := `iifname "lo" ip saddr 10.77.0.1 ip daddr 10.77.0.1 tcp dport 18443 accept comment "anas-local-relay-probe"`
	if !strings.Contains(rules, want) {
		t.Fatal("local enrollment relay probe is blocked by the endpoint default-deny rule")
	}
	if strings.Index(rules, want) > strings.Index(rules, `comment "anas-control-relay-default-deny"`) {
		t.Fatal("loopback exception appears after the rejecting verdict")
	}
}
