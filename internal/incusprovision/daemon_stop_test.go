package incusprovision

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/anas-project/ANAS/internal/incushost"
)

// Debian's incus-base prerm deliberately skips the native systemd service.
// Removing its files must not be assumed to have stopped the running daemon.
type packageRemovalRequiresStoppedDaemon struct{ *fakeRuntime }

func (r packageRemovalRequiresStoppedDaemon) RemovePackages(ctx context.Context, recipe incushost.Recipe, packages []string) error {
	if r.obs.IncusDaemonActive {
		return errors.New("owned service was not stopped before package removal")
	}
	return r.fakeRuntime.RemovePackages(ctx, recipe, packages)
}

func TestExplicitPackageRemovalStopsOwnedDaemonAfterInventory(t *testing.T) {
	ctx := context.Background()
	store, rt := &memoryStore{}, newFakeRuntime(t)
	backend := newBackendForTest(store, packageRemovalRequiresStoppedDaemon{rt})
	plan, err := backend.Plan(ctx, Request{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := backend.Install(ctx, Request{}, bind(plan, PhaseInstall)); err != nil {
		t.Fatal(err)
	}
	rt.calls = nil
	request := Request{}
	plan, err = backend.Plan(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	out, err := backend.Uninstall(ctx, request, bind(plan, PhaseUninstall))
	if err != nil || out.Disposition != "uninstalled" || store.state.Ownership.IncusServiceByANAS {
		t.Fatal("explicit owned package removal relied on package-maintainer service stopping", err)
	}
	if !slices.Equal(rt.calls, []string{"list-guests", "check-uninstall-resources", "stop-incus", "remove-packages"}) {
		t.Fatal("owned daemon stopping did not follow complete preflight and precede removal", rt.calls)
	}
}

func TestOwnedDaemonStopFailureDoesNotDeletePackagesOrClearOwnership(t *testing.T) {
	for _, mode := range []string{"command failure", "still active", "canceled"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			store, rt := &memoryStore{}, newFakeRuntime(t)
			backend := newBackendForTest(store, rt)
			plan, err := backend.Plan(ctx, Request{})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := backend.Install(ctx, Request{}, bind(plan, PhaseInstall)); err != nil {
				t.Fatal(err)
			}
			before := slices.Clone(store.state.Ownership.ManagedPackages)
			switch mode {
			case "command failure":
				rt.fail["stop-incus"] = ErrExternalEffects
			case "canceled":
				rt.fail["stop-incus"] = context.Canceled
			case "still active":
				rt.observeHook = func(obs *Observation) { obs.IncusDaemonActive = true }
			}
			request := Request{}
			plan, err = backend.Plan(ctx, request)
			if err != nil {
				t.Fatal(err)
			}
			out, err := backend.Uninstall(ctx, request, bind(plan, PhaseUninstall))
			if !errors.Is(err, ErrExternalEffects) || out.Disposition != "partial" ||
				!store.state.Ownership.IncusServiceByANAS || !store.state.Ownership.PackagesInstalledByANAS ||
				!slices.Equal(before, store.state.Ownership.ManagedPackages) || len(rt.removedByCall) != 0 ||
				unrecoveredPendingIntent(store.state) != "uninstall.packages" {
				t.Fatal("unconfirmed stop deleted packages or discarded uncertain ownership", err)
			}
		})
	}
}

func TestForeignServicesAndRetainedResourcesNeverStopDaemon(t *testing.T) {
	for _, mode := range []string{"externally started", "retained resource"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			store, rt := &memoryStore{}, newFakeRuntime(t)
			backend := newBackendForTest(store, rt)
			plan, err := backend.Plan(ctx, Request{})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := backend.Install(ctx, Request{}, bind(plan, PhaseInstall)); err != nil {
				t.Fatal(err)
			}
			request := Request{}
			if mode == "externally started" {
				store.state.Ownership.IncusServiceByANAS = false
			}
			if mode == "retained resource" {
				rt.fail["check-uninstall-resources"] = ErrBlocked
			}
			plan, err = backend.Plan(ctx, request)
			if err != nil {
				t.Fatal(err)
			}
			_, err = backend.Uninstall(ctx, request, bind(plan, PhaseUninstall))
			if !errors.Is(err, ErrBlocked) ||
				countCalls(rt.calls, "stop-incus") != 0 || len(rt.removedByCall) != 0 || !rt.obs.IncusDaemonActive || len(store.state.Intents) != 2 {
				t.Fatal("foreign daemon or retained data acquired stop/removal permission", err)
			}
		})
	}
}

type cancelAfterOwnedDaemonStop struct {
	*fakeRuntime
	cancel context.CancelFunc
}

func (r cancelAfterOwnedDaemonStop) StopIncus(ctx context.Context) error {
	if err := r.fakeRuntime.StopIncus(ctx); err != nil {
		return err
	}
	r.cancel()
	return nil
}

func TestCancellationAfterDaemonStopCannotBeginPackageDeletion(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	store, rt := &memoryStore{}, newFakeRuntime(t)
	backend := newBackendForTest(store, cancelAfterOwnedDaemonStop{rt, cancel})
	plan, err := backend.Plan(ctx, Request{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := backend.Install(ctx, Request{}, bind(plan, PhaseInstall)); err != nil {
		t.Fatal(err)
	}
	request := Request{}
	plan, err = backend.Plan(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	_, err = backend.Uninstall(ctx, request, bind(plan, PhaseUninstall))
	if !errors.Is(err, context.Canceled) || len(rt.removedByCall) != 0 ||
		!store.state.Ownership.IncusServiceByANAS || unrecoveredPendingIntent(store.state) != "uninstall.packages" {
		t.Fatal("cancellation after stopped-service readback entered package deletion or lost the effect", err)
	}
}
