package incusprovision

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/anas-project/ANAS/internal/incusingresshost"
)

// A refresh may have reached the kernel before its caller receives an error.
// Failure injection must preserve that effect, rather than model every error
// as if the write never happened.
type acceptedForwardingRefresh struct {
	forwardingPermissionKernel
	after func() error
}

type failingForwardingWithdrawal struct {
	forwardingPermissionKernel
	t      *testing.T
	closes int
}

func (k *failingForwardingWithdrawal) Close(ctx context.Context, r incusingresshost.ForwardingKernelReceipt, save incusingresshost.ForwardingKernelSave) (incusingresshost.ForwardingKernelReceipt, error) {
	k.t.Helper()
	k.closes++
	deadline, bounded := ctx.Deadline()
	if ctx.Err() != nil || !bounded || time.Until(deadline) > 30*time.Second {
		k.t.Fatal("withdrawal did not receive its independent bounded cleanup context")
	}
	return k.forwardingPermissionKernel.Close(ctx, r, save)
}

func TestForwardingFailedCompensationKeepsPendingEvidence(t *testing.T) {
	b, store, k, request := permissionFixture(t)
	binding := permissionBinding(t, b, request)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	withdrawal := &failingForwardingWithdrawal{forwardingPermissionKernel: k, t: t}
	b.kernel = func() (forwardingPermissionKernel, error) {
		return acceptedForwardingRefresh{withdrawal, func() error {
			k.fail = "close"
			cancel()
			return errors.New("accepted refresh then lost execution")
		}}, nil
	}
	out, err := b.Apply(ctx, request, binding)
	r := store.state.ForwardingScopes[request.scopeKey()]
	if err == nil || !errors.Is(err, context.Canceled) || out.Schema != "" || withdrawal.closes != 1 ||
		r.Status != "failed" || r.Kernel.ConnectionsRevoked || r.Kernel.PendingStep != "connections.close" ||
		len(r.Kernel.Failures) != 1 || len(r.Kernel.Instances) != 1 || unrecoveredPendingIntent(store.state) == "" {
		t.Fatal("unconfirmed compensation retried, erased evidence or reported completion", out, err, withdrawal.closes)
	}
}

func (k acceptedForwardingRefresh) Refresh(ctx context.Context, r incusingresshost.ForwardingKernelReceipt, instances []incusingresshost.ForwardingInstanceProof, known []incusingresshost.ForwardingKernelScope, save incusingresshost.ForwardingKernelSave) (incusingresshost.ForwardingKernelReceipt, error) {
	r, err := k.forwardingPermissionKernel.Refresh(ctx, r, instances, known, save)
	if err == nil {
		err = k.after()
	}
	return r, err
}

func TestForwardingEnableFailureWithdrawsAnAcceptedRefresh(t *testing.T) {
	for _, scenario := range []string{"refresh-readback", "final-save", "session-close", "cancel-after-refresh"} {
		t.Run(scenario, func(t *testing.T) {
			b, store, k, request := permissionFixture(t)
			binding := permissionBinding(t, b, request)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			b.kernel = func() (forwardingPermissionKernel, error) {
				return acceptedForwardingRefresh{k, func() error {
					switch scenario {
					case "refresh-readback":
						return errors.New("accepted refresh could not be confirmed")
					case "final-save":
						store.failSaveAt = store.saves + 1
					case "cancel-after-refresh":
						cancel()
					}
					return nil
				}}, nil
			}
			open := b.open
			b.open = func(c context.Context, r ForwardingPermissionRequest, s State, g *ForwardingLeaseGrant) (*forwardingLeaseSession, error) {
				session, err := open(c, r, s, g)
				if err == nil && scenario == "session-close" {
					session.close = func() error { return ErrUnsafeState }
				}
				return session, err
			}
			out, err := b.Apply(ctx, request, binding)
			if err == nil || out.Schema != "" {
				t.Fatal("unconfirmed enable returned success", out, err)
			}
			r := store.state.ForwardingScopes[request.scopeKey()]
			if !slices.Contains(k.calls, "refresh") || len(k.calls) == 0 || k.calls[len(k.calls)-1] != "close" ||
				r.Status != "failed" || !r.Kernel.NewConnectionsClosed || !r.Kernel.ConnectionsRevoked || r.Kernel.Phase != "closed" {
				t.Fatal("failed enable retained a live permit instead of withdrawing it", scenario, k.calls, r.Status, r.Kernel.Phase)
			}
			if unrecoveredPendingIntent(store.state) == "" || len(r.Kernel.Instances) != 1 {
				t.Fatal("withdrawal erased ownership or removed the dependency barrier")
			}
		})
	}
}
