package incusprovision

import (
	"context"
	"errors"
	"math"
	"reflect"
	"slices"
	"time"

	"github.com/anas-project/ANAS/internal/incusingresshost"
)

const ForwardingWithdrawalSchema = "anas.incus-forwarding-withdrawal/v1"

// Withdrawal can only close previously recorded permissions in one workspace.
// It cannot install, renew, release a deny fence, or select kernel identities.
type ForwardingWithdrawalRequest struct {
	Schema      string `json:"schema"`
	WorkspaceID string `json:"workspace_id"`
}

func (r ForwardingWithdrawalRequest) Validate() error {
	if r.Schema != ForwardingWithdrawalSchema || !pruneIdentifier.MatchString(r.WorkspaceID) {
		return ErrInvalid
	}
	return nil
}

type ForwardingWithdrawalResult struct {
	Schema               string `json:"schema"`
	WorkspaceID          string `json:"workspace_id"`
	Scopes               int    `json:"scopes"`
	NewConnectionsClosed bool   `json:"new_connections_closed"`
	ConnectionsRevoked   bool   `json:"connections_revoked"`
}

func (r ForwardingWithdrawalResult) Validate() error {
	if (ForwardingWithdrawalRequest{Schema: r.Schema, WorkspaceID: r.WorkspaceID}).Validate() != nil ||
		r.Scopes < 0 || r.Scopes > 32 || !r.NewConnectionsClosed || !r.ConnectionsRevoked {
		return ErrInvalid
	}
	return nil
}

// NewForwardingWithdrawalBackend also works before Incus was installed: the
// normal host-state lock is initialized by the existing store, and an empty
// validated state causes no network operation. Missing observation locks must
// not turn optional compute into a prerequisite for unrelated Core changes.
func NewForwardingWithdrawalBackend() *ForwardingPermissionBackend {
	b := NewForwardingPermissionBackend()
	b.lock = func(ctx context.Context, _ bool) (func() error, error) {
		lock, err := b.store.Lock(ctx)
		if err != nil {
			return nil, err
		}
		return lock.Unlock, nil
	}
	return b
}

// Withdraw is the close-only dependency of an authorized Core mutation. It
// shares the existing host-state lock, receipts and kernel transaction with
// permission.apply. Revoked credentials and stopped Core metadata are not
// reauthorized: the ORIGINAL grant is the only source of cleanup identities.
func (b *ForwardingPermissionBackend) Withdraw(ctx context.Context, request ForwardingWithdrawalRequest) (out ForwardingWithdrawalResult, result error) {
	if b == nil || b.store == nil || b.lock == nil || b.kernel == nil || ctx == nil || request.Validate() != nil {
		return out, ErrInvalid
	}
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	unlock, err := b.lock(ctx, true)
	if err != nil {
		return out, err
	}
	defer func() {
		result = errors.Join(result, unlock(), ctx.Err())
		if result != nil {
			out = ForwardingWithdrawalResult{}
		}
	}()
	state, err := b.store.Load(ctx)
	if err != nil || state.Schema != StateSchema || validateForwardingRecords(state) != nil {
		return out, ErrUnsafeState
	}
	keys := []string{}
	for key, record := range state.ForwardingScopes {
		if record.Grant.WorkspaceID == request.WorkspaceID {
			keys = append(keys, key)
		}
	}
	slices.Sort(keys)
	out = ForwardingWithdrawalResult{Schema: ForwardingWithdrawalSchema, WorkspaceID: request.WorkspaceID, Scopes: len(keys), NewConnectionsClosed: true, ConnectionsRevoked: true}
	if len(keys) == 0 {
		return out, nil
	}
	kernel, err := b.kernel()
	if err != nil || kernel == nil {
		return out, ErrBlocked
	}
	for _, key := range keys {
		record := state.ForwardingScopes[key]
		if record.Generation == math.MaxUint64 {
			result = errors.Join(result, ErrBlocked)
			continue
		}
		record.Generation++
		record.Status = "pending"
		state.ForwardingScopes[key] = record
		if err = b.store.Save(ctx, state); err != nil {
			return out, errors.Join(result, err)
		}
		save := func(saveCtx context.Context, k incusingresshost.ForwardingKernelReceipt) error {
			if k.Validate() != nil || !reflect.DeepEqual(k.Scope, record.Kernel.Scope) {
				return ErrUnsafeState
			}
			record.Kernel = k
			state.ForwardingScopes[key] = record
			return b.store.Save(saveCtx, state)
		}
		_, closeErr := kernel.Close(ctx, record.Kernel, save)
		if closeErr == nil && (ctx.Err() != nil || record.Kernel.PendingStep != "" || !record.Kernel.NewConnectionsClosed || !record.Kernel.ConnectionsRevoked ||
			(record.Kernel.Phase != "closed" && record.Kernel.Phase != "released")) {
			closeErr = ErrUnsafeState
		}
		record.Status = "disabled"
		if closeErr != nil {
			record.Status = "failed"
		}
		state.ForwardingScopes[key] = record
		// Failure evidence survives caller cancellation; this never retries an
		// external effect or refreshes the previously approved permission.
		clean, stop := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		saveErr := b.store.Save(clean, state)
		stop()
		result = errors.Join(result, closeErr, saveErr)
		if saveErr != nil {
			return out, result
		}
	}
	return out, result
}
