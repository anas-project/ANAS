package jobexecutor

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/anas-project/ANAS/internal/actionabi"
	"github.com/anas-project/ANAS/internal/application"
	"github.com/anas-project/ANAS/internal/computeingressruntime"
	"github.com/anas-project/ANAS/internal/consolejobs"
	"github.com/anas-project/ANAS/internal/hostaction"
	"github.com/anas-project/ANAS/internal/incusprovision"
)

func TestWorkspaceMutationWaitsForForwardingWithdrawalThroughSharedQueue(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(map[bool]string{false: "confirmed", true: "failed"}[fail], func(t *testing.T) {
			s, r, store, lease := serviceFixture(t, nil)
			entered, allow := make(chan struct{}), make(chan struct{})
			r.execute = func(ctx context.Context, id string, o consolejobs.JobCommitObserver) (consolejobs.Job, error) {
				job, err := store.Get(ctx, id)
				if err != nil {
					return job, err
				}
				if job.Action.Name != hostaction.ActionForwardingWithdraw {
					return job, errors.New("wrong cleanup action")
				}
				close(entered)
				select {
				case <-allow:
				case <-ctx.Done():
					return job, ctx.Err()
				}
				event := actionabi.Event{ABI: actionabi.Version, JobID: id, InvocationID: job.Action.InvocationID}
				if fail {
					event.Type = "error"
					event.Error = &actionabi.Failure{Outcome: actionabi.Failed, Code: "withdrawal_unconfirmed", Message: "closure unavailable"}
				} else {
					changed := true
					body, _ := json.Marshal(incusprovision.ForwardingWithdrawalResult{Schema: incusprovision.ForwardingWithdrawalSchema, WorkspaceID: "main", Scopes: 1, NewConnectionsClosed: true, ConnectionsRevoked: true})
					event.Type = "result"
					event.Result = &actionabi.Result{Outcome: actionabi.Succeeded, Changed: &changed, Value: body}
				}
				return store.CompleteActionObserved(ctx, lease, event, o)
			}
			stop, done := runServiceFixture(t, s)
			defer func() { stop(); <-done }()
			job := createApplyJob(t, store, "main", "forwarding-gate", application.ApplyRequest{})
			e := newIngressWorkspaceExecutor(t, s, store, nil)
			e.forwardingDrainer = s
			_, found, err := e.claimWorkspaceJob(context.Background(), "main")
			if found || err != nil && !errors.Is(err, errWorkspaceIngressPending) {
				t.Fatal(found, err)
			}
			select {
			case <-entered:
			case <-time.After(3 * time.Second):
				close(allow)
				t.Fatal("shared queue never ran withdrawal")
			}
			before, _ := store.Get(context.Background(), job.ID)
			if before.Status != consolejobs.StatusQueued || before.StartedAt != nil {
				t.Fatal("Core mutation claimed before withdrawal")
			}
			if _, err := s.options.IngressCoordinator.BeginChange(context.Background(), []string{"main"}); !errors.Is(err, computeingressruntime.ErrControllerChange) {
				t.Fatal("scope fence was released")
			}
			close(allow)
			if fail {
				select {
				case <-e.workspaceChange("main").withdrawalDone:
				case <-time.After(3 * time.Second):
					t.Fatal("withdrawal result not observed")
				}
				_, found, err := e.claimWorkspaceJob(context.Background(), "main")
				if found || !errors.Is(err, consolejobs.ErrCompensationRequired) {
					t.Fatal("failed mutating host job bypassed compensation", err)
				}
				v, _ := store.Get(context.Background(), job.ID)
				if v.StartedAt != nil || v.Status != consolejobs.StatusQueued {
					t.Fatal("failed host cleanup started Core", v.Status)
				}
				if _, err := s.options.IngressCoordinator.BeginChange(context.Background(), []string{"main"}); !errors.Is(err, computeingressruntime.ErrControllerChange) {
					t.Fatal("failure released launch fence", err)
				}
			} else {
				driveWorkspaceUntil(t, e, func(v consolejobs.Job, found bool) bool { return found && v.ID == job.ID })
			}
		})
	}
}

func TestForwardingWithdrawalIsNotAnUnconfirmedPublicMutation(t *testing.T) {
	s, _, _, _ := serviceFixture(t, nil)
	s.available = true
	body := json.RawMessage(`{"schema":"anas.incus-forwarding-withdrawal/v1","workspace_id":"main"}`)
	if _, err := s.Invoke(context.Background(), "alice", "main", hostaction.ActionForwardingWithdraw, body, ""); !errors.Is(err, hostaction.ErrRequest) {
		t.Fatal("public Invoke opened a mutation", err)
	}
}
