package incusprovision

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"
)

type uninstallInventoryRuntime struct {
	*fakeRuntime
	checkErr           error
	check              func()
	checks             int
	removeShared       bool
	ignoreCancellation bool
}

func (r *uninstallInventoryRuntime) CheckUninstallResources(ctx context.Context, ownership Ownership, removeShared bool) error {
	r.checks++
	r.removeShared = removeShared
	if r.check != nil {
		r.check()
	}
	if err := ctx.Err(); err != nil && !r.ignoreCancellation {
		return err
	}
	return r.checkErr
}

// INCUS-R-048/R-051: a refusal to delete a still-used pool or network must
// occur before revoking its management connection, not halfway through teardown.
func TestUninstallInventoryRefusalPreservesConnectionAndAllOwnership(t *testing.T) {
	for _, checkErr := range []error{ErrBlocked, ErrIncomplete, ErrExternalEffects, context.Canceled} {
		t.Run(checkErr.Error(), func(t *testing.T) {
			ctx := context.Background()
			bundle := ConnectionBundle{Schema: BundleSchema}
			store := &memoryStore{state: State{Schema: StateSchema, Ownership: Ownership{
				ID: "owner", StoragePool: StoragePoolName, StoragePoolDriver: "btrfs",
				DockerNetwork: ControlNetworkName, DockerNetworkID: strings.Repeat("a", 64),
				ControlBridge: "br-anas-ctrl", FirewallRules: true, ControlListener: true,
				ManagementTrust: strings.Repeat("b", 64), ConnectionBundle: true,
			}, Bundle: &bundle}, bundle: &bundle}
			original := store.state
			rt := &uninstallInventoryRuntime{fakeRuntime: newFakeRuntime(t), checkErr: checkErr}
			backend := newBackendForTest(store, rt)
			plan, err := backend.Plan(ctx, Request{})
			if err != nil {
				t.Fatal(err)
			}
			out, err := backend.Uninstall(ctx, Request{}, bind(plan, PhaseUninstall))
			if !errors.Is(err, checkErr) || out.Disposition != "blocked" || rt.checks != 1 {
				t.Fatalf("uninstall bypassed independent inventory refusal: result=%s checks=%d err=%v", out.Disposition, rt.checks, err)
			}
			if store.removed || store.bundle == nil || !reflect.DeepEqual(store.state, original) {
				t.Fatal("inventory refusal changed connection, ownership or effect history")
			}
			for _, call := range rt.calls {
				if strings.HasPrefix(call, "remove-") {
					t.Fatalf("destructive effect ran before complete inventory: %s", call)
				}
			}
			// A read-only refusal must not create an uncertain effect requiring
			// operator recovery. A fresh confirmed retry may succeed after drain.
			rt.checkErr = nil
			plan, err = backend.Plan(ctx, Request{})
			if err != nil || slices.Contains(plan.Blockers, "host_effect_recovery_required") {
				t.Fatal("read-only inventory refusal poisoned later confirmed retries", err)
			}
			if _, err = backend.Uninstall(ctx, Request{}, bind(plan, PhaseUninstall)); err != nil || !store.removed {
				t.Fatal("empty verified inventory did not allow a newly confirmed uninstall", err)
			}
		})
	}
}

func TestUninstallRechecksCancellationAfterInventoryBeforeFirstEffect(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	bundle := ConnectionBundle{Schema: BundleSchema}
	store := &memoryStore{state: State{Schema: StateSchema, Ownership: Ownership{ID: "owner", ConnectionBundle: true}, Bundle: &bundle}, bundle: &bundle}
	rt := &uninstallInventoryRuntime{fakeRuntime: newFakeRuntime(t), check: cancel, ignoreCancellation: true}
	backend := newBackendForTest(store, rt)
	plan, err := backend.Plan(ctx, Request{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := backend.Uninstall(ctx, Request{}, bind(plan, PhaseUninstall)); !errors.Is(err, context.Canceled) || store.removed || len(store.state.Intents) != 0 {
		t.Fatal("cancellation after read-only inventory entered destructive effects", err)
	}
}

func TestManagedGuestDetectionDoesNotMatchMissingParentToMissingBridge(t *testing.T) {
	ownership := Ownership{DockerNetwork: ControlNetworkName}
	foreign := incusInstance{Name: "anas-foreign", ExpandedDevices: map[string]map[string]string{"root": {"type": "disk", "pool": "external", "path": "/"}}}
	if instanceUsesOwnedResource(foreign, ownership) {
		t.Fatal("missing parent was matched to missing owned bridge")
	}
}
