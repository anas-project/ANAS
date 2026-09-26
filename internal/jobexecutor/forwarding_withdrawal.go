package jobexecutor

import (
	"context"
	"encoding/json"
	"time"

	"github.com/anas-project/ANAS/internal/consolejobs"
	"github.com/anas-project/ANAS/internal/hostaction"
	"github.com/anas-project/ANAS/internal/incusprovision"
)

// ForwardingDrainer belongs to the installed owner and uses the SAME host
// queue. No consumer, HTTP parameter, or module hook supplies this adapter.
type ForwardingDrainer interface {
	WithdrawForwarding(context.Context, string, string) error
}

func (s *HostActionService) WithdrawForwarding(ctx context.Context, actor, workspace string) error {
	if ctx == nil || s == nil {
		return hostaction.ErrUnavailable
	}
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	request := incusprovision.ForwardingWithdrawalRequest{Schema: incusprovision.ForwardingWithdrawalSchema, WorkspaceID: workspace}
	body, err := json.Marshal(request)
	if err != nil {
		return hostaction.ErrRequest
	}
	created, err := s.invoke(ctx, actor, workspace, hostaction.ActionForwardingWithdraw, body, "", true)
	if err != nil {
		return err
	}
	if created.Existing || created.Job.Action == nil {
		return hostaction.ErrUnavailable
	}
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	for {
		if err := s.authorize(ctx, actor, workspace); err != nil {
			return err
		}
		job, err := s.options.Store.Get(ctx, created.Job.ID)
		if err != nil || !s.requestMatches(job) || job.Action.InvocationID != created.Job.Action.InvocationID {
			return hostaction.ErrUnavailable
		}
		if moduleActionTerminal(job.Status) {
			if job.Status != consolejobs.StatusSucceeded || job.Error != nil || job.NeedsCompensationCheck {
				return hostaction.ErrUnavailable
			}
			value, err := json.Marshal(job.Result["value"])
			var result incusprovision.ForwardingWithdrawalResult
			changed, ok := job.Result["changed"].(bool)
			if err != nil || !ok || !changed || json.Unmarshal(value, &result) != nil || result.Validate() != nil || result.WorkspaceID != workspace {
				return hostaction.ErrUnavailable
			}
			return s.authorize(ctx, actor, workspace)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}
