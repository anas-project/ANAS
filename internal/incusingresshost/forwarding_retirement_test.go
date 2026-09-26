package incusingresshost

import "testing"

func TestForwardingRetirementRequiresAnEmptyPhysicalBridge(t *testing.T) {
	if forwardingBridgePortsAbsent([]byte(`[]`)) != nil {
		t.Fatal("complete empty port inventory rejected")
	}
	for _, body := range []string{"", `null`, `{}`, `[null]`, `[{"ifindex":7,"ifname":"foreign-port"}]`, `[] []`} {
		if forwardingBridgePortsAbsent([]byte(body)) == nil {
			t.Fatal("incomplete or occupied bridge admitted", body)
		}
	}
}

func TestForwardingRetiredReceiptCannotHideKernelResidue(t *testing.T) {
	scope, _ := forwardingKernelFixture(t)
	settled := newForwardingKernelReceipt(scope)
	settled.Phase = "released"
	if settled.Validate() != nil {
		t.Fatal("complete retirement tombstone rejected")
	}
	for _, scenario := range []string{"table", "set", "compatibility", "new-connections", "existing-connections", "pending"} {
		t.Run(scenario, func(t *testing.T) {
			r := settled
			switch scenario {
			case "table":
				r.InetHandle, r.BridgeHandle = 1, 2
			case "set":
				r.SetIdentity = scope.GrantDigest
			case "compatibility":
				r.InetHandle, r.BridgeHandle, r.SetIdentity, r.Compatibility = 1, 2, scope.GrantDigest, true
			case "new-connections":
				r.NewConnectionsClosed = false
			case "existing-connections":
				r.ConnectionsRevoked = false
			case "pending":
				r.PendingStep = "nft.remove"
			}
			if r.Validate() == nil {
				t.Fatal("released label hid an unfinished external effect", scenario)
			}
		})
	}
}
