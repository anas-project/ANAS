package incusingresshost

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
)

func forwardingKernelFixture(t *testing.T) (ForwardingKernelScope, ForwardingInstanceProof) {
	t.Helper()
	s := ForwardingKernelScope{Schema: ForwardingKernelSchema, Owner: strings.Repeat("a", 32), ID: strings.Repeat("b", 32), GrantDigest: strings.Repeat("c", 64),
		Network: ForwardingNetworkProof{BootID: "11111111-1111-4111-8111-111111111111", BridgeName: "anas0123456789", BridgeMAC: "02:00:00:00:00:01", BridgeID: 10, BridgeCIDR: "10.83.0.1/24"},
		Routes:  []ForwardingRouteProof{{Destination: "192.0.2.12", Port: 8080, OutputName: "enp1s0", OutputMAC: "02:00:00:00:00:02", OutputID: 2, SourceIPv4: "192.0.2.2", Table: 254}}, MaxInstances: 4}
	p := ForwardingInstanceProof{InstanceID: "anas-fj-job1", WorkloadID: "job1", UUID: "22222222-2222-4222-8222-222222222222", Incarnation: strings.Repeat("d", 64), GuestIPv4: "10.83.0.8", GuestMAC: "02:00:00:00:00:08", HostVethName: "veth1234", HostVethMAC: "02:00:00:00:00:09", HostVethID: 11, PeerVethID: 12}
	if s.Validate() != nil || p.ValidateFor(s) != nil {
		t.Fatal("invalid forwarding test fixture")
	}
	return s, p
}

type forwardingPlatformFixture struct {
	calls  []string
	fail   string
	saved  *ForwardingKernelReceipt
	purged []ForwardingInstanceProof
}

func (p *forwardingPlatformFixture) hit(name string) error {
	p.calls = append(p.calls, name)
	if forwardingKernelStep(name) && (p.saved == nil || p.saved.PendingStep != name) {
		return errors.New("effect ran before its durable intent")
	}
	if p.fail == name {
		return errors.New("injected external-effect failure")
	}
	return nil
}
func (p *forwardingPlatformFixture) preflight(context.Context, ForwardingKernelScope, []ForwardingKernelScope) (ForwardingKernelObservation, error) {
	return ForwardingKernelObservation{Digest: strings.Repeat("f", 64), ForwardPolicy: "DROP", DefaultDockerPath: true}, p.hit("preflight")
}
func (p *forwardingPlatformFixture) absent(context.Context, ForwardingKernelScope) error {
	return p.hit("absent")
}
func (p *forwardingPlatformFixture) installNFT(context.Context, ForwardingKernelScope) (uint64, uint64, error) {
	return 1, 2, p.hit("nft.install")
}
func (p *forwardingPlatformFixture) verifyNFT(context.Context, ForwardingKernelReceipt, bool, bool) error {
	return p.hit("nft.verify")
}
func (p *forwardingPlatformFixture) installSet(context.Context, ForwardingKernelScope) (string, error) {
	return strings.Repeat("e", 64), p.hit("set.install")
}
func (p *forwardingPlatformFixture) installCompatibility(context.Context, ForwardingKernelScope, []ForwardingKernelScope) error {
	return p.hit("compat.install")
}
func (p *forwardingPlatformFixture) closeNFT(context.Context, ForwardingKernelReceipt) error {
	return p.hit("nft.close")
}
func (p *forwardingPlatformFixture) closeSet(context.Context, ForwardingKernelReceipt) error {
	return p.hit("set.close")
}
func (p *forwardingPlatformFixture) closeConnections(_ context.Context, _ ForwardingKernelReceipt, proofs []ForwardingInstanceProof) error {
	p.purged = append([]ForwardingInstanceProof{}, proofs...)
	return p.hit("connections.close")
}
func (p *forwardingPlatformFixture) refreshSet(context.Context, ForwardingKernelReceipt, []ForwardingInstanceProof) error {
	return p.hit("set.refresh")
}
func (p *forwardingPlatformFixture) refreshNFT(context.Context, ForwardingKernelReceipt, []ForwardingInstanceProof) error {
	return p.hit("nft.refresh")
}
func (p *forwardingPlatformFixture) verifyLive(context.Context, ForwardingKernelReceipt, []ForwardingKernelScope) error {
	return p.hit("live.verify")
}
func (p *forwardingPlatformFixture) removeCompatibility(context.Context, ForwardingKernelReceipt) error {
	return p.hit("compat.remove")
}
func (p *forwardingPlatformFixture) removeSet(context.Context, ForwardingKernelReceipt) error {
	return p.hit("set.remove")
}
func (p *forwardingPlatformFixture) removeNFT(context.Context, ForwardingKernelReceipt) error {
	return p.hit("nft.remove")
}
func (p *forwardingPlatformFixture) save(ctx context.Context, r ForwardingKernelReceipt) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := r.Validate(); err != nil {
		return err
	}
	copy := r
	copy.Instances = append([]ForwardingInstanceProof{}, r.Instances...)
	copy.PendingInstances = append([]ForwardingInstanceProof{}, r.PendingInstances...)
	copy.Failures = append([]ForwardingKernelFailure{}, r.Failures...)
	p.saved = &copy
	return nil
}

func TestForwardingKernelWritesOnlyAfterIntentAndClosesBeforePurge(t *testing.T) {
	s, instance := forwardingKernelFixture(t)
	platform := &forwardingPlatformFixture{}
	b := &ForwardingKernelBackend{platform: platform}
	ctx := context.Background()
	r, err := b.Install(ctx, s, nil, platform.save)
	if err != nil {
		t.Fatal(err)
	}
	if r.Phase != "closed" || !r.NewConnectionsClosed || len(r.Instances) != 0 {
		t.Fatal("install itself opened forwarding")
	}
	platform.calls = nil
	r, err = b.Refresh(ctx, r, []ForwardingInstanceProof{instance}, []ForwardingKernelScope{s}, platform.save)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"preflight", "nft.verify", "nft.close", "set.close", "connections.close", "set.refresh", "nft.refresh", "live.verify"}
	if !reflect.DeepEqual(platform.calls, want) || r.Phase != "live" || len(platform.purged) != 1 || platform.purged[0] != instance {
		t.Fatal(platform.calls, r.Phase, "first grant must purge preexisting exact tuples")
	}
	platform.calls = nil
	r, err = b.Close(ctx, r, platform.save)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(platform.calls, []string{"nft.close", "set.close", "connections.close"}) || !r.NewConnectionsClosed || !r.ConnectionsRevoked {
		t.Fatal("revocation ordering changed")
	}
	r, err = b.Release(ctx, r, platform.save)
	if err != nil {
		t.Fatal(err)
	}
	if r.Phase != "released" {
		t.Fatal("release did not verify absence")
	}
	if _, err = b.Close(ctx, r, platform.save); err != nil {
		t.Fatal("already released receipt is not idempotently closed", err)
	}
}

func TestForwardingKernelConnectionFailureNeverReportsFullRevocation(t *testing.T) {
	s, instance := forwardingKernelFixture(t)
	platform := &forwardingPlatformFixture{}
	b := &ForwardingKernelBackend{platform: platform}
	ctx := context.Background()
	r, err := b.Install(ctx, s, nil, platform.save)
	if err != nil {
		t.Fatal(err)
	}
	r, err = b.Refresh(ctx, r, []ForwardingInstanceProof{instance}, []ForwardingKernelScope{s}, platform.save)
	if err != nil {
		t.Fatal(err)
	}
	platform.fail = "connections.close"
	r, err = b.Close(ctx, r, platform.save)
	if err == nil || !r.NewConnectionsClosed || r.ConnectionsRevoked || r.PendingStep != "connections.close" || len(r.Failures) != 1 {
		t.Fatal("connection cleanup failure was hidden")
	}
	platform.fail = ""
	r, err = b.Close(ctx, r, platform.save)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Failures) < 1 || !r.ConnectionsRevoked {
		t.Fatal("recovery erased failure evidence")
	}
}

func TestForwardingKernelInterruptedIntentSurvivesCleanup(t *testing.T) {
	s, _ := forwardingKernelFixture(t)
	platform := &forwardingPlatformFixture{}
	b := &ForwardingKernelBackend{platform: platform}
	ctx := context.Background()
	r, err := b.Install(ctx, s, nil, platform.save)
	if err != nil {
		t.Fatal(err)
	}
	r.PendingStep = "nft.refresh"
	r.Phase = "failed"
	r, err = b.Close(ctx, r, platform.save)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, failure := range r.Failures {
		if failure.Step == "nft.refresh" {
			found = true
		}
	}
	if !found {
		t.Fatal("interrupted external-effect intent was overwritten by cleanup")
	}
}

func TestForwardingKernelSaveFailureStopsExternalEffects(t *testing.T) {
	s, _ := forwardingKernelFixture(t)
	platform := &forwardingPlatformFixture{}
	b := &ForwardingKernelBackend{platform: platform}
	_, err := b.Install(context.Background(), s, nil, func(context.Context, ForwardingKernelReceipt) error { return errors.New("durability unavailable") })
	if err == nil || !reflect.DeepEqual(platform.calls, []string{"preflight", "absent"}) {
		t.Fatal("side effects preceded durable state", platform.calls)
	}
}

func TestForwardingRefreshFailureCannotClaimClosedConnections(t *testing.T) {
	s, instance := forwardingKernelFixture(t)
	p := &forwardingPlatformFixture{}
	b := &ForwardingKernelBackend{platform: p}
	r, err := b.Install(context.Background(), s, nil, p.save)
	if err != nil {
		t.Fatal(err)
	}
	p.fail = "nft.refresh"
	r, err = b.Refresh(context.Background(), r, []ForwardingInstanceProof{instance}, []ForwardingKernelScope{s}, p.save)
	if err == nil || r.PendingStep != "nft.refresh" || r.NewConnectionsClosed || r.ConnectionsRevoked {
		t.Fatal("an accepted-but-unconfirmed refresh retained false revocation evidence")
	}
	if p.saved == nil || p.saved.NewConnectionsClosed || p.saved.ConnectionsRevoked {
		t.Fatal("durable record falsely claims closure")
	}
}
