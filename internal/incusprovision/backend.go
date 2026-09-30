package incusprovision

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net/netip"
	"slices"
	"time"

	"github.com/anas-project/ANAS/internal/incushost"
)

type runtimeOps interface {
	Observe(context.Context, Request, State) (Observation, error)
	InstallPackages(context.Context, incushost.Recipe, []string, bool) error
	EnableIncus(context.Context) error
	StopIncus(context.Context) error
	EnsureStoragePool(context.Context, int, string) error
	EnsureDockerControlNetwork(context.Context, ControlNetworkPlan) error
	ApplyControlFirewall(context.Context, ControlNetworkPlan) error
	ConfigureControlListener(context.Context, ControlNetworkPlan) error
	TrustManagementCertificate(context.Context, Credential) error
	ReadBackManagementCertificate(context.Context, string) (bool, error)
	ReadServerCertificatePEM(context.Context) (string, error)
	VerifyManagementEndpoint(context.Context, ConnectionBundle) error
	ListRunningManagedGuests(context.Context, Ownership) (int, error)
	CheckUninstallResources(context.Context, Ownership, bool) error
	RemoveControlListener(context.Context) error
	RemoveControlFirewall(context.Context) error
	RemoveDockerControlNetwork(context.Context, string, string, string) error
	RemoveStoragePool(context.Context, string, string) error
	RemoveManagementCertificate(context.Context, string) error
	RemovePackages(context.Context, incushost.Recipe, []string) error
}

type Backend struct {
	store stateStore
	rt    runtimeOps
}

func NewLocalBackend() *Backend {
	return &Backend{store: newFileStateStore(), rt: newLocalRuntime()}
}

func newBackendForTest(store stateStore, rt runtimeOps) *Backend {
	return &Backend{store: store, rt: rt}
}

func (b *Backend) Inspect(ctx context.Context, request Request) (InspectResult, error) {
	if b == nil || b.store == nil || b.rt == nil || ctx == nil {
		return InspectResult{}, ErrInvalid
	}
	request, err := request.normalized()
	if err != nil {
		return InspectResult{}, err
	}
	state, err := b.store.Load(ctx)
	if err != nil {
		return InspectResult{}, err
	}
	obs, err := b.rt.Observe(ctx, request, state)
	if err != nil {
		return InspectResult{}, err
	}
	plan, err := buildPlan(request, obs, state)
	if err != nil {
		return InspectResult{}, err
	}
	return InspectResult{Schema: Schema, ObservedAt: time.Now().UTC(), Observation: obs, State: state.Public(), Plan: plan}, nil
}

func (b *Backend) Plan(ctx context.Context, request Request) (Plan, error) {
	result, err := b.Inspect(ctx, request)
	return result.Plan, err
}

func (b *Backend) ReadPrivateConnectionBundle(ctx context.Context) (ConnectionBundle, error) {
	if b == nil || b.store == nil || ctx == nil {
		return ConnectionBundle{}, ErrInvalid
	}
	return b.store.ReadBundle(ctx)
}

func (b *Backend) Install(ctx context.Context, request Request, binding Binding) (ApplyResult, error) {
	return b.apply(ctx, request, PhaseInstall, binding, b.applyInstall)
}

func (b *Backend) Configure(ctx context.Context, request Request, binding Binding) (ApplyResult, error) {
	return b.apply(ctx, request, PhaseConfigure, binding, b.applyConfigure)
}

func (b *Backend) Enroll(ctx context.Context, request Request, binding Binding) (ApplyResult, error) {
	return b.apply(ctx, request, PhaseEnroll, binding, b.applyEnroll)
}

func (b *Backend) Uninstall(ctx context.Context, request Request, binding Binding) (ApplyResult, error) {
	return b.apply(ctx, request, PhaseUninstall, binding, b.applyUninstall)
}

type phaseFunc func(context.Context, Request, Plan, Observation, *State) (ApplyResult, error)

func (b *Backend) apply(ctx context.Context, request Request, phase Phase, binding Binding, fn phaseFunc) (out ApplyResult, retErr error) {
	if b == nil || b.store == nil || b.rt == nil || ctx == nil || !phase.valid() || fn == nil {
		return ApplyResult{}, ErrInvalid
	}
	// A skip plan describes no configuration/enrollment/deletion. It cannot
	// authorize those effects, even with an otherwise valid confirmation.
	if request.Skip && phase != PhaseInstall {
		return ApplyResult{}, ErrInvalid
	}
	request, err := request.normalized()
	if err != nil {
		return ApplyResult{}, err
	}
	lock, err := b.store.Lock(ctx)
	if err != nil {
		return ApplyResult{}, err
	}
	var result ApplyResult
	var applyErr error
	defer func() {
		if unlockErr := lock.Unlock(); unlockErr != nil {
			retErr = errors.Join(retErr, unlockErr)
		}
	}()
	state, err := b.store.Load(ctx)
	if err != nil {
		return ApplyResult{}, err
	}
	obs, err := b.rt.Observe(ctx, request, state)
	if err != nil {
		return ApplyResult{}, err
	}
	plan, err := buildPlan(request, obs, state)
	if err != nil {
		return ApplyResult{}, err
	}
	if err := binding.validate(phase, plan.Digest); err != nil {
		return ApplyResult{}, err
	}
	if state.Ownership.ID == "" {
		id, err := stableOwnershipID()
		if err != nil {
			return ApplyResult{}, err
		}
		state.Ownership.ID = id
		if err := b.store.Save(ctx, state); err != nil {
			return ApplyResult{}, err
		}
	}
	if pending := unrecoveredPendingIntent(state); pending != "" {
		return ApplyResult{Disposition: "blocked", Blockers: []string{"pending_intent_requires_operator_recovery:" + pending}}, ErrBlocked
	}
	result, err = fn(ctx, request, plan, obs, &state)
	saveErr := persistWithCleanupContext(func(cleanup context.Context) error { return b.store.Save(cleanup, state) })
	if saveErr != nil {
		err = errors.Join(err, saveErr)
	}
	applyErr = err
	if err != nil {
		result.Schema, result.Phase, result.PlanDigest = Schema, phase, plan.Digest
		result.Receipts = append([]Receipt{}, state.Receipts...)
		return result, applyErr
	}
	result.Schema, result.Phase, result.PlanDigest = Schema, phase, plan.Digest
	result.Receipts = append([]Receipt{}, state.Receipts...)
	return result, applyErr
}

func buildPlan(request Request, obs Observation, state State) (Plan, error) {
	if obs.Preflight.Schema == "" {
		return Plan{}, ErrInvalid
	}
	obsDigest := observationDigest(obs)
	stateDigest := state.digest()
	plan := Plan{
		Schema: Schema, Request: request, ObservationDigest: obsDigest, StateDigest: stateDigest,
		Preflight: obs.Preflight, Disposition: "disabled", ComputeReady: false,
	}
	if unrecoveredPendingIntent(state) != "" {
		plan.Disposition = "blocked"
		plan.Blockers = []string{"host_effect_recovery_required"}
		plan.Steps = []Step{}
		return finalizePlan(plan), nil
	}
	if request.Skip || obs.Preflight.Disposition == "skipped" {
		plan.Disposition = "skipped"
		plan.Blockers = append(plan.Blockers, "compute_skipped")
		plan.Steps = append(plan.Steps, Step{Phase: PhaseInstall, ID: "skip", Effect: "record compute disabled by request", Skipped: true, Reason: "skip_requested"})
		return finalizePlan(plan), nil
	}
	hardBlockers := provisioningHardBlockers(obs.Preflight.Blockers)
	if obs.Preflight.Recipe == nil || len(hardBlockers) > 0 {
		plan.Blockers = append(plan.Blockers, hardBlockers...)
		plan.Steps = append(plan.Steps, Step{Phase: PhaseInstall, ID: "unsupported", Effect: "leave compute disabled", Skipped: true, Reason: "unsupported_or_unverified_host"})
		return finalizePlan(plan), nil
	}
	for _, blocker := range obs.Preflight.Blockers {
		if !slices.Contains(hardBlockers, blocker) {
			plan.Warnings = append(plan.Warnings, "will_verify_"+blocker)
		}
	}
	if !obs.PackageInstalled {
		dependencies := "distribution archives"
		if request.ChineseSpeedup {
			dependencies = "mirrors.aliyun.com"
		}
		plan.Steps = append(plan.Steps, Step{Phase: PhaseInstall, ID: "packages", Effect: "install Incus from Zabbly lts-7.0 and dependencies from " + dependencies, Owned: true, Destructive: true})
	}
	if !obs.IncusDaemonActive {
		plan.Steps = append(plan.Steps, Step{Phase: PhaseInstall, ID: "incus-service", Effect: "enable and start fixed Incus service", Owned: !obs.IncusDaemonActive, Destructive: true})
	}
	if !obs.StoragePoolExists {
		plan.Steps = append(plan.Steps, Step{Phase: PhaseConfigure, ID: "storage-pool", Effect: "create managed btrfs loop-backed storage pool", Owned: true, Destructive: true})
	}
	if !obs.DockerNetworkExists {
		plan.Steps = append(plan.Steps, Step{Phase: PhaseConfigure, ID: "docker-control-network", Effect: "create ANAS-owned Docker control bridge", Owned: true, Destructive: true})
	}
	if !obs.FirewallInstalled {
		plan.Steps = append(plan.Steps, Step{Phase: PhaseConfigure, ID: "control-firewall", Effect: "install scoped default-deny INPUT/FORWARD rules for control bridge", Owned: true, Destructive: true})
	}
	if !obs.IncusHTTPSControl || !obs.IncusAfterDocker {
		plan.Steps = append(plan.Steps, Step{Phase: PhaseConfigure, ID: "control-listener", Effect: "order Incus after Docker and serve its HTTPS API only on the control bridge gateway", Owned: true, Destructive: true})
	}
	if !obs.ManagementTrusted {
		plan.Steps = append(plan.Steps, Step{Phase: PhaseEnroll, ID: "management-credential", Effect: "generate stable management client credential and trust it in Incus", Owned: true, Destructive: true})
	}
	if !obs.EndpointVerified {
		plan.Steps = append(plan.Steps, Step{Phase: PhaseEnroll, ID: "connection-bundle", Effect: "verify the control bridge connection and persist root-only connection bundle", Owned: true, Destructive: true})
	}
	if state.Bundle != nil && obs.EndpointVerified {
		plan.Disposition, plan.ComputeReady = "connection_ready", false
		plan.Warnings = append(plan.Warnings, "consumer_bridge_reachability_unverified")
	} else {
		plan.Disposition = "pending"
	}
	if state.Ownership.ExternalDaemonPreserved {
		plan.Warnings = append(plan.Warnings, "preexisting Incus daemon is classified external and will be preserved on uninstall")
	}
	plan.Steps = append(plan.Steps, Step{Phase: PhaseUninstall, ID: "owned-artifacts-only", Effect: "remove only ANAS-owned trust, control listener, firewall, network, pool and ANAS-installed packages after guest preflight", Destructive: true})
	return finalizePlan(plan), nil
}

func finalizePlan(plan Plan) Plan {
	type digestPlan Plan
	copy := digestPlan(plan)
	copy.Digest = ""
	body, _ := json.Marshal(copy)
	plan.Digest = digestBytes(body)
	return plan
}

func provisioningHardBlockers(blockers []string) []string {
	hard := []string{}
	for _, blocker := range blockers {
		switch blocker {
		case "linux_required", "distribution_not_adapted", "architecture_not_adapted", "init_not_adapted", "release_identity_conflict", "kvm_not_observed":
			hard = append(hard, blocker)
		}
	}
	return hard
}

func persistWithCleanupContext(write func(context.Context) error) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return write(ctx)
}

func stableDigest(v any) string {
	body, _ := json.Marshal(v)
	return digestBytes(body)
}

func observationDigest(obs Observation) string {
	// API/network enumeration order is not topology. Docker can return the
	// same network IDs/CIDRs in a different order between plan and apply.
	// Sort copies only: preserve duplicate counts, every CIDR, immutable
	// network ID, gateway, interface binding and all other observed facts.
	// A real change still invalidates approval; this is not a drift retry.
	obs.ExternalCIDRs = slices.Clone(obs.ExternalCIDRs)
	obs.DockerCIDRs = slices.Clone(obs.DockerCIDRs)
	obs.IncusCIDRs = slices.Clone(obs.IncusCIDRs)
	slices.Sort(obs.ExternalCIDRs)
	slices.Sort(obs.DockerCIDRs)
	slices.Sort(obs.IncusCIDRs)
	return stableDigest(obs)
}

func (b *Backend) applyInstall(ctx context.Context, request Request, plan Plan, obs Observation, state *State) (ApplyResult, error) {
	if request.Skip || plan.Preflight.Recipe == nil {
		state.Disabled = true
		state.addReceipt("install.disabled", plan.Digest, "ok", "compute disabled")
		return ApplyResult{Disposition: "disabled", ComputeReady: false, Blockers: append([]string{}, plan.Blockers...)}, nil
	}
	if hard := provisioningHardBlockers(plan.Preflight.Blockers); len(hard) > 0 {
		state.Disabled = true
		state.addReceipt("install.unsupported", plan.Digest, "ok", "compute disabled on unsupported host")
		return ApplyResult{Disposition: "disabled", ComputeReady: false, Blockers: append([]string{}, hard...)}, nil
	}
	if !packageInventoryValid(obs) {
		return ApplyResult{Disposition: "blocked", Blockers: []string{"package_inventory_unverified"}}, ErrBlocked
	}
	if externalDaemonObserved(obs, state.Ownership, *plan.Preflight.Recipe) {
		state.Ownership.ExternalDaemonPreserved = true
		// Installing a missing helper must not turn preexisting daemon code or
		// retained configuration into a newly owned service. The explicit
		// remote/administrator-managed connection path remains separate.
		return ApplyResult{Disposition: "blocked", Blockers: []string{"external_daemon_service_not_modified"}}, ErrBlocked
	}
	if !obs.PackageInstalled {
		requested := missingPackages(plan.Preflight.Recipe.Packages, obs.InstalledPackages)
		newlyOwned := missingPackages(plan.Preflight.Recipe.Packages, obs.ExistingPackages)
		intent, err := b.beginEffect(ctx, state, PhaseInstall, plan.Digest, "install.packages")
		if err != nil {
			return ApplyResult{Disposition: "partial"}, err
		}
		if err := b.rt.InstallPackages(ctx, *plan.Preflight.Recipe, requested, request.ChineseSpeedup); err != nil {
			saveErr := b.finishEffect(state, intent, "failed", "package installation failed")
			return ApplyResult{Disposition: "partial"}, errors.Join(ErrExternalEffects, err, saveErr)
		}
		refreshed, err := b.rt.Observe(ctx, request, *state)
		if err != nil || !refreshed.PackageInstalled || !packageInventoryValid(refreshed) {
			saveErr := b.finishEffect(state, intent, "failed", "package installation readback failed")
			return ApplyResult{Disposition: "partial", Blockers: []string{"package_readback_failed"}}, errors.Join(ErrExternalEffects, err, saveErr)
		}
		obs = refreshed
		state.Ownership.PackagesInstalledByANAS = true
		state.Ownership.ManagedPackages = append(state.Ownership.ManagedPackages, newlyOwned...)
		slices.Sort(state.Ownership.ManagedPackages)
		state.Ownership.ManagedPackages = slices.Compact(state.Ownership.ManagedPackages)
		if err := b.finishEffect(state, intent, "ok", "packages installed"); err != nil {
			return ApplyResult{Disposition: "partial"}, err
		}
	}
	if !obs.IncusDaemonActive || (state.Ownership.PackagesInstalledByANAS && !state.Ownership.IncusServiceByANAS && !state.Ownership.ExternalDaemonPreserved) {
		if state.Ownership.ExternalDaemonPreserved && !state.Ownership.PackagesInstalledByANAS {
			return ApplyResult{Disposition: "blocked", Blockers: []string{"external_daemon_service_not_modified"}}, ErrBlocked
		}
		intent, err := b.beginEffect(ctx, state, PhaseInstall, plan.Digest, "install.service")
		if err != nil {
			return ApplyResult{Disposition: "partial"}, err
		}
		if err := b.rt.EnableIncus(ctx); err != nil {
			saveErr := b.finishEffect(state, intent, "failed", "service activation failed")
			return ApplyResult{Disposition: "partial"}, errors.Join(ErrExternalEffects, err, saveErr)
		}
		refreshed, err := b.rt.Observe(ctx, request, *state)
		if err != nil || !refreshed.IncusDaemonActive {
			saveErr := b.finishEffect(state, intent, "failed", "service activation readback failed")
			return ApplyResult{Disposition: "partial", Blockers: []string{"incus_service_readback_failed"}}, errors.Join(ErrExternalEffects, err, saveErr)
		}
		obs = refreshed
		state.Ownership.IncusServiceByANAS = true
		if err := b.finishEffect(state, intent, "ok", "service active"); err != nil {
			return ApplyResult{Disposition: "partial"}, err
		}
	}
	return ApplyResult{Disposition: "installed"}, nil
}

func (b *Backend) applyConfigure(ctx context.Context, request Request, plan Plan, obs Observation, state *State) (ApplyResult, error) {
	if plan.Preflight.Recipe == nil || len(provisioningHardBlockers(plan.Preflight.Blockers)) > 0 {
		return ApplyResult{Disposition: "disabled", Blockers: append([]string{}, provisioningHardBlockers(plan.Preflight.Blockers)...)}, ErrUnsupported
	}
	if externalDaemonObserved(obs, state.Ownership, *plan.Preflight.Recipe) {
		state.Ownership.ExternalDaemonPreserved = true
	}
	if state.Ownership.ExternalDaemonPreserved {
		return ApplyResult{Disposition: "blocked", Blockers: []string{"external_daemon_not_modified"}}, ErrBlocked
	}
	if !obs.StoragePoolExists {
		intent, err := b.beginEffect(ctx, state, PhaseConfigure, plan.Digest, "configure.storage")
		if err != nil {
			return ApplyResult{Disposition: "partial"}, err
		}
		if err := b.rt.EnsureStoragePool(ctx, request.StorageSizeGiB, state.Ownership.ID); err != nil {
			saveErr := b.finishEffect(state, intent, "failed", "storage pool creation failed")
			return ApplyResult{Disposition: "partial"}, errors.Join(ErrExternalEffects, err, saveErr)
		}
		refreshed, err := b.rt.Observe(ctx, request, *state)
		if err != nil || !refreshed.StoragePoolExists {
			saveErr := b.finishEffect(state, intent, "failed", "storage pool readback failed")
			return ApplyResult{Disposition: "partial", Blockers: []string{"storage_pool_readback_failed"}}, errors.Join(ErrExternalEffects, err, saveErr)
		}
		obs = refreshed
		state.Ownership.StoragePool = StoragePoolName
		state.Ownership.StoragePoolDriver = "btrfs"
		if err := b.finishEffect(state, intent, "ok", "storage pool owned"); err != nil {
			return ApplyResult{Disposition: "partial"}, err
		}
	}
	network, err := controlPlans(request, obs, *state)
	if err != nil {
		return ApplyResult{}, err
	}
	if !obs.DockerNetworkExists {
		intent, err := b.beginEffect(ctx, state, PhaseConfigure, plan.Digest, "configure.docker-network")
		if err != nil {
			return ApplyResult{Disposition: "partial"}, err
		}
		if err := b.rt.EnsureDockerControlNetwork(ctx, network); err != nil {
			saveErr := b.finishEffect(state, intent, "failed", "control network creation failed")
			return ApplyResult{Disposition: "partial"}, errors.Join(ErrExternalEffects, err, saveErr)
		}
		refreshed, err := b.rt.Observe(ctx, request, *state)
		if err != nil {
			return ApplyResult{Disposition: "partial"}, err
		}
		if !refreshed.DockerNetworkExists || refreshed.ControlInterfaceIndex <= 0 || refreshed.ControlInterfaceName == "" {
			return ApplyResult{Disposition: "partial", Blockers: []string{"control_network_readback_failed"}}, ErrExternalEffects
		}
		obs = refreshed
		network, err = controlPlans(request, obs, *state)
		if err != nil {
			return ApplyResult{}, err
		}
		state.Ownership.DockerNetwork = ControlNetworkName
		state.Ownership.ControlSubnet = network.Subnet
		state.Ownership.ControlGateway = network.Gateway
		state.Ownership.ControlBridge = network.Bridge
		state.Ownership.DockerNetworkID = obs.ControlNetworkID
		state.Ownership.ControlInterfaceName = obs.ControlInterfaceName
		state.Ownership.ControlInterfaceIndex = obs.ControlInterfaceIndex
		if err := b.finishEffect(state, intent, "ok", "control network owned"); err != nil {
			return ApplyResult{Disposition: "partial"}, err
		}
	}
	if !obs.FirewallInstalled {
		intent, err := b.beginEffect(ctx, state, PhaseConfigure, plan.Digest, "configure.firewall")
		if err != nil {
			return ApplyResult{Disposition: "partial"}, err
		}
		if err := b.rt.ApplyControlFirewall(ctx, network); err != nil {
			saveErr := b.finishEffect(state, intent, "failed", "control firewall failed")
			return ApplyResult{Disposition: "partial"}, errors.Join(ErrExternalEffects, err, saveErr)
		}
		state.Ownership.FirewallRules = true
		refreshed, err := b.rt.Observe(ctx, request, *state)
		if err != nil || !refreshed.FirewallInstalled {
			state.Ownership.FirewallRules = false
			saveErr := b.finishEffect(state, intent, "failed", "control firewall readback failed")
			return ApplyResult{Disposition: "partial", Blockers: []string{"control_firewall_readback_failed"}}, errors.Join(ErrExternalEffects, err, saveErr)
		}
		obs = refreshed
		if err := b.finishEffect(state, intent, "ok", "control firewall owned"); err != nil {
			return ApplyResult{Disposition: "partial"}, err
		}
	}
	// The listener moves to the gateway only after the firewall that limits
	// it to the control bridge is in place.
	if !obs.IncusHTTPSControl || !obs.IncusAfterDocker {
		intent, err := b.beginEffect(ctx, state, PhaseConfigure, plan.Digest, "configure.listener")
		if err != nil {
			return ApplyResult{Disposition: "partial"}, err
		}
		// Recorded before the effect: uninstall must undo a partial one.
		state.Ownership.ControlListener = true
		if err := b.rt.ConfigureControlListener(ctx, network); err != nil {
			saveErr := b.finishEffect(state, intent, "failed", "control listener configuration failed")
			return ApplyResult{Disposition: "partial"}, errors.Join(ErrExternalEffects, err, saveErr)
		}
		refreshed, err := b.rt.Observe(ctx, request, *state)
		if err != nil || !refreshed.IncusHTTPSControl || !refreshed.IncusAfterDocker {
			saveErr := b.finishEffect(state, intent, "failed", "control listener readback failed")
			return ApplyResult{Disposition: "partial", Blockers: []string{"control_listener_readback_failed"}}, errors.Join(ErrExternalEffects, err, saveErr)
		}
		obs = refreshed
		if err := b.finishEffect(state, intent, "ok", "incus listens on the control gateway"); err != nil {
			return ApplyResult{Disposition: "partial"}, err
		}
	}
	return ApplyResult{Disposition: "configured"}, nil
}

func (b *Backend) applyEnroll(ctx context.Context, request Request, plan Plan, obs Observation, state *State) (ApplyResult, error) {
	if plan.Preflight.Recipe == nil || len(provisioningHardBlockers(plan.Preflight.Blockers)) > 0 {
		return ApplyResult{Disposition: "disabled", Blockers: append([]string{}, provisioningHardBlockers(plan.Preflight.Blockers)...)}, ErrUnsupported
	}
	if externalDaemonObserved(obs, state.Ownership, *plan.Preflight.Recipe) {
		state.Ownership.ExternalDaemonPreserved = true
	}
	if state.Ownership.ExternalDaemonPreserved {
		return ApplyResult{Disposition: "blocked", Blockers: []string{"external_daemon_not_modified"}}, ErrBlocked
	}
	if !obs.IncusDaemonActive || !obs.IncusHTTPSControl || !obs.IncusAfterDocker || !obs.StoragePoolExists || !obs.DockerNetworkExists || !obs.FirewallInstalled {
		return ApplyResult{Disposition: "blocked", Blockers: []string{"required_network_daemon_storage_identity_checks_unverified"}}, ErrBlocked
	}
	// This is the local, observed daemon host, not the CLI client's machine.
	// The Module must not infer a remote guest architecture from its own runtime.
	if (obs.Preflight.Facts.Architecture != "amd64" && obs.Preflight.Facts.Architecture != "arm64") || state.Ownership.StoragePool != StoragePoolName {
		return ApplyResult{Disposition: "blocked", Blockers: []string{"connection_target_metadata_unverified"}}, ErrBlocked
	}
	credential := state.Credential
	if credential == nil {
		generated, err := generateCredential()
		if err != nil {
			return ApplyResult{}, err
		}
		credential = &generated
		state.Credential = credential
		if err := b.store.Save(ctx, *state); err != nil {
			return ApplyResult{Disposition: "partial"}, err
		}
	}
	if !obs.ManagementTrusted {
		intent, err := b.beginEffect(ctx, state, PhaseEnroll, plan.Digest, "enroll.trust")
		if err != nil {
			return ApplyResult{Disposition: "partial"}, err
		}
		if err := b.rt.TrustManagementCertificate(ctx, *credential); err != nil {
			saveErr := b.finishEffect(state, intent, "failed", "management trust failed")
			return ApplyResult{Disposition: "partial"}, errors.Join(ErrExternalEffects, err, saveErr)
		}
		ok, err := b.rt.ReadBackManagementCertificate(ctx, credential.Fingerprint)
		if err != nil || !ok {
			saveErr := b.finishEffect(state, intent, "failed", "management trust readback failed")
			return ApplyResult{Disposition: "partial"}, errors.Join(ErrExternalEffects, err, saveErr)
		}
		state.Ownership.ManagementTrust = credential.Fingerprint
		if err := b.finishEffect(state, intent, "ok", "management certificate trusted"); err != nil {
			return ApplyResult{Disposition: "partial"}, err
		}
	}
	serverCert, err := b.rt.ReadServerCertificatePEM(ctx)
	if err != nil {
		return ApplyResult{Disposition: "partial"}, errors.Join(ErrExternalEffects, err)
	}
	network, err := controlPlans(request, obs, *state)
	if err != nil {
		return ApplyResult{}, err
	}
	bundle := ConnectionBundle{
		Schema: BundleSchema, Endpoint: "https://" + controlListenAddress(network.Gateway),
		ServerCertificatePEM: serverCert, AdminCertificatePEM: credential.Certificate, AdminPrivateKeyPEM: credential.PrivateKey,
		ControlNetwork: ControlNetworkName, ControlSubnet: network.Subnet, ControlGateway: network.Gateway,
		ManagementFingerprint: credential.Fingerprint, Architecture: obs.Preflight.Facts.Architecture, StoragePool: state.Ownership.StoragePool,
	}
	if !sameConnectionBundle(state.Bundle, &bundle) || !obs.EndpointVerified {
		intent, err := b.beginEffect(ctx, state, PhaseEnroll, plan.Digest, "enroll.bundle")
		if err != nil {
			return ApplyResult{Disposition: "partial"}, err
		}
		if err := b.rt.VerifyManagementEndpoint(ctx, bundle); err != nil {
			saveErr := b.finishEffect(state, intent, "failed", "control connection verification failed")
			return ApplyResult{Disposition: "partial"}, errors.Join(ErrExternalEffects, err, saveErr)
		}
		if err := persistWithCleanupContext(func(cleanup context.Context) error { return b.store.WriteBundle(cleanup, bundle) }); err != nil {
			saveErr := b.finishEffect(state, intent, "failed", "bundle persistence failed")
			return ApplyResult{Disposition: "partial"}, errors.Join(err, saveErr)
		}
		state.Bundle = &bundle
		state.Ownership.ConnectionBundle = true
		if err := b.finishEffect(state, intent, "ok", "connection bundle persisted"); err != nil {
			return ApplyResult{Disposition: "partial"}, err
		}
	}
	// A previous explicit skip or uninstall is reversible only after a newly
	// confirmed enrollment has verified and persisted its management bundle.
	// This clears a historical status, not the independent compute-ready gates.
	state.Disabled = false
	return ApplyResult{Disposition: "connection_ready", ComputeReady: false, ConnectionReady: true, Unsupported: []UnsupportedOutput{
		{Feature: "consumer_bridge_reachability", Reason: "bridge-origin reachability has not been verified by this backend"},
		{Feature: "image_import", Reason: "image import is not implemented by this backend; image readiness remains unsupported"},
	}}, nil
}

func (b *Backend) applyUninstall(ctx context.Context, request Request, plan Plan, obs Observation, state *State) (ApplyResult, error) {
	// Uninstall removes exactly the packages ANAS recorded as its own; a
	// preserved external daemon or pre-existing package is never touched.
	removePackages := state.Ownership.PackagesInstalledByANAS && !state.Ownership.ExternalDaemonPreserved
	if removePackages &&
		(plan.Preflight.Recipe == nil || !validPackageSubset(*plan.Preflight.Recipe, state.Ownership.ManagedPackages) || !packageInventoryValid(obs) ||
			unownedDaemonPackage(obs, state.Ownership, *plan.Preflight.Recipe) ||
			(obs.IncusDaemonActive && !state.Ownership.IncusServiceByANAS)) {
		return ApplyResult{Disposition: "blocked", Blockers: []string{"package_ownership_unverified"}}, ErrBlocked
	}
	running, err := b.rt.ListRunningManagedGuests(ctx, state.Ownership)
	if err != nil {
		return ApplyResult{}, errors.Join(ErrExternalEffects, err)
	}
	if running > 0 {
		return ApplyResult{Disposition: "blocked", Blockers: []string{"running_managed_guests"}}, ErrBlocked
	}
	// Check stopped/frozen instances, retained storage references and attached
	// control-network endpoints before revoking any working connection. The
	// individual delete operations still recheck ownership and absence later.
	if err := b.rt.CheckUninstallResources(ctx, state.Ownership, removePackages); err != nil {
		return ApplyResult{Disposition: "blocked", Blockers: []string{"uninstall_resources_in_use_or_unverified"}}, errors.Join(ErrBlocked, err)
	}
	if err := ctx.Err(); err != nil {
		return ApplyResult{Disposition: "blocked"}, err
	}
	if state.Ownership.ConnectionBundle || state.Bundle != nil {
		intent, err := b.beginEffect(ctx, state, PhaseUninstall, plan.Digest, "uninstall.bundle")
		if err != nil {
			return ApplyResult{Disposition: "partial"}, err
		}
		if err := b.store.RemoveBundle(ctx); err != nil {
			saveErr := b.finishEffect(state, intent, "failed", "bundle removal failed")
			return ApplyResult{Disposition: "partial"}, errors.Join(err, saveErr)
		}
		state.Bundle = nil
		state.Ownership.ConnectionBundle = false
		if err := b.finishEffect(state, intent, "ok", "bundle removed"); err != nil {
			return ApplyResult{Disposition: "partial"}, err
		}
	}
	if state.Ownership.ManagementTrust != "" {
		intent, err := b.beginEffect(ctx, state, PhaseUninstall, plan.Digest, "uninstall.trust")
		if err != nil {
			return ApplyResult{Disposition: "partial"}, err
		}
		if err := b.rt.RemoveManagementCertificate(ctx, state.Ownership.ManagementTrust); err != nil {
			saveErr := b.finishEffect(state, intent, "failed", "trust removal failed")
			return ApplyResult{Disposition: "partial"}, errors.Join(ErrExternalEffects, err, saveErr)
		}
		state.Ownership.ManagementTrust = ""
		if err := b.finishEffect(state, intent, "ok", "trust removed"); err != nil {
			return ApplyResult{Disposition: "partial"}, err
		}
	}
	// The gateway listener goes before the firewall that confines it.
	if state.Ownership.ControlListener {
		intent, err := b.beginEffect(ctx, state, PhaseUninstall, plan.Digest, "uninstall.listener")
		if err != nil {
			return ApplyResult{Disposition: "partial"}, err
		}
		if err := b.rt.RemoveControlListener(ctx); err != nil {
			saveErr := b.finishEffect(state, intent, "failed", "control listener removal failed")
			return ApplyResult{Disposition: "partial"}, errors.Join(ErrExternalEffects, err, saveErr)
		}
		state.Ownership.ControlListener = false
		if err := b.finishEffect(state, intent, "ok", "control listener removed"); err != nil {
			return ApplyResult{Disposition: "partial"}, err
		}
	}
	if state.Ownership.FirewallRules {
		intent, err := b.beginEffect(ctx, state, PhaseUninstall, plan.Digest, "uninstall.firewall")
		if err != nil {
			return ApplyResult{Disposition: "partial"}, err
		}
		if err := b.rt.RemoveControlFirewall(ctx); err != nil {
			saveErr := b.finishEffect(state, intent, "failed", "firewall removal failed")
			return ApplyResult{Disposition: "partial"}, errors.Join(ErrExternalEffects, err, saveErr)
		}
		state.Ownership.FirewallRules = false
		if err := b.finishEffect(state, intent, "ok", "firewall removed"); err != nil {
			return ApplyResult{Disposition: "partial"}, err
		}
	}
	if state.Ownership.DockerNetwork != "" {
		intent, err := b.beginEffect(ctx, state, PhaseUninstall, plan.Digest, "uninstall.docker-network")
		if err != nil {
			return ApplyResult{Disposition: "partial"}, err
		}
		if err := b.rt.RemoveDockerControlNetwork(ctx, state.Ownership.DockerNetwork, state.Ownership.ID, state.Ownership.DockerNetworkID); err != nil {
			saveErr := b.finishEffect(state, intent, "failed", "network removal failed")
			return ApplyResult{Disposition: "partial"}, errors.Join(ErrExternalEffects, err, saveErr)
		}
		state.Ownership.DockerNetwork = ""
		state.Ownership.DockerNetworkID = ""
		state.Ownership.ControlSubnet = ""
		state.Ownership.ControlGateway = ""
		state.Ownership.ControlInterfaceName = ""
		state.Ownership.ControlInterfaceIndex = 0
		if err := b.finishEffect(state, intent, "ok", "network removed"); err != nil {
			return ApplyResult{Disposition: "partial"}, err
		}
	}
	if state.Ownership.StoragePool != "" {
		intent, err := b.beginEffect(ctx, state, PhaseUninstall, plan.Digest, "uninstall.storage")
		if err != nil {
			return ApplyResult{Disposition: "partial"}, err
		}
		if err := b.rt.RemoveStoragePool(ctx, state.Ownership.StoragePool, state.Ownership.ID); err != nil {
			saveErr := b.finishEffect(state, intent, "failed", "storage removal failed")
			return ApplyResult{Disposition: "partial"}, errors.Join(ErrExternalEffects, err, saveErr)
		}
		state.Ownership.StoragePool = ""
		state.Ownership.StoragePoolDriver = ""
		if err := b.finishEffect(state, intent, "ok", "storage pool removed"); err != nil {
			return ApplyResult{Disposition: "partial"}, err
		}
	}
	if removePackages && plan.Preflight.Recipe != nil {
		intent, err := b.beginEffect(ctx, state, PhaseUninstall, plan.Digest, "uninstall.packages")
		if err != nil {
			return ApplyResult{Disposition: "partial"}, err
		}
		// Package maintainer scripts are not service-exit evidence. Debian's
		// incus-base skips systemd-native stopping during removal. Stop only
		// our explicitly owned daemon, after all shared-resource preflights,
		// while its executable/unit still exist. Keep ownership until every
		// package and daemon readback below succeeds; do not retry failed effects.
		if state.Ownership.IncusServiceByANAS {
			if err := b.rt.StopIncus(ctx); err != nil {
				saveErr := b.finishEffect(state, intent, "failed", "owned daemon stop failed")
				return ApplyResult{Disposition: "partial"}, errors.Join(ErrExternalEffects, err, saveErr)
			}
			stopped, err := b.rt.Observe(ctx, request, *state)
			if err != nil || stopped.IncusDaemonActive {
				saveErr := b.finishEffect(state, intent, "failed", "owned daemon stop readback failed")
				return ApplyResult{Disposition: "partial"}, errors.Join(ErrExternalEffects, err, saveErr)
			}
			if err := ctx.Err(); err != nil {
				saveErr := b.finishEffect(state, intent, "failed", "package removal canceled after daemon stop")
				return ApplyResult{Disposition: "partial"}, errors.Join(ErrExternalEffects, err, saveErr)
			}
		}
		if err := b.rt.RemovePackages(ctx, *plan.Preflight.Recipe, slices.Clone(state.Ownership.ManagedPackages)); err != nil {
			saveErr := b.finishEffect(state, intent, "failed", "package removal failed")
			return ApplyResult{Disposition: "partial"}, errors.Join(ErrExternalEffects, err, saveErr)
		}
		refreshed, readErr := b.rt.Observe(ctx, request, *state)
		if readErr != nil || !packageInventoryValid(refreshed) ||
			(state.Ownership.IncusServiceByANAS && refreshed.IncusDaemonActive) ||
			slices.ContainsFunc(state.Ownership.ManagedPackages, func(name string) bool { return slices.Contains(refreshed.InstalledPackages, name) }) {
			saveErr := b.finishEffect(state, intent, "failed", "package removal readback failed")
			return ApplyResult{Disposition: "partial"}, errors.Join(ErrExternalEffects, readErr, saveErr)
		}
		state.Ownership.PackagesInstalledByANAS = false
		state.Ownership.ManagedPackages = nil
		state.Ownership.IncusServiceByANAS = false
		if err := b.finishEffect(state, intent, "ok", "packages removed"); err != nil {
			return ApplyResult{Disposition: "partial"}, err
		}
	} else if !removePackages {
		state.addReceipt("uninstall.packages", plan.Digest, "ok", "no ANAS-owned packages to remove")
	}
	state.Disabled = true
	return ApplyResult{Disposition: "uninstalled", ComputeReady: false}, nil
}

func generateCredential() (Credential, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return Credential{}, ErrExternalEffects
	}
	serialLimit := new(big.Int).Lsh(big.NewInt(1), 128)
	serial, err := rand.Int(rand.Reader, serialLimit)
	if err != nil {
		return Credential{}, ErrExternalEffects
	}
	now := time.Now().UTC()
	template := &x509.Certificate{
		SerialNumber: serial, Subject: pkix.Name{CommonName: ManagementCertName},
		NotBefore: now.Add(-time.Minute), NotAfter: now.AddDate(10, 0, 0),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		return Credential{}, ErrExternalEffects
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return Credential{}, ErrExternalEffects
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	sum := sha256.Sum256(der)
	return Credential{Name: ManagementCertName, Fingerprint: hex.EncodeToString(sum[:]), Certificate: string(certPEM), PrivateKey: string(keyPEM)}, nil
}

type ControlNetworkPlan struct {
	Name        string `json:"name"`
	Subnet      string `json:"subnet"`
	Gateway     string `json:"gateway"`
	Bridge      string `json:"bridge"`
	OwnershipID string `json:"ownership_id"`
}

func controlPlans(_ Request, obs Observation, state State) (ControlNetworkPlan, error) {
	if obs.DockerNetworkExists && obs.ControlSubnet != "" && obs.ControlGateway != "" {
		subnet, err := netip.ParsePrefix(obs.ControlSubnet)
		gateway, gatewayErr := netip.ParseAddr(obs.ControlGateway)
		if err != nil || gatewayErr != nil || !subnet.Contains(gateway) {
			return ControlNetworkPlan{}, ErrBlocked
		}
		return ControlNetworkPlan{Name: ControlNetworkName, Subnet: subnet.Masked().String(), Gateway: gateway.String(), Bridge: "br-anas-ctrl", OwnershipID: state.Ownership.ID}, nil
	}
	if state.Ownership.ControlSubnet != "" || state.Ownership.ControlGateway != "" {
		subnet, err := netip.ParsePrefix(state.Ownership.ControlSubnet)
		gateway, gatewayErr := netip.ParseAddr(state.Ownership.ControlGateway)
		if err != nil || gatewayErr != nil || !subnet.Contains(gateway) {
			return ControlNetworkPlan{}, ErrBlocked
		}
		bridge := state.Ownership.ControlBridge
		if bridge == "" {
			bridge = "br-anas-ctrl"
		}
		return ControlNetworkPlan{Name: ControlNetworkName, Subnet: subnet.Masked().String(), Gateway: gateway.String(), Bridge: bridge, OwnershipID: state.Ownership.ID}, nil
	}
	candidates := []netip.Prefix{
		netip.MustParsePrefix("10.77.0.0/24"),
		netip.MustParsePrefix("10.78.0.0/24"),
		netip.MustParsePrefix("172.29.77.0/24"),
	}
	for _, candidate := range candidates {
		if cidrAvailable(candidate, obs.ExternalCIDRs) {
			gateway := nthIPv4(candidate, 1)
			return ControlNetworkPlan{Name: ControlNetworkName, Subnet: candidate.String(), Gateway: gateway.String(), Bridge: "br-anas-ctrl", OwnershipID: state.Ownership.ID}, nil
		}
	}
	return ControlNetworkPlan{}, ErrBlocked
}

func (b *Backend) beginEffect(ctx context.Context, state *State, phase Phase, digest, step string) (EffectIntent, error) {
	if b == nil || state == nil || state.Ownership.ID == "" || step == "" || digest == "" {
		return EffectIntent{}, ErrInvalid
	}
	id, err := stableOwnershipID()
	if err != nil {
		return EffectIntent{}, err
	}
	now := time.Now().UTC()
	intent := EffectIntent{ID: id, OwnershipID: state.Ownership.ID, Step: step, Phase: phase, Digest: digest, Status: "pending", CreatedAt: now}
	state.Intents = append(state.Intents, intent)
	if len(state.Intents) > 256 {
		state.Intents = append([]EffectIntent{}, state.Intents[len(state.Intents)-256:]...)
	}
	if err := b.store.Save(ctx, *state); err != nil {
		return EffectIntent{}, err
	}
	return intent, nil
}

func (b *Backend) finishEffect(state *State, intent EffectIntent, status, detail string) error {
	if b == nil || state == nil || intent.ID == "" {
		return ErrInvalid
	}
	state.addReceiptForIntent(intent, status, detail)
	return persistWithCleanupContext(func(cleanup context.Context) error { return b.store.Save(cleanup, *state) })
}

func unrecoveredPendingIntent(state State) string {
	if pending := unrecoveredProvisionIntent(state); pending != "" {
		return pending
	}
	return ""
}

func unrecoveredProvisionIntent(state State) string {
	for _, intent := range state.Intents {
		// An error return is not proof of no effect: a package operation can
		// partially complete and an Incus operation can outlive this process.
		// A receipt for an earlier attempt cannot settle a different intent ID.
		if intent.Status == "pending" || intent.Status == "failed" {
			return intent.Step
		}
	}
	return ""
}

func cidrAvailable(candidate netip.Prefix, used []string) bool {
	for _, text := range used {
		prefix, err := netip.ParsePrefix(text)
		if err != nil {
			return false
		}
		if prefixesOverlap(candidate, prefix) {
			return false
		}
	}
	return true
}

func prefixesOverlap(a, b netip.Prefix) bool {
	a, b = a.Masked(), b.Masked()
	return a.Contains(b.Addr()) || b.Contains(a.Addr())
}

func nthIPv4(prefix netip.Prefix, n uint32) netip.Addr {
	v := prefix.Masked().Addr().As4()
	x := uint32(v[0])<<24 | uint32(v[1])<<16 | uint32(v[2])<<8 | uint32(v[3])
	x += n
	return netip.AddrFrom4([4]byte{byte(x >> 24), byte(x >> 16), byte(x >> 8), byte(x)})
}

func containsStep(steps []Step, phase Phase, id string) bool {
	return slices.ContainsFunc(steps, func(s Step) bool { return s.Phase == phase && s.ID == id })
}

func safeError(err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%w", err)
}
