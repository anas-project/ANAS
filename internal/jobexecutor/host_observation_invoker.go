package jobexecutor

import (
	"context"
	"encoding/json"
	"time"

	"github.com/anas-project/ANAS/internal/consolejobs"
	"github.com/anas-project/ANAS/internal/hostaction"
	"github.com/anas-project/ANAS/internal/incusingresshost"
)

// HostObservationInvoker is a trusted anasd-side adapter, not a new listener
// or queue. Every read is a new shared job with no retry key. Disconnect merely
// abandons waiting: the existing supervisor owns timeout, audit and completion.
type HostObservationInvoker struct {
	Service     *HostActionService
	Actor       string
	WorkspaceID string
}

var _ incusingresshost.ProjectionInvoker = HostObservationInvoker{}

func (i HostObservationInvoker) InvokeHostAction(ctx context.Context, action string, body []byte) ([]byte, error) {
	if ctx == nil || i.Service == nil || action != hostaction.ActionObserveHTTP {
		return nil, hostaction.ErrRequest
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	canonical, err := hostaction.CanonicalParameters(action, body)
	if err != nil || !hostaction.ObservationScopeMatchesWorkspace(action, canonical, i.WorkspaceID) {
		return nil, hostaction.ErrDenied
	}
	var request incusingresshost.ProjectionRequest
	if json.Unmarshal(canonical, &request) != nil {
		return nil, hostaction.ErrRequest
	}
	created, err := i.Service.Invoke(ctx, i.Actor, i.WorkspaceID, action, canonical, "")
	if err != nil {
		return nil, err
	}
	if created.Existing || created.Job.Action == nil {
		return nil, hostaction.ErrUnavailable
	}
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if i.Service.authorize(ctx, i.Actor, i.WorkspaceID) != nil {
			return nil, hostaction.ErrDenied
		}
		job, err := i.Service.options.Store.Get(ctx, created.Job.ID)
		if err != nil || !i.Service.requestMatches(job) || job.Action.InvocationID != created.Job.Action.InvocationID {
			return nil, hostaction.ErrUnavailable
		}
		if moduleActionTerminal(job.Status) {
			changed, ok := job.Result["changed"].(bool)
			if job.Status != consolejobs.StatusSucceeded || job.Error != nil || !ok || changed {
				return nil, hostaction.ErrUnavailable
			}
			result, err := json.Marshal(job.Result["value"])
			var response incusingresshost.ProjectionResponse
			if err != nil || json.Unmarshal(result, &response) != nil || response.ValidateFor(request) != nil ||
				i.Service.authorize(ctx, i.Actor, i.WorkspaceID) != nil {
				return nil, hostaction.ErrUnavailable
			}
			return result, nil
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-ticker.C:
		}
	}
}
