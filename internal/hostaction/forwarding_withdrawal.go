package hostaction

import (
	"context"

	"github.com/anas-project/ANAS/internal/incusprovision"
)

type forwardingWithdrawalBackend interface {
	Withdraw(context.Context, incusprovision.ForwardingWithdrawalRequest) (incusprovision.ForwardingWithdrawalResult, error)
}

var newForwardingWithdrawalBackend = func() forwardingWithdrawalBackend { return incusprovision.NewForwardingWithdrawalBackend() }

func executeForwardingWithdrawal(ctx context.Context, body []byte) (incusprovision.ForwardingWithdrawalResult, error) {
	var r incusprovision.ForwardingWithdrawalRequest
	if strictDecode(body, &r) != nil || r.Validate() != nil {
		return incusprovision.ForwardingWithdrawalResult{}, ErrRequest
	}
	b := newForwardingWithdrawalBackend()
	if b == nil {
		return incusprovision.ForwardingWithdrawalResult{}, ErrUnavailable
	}
	out, err := b.Withdraw(ctx, r)
	if err == nil && (out.Validate() != nil || out.WorkspaceID != r.WorkspaceID) {
		return incusprovision.ForwardingWithdrawalResult{}, ErrRequest
	}
	return out, err
}
