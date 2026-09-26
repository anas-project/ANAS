package incusprovision

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/anas-project/ANAS/internal/incusingresshost"
)

func TestForwardingRetirementUsesAnExplicitConfirmedOperation(t *testing.T) {
	_, _, _, request := permissionFixture(t)
	request.Operation = "retire"
	request.Destinations = nil
	if _, err := request.Canonical(); err != nil {
		t.Fatal("a disabled forwarding scope has no explicit retirement operation", err)
	}
	request.Destinations = []ForwardingDestination{{IPv4: "192.0.2.12", Port: 8080}}
	if request.Validate() == nil {
		t.Fatal("retirement can introduce a destination grant")
	}
}

func retiredPermissionFixture(t *testing.T) (*ForwardingPermissionBackend, *forwardingStoreFixture, *permissionKernelFixture, ForwardingPermissionRequest, *int) {
	t.Helper()
	b, store, kernel, request := permissionFixture(t)
	if _, err := b.Apply(context.Background(), request, permissionBinding(t, b, request)); err != nil {
		t.Fatal(err)
	}
	request.Operation, request.Destinations = "disable", nil
	if _, err := b.Apply(context.Background(), request, permissionBinding(t, b, request)); err != nil {
		t.Fatal(err)
	}
	request.Operation = "retire"
	held := 0
	b.retirement = func(ctx context.Context, state State, r ForwardingPermissionRecord) (*forwardingRetirementSession, error) {
		if !store.locked || held != 0 || !forwardingRetirementReady(r) || state.Ownership.ID != r.Grant.OwnershipID {
			t.Fatal("retirement escaped original host/record ownership")
		}
		held++
		return &forwardingRetirementSession{stamp: strings.Repeat("8", 64), check: func(ctx context.Context) error {
			if !store.locked || held != 1 {
				t.Fatal("retirement view was released before the final check")
			}
			return ctx.Err()
		}, close: func() error { held--; return nil }}, nil
	}
	return b, store, kernel, request, &held
}

func TestForwardingRetirementReleasesOwnedRulesAndKeepsEvidence(t *testing.T) {
	b, store, kernel, request, held := retiredPermissionFixture(t)
	record := store.state.ForwardingScopes[request.scopeKey()]
	record.Kernel.Failures = []incusingresshost.ForwardingKernelFailure{{Step: "connections.close", At: time.Now().UTC()}}
	store.state.ForwardingScopes[request.scopeKey()] = record
	before := store.saves
	binding := permissionBinding(t, b, request)
	if store.saves != before || *held != 0 {
		t.Fatal("retirement plan mutated state or leaked its lock")
	}
	out, err := b.Apply(context.Background(), request, binding)
	if err != nil || out.Validate() != nil || !out.Retired || out.Enabled || out.ComputeReady || !out.ConnectionsRevoked || *held != 0 {
		t.Fatal("retirement was not fully verified", out, err)
	}
	after := store.state.ForwardingScopes[request.scopeKey()]
	if after.Kernel.Phase != "released" || len(after.Kernel.Failures) != 1 || after.Grant.Epoch != record.Grant.Epoch ||
		len(after.Kernel.Instances) != len(record.Kernel.Instances) || unrecoveredPendingIntent(store.state) != "" || kernel.calls[len(kernel.calls)-1] != "release" {
		t.Fatal("retirement erased evidence, omitted removal or retained the uninstall block")
	}
	if _, err := b.Apply(context.Background(), request, binding); err == nil {
		t.Fatal("old confirmation replayed")
	}
	if _, err := b.Apply(context.Background(), request, permissionBinding(t, b, request)); err != nil {
		t.Fatal("explicit repeated retirement failed", err)
	}
}

func TestForwardingRetirementRejectsDriftAndPreservesFailures(t *testing.T) {
	for _, scenario := range []string{"source", "check", "save", "release", "postcheck", "close", "cancel"} {
		t.Run(scenario, func(t *testing.T) {
			b, store, kernel, request, _ := retiredPermissionFixture(t)
			binding := permissionBinding(t, b, request)
			prior := store.state.ForwardingScopes[request.scopeKey()]
			calls := len(kernel.calls)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			old := b.retirement
			b.retirement = func(ctx context.Context, s State, r ForwardingPermissionRecord) (*forwardingRetirementSession, error) {
				if scenario == "source" {
					return nil, ErrBlocked
				}
				v, err := old(ctx, s, r)
				if err != nil {
					return nil, err
				}
				check, close := v.check, v.close
				v.check = func(ctx context.Context) error {
					if scenario == "check" || scenario == "postcheck" && len(kernel.calls) > calls {
						return ErrDrift
					}
					return check(ctx)
				}
				v.close = func() error {
					err := close()
					if scenario == "close" {
						return errors.Join(err, ErrUnsafeState)
					}
					return err
				}
				return v, nil
			}
			if scenario == "save" {
				store.failSaveAt = store.saves + 1
			}
			if scenario == "release" {
				kernel.fail = "release"
			}
			if scenario == "cancel" {
				cancel()
			}
			out, err := b.Apply(ctx, request, binding)
			if err == nil || out.Schema != "" {
				t.Fatal("unconfirmed retirement returned a successful result")
			}
			after := store.state.ForwardingScopes[request.scopeKey()]
			if after.Grant.Epoch != prior.Grant.Epoch || len(after.Kernel.Instances) != len(prior.Kernel.Instances) {
				t.Fatal("retirement failure erased old ownership")
			}
			if scenario == "source" || scenario == "check" || scenario == "save" || scenario == "cancel" {
				if len(kernel.calls) != calls {
					t.Fatal("unsafe retirement reached kernel mutation")
				}
			}
			if scenario == "release" || scenario == "postcheck" || scenario == "close" {
				if after.Status != "failed" || unrecoveredPendingIntent(store.state) == "" {
					t.Fatal("failed retirement lost its durable barrier")
				}
			}
		})
	}
}

func TestForwardingRetirementNeverImplicitlyDisablesAnEnabledLease(t *testing.T) {
	b, store, kernel, request := permissionFixture(t)
	if _, err := b.Apply(context.Background(), request, permissionBinding(t, b, request)); err != nil {
		t.Fatal(err)
	}
	before, effects := store.saves, len(kernel.calls)
	request.Operation, request.Destinations = "retire", nil
	if _, err := b.Plan(context.Background(), request); err == nil || store.saves != before || len(kernel.calls) != effects {
		t.Fatal("retirement bypassed explicit withdrawal or changed an enabled lease")
	}
}
