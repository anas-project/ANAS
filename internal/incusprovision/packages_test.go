package incusprovision

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
)

func TestPackageQueryFormatsRespectFixedCommandArgumentBoundary(t *testing.T) {
	for _, item := range []struct {
		format string
		tabs   int
	}{
		{packageObservationFormat, 2},
		{packageRemovalFormat, 1},
	} {
		if strings.ContainsAny(item.format, "\x00\r\n\t") || !strings.HasPrefix(item.format, "-f=${binary:Package}") || !strings.HasSuffix(item.format, `\n`) || strings.Count(item.format, `\t`) != item.tabs {
			t.Fatal("fixed package-query argv must use dpkg format escapes, not control bytes")
		}
	}
}

func TestInstallTracksOnlyPackagesAbsentBeforeEffect(t *testing.T) {
	ctx := context.Background()
	store := &memoryStore{}
	rt := newFakeRuntime(t)
	rt.obs.ExistingPackages = []string{"btrfs-progs", "nftables"}
	rt.obs.InstalledPackages = []string{"btrfs-progs", "nftables"}
	backend := newBackendForTest(store, rt)
	plan, err := backend.Plan(ctx, Request{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := backend.Install(ctx, Request{}, bind(plan, PhaseInstall)); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(store.state.Ownership.ManagedPackages, []string{"dnsmasq-base", "incus", "incus-base", "incus-client"}) {
		t.Fatal("install did not preserve per-package pre-existing ownership")
	}
	if !slices.Equal(rt.installedByCall, []string{"dnsmasq-base", "incus", "incus-base", "incus-client"}) {
		t.Fatal("install unnecessarily requested pre-existing packages")
	}
	request := Request{RemovePackages: true}
	plan, err = backend.Plan(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = backend.Uninstall(ctx, request, bind(plan, PhaseUninstall)); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(rt.removedByCall, []string{"dnsmasq-base", "incus", "incus-base", "incus-client"}) || !slices.Equal(rt.obs.InstalledPackages, []string{"btrfs-progs", "nftables"}) {
		t.Fatal("uninstall removed unowned host packages")
	}
}

func TestInstallAndUninstallPreservePreexistingBridgeHelper(t *testing.T) {
	ctx := context.Background()
	store, rt := &memoryStore{}, newFakeRuntime(t)
	rt.obs.ExistingPackages, rt.obs.InstalledPackages = []string{"dnsmasq-base"}, []string{"dnsmasq-base"}
	backend := newBackendForTest(store, rt)
	plan, err := backend.Plan(ctx, Request{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := backend.Install(ctx, Request{}, bind(plan, PhaseInstall)); err != nil {
		t.Fatal(err)
	}
	if slices.Contains(store.state.Ownership.ManagedPackages, "dnsmasq-base") || slices.Contains(rt.installedByCall, "dnsmasq-base") {
		t.Fatal("existing bridge helper was adopted")
	}
	request := Request{RemovePackages: true}
	plan, err = backend.Plan(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := backend.Uninstall(ctx, request, bind(plan, PhaseUninstall)); err != nil {
		t.Fatal(err)
	}
	if slices.Contains(rt.removedByCall, "dnsmasq-base") || !slices.Contains(rt.obs.InstalledPackages, "dnsmasq-base") {
		t.Fatal("preexisting bridge helper was removed")
	}
}

func TestUninstallLegacyPackageOwnershipFailsBeforeAnyEffect(t *testing.T) {
	ctx := context.Background()
	store := &memoryStore{state: State{Schema: StateSchema, Ownership: Ownership{ID: "owned", PackagesInstalledByANAS: true, IncusServiceByANAS: true, RelayService: true}}}
	rt := newFakeRuntime(t)
	rt.obs.PackageInstalled = true
	rt.obs.ExistingPackages = slices.Clone(rt.obs.Preflight.Recipe.Packages)
	rt.obs.InstalledPackages = slices.Clone(rt.obs.ExistingPackages)
	backend := newBackendForTest(store, rt)
	request := Request{RemovePackages: true}
	plan, err := backend.Plan(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := backend.Uninstall(ctx, request, bind(plan, PhaseUninstall)); !errors.Is(err, ErrBlocked) || len(rt.calls) != 0 {
		t.Fatalf("unproven legacy ownership removed host artifacts: err=%v calls=%v", err, rt.calls)
	}
}

func TestPackageObservationDistinguishesMissingFromBrokenOrPartiallyInstalled(t *testing.T) {
	for _, tc := range []struct {
		body           string
		code           int
		present, ready bool
	}{
		{"", 1, false, false}, {"incus\tinstalled\tok\n", 0, true, true},
		{"incus:amd64\tinstalled\tok\n", 0, true, true}, {"incus\tconfig-files\tok\n", 0, true, false},
		{"incus\tnot-installed\tok\n", 0, false, false},
	} {
		present, ready, err := parsePackageObservation([]byte(tc.body), tc.code, "incus", "amd64")
		if err != nil || present != tc.present || ready != tc.ready {
			t.Fatal("valid package inventory rejected", err)
		}
	}
	for _, tc := range []struct {
		body string
		code int
	}{
		{"", 0}, {"", 2}, {"private-marker", 1}, {"incus\tinstalled\tok\n", 1},
		{"incus:arm64\tinstalled\tok\n", 0}, {"other\tinstalled\tok\n", 0},
		{"incus\tinstalled\treinstreq\n", 0}, {"incus\thalf-configured\tok\n", 0},
		{"incus\tunpacked\tok\n", 0}, {"incus\tinstalled\tok", 0},
		{"incus\tinstalled\tok\nincus\tinstalled\tok\n", 0}, {strings.Repeat("x", 4097), 0},
	} {
		if _, _, err := parsePackageObservation([]byte(tc.body), tc.code, "incus", "amd64"); err == nil || strings.Contains(err.Error(), "private-marker") {
			t.Fatal("unconfirmed package observation accepted or leaked")
		}
	}
}
