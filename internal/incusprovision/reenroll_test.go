package incusprovision

import (
	"context"
	"errors"
	"testing"
)

// INCUS-R-048: a skip is reversible through newly confirmed phases. Clearing
// the historical disabled state must wait for verified enrollment, not merely
// a fresh install plan or an attempted trust operation.
func TestVerifiedEnrollmentClearsHistoricalDisabledState(t *testing.T) {
	for _, failure := range []string{"", "trust", "verify-endpoint"} {
		name := failure
		if name == "" {
			name = "verified"
		}
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			store := &memoryStore{}
			rt := newFakeRuntime(t)
			backend := newBackendForTest(store, rt)
			skip := Request{Skip: true}
			plan, err := backend.Plan(ctx, skip)
			if err != nil {
				t.Fatal(err)
			}
			result, err := backend.Install(ctx, skip, bind(plan, PhaseInstall))
			if err != nil || result.Disposition != "disabled" || !store.state.Disabled || len(rt.calls) != 0 {
				t.Fatal("skip must disable compute without host effects", err)
			}
			request := Request{StorageSizeGiB: 16}
			for _, phase := range []Phase{PhaseInstall, PhaseConfigure} {
				plan, err = backend.Plan(ctx, request)
				if err != nil {
					t.Fatal(err)
				}
				if phase == PhaseInstall {
					result, err = backend.Install(ctx, request, bind(plan, phase))
				} else {
					result, err = backend.Configure(ctx, request, bind(plan, phase))
				}
				if err != nil || result.ComputeReady || result.ConnectionReady || !store.state.Disabled {
					t.Fatal("installation/configuration must not clear the previous disabled state before enrollment", err)
				}
			}
			if failure != "" {
				rt.fail[failure] = errors.New("native-operation fixture failure")
			}
			plan, err = backend.Plan(ctx, request)
			if err != nil {
				t.Fatal(err)
			}
			result, err = backend.Enroll(ctx, request, bind(plan, PhaseEnroll))
			if failure != "" {
				if err == nil || !store.state.Disabled || result.ConnectionReady || result.ComputeReady {
					t.Fatal("failed enrollment incorrectly re-enabled a previously disabled installation")
				}
				return
			}
			if err != nil || result.Disposition != "connection_ready" || !result.ConnectionReady || result.ComputeReady {
				t.Fatal("verified management connection must not imply full compute readiness", err)
			}
			if store.state.Disabled || store.state.Public().Disabled {
				t.Fatal("successful re-enrollment retained the obsolete disabled state")
			}
			if store.bundle == nil || !store.state.Ownership.ConnectionBundle {
				t.Fatal("historical disabled state was cleared without persisting the verified bundle")
			}
		})
	}
}
