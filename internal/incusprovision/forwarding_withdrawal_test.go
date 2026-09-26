package incusprovision

import (
	"context"
	"reflect"
	"testing"

	"github.com/anas-project/ANAS/internal/incusingresshost"
)

func TestForwardingWorkspaceWithdrawalUsesOriginalReceipt(t *testing.T) {
	b, s, k, r := permissionFixture(t)
	if _, err := b.Apply(context.Background(), r, permissionBinding(t, b, r)); err != nil {
		t.Fatal(err)
	}
	original := s.state.ForwardingScopes[r.scopeKey()]
	b.open = func(context.Context, ForwardingPermissionRequest, State, *ForwardingLeaseGrant) (*forwardingLeaseSession, error) {
		t.Fatal("withdrawal reauthorized a revoked lease")
		return nil, ErrBlocked
	}
	b.runtimeOwnerReady = false
	k.calls = nil
	out, err := b.Withdraw(context.Background(), ForwardingWithdrawalRequest{Schema: ForwardingWithdrawalSchema, WorkspaceID: r.WorkspaceID})
	record := s.state.ForwardingScopes[r.scopeKey()]
	if err != nil || out.Validate() != nil || out.Scopes != 1 || !reflect.DeepEqual(k.calls, []string{"close"}) ||
		record.Status != "disabled" || record.Generation != original.Generation+1 || !reflect.DeepEqual(record.Grant, original.Grant) ||
		!record.Kernel.NewConnectionsClosed || !record.Kernel.ConnectionsRevoked || record.Kernel.InetHandle == 0 {
		t.Fatalf("withdrawal lost original ownership or closure: %+v %v %+v", out, err, record)
	}
	if unrecoveredPendingIntent(s.state) == "" {
		t.Fatal("withdrawal incorrectly retired the deny baseline")
	}
}

func TestForwardingWorkspaceWithdrawalPreservesFailureAndScope(t *testing.T) {
	b, s, k, r := permissionFixture(t)
	if _, err := b.Apply(context.Background(), r, permissionBinding(t, b, r)); err != nil {
		t.Fatal(err)
	}
	request := ForwardingWithdrawalRequest{Schema: ForwardingWithdrawalSchema, WorkspaceID: "other"}
	k.calls = nil
	if out, err := b.Withdraw(context.Background(), request); err != nil || out.Scopes != 0 || len(k.calls) != 0 {
		t.Fatal(out, err)
	}
	request.WorkspaceID = r.WorkspaceID
	k.fail = "close"
	if _, err := b.Withdraw(context.Background(), request); err == nil {
		t.Fatal("unconfirmed revocation passed")
	}
	record := s.state.ForwardingScopes[r.scopeKey()]
	if record.Status != "failed" || record.Kernel.ConnectionsRevoked || record.Kernel.PendingStep != "connections.close" || len(record.Kernel.Failures) == 0 {
		t.Fatal("withdrawal discarded failure evidence")
	}
	k.fail = ""
	if _, err := b.Withdraw(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	if len(s.state.ForwardingScopes[r.scopeKey()].Kernel.Failures) == 0 {
		t.Fatal("explicit retry erased failure history")
	}
}

func TestForwardingWorkspaceWithdrawalRequiresDurableIntent(t *testing.T) {
	b, s, k, r := permissionFixture(t)
	if _, err := b.Apply(context.Background(), r, permissionBinding(t, b, r)); err != nil {
		t.Fatal(err)
	}
	k.calls = nil
	s.failSaveAt = s.saves + 1
	if _, err := b.Withdraw(context.Background(), ForwardingWithdrawalRequest{Schema: ForwardingWithdrawalSchema, WorkspaceID: r.WorkspaceID}); err == nil || len(k.calls) != 0 {
		t.Fatal("withdrawal ignored failed intent persistence")
	}
}

// A backend's nil return is not a revocation receipt. The host boundary must
// inspect the saved receipt before allowing Core to tear down dependencies.
type unconfirmedWithdrawalKernel struct{ forwardingPermissionKernel }

func (k unconfirmedWithdrawalKernel) Close(_ context.Context, r incusingresshost.ForwardingKernelReceipt, _ incusingresshost.ForwardingKernelSave) (incusingresshost.ForwardingKernelReceipt, error) {
	return r, nil
}

func TestForwardingWorkspaceWithdrawalRejectsMissingReadback(t *testing.T) {
	b, s, k, r := permissionFixture(t)
	if _, err := b.Apply(context.Background(), r, permissionBinding(t, b, r)); err != nil {
		t.Fatal(err)
	}
	b.kernel = func() (forwardingPermissionKernel, error) { return unconfirmedWithdrawalKernel{k}, nil }
	if _, err := b.Withdraw(context.Background(), ForwardingWithdrawalRequest{Schema: ForwardingWithdrawalSchema, WorkspaceID: r.WorkspaceID}); err == nil {
		t.Fatal("missing revocation receipt passed")
	}
	if s.state.ForwardingScopes[r.scopeKey()].Status != "failed" {
		t.Fatal("failure barrier lost")
	}
}
