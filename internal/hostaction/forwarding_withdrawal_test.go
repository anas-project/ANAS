package hostaction

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/anas-project/ANAS/internal/incusprovision"
)

func TestForwardingWithdrawalHasNoGrantOrCallerSelectedKernelIdentity(t *testing.T) {
	const action = "incus.forwarding.withdraw"
	valid := []byte(`{"schema":"anas.incus-forwarding-withdrawal/v1","workspace_id":"main"}`)
	if _, err := CanonicalWireParameters(action, valid); err != nil {
		t.Fatal(err)
	}
	if !ObservationScopeMatchesWorkspace(action, valid, "main") || ObservationScopeMatchesWorkspace(action, valid, "other") {
		t.Fatal("workspace boundary missing")
	}
	for _, field := range []string{`"operation":"enable"`, `"source":"10.0.0.2"`, `"scope_key":"abc"`, `"destinations":[]`, `"_confirmation_binding_digest":"abc"`} {
		body := append(append([]byte{}, valid[:len(valid)-1]...), []byte(","+field+"}")...)
		if _, err := CanonicalWireParameters(action, body); err == nil {
			t.Fatal("extra authority accepted", field)
		}
	}
}

type withdrawalActionFixture struct{ calls int }

func (f *withdrawalActionFixture) Withdraw(_ context.Context, r incusprovision.ForwardingWithdrawalRequest) (incusprovision.ForwardingWithdrawalResult, error) {
	f.calls++
	return incusprovision.ForwardingWithdrawalResult{Schema: r.Schema, WorkspaceID: r.WorkspaceID, Scopes: 1, NewConnectionsClosed: true, ConnectionsRevoked: true}, nil
}

func TestForwardingWithdrawalUsesAuditedHostExecutor(t *testing.T) {
	old := newForwardingWithdrawalBackend
	defer func() { newForwardingWithdrawalBackend = old }()
	f := &withdrawalActionFixture{}
	newForwardingWithdrawalBackend = func() forwardingWithdrawalBackend { return f }
	r := testRequest()
	r.Action = ActionForwardingWithdraw
	r.Parameters = []byte(`{"schema":"anas.incus-forwarding-withdrawal/v1","workspace_id":"main"}`)
	call, err := prepare(r, testPeer())
	if err != nil {
		t.Fatal(err)
	}
	event, err := executeIncusProvision(context.Background(), call, &memoryAudit{}, installedRelease())
	if err != nil || event.Result == nil || f.calls != 1 {
		t.Fatal(event, err)
	}
	if _, err = ProjectActionEvent(r.Action, event); err != nil {
		t.Fatal(err)
	}
	var out incusprovision.ForwardingWithdrawalResult
	if json.Unmarshal(event.Result.Value, &out) != nil || out.Validate() != nil {
		t.Fatal("invalid closure proof")
	}
	out.ConnectionsRevoked = false
	event.Result.Value, _ = json.Marshal(out)
	if _, err = ProjectActionEvent(r.Action, event); err == nil {
		t.Fatal("accepted new-connections-only result")
	}
}
