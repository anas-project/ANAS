package dms

import (
	"context"
	"strings"
	"testing"

	"github.com/anas-project/ANAS/modules/mail/identity"
	"github.com/anas-project/ANAS/modules/mail/store"
)

func TestAdapterProbesSelectedBoxAndRetainsOtherConfiguration(t *testing.T) {
	a, in := fixture(t)
	if _, err := a.Apply(t.Context(), in); err != nil {
		t.Fatal(err)
	}
	second := in
	second.Projection.Resource = in.Projection.Resource
	second.Projection.Resource.ID = "cccccccc-cccc-4ccc-8ccc-cccccccccccc"
	second.Projection.Resource.Kind = "additional"
	second.Projection.Resource.DefaultAddress = "projects@example.test"
	second.Projection.Resource.Addresses = []identity.AddressResource{{Address: "projects@example.test", Kind: "additional", State: "active", Version: second.Projection.Resource.Version, OperationID: second.Projection.Resource.OperationID, OperationState: "complete"}}
	second.Projection.Mailbox.ID = second.Projection.Resource.ID
	second.Projection.Mailbox.DefaultAddress = "projects@example.test"
	second.Projection.Mailbox.ProtocolLogin = "mbx_" + strings.Repeat("d", 32)
	second.Projection.Mailbox.StorageRef = "store_" + strings.Repeat("e", 32)
	second.Projection.Mailbox.RoundcubeKey = "webmail_" + strings.Repeat("f", 32)
	second.Projection.Fingerprint, _, _ = store.ProjectionFingerprint(second.Projection.Resource)
	want := second.Projection.Mailbox.ID
	checks := 0
	a.probe = func(_ context.Context, boxes map[string]entry) error {
		checks++
		if len(boxes) != 1 || boxes[want].Projection.Mailbox.ID != want {
			t.Fatal("unrelated boxes included in live recipient probes")
		}
		return nil
	}
	if _, err := a.Apply(t.Context(), second); err != nil {
		t.Fatal(err)
	}
	state, err := a.load()
	if err != nil || len(state.Boxes) != 2 {
		t.Fatal("other box configuration lost", err)
	}
	want = in.Projection.Mailbox.ID
	if _, err = a.Read(t.Context(), want); err != nil || checks != 2 {
		t.Fatal("per-box readback failed", err)
	}
}
