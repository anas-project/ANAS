package incusprovision

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"slices"
	"testing"
)

// systemctl cancellation does not cancel a job already accepted by systemd.
// A later active daemon therefore must not erase the failed effect or grant
// install/configure/enrollment authority after a backend/state-store reopen.
func TestLateServiceActivationDoesNotResolveFailedIntent(t *testing.T) {
	for _, failure := range []error{context.DeadlineExceeded, context.Canceled} {
		t.Run(failure.Error(), func(t *testing.T) {
			ctx := context.Background()
			store := &memoryStore{}
			runtime := newFakeRuntime(t)
			backend := newBackendForTest(store, runtime)
			bind := func(t *testing.T, b *Backend, phase Phase, request Request) Binding {
				t.Helper()
				plan, err := b.Plan(ctx, request)
				if err != nil {
					t.Fatal(err)
				}
				return Binding{Schema: Schema, Phase: phase, PlanDigest: plan.Digest, Destructive: true}
			}
			skip := Request{Skip: true}
			if _, err := backend.Install(ctx, skip, bind(t, backend, PhaseInstall, skip)); err != nil {
				t.Fatal(err)
			}
			request := Request{Interface: "incus_container", StorageSizeGiB: 16}
			runtime.fail["enable-incus"] = failure
			result, err := backend.Install(ctx, request, bind(t, backend, PhaseInstall, request))
			if !errors.Is(err, failure) || !errors.Is(err, ErrExternalEffects) || result.Disposition != "partial" || result.ComputeReady {
				t.Fatalf("uncertain activation was reported as success: %#v, %v", result, err)
			}
			if !store.state.Disabled || !store.state.Ownership.PackagesInstalledByANAS || store.state.Ownership.IncusServiceByANAS ||
				store.state.Bundle != nil || store.bundle != nil || unrecoveredPendingIntent(store.state) != "install.service" {
				t.Fatal("service failure did not preserve disabled state and the exact pending effect")
			}
			before, err := json.Marshal(store.state)
			if err != nil {
				t.Fatal(err)
			}
			var persisted State
			if err := json.Unmarshal(before, &persisted); err != nil {
				t.Fatal(err)
			}
			reopened := &memoryStore{state: persisted}
			backend = newBackendForTest(reopened, runtime)
			calls := slices.Clone(runtime.calls)
			// Reproduce the independently accepted systemd job completing after
			// the CLI deadline. This is not a new approved recovery operation.
			runtime.obs.IncusDaemonActive = true
			delete(runtime.fail, "enable-incus")
			plan, err := backend.Plan(ctx, request)
			if err != nil || plan.Disposition != "blocked" || plan.ComputeReady ||
				!slices.Contains(plan.Blockers, "host_effect_recovery_required") || len(plan.Steps) != 0 {
				t.Fatal("late service activation incorrectly made the fresh plan executable", err)
			}
			for _, phase := range []Phase{PhaseInstall, PhaseConfigure, PhaseEnroll, PhaseUninstall} {
				binding := bind(t, backend, phase, request)
				var err error
				switch phase {
				case PhaseInstall:
					_, err = backend.Install(ctx, request, binding)
				case PhaseConfigure:
					_, err = backend.Configure(ctx, request, binding)
				case PhaseEnroll:
					_, err = backend.Enroll(ctx, request, binding)
				case PhaseUninstall:
					_, err = backend.Uninstall(ctx, request, binding)
				}
				if !errors.Is(err, ErrBlocked) || !slices.Equal(calls, runtime.calls) {
					t.Fatalf("%s bypassed the persisted failed activation: %v", phase, err)
				}
			}
			after, err := json.Marshal(reopened.state)
			if err != nil || !bytes.Equal(before, after) || reopened.saves != 0 || reopened.bundle != nil {
				t.Fatal("retries changed the failed intent, receipt, ownership or private connection")
			}
		})
	}
}
