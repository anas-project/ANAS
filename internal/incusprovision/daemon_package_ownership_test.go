package incusprovision

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/anas-project/ANAS/internal/incushost"
)

func TestMissingHelpersCannotAdoptPreexistingDaemon(t *testing.T) {
	rows, err := incushost.Recipes()
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range rows {
		for _, active := range []bool{false, true} {
			name := row.ID + "/inactive"
			if active {
				name = row.ID + "/active"
			}
			t.Run(name, func(t *testing.T) {
				ctx := context.Background()
				store, rt := &memoryStore{}, newFakeRuntime(t)
				rt.obs.Preflight.Recipe = &row
				daemon := daemonPackage(row)
				rt.obs.ExistingPackages = []string{daemon}
				rt.obs.InstalledPackages = []string{daemon}
				rt.obs.IncusDaemonActive = active
				backend := newBackendForTest(store, rt)
				plan, err := backend.Plan(ctx, Request{})
				if err != nil {
					t.Fatal(err)
				}
				out, err := backend.Install(ctx, Request{}, bind(plan, PhaseInstall))
				if !errors.Is(err, ErrBlocked) || out.Disposition != "blocked" || !store.state.Ownership.ExternalDaemonPreserved ||
					store.state.Ownership.PackagesInstalledByANAS || store.state.Ownership.IncusServiceByANAS ||
					len(store.state.Ownership.ManagedPackages) != 0 || len(store.state.Intents) != 0 || len(rt.calls) != 0 || rt.obs.IncusDaemonActive != active {
					t.Fatalf("installing missing helpers adopted or changed an external daemon: result=%s error=%v effects=%v", out.Disposition, err, rt.calls)
				}
			})
		}
	}
}

func TestSplitDaemonInstallRecordsBaseOwnershipIndividually(t *testing.T) {
	rows, err := incushost.Recipes()
	if err != nil {
		t.Fatal(err)
	}
	seen := 0
	for _, row := range rows {
		if daemonPackage(row) != "incus-base" {
			continue
		}
		seen++
		t.Run(row.ID, func(t *testing.T) {
			ctx := context.Background()
			store, rt := &memoryStore{}, newFakeRuntime(t)
			rt.obs.Preflight.Recipe = &row
			rt.obs.ExistingPackages = []string{"btrfs-progs", "nftables"}
			rt.obs.InstalledPackages = slices.Clone(rt.obs.ExistingPackages)
			backend := newBackendForTest(store, rt)
			plan, err := backend.Plan(ctx, Request{})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := backend.Install(ctx, Request{}, bind(plan, PhaseInstall)); err != nil {
				t.Fatal(err)
			}
			want := []string{"dnsmasq-base", "incus", "incus-base", "incus-client"}
			if !slices.Equal(store.state.Ownership.ManagedPackages, want) || !slices.Equal(rt.installedByCall, want) || store.state.Ownership.ExternalDaemonPreserved {
				t.Fatal("fresh daemon dependency was not individually recorded as owned")
			}
			request := Request{}
			plan, err = backend.Plan(ctx, request)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := backend.Uninstall(ctx, request, bind(plan, PhaseUninstall)); err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(rt.removedByCall, want) || !slices.Equal(rt.obs.InstalledPackages, []string{"btrfs-progs", "nftables"}) {
				t.Fatal("explicit removal did not preserve original packages and remove the owned daemon package")
			}
		})
	}
	if seen != len(rows) {
		t.Fatal("an independently verified split daemon recipe is missing")
	}
}

type activeAfterPackageRemoval struct{ *fakeRuntime }

func (r activeAfterPackageRemoval) RemovePackages(ctx context.Context, recipe incushost.Recipe, packages []string) error {
	if err := r.fakeRuntime.RemovePackages(ctx, recipe, packages); err != nil {
		return err
	}
	r.obs.IncusDaemonActive = true
	return nil
}

func TestPackageRemovalCannotForgetAnActiveOwnedDaemon(t *testing.T) {
	ctx := context.Background()
	store, rt := &memoryStore{}, newFakeRuntime(t)
	backend := newBackendForTest(store, activeAfterPackageRemoval{rt})
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
	out, err := backend.Uninstall(ctx, request, bind(plan, PhaseUninstall))
	if !errors.Is(err, ErrExternalEffects) || out.Disposition != "partial" || !store.state.Ownership.IncusServiceByANAS ||
		!store.state.Ownership.PackagesInstalledByANAS || unrecoveredPendingIntent(store.state) != "uninstall.packages" {
		t.Fatal("absence of meta-packages falsely proved that the owned daemon had stopped", err)
	}
}

func TestAggregatePackageOwnershipCannotAuthorizeDaemonAdoption(t *testing.T) {
	for _, phase := range []Phase{PhaseInstall, PhaseConfigure, PhaseEnroll, PhaseUninstall} {
		t.Run(string(phase), func(t *testing.T) {
			ctx := context.Background()
			// A legacy aggregate boolean is not evidence that the daemon
			// package was absent before this installation. No implicit migration.
			store := &memoryStore{state: State{Schema: StateSchema, Ownership: Ownership{
				PackagesInstalledByANAS: true, IncusServiceByANAS: true,
			}}}
			rt := newFakeRuntime(t)
			rt.obs.PackageInstalled = true
			rt.obs.IncusDaemonActive = true
			backend := newBackendForTest(store, rt)
			request := Request{}
			plan, err := backend.Plan(ctx, request)
			if err != nil {
				t.Fatal(err)
			}
			switch phase {
			case PhaseInstall:
				_, err = backend.Install(ctx, request, bind(plan, phase))
			case PhaseConfigure:
				_, err = backend.Configure(ctx, request, bind(plan, phase))
			case PhaseEnroll:
				_, err = backend.Enroll(ctx, request, bind(plan, phase))
			case PhaseUninstall:
				_, err = backend.Uninstall(ctx, request, bind(plan, phase))
			}
			if !errors.Is(err, ErrBlocked) || len(rt.calls) != 0 || len(store.state.Intents) != 0 || store.bundle != nil {
				t.Fatal("legacy package boolean acquired daemon or connection authority", err)
			}
		})
	}
}

// Uninstall removes owned packages with dpkg --remove, which keeps their
// configuration files. Those leftovers are ANAS's own, so a later install on
// the same host must own the packages again instead of calling the retained
// daemon configuration foreign (found on finance.hlong.wang, 2026-10-11).
func TestReinstallAfterUninstallOwnsRetainedPackageConfiguration(t *testing.T) {
	rows, err := incushost.Recipes()
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range rows {
		t.Run(row.ID, func(t *testing.T) {
			ctx := context.Background()
			store, rt := &memoryStore{}, newFakeRuntime(t)
			rt.obs.Preflight.Recipe = &row
			rt.obs.ExistingPackages, rt.obs.InstalledPackages = []string{}, []string{}
			backend := newBackendForTest(store, rt)
			want := slices.Sorted(slices.Values(row.Packages))
			for round := 1; round <= 2; round++ {
				plan, err := backend.Plan(ctx, Request{})
				if err != nil {
					t.Fatal(err)
				}
				out, err := backend.Install(ctx, Request{}, bind(plan, PhaseInstall))
				if err != nil || out.Disposition != "installed" || store.state.Ownership.ExternalDaemonPreserved ||
					!slices.Equal(store.state.Ownership.ManagedPackages, want) || len(store.state.Ownership.RemovedPackages) != 0 {
					t.Fatalf("install round %d: result=%s error=%v ownership=%+v", round, out.Disposition, err, store.state.Ownership)
				}
				plan, err = backend.Plan(ctx, Request{})
				if err != nil {
					t.Fatal(err)
				}
				if _, err := backend.Uninstall(ctx, Request{}, bind(plan, PhaseUninstall)); err != nil {
					t.Fatalf("uninstall round %d: %v", round, err)
				}
				if len(rt.obs.InstalledPackages) != 0 || !slices.Equal(rt.obs.ExistingPackages, want) ||
					!slices.Equal(store.state.Ownership.RemovedPackages, want) || len(store.state.Ownership.ManagedPackages) != 0 {
					t.Fatalf("uninstall round %d did not record its retained packages: %+v", round, store.state.Ownership)
				}
			}
		})
	}
}

// Retained configuration ANAS has no removal record for is still foreign.
func TestRetainedDaemonConfigurationOfUnknownOriginIsNotAdopted(t *testing.T) {
	rows, err := incushost.Recipes()
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range rows {
		t.Run(row.ID, func(t *testing.T) {
			ctx := context.Background()
			store, rt := &memoryStore{}, newFakeRuntime(t)
			rt.obs.Preflight.Recipe = &row
			rt.obs.ExistingPackages, rt.obs.InstalledPackages = []string{daemonPackage(row)}, []string{}
			// A removal record for other packages does not cover the daemon.
			store.state.Ownership.RemovedPackages = slices.DeleteFunc(slices.Clone(row.Packages), func(name string) bool { return name == daemonPackage(row) })
			backend := newBackendForTest(store, rt)
			plan, err := backend.Plan(ctx, Request{})
			if err != nil {
				t.Fatal(err)
			}
			out, err := backend.Install(ctx, Request{}, bind(plan, PhaseInstall))
			if !errors.Is(err, ErrBlocked) || out.Disposition != "blocked" || !store.state.Ownership.ExternalDaemonPreserved || len(rt.calls) != 0 {
				t.Fatalf("retained configuration of unknown origin was adopted: result=%s error=%v effects=%v", out.Disposition, err, rt.calls)
			}
		})
	}
}
