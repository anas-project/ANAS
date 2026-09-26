package incusprovision

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/anas-project/ANAS/internal/computeingressruntime"
	"github.com/anas-project/ANAS/internal/incusingresshost"
)

type forwardingStoreFixture struct{ memoryStore }

func (s *forwardingStoreFixture) Load(ctx context.Context) (State, error) {
	v, e := s.memoryStore.Load(ctx)
	body, _ := json.Marshal(v)
	var copy State
	_ = json.Unmarshal(body, &copy)
	return copy, e
}
func (s *forwardingStoreFixture) Save(ctx context.Context, v State) error {
	body, _ := json.Marshal(v)
	var copy State
	_ = json.Unmarshal(body, &copy)
	return s.memoryStore.Save(ctx, copy)
}

type permissionKernelFixture struct {
	store *forwardingStoreFixture
	key   string
	calls []string
	fail  string
}

func (k *permissionKernelFixture) Preflight(context.Context, incusingresshost.ForwardingKernelScope, []incusingresshost.ForwardingKernelScope) (incusingresshost.ForwardingKernelObservation, error) {
	return incusingresshost.ForwardingKernelObservation{Digest: strings.Repeat("f", 64), DefaultDockerPath: true, ForwardPolicy: "DROP"}, nil
}
func (k *permissionKernelFixture) hit(step string) error {
	if k.store.state.ForwardingScopes[k.key].Status != "pending" {
		return errors.New("external effect preceded authorization intent")
	}
	k.calls = append(k.calls, step)
	if step == k.fail {
		return errors.New("effect failure")
	}
	return nil
}
func (k *permissionKernelFixture) Install(ctx context.Context, s incusingresshost.ForwardingKernelScope, _ []incusingresshost.ForwardingKernelScope, save incusingresshost.ForwardingKernelSave) (incusingresshost.ForwardingKernelReceipt, error) {
	r := incusingresshost.ForwardingKernelReceipt{Schema: incusingresshost.ForwardingKernelReceiptSchema, Scope: s, Phase: "closed", InetHandle: 1, BridgeHandle: 2, SetIdentity: strings.Repeat("a", 64), Compatibility: true, NewConnectionsClosed: true, ConnectionsRevoked: true}
	if err := k.hit("install"); err != nil {
		return r, err
	}
	return r, save(ctx, r)
}
func (k *permissionKernelFixture) Refresh(ctx context.Context, r incusingresshost.ForwardingKernelReceipt, p []incusingresshost.ForwardingInstanceProof, _ []incusingresshost.ForwardingKernelScope, save incusingresshost.ForwardingKernelSave) (incusingresshost.ForwardingKernelReceipt, error) {
	if err := k.hit("refresh"); err != nil {
		return r, err
	}
	r.Instances = p
	r.NewConnectionsClosed = false
	r.ConnectionsRevoked = false
	r.Phase = "live"
	r.LastRefresh = time.Now().UTC()
	return r, save(ctx, r)
}
func (k *permissionKernelFixture) Close(ctx context.Context, r incusingresshost.ForwardingKernelReceipt, save incusingresshost.ForwardingKernelSave) (incusingresshost.ForwardingKernelReceipt, error) {
	err := k.hit("close")
	r.NewConnectionsClosed = true
	r.ConnectionsRevoked = err == nil
	r.Phase = "closed"
	if err == nil {
		r.PendingStep = ""
	}
	if err != nil {
		r.Phase = "failed"
		r.PendingStep = "connections.close"
		r.Failures = append(r.Failures, incusingresshost.ForwardingKernelFailure{Step: "connections.close", At: time.Now().UTC()})
	}
	return r, errors.Join(err, save(ctx, r))
}

func (k *permissionKernelFixture) Release(ctx context.Context, r incusingresshost.ForwardingKernelReceipt, save incusingresshost.ForwardingKernelSave) (incusingresshost.ForwardingKernelReceipt, error) {
	if err := k.hit("release"); err != nil {
		return r, err
	}
	r.Phase, r.InetHandle, r.BridgeHandle, r.SetIdentity, r.Compatibility = "released", 0, 0, "", false
	return r, save(ctx, r)
}

func permissionFixture(t *testing.T) (*ForwardingPermissionBackend, *forwardingStoreFixture, *permissionKernelFixture, ForwardingPermissionRequest) {
	t.Helper()
	req := ForwardingPermissionRequest{Schema: ForwardingPermissionSchema, WorkspaceID: "main", Consumer: "forgejo", Resource: "runners", Operation: "enable", Destinations: []ForwardingDestination{{IPv4: "192.0.2.12", Port: 8080}}}
	g := ForwardingLeaseGrant{Schema: ForwardingPermissionSchema, WorkspaceID: "main", WorkspaceDigest: "sha256:" + strings.Repeat("1", 64), Epoch: strings.Repeat("2", 64), OwnershipID: "anas-incus-" + strings.Repeat("3", 32), BundleDigest: strings.Repeat("4", 64), ServerVersion: "6.0.5",
		Lease: computeingressruntime.IncusLeaseObservationScope{Deployment: "dep1", Consumer: "forgejo", Resource: "runners", Provider: "incus", Interface: "incus_container", Project: "anas-forgejo-runners", InstancePrefix: "anas-fj-", CredentialFingerprint: strings.Repeat("5", 64), MaxInstances: 4}, Destinations: req.Destinations,
		Network: incusingresshost.ForwardingNetworkProof{BootID: "11111111-1111-4111-8111-111111111111", BridgeName: "anas0123456789", BridgeMAC: "02:00:00:00:00:01", BridgeID: 10, BridgeCIDR: "10.83.0.1/24"},
		Routes:  []incusingresshost.ForwardingRouteProof{{Destination: "192.0.2.12", Port: 8080, OutputName: "enp1s0", OutputMAC: "02:00:00:00:00:02", OutputID: 2, SourceIPv4: "192.0.2.2", Table: 254}}}
	if g.Validate() != nil {
		t.Fatal("invalid grant fixture")
	}
	s := &forwardingStoreFixture{memoryStore: memoryStore{state: State{Schema: StateSchema, Ownership: Ownership{ID: g.OwnershipID}}}}
	k := &permissionKernelFixture{store: s, key: req.scopeKey()}
	b := &ForwardingPermissionBackend{runtimeOwnerReady: true, store: s, lock: func(ctx context.Context, _ bool) (func() error, error) {
		l, e := s.Lock(ctx)
		if e != nil {
			return nil, e
		}
		return l.Unlock, nil
	}, kernel: func() (forwardingPermissionKernel, error) { return k, nil },
		open: func(context.Context, ForwardingPermissionRequest, State, *ForwardingLeaseGrant) (*forwardingLeaseSession, error) {
			return &forwardingLeaseSession{grant: g, stamp: strings.Repeat("6", 64), check: func(ctx context.Context) error { return ctx.Err() }, close: func() error { return nil }}, nil
		},
		instances: func(_ context.Context, _ *forwardingLeaseSession, scope incusingresshost.ForwardingKernelScope) ([]incusingresshost.ForwardingInstanceProof, error) {
			return []incusingresshost.ForwardingInstanceProof{{InstanceID: "anas-fj-job1", WorkloadID: "job1", UUID: "22222222-2222-4222-8222-222222222222", Incarnation: strings.Repeat("7", 64), GuestIPv4: "10.83.0.8", GuestMAC: "02:00:00:00:00:08", HostVethName: "veth1234", HostVethMAC: "02:00:00:00:00:09", HostVethID: 11, PeerVethID: 12}}, nil
		},
	}
	return b, s, k, req
}

func permissionBinding(t *testing.T, b *ForwardingPermissionBackend, r ForwardingPermissionRequest) ForwardingPermissionBinding {
	t.Helper()
	p, e := b.Plan(context.Background(), r)
	if e != nil {
		t.Fatal(e)
	}
	return ForwardingPermissionBinding{Schema: ForwardingPermissionSchema, WorkspaceID: r.WorkspaceID, PlanDigest: p.Digest, StateDigest: p.StateDigest}
}

func TestForwardingPermissionConfirmedResourceAndRevocation(t *testing.T) {
	b, s, k, r := permissionFixture(t)
	binding := permissionBinding(t, b, r)
	if s.saves != 0 || len(k.calls) != 0 {
		t.Fatal("plan mutated state")
	}
	bad := binding
	bad.StateDigest = strings.Repeat("9", 64)
	if _, err := b.Apply(context.Background(), r, bad); err == nil || s.saves != 0 {
		t.Fatal("stale confirmation mutated state")
	}
	out, err := b.Apply(context.Background(), r, binding)
	if err != nil || out.Validate() != nil || out.ComputeReady || !out.Enabled || out.PermitTTLSeconds != 30 || out.ActiveInstances != 1 {
		t.Fatal(out, err)
	}
	if validateForwardingRecords(s.state) != nil {
		t.Fatal("invalid durable permission")
	}
	if _, err = b.Apply(context.Background(), r, binding); err == nil {
		t.Fatal("consumed plan replayed after state transition")
	}
	r.Operation = "disable"
	r.Destinations = nil
	// Lease revocation must not prevent closing the previously owned permit.
	b.open = func(context.Context, ForwardingPermissionRequest, State, *ForwardingLeaseGrant) (*forwardingLeaseSession, error) {
		t.Fatal("disable tried to reauthorize revoked lease")
		return nil, ErrBlocked
	}
	out, err = b.Apply(context.Background(), r, permissionBinding(t, b, r))
	if err != nil || out.Enabled || !out.ConnectionsRevoked || s.state.ForwardingScopes[r.scopeKey()].Kernel.InetHandle == 0 {
		t.Fatal("disable lost the deny baseline or revocation proof", out, err)
	}
}

func TestForwardingPermissionFailuresRetainOriginalKernelEvidence(t *testing.T) {
	b, s, k, r := permissionFixture(t)
	if _, err := b.Apply(context.Background(), r, permissionBinding(t, b, r)); err != nil {
		t.Fatal(err)
	}
	r.Operation = "disable"
	r.Destinations = nil
	k.fail = "close"
	if _, err := b.Apply(context.Background(), r, permissionBinding(t, b, r)); err == nil {
		t.Fatal("purge failure reported success")
	}
	record := s.state.ForwardingScopes[r.scopeKey()]
	if record.Status != "failed" || record.Kernel.ConnectionsRevoked || record.Kernel.PendingStep != "connections.close" || len(record.Kernel.Failures) != 1 {
		t.Fatal("failed revocation evidence discarded")
	}
	k.fail = ""
	if _, err := b.Apply(context.Background(), r, permissionBinding(t, b, r)); err != nil {
		t.Fatal(err)
	}
	if len(s.state.ForwardingScopes[r.scopeKey()].Kernel.Failures) != 1 {
		t.Fatal("retry erased failure history")
	}
}

func TestForwardingPermissionRequiresDurabilityAndExactLease(t *testing.T) {
	for _, scenario := range []string{"save", "consumer", "destination", "cancel"} {
		t.Run(scenario, func(t *testing.T) {
			b, s, k, r := permissionFixture(t)
			binding := permissionBinding(t, b, r)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			switch scenario {
			case "save":
				s.failSaveAt = 1
			case "consumer":
				r.Consumer = "other"
			case "destination":
				r.Destinations[0].Port++
			case "cancel":
				cancel()
			}
			if _, err := b.Apply(ctx, r, binding); err == nil || len(k.calls) != 0 {
				t.Fatal("unauthorized or unjournaled write occurred")
			}
		})
	}
}

func TestForwardingPermissionInstalledEnableGateCannotBeConfirmedAway(t *testing.T) {
	b, s, k, r := permissionFixture(t)
	b.runtimeOwnerReady = false
	p, err := b.Plan(context.Background(), r)
	if err != nil || len(p.Blockers) != 1 || p.Blockers[0] != "forwarding_lifecycle_integration_unavailable" {
		t.Fatal(p, err)
	}
	if _, err = b.Apply(context.Background(), r, permissionBinding(t, b, r)); !errors.Is(err, ErrBlocked) || s.saves != 0 || len(k.calls) != 0 {
		t.Fatal("confirmation bypassed unavailable lifecycle owner", err)
	}
	if NewForwardingPermissionBackend().runtimeOwnerReady {
		t.Fatal("installed constructor enables unaccepted lifecycle")
	}
}

func TestRetainedForwardingBlocksHostRemovalAndDependencyChanges(t *testing.T) {
	b, s, _, r := permissionFixture(t)
	if _, err := b.Apply(context.Background(), r, permissionBinding(t, b, r)); err != nil {
		t.Fatal(err)
	}
	for _, status := range []string{"enabled", "disabled", "failed", "pending"} {
		t.Run(status, func(t *testing.T) {
			v := s.state.ForwardingScopes[r.scopeKey()]
			v.Status = status
			s.state.ForwardingScopes[r.scopeKey()] = v
			if unrecoveredPendingIntent(s.state) == "" {
				t.Fatal("retained permission allows destructive host mutation")
			}
			p, err := buildPlan(Request{}, newFakeRuntime(t).obs, s.state)
			if err != nil || p.Disposition != "blocked" || len(p.Steps) != 0 {
				t.Fatal("host plan offered dependency mutation before forwarding retirement")
			}
		})
	}
}

func TestForwardingRecordRejectsSubstitutedKernelScope(t *testing.T) {
	b, s, _, r := permissionFixture(t)
	if _, err := b.Apply(context.Background(), r, permissionBinding(t, b, r)); err != nil {
		t.Fatal(err)
	}
	record := s.state.ForwardingScopes[r.scopeKey()]
	record.Kernel.Scope.ID = strings.Repeat("f", 32)
	s.state.ForwardingScopes[r.scopeKey()] = record
	if validateForwardingRecords(s.state) == nil {
		t.Fatal("record adopted a different kernel scope with the same lease")
	}
}
