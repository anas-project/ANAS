package incusingresshost

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"time"
)

const ForwardingKernelReceiptSchema = "anas.incus-forwarding-kernel-receipt/v1"

type ForwardingKernelFailure struct {
	Step string    `json:"step"`
	At   time.Time `json:"at"`
}

// This record is persisted by the existing host state owner, before every
// external effect. Failed attempts and pending desired identities are retained.
// It is never accepted as a consumer request or reconstructed from rule names.
type ForwardingKernelReceipt struct {
	Schema               string                    `json:"schema"`
	Scope                ForwardingKernelScope     `json:"scope"`
	Phase                string                    `json:"phase"`
	InetHandle           uint64                    `json:"inet_handle"`
	BridgeHandle         uint64                    `json:"bridge_handle"`
	SetIdentity          string                    `json:"set_identity"`
	Compatibility        bool                      `json:"compatibility"`
	Instances            []ForwardingInstanceProof `json:"instances"`
	PendingInstances     []ForwardingInstanceProof `json:"pending_instances"`
	PendingStep          string                    `json:"pending_step"`
	NewConnectionsClosed bool                      `json:"new_connections_closed"`
	ConnectionsRevoked   bool                      `json:"connections_revoked"`
	LastRefresh          time.Time                 `json:"last_refresh,omitempty"`
	Failures             []ForwardingKernelFailure `json:"failures,omitempty"`
}

func (r ForwardingKernelReceipt) Validate() error {
	if r.Phase == "released" && (r.InetHandle != 0 || r.BridgeHandle != 0 || r.SetIdentity != "" || r.Compatibility ||
		r.PendingStep != "" || !r.NewConnectionsClosed || !r.ConnectionsRevoked) {
		return fmt.Errorf("released forwarding receipt contains unresolved kernel effects")
	}
	if r.Schema != ForwardingKernelReceiptSchema || r.Scope.Validate() != nil || !slices.Contains([]string{"new", "guarded", "prepared", "closed", "live", "released", "failed"}, r.Phase) ||
		validateForwardingInstances(r.Scope, r.Instances) != nil || validateForwardingInstances(r.Scope, r.PendingInstances) != nil || len(r.Failures) > 128 ||
		(r.InetHandle == 0) != (r.BridgeHandle == 0) || (r.SetIdentity != "" && !forwardingDigest.MatchString(r.SetIdentity)) ||
		(r.Compatibility && (r.InetHandle == 0 || r.SetIdentity == "")) || (r.Phase == "live" && (r.NewConnectionsClosed || r.ConnectionsRevoked || !r.Compatibility || r.LastRefresh.IsZero())) {
		return fmt.Errorf("invalid forwarding kernel ownership record")
	}
	if r.PendingStep != "" && !forwardingKernelStep(r.PendingStep) {
		return fmt.Errorf("invalid pending forwarding effect")
	}
	for _, failure := range r.Failures {
		if !forwardingKernelStep(failure.Step) || failure.At.IsZero() {
			return fmt.Errorf("invalid retained forwarding failure")
		}
	}
	return nil
}

func forwardingKernelStep(step string) bool {
	return slices.Contains([]string{"nft.install", "set.install", "compat.install", "nft.close", "set.close", "connections.close", "set.refresh", "nft.refresh", "compat.remove", "set.remove", "nft.remove"}, step)
}

type ForwardingKernelObservation struct {
	Digest            string `json:"digest"`
	ForwardPolicy     string `json:"forward_policy"`
	DefaultDockerPath bool   `json:"default_docker_path"`
}

// A narrow driver implemented only by the compiled local Linux executor. The
// portable transaction tests inject this private interface, not shell scripts.
type forwardingKernelPlatform interface {
	preflight(context.Context, ForwardingKernelScope, []ForwardingKernelScope) (ForwardingKernelObservation, error)
	absent(context.Context, ForwardingKernelScope) error
	installNFT(context.Context, ForwardingKernelScope) (uint64, uint64, error)
	verifyNFT(context.Context, ForwardingKernelReceipt, bool, bool) error
	installSet(context.Context, ForwardingKernelScope) (string, error)
	installCompatibility(context.Context, ForwardingKernelScope, []ForwardingKernelScope) error
	closeNFT(context.Context, ForwardingKernelReceipt) error
	closeSet(context.Context, ForwardingKernelReceipt) error
	closeConnections(context.Context, ForwardingKernelReceipt, []ForwardingInstanceProof) error
	refreshSet(context.Context, ForwardingKernelReceipt, []ForwardingInstanceProof) error
	refreshNFT(context.Context, ForwardingKernelReceipt, []ForwardingInstanceProof) error
	verifyLive(context.Context, ForwardingKernelReceipt, []ForwardingKernelScope) error
	removeCompatibility(context.Context, ForwardingKernelReceipt) error
	removeSet(context.Context, ForwardingKernelReceipt) error
	removeNFT(context.Context, ForwardingKernelReceipt) error
}

type ForwardingKernelBackend struct{ platform forwardingKernelPlatform }

// Save must be the owning host-state transaction. A successful return means
// the intent/receipt was durably saved; it is not an optional progress callback.
type ForwardingKernelSave func(context.Context, ForwardingKernelReceipt) error

func NewForwardingKernelBackend() (*ForwardingKernelBackend, error) {
	platform, err := newLocalForwardingPlatform()
	if err != nil {
		return nil, err
	}
	return &ForwardingKernelBackend{platform: platform}, nil
}

func (b *ForwardingKernelBackend) Preflight(ctx context.Context, scope ForwardingKernelScope, known []ForwardingKernelScope) (ForwardingKernelObservation, error) {
	if b == nil || b.platform == nil || ctx == nil || scope.Validate() != nil {
		return ForwardingKernelObservation{}, fmt.Errorf("invalid forwarding preflight")
	}
	return b.platform.preflight(ctx, scope, known)
}

func newForwardingKernelReceipt(scope ForwardingKernelScope) ForwardingKernelReceipt {
	return ForwardingKernelReceipt{Schema: ForwardingKernelReceiptSchema, Scope: scope, Phase: "new", Instances: []ForwardingInstanceProof{}, PendingInstances: []ForwardingInstanceProof{}, NewConnectionsClosed: true, ConnectionsRevoked: true}
}

func (b *ForwardingKernelBackend) effect(ctx context.Context, r *ForwardingKernelReceipt, save ForwardingKernelSave, step string, run func() error) error {
	if ctx == nil || ctx.Err() != nil || r == nil || save == nil || run == nil || !forwardingKernelStep(step) || len(r.Failures) >= 128 {
		return fmt.Errorf("forwarding effect is not safely journaled")
	}
	if r.PendingStep != "" {
		// Recovery is a new effect, not evidence that the interrupted one did
		// nothing. Retain its identity before recording the cleanup intent.
		r.Failures = append(r.Failures, ForwardingKernelFailure{Step: r.PendingStep, At: time.Now().UTC()})
		if len(r.Failures) >= 128 {
			return fmt.Errorf("forwarding recovery evidence is full")
		}
	}
	r.PendingStep = step
	if err := save(ctx, *r); err != nil {
		return err
	}
	if err := run(); err != nil {
		r.Failures = append(r.Failures, ForwardingKernelFailure{Step: step, At: time.Now().UTC()})
		r.Phase = "failed"
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return errors.Join(err, save(cleanup, *r))
	}
	r.PendingStep = ""
	if err := save(ctx, *r); err != nil {
		// The durable copy still contains the pending intent. Do not return a
		// settled in-memory receipt that a later caller could overwrite it with.
		r.PendingStep = step
		r.Phase = "failed"
		return err
	}
	return nil
}

func (b *ForwardingKernelBackend) Install(ctx context.Context, scope ForwardingKernelScope, known []ForwardingKernelScope, save ForwardingKernelSave) (ForwardingKernelReceipt, error) {
	r := newForwardingKernelReceipt(scope)
	if b == nil || b.platform == nil || ctx == nil || scope.Validate() != nil || save == nil {
		return r, fmt.Errorf("invalid forwarding installation")
	}
	if _, err := b.platform.preflight(ctx, scope, known); err != nil {
		return r, err
	}
	if err := b.platform.absent(ctx, scope); err != nil {
		return r, err
	}
	if err := save(ctx, r); err != nil {
		return r, err
	}
	if err := b.effect(ctx, &r, save, "nft.install", func() error {
		inet, bridge, err := b.platform.installNFT(ctx, scope)
		if err != nil {
			return err
		}
		r.InetHandle, r.BridgeHandle, r.Phase = inet, bridge, "guarded"
		return nil
	}); err != nil {
		return r, err
	}
	if err := b.effect(ctx, &r, save, "set.install", func() error {
		identity, err := b.platform.installSet(ctx, scope)
		if err != nil {
			return err
		}
		r.SetIdentity = identity
		return nil
	}); err != nil {
		return r, err
	}
	if err := b.effect(ctx, &r, save, "compat.install", func() error {
		if err := b.platform.installCompatibility(ctx, scope, known); err != nil {
			return err
		}
		r.Compatibility = true
		r.Phase = "closed"
		return nil
	}); err != nil {
		return r, err
	}
	return r, nil
}

func forwardingCleanupInstances(r ForwardingKernelReceipt) []ForwardingInstanceProof {
	result := slices.Clone(r.Instances)
	seen := map[string]bool{}
	for _, p := range result {
		seen[p.GuestIPv4] = true
	}
	for _, p := range r.PendingInstances {
		if !seen[p.GuestIPv4] {
			result = append(result, p)
			seen[p.GuestIPv4] = true
		}
	}
	return result
}

// Close first prevents packet-based recreation, then removes exact observed
// bidirectional NAT connections. It does not remove the owned deny baseline.
func (b *ForwardingKernelBackend) Close(ctx context.Context, r ForwardingKernelReceipt, save ForwardingKernelSave) (ForwardingKernelReceipt, error) {
	if b == nil || b.platform == nil || ctx == nil || r.Validate() != nil || save == nil {
		return r, fmt.Errorf("invalid forwarding close ownership")
	}
	if r.Phase == "released" {
		return r, b.platform.absent(ctx, r.Scope)
	}
	if r.InetHandle == 0 {
		return r, fmt.Errorf("forwarding close has no verified kernel ownership")
	}
	if err := b.effect(ctx, &r, save, "nft.close", func() error {
		if err := b.platform.closeNFT(ctx, r); err != nil {
			return err
		}
		r.NewConnectionsClosed = true
		r.ConnectionsRevoked = false
		r.Phase = "prepared"
		return nil
	}); err != nil {
		return r, err
	}
	if r.SetIdentity != "" {
		if err := b.effect(ctx, &r, save, "set.close", func() error { return b.platform.closeSet(ctx, r) }); err != nil {
			return r, err
		}
	}
	if err := b.effect(ctx, &r, save, "connections.close", func() error {
		if err := b.platform.closeConnections(ctx, r, forwardingCleanupInstances(r)); err != nil {
			return err
		}
		r.ConnectionsRevoked = true
		r.Phase = "closed"
		return nil
	}); err != nil {
		return r, err
	}
	return r, nil
}

func (b *ForwardingKernelBackend) Refresh(ctx context.Context, r ForwardingKernelReceipt, instances []ForwardingInstanceProof, known []ForwardingKernelScope, save ForwardingKernelSave) (ForwardingKernelReceipt, error) {
	if b == nil || b.platform == nil || ctx == nil || r.Validate() != nil || save == nil || r.PendingStep != "" || r.Phase == "failed" || !r.Compatibility || validateForwardingInstances(r.Scope, instances) != nil {
		return r, fmt.Errorf("forwarding refresh requires settled owned state")
	}
	if _, err := b.platform.preflight(ctx, r.Scope, known); err != nil {
		return r, err
	}
	desired := canonicalForwardingInstances(instances)
	unchanged := reflect.DeepEqual(canonicalForwardingInstances(r.Instances), desired)
	if err := b.platform.verifyNFT(ctx, r, r.NewConnectionsClosed, true); err != nil {
		return r, err
	}
	if !unchanged || r.NewConnectionsClosed || len(desired) == 0 {
		r.PendingInstances = desired
		if err := save(ctx, r); err != nil {
			return r, err
		}
		var err error
		r, err = b.Close(ctx, r, save)
		if err != nil {
			return r, err
		}
		if len(desired) == 0 {
			r.PendingInstances = []ForwardingInstanceProof{}
			return r, save(ctx, r)
		}
	}
	if err := b.effect(ctx, &r, save, "set.refresh", func() error { return b.platform.refreshSet(ctx, r, desired) }); err != nil {
		return r, err
	}
	// A failed command/readback can still have opened the kernel transaction.
	// Persist uncertainty before that effect, not the previous closed state.
	r.NewConnectionsClosed, r.ConnectionsRevoked = false, false
	if err := b.effect(ctx, &r, save, "nft.refresh", func() error {
		if err := b.platform.refreshNFT(ctx, r, desired); err != nil {
			return err
		}
		r.Instances = desired
		r.PendingInstances = []ForwardingInstanceProof{}
		r.NewConnectionsClosed = false
		r.ConnectionsRevoked = false
		r.Phase = "live"
		r.LastRefresh = time.Now().UTC()
		return b.platform.verifyLive(ctx, r, known)
	}); err != nil {
		return r, err
	}
	return r, nil
}

// The host authority separately proves that the lease is no longer in use
// before Release. This removes only the receipt-owned empty compatibility
// objects and the closed deny baseline, restoring existing administrator policy.
func (b *ForwardingKernelBackend) Release(ctx context.Context, r ForwardingKernelReceipt, save ForwardingKernelSave) (ForwardingKernelReceipt, error) {
	if b == nil || b.platform == nil || ctx == nil || r.Validate() != nil || save == nil || !r.NewConnectionsClosed || !r.ConnectionsRevoked || r.PendingStep != "" {
		return r, fmt.Errorf("forwarding release requires verified connection revocation")
	}
	if r.Phase == "released" {
		return r, b.platform.absent(ctx, r.Scope)
	}
	if err := b.platform.verifyNFT(ctx, r, true, true); err != nil {
		return r, err
	}
	if r.Compatibility {
		if err := b.effect(ctx, &r, save, "compat.remove", func() error {
			if err := b.platform.removeCompatibility(ctx, r); err != nil {
				return err
			}
			r.Compatibility = false
			return nil
		}); err != nil {
			return r, err
		}
	}
	if r.SetIdentity != "" {
		if err := b.effect(ctx, &r, save, "set.remove", func() error {
			if err := b.platform.removeSet(ctx, r); err != nil {
				return err
			}
			r.SetIdentity = ""
			return nil
		}); err != nil {
			return r, err
		}
	}
	if err := b.effect(ctx, &r, save, "nft.remove", func() error {
		if err := b.platform.removeNFT(ctx, r); err != nil {
			return err
		}
		r.InetHandle = 0
		r.BridgeHandle = 0
		r.Phase = "released"
		return b.platform.absent(ctx, r.Scope)
	}); err != nil {
		return r, err
	}
	return r, nil
}
