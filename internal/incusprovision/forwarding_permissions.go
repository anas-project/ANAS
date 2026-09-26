package incusprovision

import (
	"context"
	"errors"
	"math"
	"reflect"
	"strings"
	"time"

	"github.com/anas-project/ANAS/internal/incusingresshost"
)

// Stored in the existing root-owned host state. A disabled record retains its
// deny baseline and all failed/pending evidence; it is not an uninstall receipt.
type ForwardingPermissionRecord struct {
	Grant      ForwardingLeaseGrant                     `json:"grant"`
	Generation uint64                                   `json:"generation"`
	Status     string                                   `json:"status"`
	Kernel     incusingresshost.ForwardingKernelReceipt `json:"kernel"`
}

type ForwardingPermissionResult struct {
	Schema             string `json:"schema"`
	WorkspaceID        string `json:"workspace_id"`
	ScopeKey           string `json:"scope_key"`
	Operation          string `json:"operation"`
	PlanDigest         string `json:"plan_digest"`
	Generation         uint64 `json:"generation"`
	Enabled            bool   `json:"enabled"`
	ActiveInstances    int    `json:"active_instances"`
	PermitTTLSeconds   uint16 `json:"permit_ttl_seconds"`
	ConnectionsRevoked bool   `json:"connections_revoked"`
	Retired            bool   `json:"retired"`
	ComputeReady       bool   `json:"compute_ready"`
}

func (r ForwardingPermissionResult) Validate() error {
	if r.Schema != ForwardingPermissionSchema || !pruneIdentifier.MatchString(r.WorkspaceID) || len(r.ScopeKey) != 32 || strings.Trim(r.ScopeKey, "0123456789abcdef") != "" ||
		!digestPattern.MatchString(r.PlanDigest) || r.Generation == 0 || r.ComputeReady || r.ActiveInstances < 0 || r.ActiveInstances > 256 ||
		(r.Operation != "enable" && r.Operation != "disable" && r.Operation != "retire") || r.Enabled != (r.Operation == "enable") ||
		r.Retired != (r.Operation == "retire") ||
		(r.Enabled && r.PermitTTLSeconds != uint16(incusingresshost.ForwardingPermitTTL/time.Second)) ||
		(!r.Enabled && (r.PermitTTLSeconds != 0 || r.ActiveInstances != 0 || !r.ConnectionsRevoked)) {
		return ErrInvalid
	}
	return nil
}

func validateForwardingRecords(state State) error {
	if len(state.ForwardingScopes) > 32 {
		return ErrUnsafeState
	}
	for key, record := range state.ForwardingScopes {
		r := ForwardingPermissionRequest{WorkspaceID: record.Grant.WorkspaceID, Consumer: record.Grant.Lease.Consumer, Resource: record.Grant.Lease.Resource}
		if key != r.scopeKey() || record.Grant.Validate() != nil || record.Grant.OwnershipID != state.Ownership.ID || record.Generation == 0 ||
			(record.Status != "pending" && record.Status != "enabled" && record.Status != "disabled" && record.Status != "failed") || record.Kernel.Validate() != nil ||
			record.Kernel.Scope.GrantDigest != stableDigest(record.Grant) || record.Kernel.Scope.Owner != strings.TrimPrefix(record.Grant.OwnershipID, "anas-incus-") ||
			record.Kernel.Scope.Network != record.Grant.Network || !reflect.DeepEqual(record.Kernel.Scope.Routes, record.Grant.Routes) ||
			record.Kernel.Scope.MaxInstances != record.Grant.Lease.MaxInstances ||
			record.Kernel.Scope.ID != forwardingKernelScopeID(record.Grant, key) {
			return ErrUnsafeState
		}
	}
	return nil
}

func forwardingKernelScopeID(grant ForwardingLeaseGrant, key string) string {
	return stableDigest([]string{grant.OwnershipID, key, stableDigest(grant)})[:32]
}

type forwardingPermissionKernel interface {
	Preflight(context.Context, incusingresshost.ForwardingKernelScope, []incusingresshost.ForwardingKernelScope) (incusingresshost.ForwardingKernelObservation, error)
	Install(context.Context, incusingresshost.ForwardingKernelScope, []incusingresshost.ForwardingKernelScope, incusingresshost.ForwardingKernelSave) (incusingresshost.ForwardingKernelReceipt, error)
	Refresh(context.Context, incusingresshost.ForwardingKernelReceipt, []incusingresshost.ForwardingInstanceProof, []incusingresshost.ForwardingKernelScope, incusingresshost.ForwardingKernelSave) (incusingresshost.ForwardingKernelReceipt, error)
	Close(context.Context, incusingresshost.ForwardingKernelReceipt, incusingresshost.ForwardingKernelSave) (incusingresshost.ForwardingKernelReceipt, error)
	Release(context.Context, incusingresshost.ForwardingKernelReceipt, incusingresshost.ForwardingKernelSave) (incusingresshost.ForwardingKernelReceipt, error)
}

// The public request contains only an approved resource and exact destination
// scope. There is no command callback, arbitrary path or consumer root entry.
type ForwardingPermissionBackend struct {
	// Until lifecycle ownership and native guest acceptance are connected,
	// installed constructors cannot open traffic. This is not caller input.
	runtimeOwnerReady bool
	store             stateStore
	lock              func(context.Context, bool) (func() error, error)
	open              func(context.Context, ForwardingPermissionRequest, State, *ForwardingLeaseGrant) (*forwardingLeaseSession, error)
	kernel            func() (forwardingPermissionKernel, error)
	instances         func(context.Context, *forwardingLeaseSession, incusingresshost.ForwardingKernelScope) ([]incusingresshost.ForwardingInstanceProof, error)
	retirement        func(context.Context, State, ForwardingPermissionRecord) (*forwardingRetirementSession, error)
}

func NewForwardingPermissionBackend() *ForwardingPermissionBackend {
	return &ForwardingPermissionBackend{store: newFileStateStore(), open: openInstalledForwardingLease, retirement: openInstalledForwardingRetirement,
		lock: func(ctx context.Context, write bool) (func() error, error) {
			l, err := openExistingObservationLock(ctx, DefaultStatePath+".lock", write)
			if err != nil {
				return nil, err
			}
			return l.close, nil
		},
		kernel: func() (forwardingPermissionKernel, error) { return incusingresshost.NewForwardingKernelBackend() },
		instances: func(ctx context.Context, s *forwardingLeaseSession, k incusingresshost.ForwardingKernelScope) ([]incusingresshost.ForwardingInstanceProof, error) {
			return s.instances(ctx, k)
		},
	}
}

func (b *ForwardingPermissionBackend) Plan(ctx context.Context, r ForwardingPermissionRequest) (ForwardingPermissionPlan, error) {
	p, _, err := b.run(ctx, r, nil)
	return p, err
}

func (b *ForwardingPermissionBackend) Apply(ctx context.Context, r ForwardingPermissionRequest, binding ForwardingPermissionBinding) (ForwardingPermissionResult, error) {
	if binding.Validate() != nil || binding.WorkspaceID != r.WorkspaceID {
		return ForwardingPermissionResult{}, ErrUnconfirmed
	}
	_, out, err := b.run(ctx, r, &binding)
	return out, err
}

func (b *ForwardingPermissionBackend) run(ctx context.Context, r ForwardingPermissionRequest, binding *ForwardingPermissionBinding) (plan ForwardingPermissionPlan, out ForwardingPermissionResult, result error) {
	if b == nil || b.store == nil || b.lock == nil || b.open == nil || b.kernel == nil || b.instances == nil || ctx == nil {
		return plan, out, ErrInvalid
	}
	r, err := r.Canonical()
	if err != nil {
		return plan, out, err
	}
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	unlock, err := b.lock(ctx, binding != nil)
	if err != nil {
		return plan, out, err
	}
	defer func() {
		if unlock() != nil {
			result = errors.Join(result, ErrUnsafeState)
		}
		if ctx.Err() != nil {
			result = errors.Join(result, ctx.Err())
		}
		if result != nil {
			plan = ForwardingPermissionPlan{}
			out = ForwardingPermissionResult{}
		}
	}()
	state, err := b.store.Load(ctx)
	if err != nil || state.Schema != StateSchema || validateForwardingRecords(state) != nil {
		return plan, out, ErrUnsafeState
	}
	key := r.scopeKey()
	record, exists := state.ForwardingScopes[key]
	if record.Generation == math.MaxUint64 || (!exists && len(state.ForwardingScopes) >= 32) || (!exists && r.Operation != "enable") {
		return plan, out, ErrBlocked
	}
	if r.Operation == "retire" && (b.retirement == nil || !forwardingRetirementReady(record)) {
		return plan, out, ErrBlocked
	}
	kernel, err := b.kernel()
	if err != nil || kernel == nil {
		return plan, out, ErrBlocked
	}
	known := []incusingresshost.ForwardingKernelScope{}
	for _, v := range state.ForwardingScopes {
		if v.Kernel.Compatibility {
			known = append(known, v.Kernel.Scope)
		}
	}
	var session *forwardingLeaseSession
	var retirement *forwardingRetirementSession
	effectsStarted := false
	var save incusingresshost.ForwardingKernelSave
	// Registered before either session closer, so their errors also reach this
	// finalizer while the original host-state lock remains held. An accepted
	// refresh followed by a failed readback/save/close is not a live grant.
	defer func() {
		if ctx.Err() != nil {
			result = errors.Join(result, ctx.Err())
		}
		if result == nil || !effectsStarted {
			return
		}
		if r.Operation == "enable" && record.Kernel.InetHandle != 0 && record.Kernel.Phase != "released" {
			clean, stop := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
			// This is withdrawal only, not a retry of install or refresh. Persist
			// its pending status before any effect; the kernel owner additionally
			// journals every step and retains the failed refresh's original intent.
			record.Status = "pending"
			state.ForwardingScopes[key] = record
			if err := b.store.Save(clean, state); err != nil {
				result = errors.Join(result, err)
			} else {
				_, err := kernel.Close(clean, record.Kernel, save)
				result = errors.Join(result, err)
			}
			stop()
		}
		// Even successful withdrawal does not settle a failed enable/retire or
		// clear the dependency barrier. Keep the grant, receipts and tombstone.
		record.Status = "failed"
		state.ForwardingScopes[key] = record
		clean, stop := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		result = errors.Join(result, b.store.Save(clean, state))
		stop()
		plan, out = ForwardingPermissionPlan{}, ForwardingPermissionResult{}
	}()
	var grant ForwardingLeaseGrant
	stamp, observation := "", ""
	if r.Operation == "enable" {
		if state.Disabled || unrecoveredProvisionIntent(state) != "" || (exists && (record.Status == "pending" || record.Status == "failed" || record.Kernel.PendingStep != "" || record.Kernel.Phase == "released")) {
			return plan, out, ErrBlocked
		}
		var expected *ForwardingLeaseGrant
		if exists {
			expected = &record.Grant
		}
		session, err = b.open(ctx, r, state, expected)
		if err != nil {
			return plan, out, err
		}
		if session == nil || session.close == nil {
			return plan, out, ErrBlocked
		}
		defer func() {
			if session.close() != nil {
				result = errors.Join(result, ErrUnsafeState)
			}
			if result != nil {
				plan = ForwardingPermissionPlan{}
				out = ForwardingPermissionResult{}
			}
		}()
		if session.check == nil || session.check(ctx) != nil || validateForwardingGrantForRequest(session.grant, r) != nil {
			return plan, out, ErrBlocked
		}
		grant, stamp = session.grant, session.stamp
		if exists && !reflect.DeepEqual(grant, record.Grant) {
			return plan, out, ErrDrift
		}
		if !exists {
			scope := incusingresshost.ForwardingKernelScope{Schema: incusingresshost.ForwardingKernelSchema, Owner: strings.TrimPrefix(grant.OwnershipID, "anas-incus-"),
				ID: forwardingKernelScopeID(grant, key), GrantDigest: stableDigest(grant), Network: grant.Network, Routes: grant.Routes, MaxInstances: grant.Lease.MaxInstances}
			record = ForwardingPermissionRecord{Grant: grant, Status: "pending", Kernel: incusingresshost.ForwardingKernelReceipt{Schema: incusingresshost.ForwardingKernelReceiptSchema, Scope: scope, Phase: "new", NewConnectionsClosed: true, ConnectionsRevoked: true}}
		}
		probe, err := kernel.Preflight(ctx, record.Kernel.Scope, known)
		if err != nil {
			return plan, out, err
		}
		observation = probe.Digest
	}
	if r.Operation == "retire" {
		retirement, err = b.retirement(ctx, state, record)
		if err != nil {
			return plan, out, err
		}
		if retirement == nil || retirement.close == nil {
			return plan, out, ErrBlocked
		}
		defer func() {
			if retirement.close() != nil {
				result = errors.Join(result, ErrUnsafeState)
			}
			if result != nil {
				plan, out = ForwardingPermissionPlan{}, ForwardingPermissionResult{}
			}
		}()
		if retirement.check == nil || !digestPattern.MatchString(retirement.stamp) || retirement.check(ctx) != nil {
			return plan, out, ErrBlocked
		}
		stamp = retirement.stamp
	}
	plan = ForwardingPermissionPlan{Schema: ForwardingPermissionSchema, WorkspaceID: r.WorkspaceID, Consumer: r.Consumer, Resource: r.Resource, Operation: r.Operation,
		ScopeKey: key, RequiresRoot: true, Generation: record.Generation + 1, Blockers: []string{}, Warnings: []string{"guest_egress_unverified", "automatic_forwarding_renewal_unavailable"}}
	if r.Operation == "enable" {
		plan.Grant = &grant
		plan.GrantDigest = stableDigest(grant)
		if !b.runtimeOwnerReady {
			plan.Blockers = append(plan.Blockers, "forwarding_lifecycle_integration_unavailable")
		}
	}
	plan.StateDigest = stableDigest([]string{state.digest(), stamp, observation})
	plan.Digest = stableDigest(plan)
	if plan.Validate() != nil {
		return plan, out, ErrInvalid
	}
	if binding == nil {
		return plan, out, nil
	}
	if binding.PlanDigest != plan.Digest || binding.StateDigest != plan.StateDigest {
		return plan, out, ErrDrift
	}
	if len(plan.Blockers) != 0 {
		return plan, out, ErrBlocked
	}
	if session != nil && session.check(ctx) != nil {
		return plan, out, ErrDrift
	}
	if retirement != nil && retirement.check(ctx) != nil {
		return plan, out, ErrDrift
	}
	if state.ForwardingScopes == nil {
		state.ForwardingScopes = map[string]ForwardingPermissionRecord{}
	}
	record.Generation = plan.Generation
	record.Status = "pending"
	state.ForwardingScopes[key] = record
	if err = b.store.Save(ctx, state); err != nil {
		return plan, out, err
	}
	effectsStarted = true
	save = func(saveCtx context.Context, k incusingresshost.ForwardingKernelReceipt) error {
		if k.Validate() != nil || !reflect.DeepEqual(k.Scope, record.Kernel.Scope) {
			return ErrUnsafeState
		}
		record.Kernel = k
		state.ForwardingScopes[key] = record
		return b.store.Save(saveCtx, state)
	}
	fail := func(cause error) (ForwardingPermissionPlan, ForwardingPermissionResult, error) {
		// The deferred finalizer also sees errors from session closure and
		// cancellation, avoiding a separate, weaker error-return path.
		return plan, out, cause
	}
	if r.Operation == "enable" {
		if !exists {
			if _, err = kernel.Install(ctx, record.Kernel.Scope, known, save); err != nil {
				return fail(err)
			}
			known = append(known, record.Kernel.Scope)
		}
		proofs, err := b.instances(ctx, session, record.Kernel.Scope)
		if err != nil {
			return fail(err)
		}
		if session.check(ctx) != nil {
			return fail(ErrDrift)
		}
		if _, err = kernel.Refresh(ctx, record.Kernel, proofs, known, save); err != nil {
			return fail(err)
		}
		// The common finalizer withdraws on every post-effect failure, including
		// authorization lost here and failures after this check.
		if err = session.check(ctx); err != nil {
			return fail(err)
		}
		record.Status = "enabled"
		out.ActiveInstances = len(proofs)
		out.PermitTTLSeconds = uint16(incusingresshost.ForwardingPermitTTL / time.Second)
	} else if r.Operation == "retire" {
		// Withdraw is deliberately separate. Retirement must not remove a
		// deny fence while the old lease can still create or use instances.
		if retirement.check(ctx) != nil {
			return fail(ErrDrift)
		}
		if _, err = kernel.Release(ctx, record.Kernel, save); err != nil {
			return fail(err)
		}
		if retirement.check(ctx) != nil || record.Kernel.Phase != "released" || record.Kernel.PendingStep != "" {
			return fail(ErrDrift)
		}
		record.Status = "disabled"
		out.Retired = true
	} else {
		if _, err = kernel.Close(ctx, record.Kernel, save); err != nil {
			return fail(err)
		}
		record.Status = "disabled"
	}
	state.ForwardingScopes[key] = record
	if err = b.store.Save(ctx, state); err != nil {
		return fail(err)
	}
	out.Schema = ForwardingPermissionSchema
	out.WorkspaceID = r.WorkspaceID
	out.ScopeKey = key
	out.Operation = r.Operation
	out.PlanDigest = plan.Digest
	out.Generation = record.Generation
	out.Enabled = r.Operation == "enable"
	out.ConnectionsRevoked = record.Kernel.ConnectionsRevoked
	if out.Validate() != nil {
		return fail(ErrUnsafeState)
	}
	return plan, out, nil
}
