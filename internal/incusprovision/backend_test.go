package incusprovision

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/anas-project/ANAS/internal/incushost"
)

type memoryStore struct {
	state      State
	bundle     *ConnectionBundle
	removed    bool
	locked     bool
	saves      int
	failSaveAt int
}

func (s *memoryStore) Lock(context.Context) (stateLock, error) {
	if s.locked {
		return nil, ErrUnsafeState
	}
	s.locked = true
	return memoryLock{s: s}, nil
}

type memoryLock struct{ s *memoryStore }

func (l memoryLock) Unlock() error {
	l.s.locked = false
	return nil
}

func (s *memoryStore) Load(context.Context) (State, error) {
	if s.state.Schema == "" {
		s.state.Schema = StateSchema
	}
	return s.state, nil
}

func (s *memoryStore) Save(_ context.Context, state State) error {
	s.saves++
	if s.failSaveAt > 0 && s.saves == s.failSaveAt {
		return errors.New("save failed")
	}
	s.state = state
	return nil
}

func (s *memoryStore) ReadBundle(context.Context) (ConnectionBundle, error) {
	if s.bundle == nil {
		return ConnectionBundle{}, ErrUnsafeState
	}
	return *s.bundle, nil
}

func (s *memoryStore) WriteBundle(_ context.Context, bundle ConnectionBundle) error {
	copy := bundle
	s.bundle = &copy
	return nil
}

func (s *memoryStore) RemoveBundle(context.Context) error {
	s.bundle = nil
	s.removed = true
	return nil
}

type fakeRuntime struct {
	obs             Observation
	fail            map[string]error
	calls           []string
	guests          int
	serverPEM       string
	trustHook       func(Credential) error
	observeHook     func(*Observation)
	installedByCall []string
	speedupByCall   []bool
	removedByCall   []string
}

func newFakeRuntime(t *testing.T) *fakeRuntime {
	t.Helper()
	rows, err := incushost.Recipes()
	if err != nil {
		t.Fatal(err)
	}
	report := incushost.Report{Schema: incushost.PreflightSchema, Facts: incushost.Facts{OS: "linux", Architecture: "amd64"}, Interface: "incus_container", DistributionMatched: true, Recipe: &rows[0], Disposition: "disabled", ManualGuide: "fixture"}
	return &fakeRuntime{obs: Observation{Preflight: report, ExternalCIDRs: []string{"192.168.1.0/24"}, ControlInterfaceName: "br-anas-ctrl", ControlInterfaceIndex: 7}, fail: map[string]error{}, serverPEM: fixtureCertPEM}
}

func (f *fakeRuntime) record(step string) error {
	f.calls = append(f.calls, step)
	if err := f.fail[step]; err != nil {
		return err
	}
	return nil
}

func (f *fakeRuntime) Observe(context.Context, Request, State) (Observation, error) {
	if f.observeHook != nil {
		f.observeHook(&f.obs)
	}
	obs := f.obs
	// Older fixtures choose all-installed/none with the boolean. Real runtime
	// observations always include both exact lists; per-package cases below
	// supply their own lists instead of using this fixture shorthand.
	if obs.ExistingPackages == nil {
		obs.ExistingPackages = []string{}
		if obs.PackageInstalled {
			obs.ExistingPackages = slices.Clone(obs.Preflight.Recipe.Packages)
		}
	}
	if obs.InstalledPackages == nil {
		obs.InstalledPackages = []string{}
		if obs.PackageInstalled {
			obs.InstalledPackages = slices.Clone(obs.Preflight.Recipe.Packages)
		}
	}
	slices.Sort(obs.ExistingPackages)
	slices.Sort(obs.InstalledPackages)
	return obs, nil
}
func (f *fakeRuntime) InstallPackages(_ context.Context, recipe incushost.Recipe, packages []string, chineseSpeedup bool) error {
	if err := f.record("install-packages"); err != nil {
		return err
	}
	f.installedByCall = append(f.installedByCall, packages...)
	f.speedupByCall = append(f.speedupByCall, chineseSpeedup)
	f.obs.ExistingPackages = append(f.obs.ExistingPackages, packages...)
	f.obs.InstalledPackages = append(f.obs.InstalledPackages, packages...)
	slices.Sort(f.obs.ExistingPackages)
	f.obs.ExistingPackages = slices.Compact(f.obs.ExistingPackages)
	slices.Sort(f.obs.InstalledPackages)
	f.obs.InstalledPackages = slices.Compact(f.obs.InstalledPackages)
	f.obs.PackageInstalled = len(f.obs.InstalledPackages) == len(recipe.Packages)
	return nil
}
func (f *fakeRuntime) EnableIncus(context.Context) error {
	if err := f.record("enable-incus"); err != nil {
		return err
	}
	f.obs.IncusDaemonActive = true
	return nil
}
func (f *fakeRuntime) StopIncus(context.Context) error {
	if err := f.record("stop-incus"); err != nil {
		return err
	}
	f.obs.IncusDaemonActive = false
	return nil
}
func (f *fakeRuntime) EnsureStoragePool(context.Context, int, string) error {
	if err := f.record("storage"); err != nil {
		return err
	}
	f.obs.StoragePoolExists = true
	return nil
}
func (f *fakeRuntime) EnsureDockerControlNetwork(context.Context, ControlNetworkPlan) error {
	if err := f.record("docker-network"); err != nil {
		return err
	}
	f.obs.DockerNetworkExists = true
	f.obs.ControlSubnet = "10.77.0.0/24"
	f.obs.ControlGateway = "10.77.0.1"
	f.obs.ControlNetworkID = "docker-network-id"
	f.obs.ControlInterfaceName = "br-anas-ctrl"
	f.obs.ControlInterfaceIndex = 7
	return nil
}
func (f *fakeRuntime) ApplyControlFirewall(context.Context, ControlNetworkPlan) error {
	if err := f.record("firewall"); err != nil {
		return err
	}
	f.obs.FirewallInstalled = true
	return nil
}
func (f *fakeRuntime) ConfigureControlListener(_ context.Context, plan ControlNetworkPlan) error {
	if err := f.record("control-listener"); err != nil {
		return err
	}
	f.obs.IncusHTTPSControl = plan.Gateway == f.obs.ControlGateway
	f.obs.IncusAfterDocker = true
	return nil
}
func (f *fakeRuntime) TrustManagementCertificate(_ context.Context, credential Credential) error {
	if f.trustHook != nil {
		if err := f.trustHook(credential); err != nil {
			return err
		}
	}
	return f.record("trust")
}
func (f *fakeRuntime) ReadBackManagementCertificate(context.Context, string) (bool, error) {
	if err := f.record("trust-readback"); err != nil {
		return false, err
	}
	return true, nil
}
func (f *fakeRuntime) ReadServerCertificatePEM(context.Context) (string, error) {
	if err := f.record("server-cert"); err != nil {
		return "", err
	}
	return f.serverPEM, nil
}
func (f *fakeRuntime) VerifyManagementEndpoint(context.Context, ConnectionBundle) error {
	return f.record("verify-endpoint")
}
func (f *fakeRuntime) ListRunningManagedGuests(context.Context, Ownership) (int, error) {
	return f.guests, f.record("list-guests")
}
func (f *fakeRuntime) CheckUninstallResources(context.Context, Ownership, bool) error {
	return f.record("check-uninstall-resources")
}
func (f *fakeRuntime) RemoveControlListener(context.Context) error {
	return f.record("remove-control-listener")
}
func (f *fakeRuntime) RemoveControlFirewall(context.Context) error {
	return f.record("remove-firewall")
}
func (f *fakeRuntime) RemoveDockerControlNetwork(context.Context, string, string, string) error {
	return f.record("remove-network")
}
func (f *fakeRuntime) RemoveStoragePool(context.Context, string, string) error {
	return f.record("remove-storage")
}
func (f *fakeRuntime) RemoveManagementCertificate(context.Context, string) error {
	return f.record("remove-trust")
}
func (f *fakeRuntime) RemovePackages(_ context.Context, recipe incushost.Recipe, packages []string) error {
	if err := f.record("remove-packages"); err != nil {
		return err
	}
	f.removedByCall = append(f.removedByCall, packages...)
	f.obs.InstalledPackages = slices.DeleteFunc(f.obs.InstalledPackages, func(name string) bool { return slices.Contains(packages, name) })
	f.obs.PackageInstalled = len(f.obs.InstalledPackages) == len(recipe.Packages)
	if slices.Contains(packages, daemonPackage(recipe)) {
		f.obs.IncusDaemonActive = false
	}
	return nil
}

func bind(plan Plan, phase Phase) Binding {
	return Binding{Schema: Schema, PlanDigest: plan.Digest, Phase: phase, Destructive: true}
}

func TestInstallRecordsPartialFailureAndDoesNotReplayUnconfirmedEffects(t *testing.T) {
	ctx := context.Background()
	store := &memoryStore{state: State{Schema: StateSchema, Ownership: Ownership{PackagesInstalledByANAS: true, IncusServiceByANAS: true}}}
	rt := newFakeRuntime(t)
	rt.fail["enable-incus"] = errors.New("private-marker")
	backend := newBackendForTest(store, rt)
	plan, err := backend.Plan(ctx, Request{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := backend.Install(ctx, Request{}, bind(plan, PhaseInstall)); !errors.Is(err, ErrExternalEffects) {
		t.Fatalf("expected external effect failure: %v", err)
	}
	if !store.state.Ownership.PackagesInstalledByANAS || !hasReceipt(store.state, "install.packages", "ok") || !hasReceipt(store.state, "install.service", "failed") {
		t.Fatalf("partial receipts/ownership missing: %#v", store.state)
	}
	delete(rt.fail, "enable-incus")
	rt.obs.PackageInstalled = true
	plan, err = backend.Plan(ctx, Request{})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Disposition != "blocked" || !slices.Contains(plan.Blockers, "host_effect_recovery_required") {
		t.Fatal("fresh plan hid an uncertain effect")
	}
	if _, err := backend.Install(ctx, Request{}, bind(plan, PhaseInstall)); !errors.Is(err, ErrBlocked) {
		t.Fatalf("uncertain effect replayed without recovery: %v", err)
	}
	if countCalls(rt.calls, "install-packages") != 1 || countCalls(rt.calls, "enable-incus") != 1 {
		t.Fatal("retry repeated an unconfirmed host effect")
	}
}

func TestPlanDigestRejectsObservationDrift(t *testing.T) {
	ctx := context.Background()
	store := &memoryStore{state: State{Schema: StateSchema, Ownership: Ownership{PackagesInstalledByANAS: true, IncusServiceByANAS: true}}}
	rt := newFakeRuntime(t)
	backend := newBackendForTest(store, rt)
	plan, err := backend.Plan(ctx, Request{})
	if err != nil {
		t.Fatal(err)
	}
	rt.obs.PackageInstalled = true
	if _, err := backend.Install(ctx, Request{}, bind(plan, PhaseInstall)); !errors.Is(err, ErrDrift) {
		t.Fatalf("stale binding accepted: %v", err)
	}
}

func TestEnrollBlocksExternalDaemonBeforeTrustSideEffect(t *testing.T) {
	ctx := context.Background()
	store := &memoryStore{}
	rt := newFakeRuntime(t)
	rt.obs.PackageInstalled, rt.obs.IncusDaemonActive, rt.obs.IncusHTTPSControl, rt.obs.IncusAfterDocker = true, true, true, true
	rt.obs.StoragePoolExists, rt.obs.DockerNetworkExists, rt.obs.FirewallInstalled = true, true, true
	backend := newBackendForTest(store, rt)
	plan, err := backend.Plan(ctx, Request{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := backend.Enroll(ctx, Request{}, bind(plan, PhaseEnroll)); !errors.Is(err, ErrBlocked) {
		t.Fatalf("external daemon was not blocked before trust: %v", err)
	}
	if slices.Contains(rt.calls, "trust") || store.state.Credential != nil {
		t.Fatalf("enroll performed trust/secret side effect before external daemon block: calls=%v state=%#v", rt.calls, store.state)
	}
}

func TestUnsupportedDistributionDisablesWithoutEffects(t *testing.T) {
	ctx := context.Background()
	store := &memoryStore{}
	rt := newFakeRuntime(t)
	rt.obs.Preflight.Recipe = nil
	rt.obs.Preflight.DistributionMatched = false
	rt.obs.Preflight.Blockers = []string{"distribution_not_adapted"}
	backend := newBackendForTest(store, rt)
	plan, err := backend.Plan(ctx, Request{})
	if err != nil {
		t.Fatal(err)
	}
	result, err := backend.Install(ctx, Request{}, bind(plan, PhaseInstall))
	if err != nil || result.Disposition != "disabled" || len(rt.calls) != 0 {
		t.Fatalf("unsupported host caused effects: result=%#v err=%v calls=%v", result, err, rt.calls)
	}
}

func TestConfigureCreatesOwnedBoundedArtifactsAndStopsAfterFault(t *testing.T) {
	ctx := context.Background()
	store := &memoryStore{state: State{Schema: StateSchema, Ownership: Ownership{PackagesInstalledByANAS: true, ManagedPackages: []string{"incus", "incus-base"}, IncusServiceByANAS: true}}}
	rt := newFakeRuntime(t)
	rt.obs.PackageInstalled = true
	rt.obs.IncusDaemonActive = true
	rt.fail["firewall"] = errors.New("nft failed")
	backend := newBackendForTest(store, rt)
	plan, err := backend.Plan(ctx, Request{StorageSizeGiB: 32})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := backend.Configure(ctx, Request{StorageSizeGiB: 32}, bind(plan, PhaseConfigure)); !errors.Is(err, ErrExternalEffects) {
		t.Fatalf("expected firewall failure: %v", err)
	}
	if store.state.Ownership.StoragePool != StoragePoolName || store.state.Ownership.DockerNetwork != ControlNetworkName || store.state.Ownership.FirewallRules {
		t.Fatalf("ownership boundary wrong after partial configure: %#v", store.state.Ownership)
	}
	if slices.Contains(rt.calls, "control-listener") || store.state.Ownership.ControlListener {
		t.Fatal("Incus moved to the gateway after the firewall failed")
	}
}

func TestConfigureMovesListenerToGatewayOnlyAfterFirewall(t *testing.T) {
	ctx := context.Background()
	store := &memoryStore{state: State{Schema: StateSchema, Ownership: Ownership{PackagesInstalledByANAS: true, ManagedPackages: []string{"incus", "incus-base"}, IncusServiceByANAS: true}}}
	rt := newFakeRuntime(t)
	rt.obs.PackageInstalled, rt.obs.IncusDaemonActive = true, true
	backend := newBackendForTest(store, rt)
	plan, err := backend.Plan(ctx, Request{})
	if err != nil || !containsStep(plan.Steps, PhaseConfigure, "control-listener") {
		t.Fatalf("plan omits the control listener: %#v %v", plan.Steps, err)
	}
	result, err := backend.Configure(ctx, Request{}, bind(plan, PhaseConfigure))
	if err != nil || result.Disposition != "configured" || !store.state.Ownership.ControlListener {
		t.Fatalf("configure did not own the control listener: %#v %#v %v", result, store.state.Ownership, err)
	}
	firewall, listener := slices.Index(rt.calls, "firewall"), slices.Index(rt.calls, "control-listener")
	if firewall < 0 || listener < firewall || !hasReceipt(store.state, "configure.listener", "ok") {
		t.Fatalf("listener did not follow the firewall: %v", rt.calls)
	}
}

func TestConfigureKeepsListenerOwnershipWhenReadbackFails(t *testing.T) {
	ctx := context.Background()
	store := &memoryStore{state: State{Schema: StateSchema, Ownership: Ownership{PackagesInstalledByANAS: true, ManagedPackages: []string{"incus", "incus-base"}, IncusServiceByANAS: true}}}
	rt := newFakeRuntime(t)
	rt.obs.PackageInstalled, rt.obs.IncusDaemonActive = true, true
	rt.observeHook = func(obs *Observation) { obs.IncusAfterDocker = false }
	backend := newBackendForTest(store, rt)
	plan, err := backend.Plan(ctx, Request{})
	if err != nil {
		t.Fatal(err)
	}
	result, err := backend.Configure(ctx, Request{}, bind(plan, PhaseConfigure))
	if !errors.Is(err, ErrExternalEffects) || !slices.Contains(result.Blockers, "control_listener_readback_failed") {
		t.Fatalf("missing unit ordering was accepted: %#v %v", result, err)
	}
	if !store.state.Ownership.ControlListener {
		t.Fatal("a partial listener effect is not left for uninstall to undo")
	}
}

func TestEnrollPersistsBundleOnlyAfterTrustReadbackAndEndpointVerify(t *testing.T) {
	ctx := context.Background()
	store := &memoryStore{state: State{Schema: StateSchema, Ownership: Ownership{PackagesInstalledByANAS: true, ManagedPackages: []string{"incus", "incus-base"}, IncusServiceByANAS: true, StoragePool: StoragePoolName}}}
	rt := newFakeRuntime(t)
	rt.obs.PackageInstalled, rt.obs.IncusDaemonActive, rt.obs.IncusHTTPSControl, rt.obs.IncusAfterDocker = true, true, true, true
	rt.obs.StoragePoolExists, rt.obs.DockerNetworkExists, rt.obs.FirewallInstalled = true, true, true
	backend := newBackendForTest(store, rt)
	plan, err := backend.Plan(ctx, Request{})
	if err != nil {
		t.Fatal(err)
	}
	result, err := backend.Enroll(ctx, Request{}, bind(plan, PhaseEnroll))
	if err != nil || !result.ConnectionReady || result.ComputeReady || store.bundle == nil || store.state.Credential == nil {
		t.Fatalf("enroll failed: result=%#v state=%#v err=%v", result, store.state, err)
	}
	if store.bundle.Endpoint != "https://10.77.0.1:8443" || store.bundle.AdminPrivateKeyPEM == "" {
		t.Fatal("connection bundle not derived from the control listener and credential")
	}
	if store.bundle.Architecture != "amd64" || store.bundle.StoragePool != StoragePoolName {
		t.Fatal("bundle omitted its observed target architecture or owned storage pool")
	}
	if !slices.ContainsFunc(result.Unsupported, func(u UnsupportedOutput) bool { return u.Feature == "image_import" }) {
		t.Fatal("image import boundary not reported")
	}
}

func TestOfficialPreflightDiagnosticBlockersDoNotBlockInstall(t *testing.T) {
	ctx := context.Background()
	store := &memoryStore{}
	rt := newFakeRuntime(t)
	rt.obs.Preflight.Blockers = []string{"host_actions_not_installed", "package_origin_unverified", "daemon_compatibility_unverified", "storage_network_unverified"}
	backend := newBackendForTest(store, rt)
	plan, err := backend.Plan(ctx, Request{})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Blockers) != 0 || !containsStep(plan.Steps, PhaseInstall, "packages") {
		t.Fatalf("diagnostic-only blockers incorrectly blocked install plan: %#v", plan)
	}
	if _, err := backend.Install(ctx, Request{}, bind(plan, PhaseInstall)); err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(rt.calls, "install-packages") {
		t.Fatalf("install did not run with diagnostic blockers: %v", rt.calls)
	}
}

func TestInstallDoesNotRecordOwnershipBeforePackageReadback(t *testing.T) {
	ctx := context.Background()
	store := &memoryStore{}
	rt := newFakeRuntime(t)
	backend := newBackendForTest(store, rt)
	plan, err := backend.Plan(ctx, Request{})
	if err != nil {
		t.Fatal(err)
	}
	rt.observeHook = func(obs *Observation) { obs.PackageInstalled = false }
	if _, err := backend.Install(ctx, Request{}, bind(plan, PhaseInstall)); !errors.Is(err, ErrExternalEffects) {
		t.Fatalf("missing package readback did not fail closed: %v", err)
	}
	if store.state.Ownership.PackagesInstalledByANAS || hasReceipt(store.state, "install.packages", "ok") {
		t.Fatalf("package ownership/receipt recorded before readback: %#v", store.state)
	}
}

func TestPublicJSONRedactsCredentialBundleAndEndpoint(t *testing.T) {
	ctx := context.Background()
	bundle := &ConnectionBundle{
		Schema: BundleSchema, Endpoint: "https://10.77.0.1:8443", ServerCertificatePEM: fixtureCertPEM, AdminCertificatePEM: fixtureCertPEM,
		AdminPrivateKeyPEM: "-----BEGIN EC PRIVATE KEY-----\nprivate\n-----END EC PRIVATE KEY-----\n", ControlNetwork: ControlNetworkName,
	}
	store := &memoryStore{bundle: bundle, state: State{Schema: StateSchema, Ownership: Ownership{ID: "anas-incus-owned", ConnectionBundle: true, ManagementTrust: strings.Repeat("a", 64)}, Credential: &Credential{
		Name: ManagementCertName, Fingerprint: strings.Repeat("a", 64), Certificate: fixtureCertPEM, PrivateKey: "-----BEGIN EC PRIVATE KEY-----\nprivate\n-----END EC PRIVATE KEY-----\n",
	}, Bundle: bundle}}
	rt := newFakeRuntime(t)
	backend := newBackendForTest(store, rt)
	inspect, err := backend.Inspect(ctx, Request{})
	if err != nil {
		t.Fatal(err)
	}
	publicObjects := []any{inspect, inspect.Plan, inspect.State, ApplyResult{Schema: Schema, Phase: PhaseEnroll, Disposition: "connection_ready", ComputeReady: false, ConnectionReady: true}}
	for _, object := range publicObjects {
		body, err := json.Marshal(object)
		if err != nil {
			t.Fatal(err)
		}
		text := string(body)
		for _, forbidden := range []string{"BEGIN CERTIFICATE", "PRIVATE KEY", "https://10.77.0.1", "server_certificate_pem", "admin_private_key_pem", "endpoint"} {
			if strings.Contains(text, forbidden) {
				t.Fatalf("public JSON leaked %q: %s", forbidden, text)
			}
		}
	}
	if strings.Contains(fmt.Sprintf("%#v", *store.state.Credential), "PRIVATE KEY") || strings.Contains(fmt.Sprintf("%#v", *store.bundle), "BEGIN CERTIFICATE") {
		t.Fatal("String/GoString redaction leaked sensitive material")
	}
}

func TestCredentialDurableBeforeTrustRegistration(t *testing.T) {
	ctx := context.Background()
	store := &memoryStore{state: State{Schema: StateSchema, Ownership: Ownership{PackagesInstalledByANAS: true, ManagedPackages: []string{"incus", "incus-base"}, IncusServiceByANAS: true, StoragePool: StoragePoolName}}}
	rt := newFakeRuntime(t)
	rt.obs.PackageInstalled, rt.obs.IncusDaemonActive, rt.obs.IncusHTTPSControl, rt.obs.IncusAfterDocker = true, true, true, true
	rt.obs.StoragePoolExists, rt.obs.DockerNetworkExists, rt.obs.FirewallInstalled = true, true, true
	rt.trustHook = func(credential Credential) error {
		if store.state.Credential == nil || store.state.Credential.Fingerprint != credential.Fingerprint || store.state.Credential.PrivateKey == "" {
			return errors.New("credential not persisted before trust")
		}
		return nil
	}
	backend := newBackendForTest(store, rt)
	plan, err := backend.Plan(ctx, Request{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := backend.Enroll(ctx, Request{}, bind(plan, PhaseEnroll)); err != nil {
		t.Fatal(err)
	}
}

func TestSaveFailureIsJoinedWithEffectFailure(t *testing.T) {
	ctx := context.Background()
	store := &memoryStore{failSaveAt: 3}
	rt := newFakeRuntime(t)
	rt.fail["install-packages"] = errors.New("effect failed")
	backend := newBackendForTest(store, rt)
	plan, err := backend.Plan(ctx, Request{})
	if err != nil {
		t.Fatal(err)
	}
	_, err = backend.Install(ctx, Request{}, bind(plan, PhaseInstall))
	if !errors.Is(err, ErrExternalEffects) || !strings.Contains(err.Error(), "save failed") {
		t.Fatalf("effect and save failures were not both returned: %v", err)
	}
}

func TestPersistenceCleanupContextIsBoundedAndReleased(t *testing.T) {
	var observed context.Context
	want := errors.New("write failed")
	err := persistWithCleanupContext(func(ctx context.Context) error {
		observed = ctx
		if _, ok := ctx.Deadline(); !ok {
			t.Fatal("cleanup write is not bounded")
		}
		if ctx.Err() != nil {
			t.Fatal("cleanup cancelled before write")
		}
		return want
	})
	if !errors.Is(err, want) || !errors.Is(observed.Err(), context.Canceled) {
		t.Fatal("cleanup lost write error or leaked its cancellation resources")
	}
}

func TestSecondBackendLockContentionBlocksMutation(t *testing.T) {
	ctx := context.Background()
	store := &memoryStore{locked: true}
	rt := newFakeRuntime(t)
	backend := newBackendForTest(store, rt)
	plan, err := backend.Plan(ctx, Request{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := backend.Install(ctx, Request{}, bind(plan, PhaseInstall)); !errors.Is(err, ErrUnsafeState) {
		t.Fatalf("lock contention did not block mutation: %v", err)
	}
}

func TestPendingIntentWithoutReceiptFailsClosedWithRecoveryBlocker(t *testing.T) {
	ctx := context.Background()
	store := &memoryStore{state: State{Schema: StateSchema, Ownership: Ownership{ID: "anas-incus-owned"}, Intents: []EffectIntent{{
		ID: "intent-1", OwnershipID: "anas-incus-owned", Step: "configure.docker-network", Phase: PhaseConfigure, Digest: "digest", Status: "pending",
	}}}}
	rt := newFakeRuntime(t)
	backend := newBackendForTest(store, rt)
	plan, err := backend.Plan(ctx, Request{})
	if err != nil {
		t.Fatal(err)
	}
	result, err := backend.Configure(ctx, Request{}, bind(plan, PhaseConfigure))
	if !errors.Is(err, ErrBlocked) || !slices.Contains(result.Blockers, "pending_intent_requires_operator_recovery:configure.docker-network") {
		t.Fatalf("pending intent did not fail closed with recovery blocker: result=%#v err=%v", result, err)
	}
	if len(rt.calls) != 0 {
		t.Fatalf("effects ran with unrecovered pending intent: %v", rt.calls)
	}
}

func TestControlPlansReusePersistedOwnedSubnet(t *testing.T) {
	state := State{Ownership: Ownership{ID: "anas-incus-owned", ControlSubnet: "10.77.0.0/24", ControlGateway: "10.77.0.1", ControlBridge: "br-anas-ctrl", ControlInterfaceName: "br-anas-ctrl", ControlInterfaceIndex: 9}}
	obs := Observation{ExternalCIDRs: []string{"10.77.0.0/24", "10.78.0.0/24"}}
	network, err := controlPlans(Request{}, obs, state)
	if err != nil {
		t.Fatal(err)
	}
	if network.Subnet != "10.77.0.0/24" || network.Gateway != "10.77.0.1" || network.Bridge != "br-anas-ctrl" {
		t.Fatalf("persisted network identity was not reused: network=%#v", network)
	}
}

func TestUninstallRemovesOwnedArtifactsAndPreservesExternalDaemonPackages(t *testing.T) {
	ctx := context.Background()
	store := &memoryStore{state: State{Schema: StateSchema, Ownership: Ownership{
		PackagesInstalledByANAS: true, ExternalDaemonPreserved: true, StoragePool: StoragePoolName, DockerNetwork: ControlNetworkName,
		FirewallRules: true, ControlListener: true, ManagementTrust: "abcd", ConnectionBundle: true,
	}, Bundle: &ConnectionBundle{Schema: BundleSchema}}}
	rt := newFakeRuntime(t)
	backend := newBackendForTest(store, rt)
	plan, err := backend.Plan(ctx, Request{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := backend.Uninstall(ctx, Request{}, bind(plan, PhaseUninstall)); err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"remove-packages"} {
		if slices.Contains(rt.calls, forbidden) {
			t.Fatalf("uninstall touched a preserved external daemon's packages via %s: %v", forbidden, rt.calls)
		}
	}
	for _, required := range []string{"remove-trust", "remove-control-listener", "remove-firewall", "remove-network", "remove-storage"} {
		if !slices.Contains(rt.calls, required) {
			t.Fatalf("missing owned cleanup %s: %v", required, rt.calls)
		}
	}
	if slices.Index(rt.calls, "remove-control-listener") > slices.Index(rt.calls, "remove-firewall") {
		t.Fatalf("the gateway listener outlived the firewall confining it: %v", rt.calls)
	}
}

func TestUninstallBlocksRunningManagedGuests(t *testing.T) {
	ctx := context.Background()
	store := &memoryStore{state: State{Schema: StateSchema, Ownership: Ownership{DockerNetwork: ControlNetworkName}}}
	rt := newFakeRuntime(t)
	rt.guests = 1
	backend := newBackendForTest(store, rt)
	plan, err := backend.Plan(ctx, Request{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := backend.Uninstall(ctx, Request{}, bind(plan, PhaseUninstall)); !errors.Is(err, ErrBlocked) {
		t.Fatalf("running guests not blocked: %v", err)
	}
	if slices.Contains(rt.calls, "remove-network") {
		t.Fatal("removed network with running guests")
	}
}

func hasReceipt(state State, step, status string) bool {
	return slices.ContainsFunc(state.Receipts, func(r Receipt) bool { return r.Step == step && r.Status == status })
}

func countCalls(calls []string, want string) int {
	count := 0
	for _, call := range calls {
		if call == want {
			count++
		}
	}
	return count
}

const fixtureCertPEM = `-----BEGIN CERTIFICATE-----
MIIBjTCCATOgAwIBAgIQB7yW+Vd0e0C7t3QG4G8eKDAKBggqhkjOPQQDAjAhMR8w
HQYDVQQDExZhbmFzLWZpeHR1cmUtaW5jdXMtY2EwHhcNMjYwMTAxMDAwMDAwWhcN
MzYwMTAxMDAwMDAwWjAhMR8wHQYDVQQDExZhbmFzLWZpeHR1cmUtaW5jdXMtY2Ew
WTATBgcqhkjOPQIBBggqhkjOPQMBBwNCAAS4Rkde7N+xmczd5h2Ru0XGfGLnXQ8G
DSVpF5AwXcJ0jQezzq9/A63MmYSmCyE5bZgkqCPowcxuJ+5iFa3fuc3Yo0IwQDAO
BgNVHQ8BAf8EBAMCB4AwDwYDVR0TAQH/BAUwAwEB/zAdBgNVHQ4EFgQUDmY8r86R
I2wcvk5mWW5Vmh7AA1owCgYIKoZIzj0EAwIDSAAwRQIhAK4L1kUbYdH3y8clMSlC
3y1fNOYzgvl85tNvbbFtUtB8AiAG9sttWqwm4dpd2PaQYkI8S9f9v9JYd0wMM8Jx
Rsr3rQ==
-----END CERTIFICATE-----
`
