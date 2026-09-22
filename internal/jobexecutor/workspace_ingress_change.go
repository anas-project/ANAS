package jobexecutor

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/anas-project/ANAS/internal/computeingressruntime"
	"github.com/anas-project/ANAS/internal/consolejobs"
	"github.com/anas-project/ANAS/internal/deploymentaudit"
)

var (
	errWorkspaceIngressPending  = errors.New("workspace ingress change is pending")
	errWorkspaceIngressIdentity = errors.New("workspace ingress job binding changed; recovery is required")
	errWorkspaceIngressDenied   = errors.New("workspace job is no longer authorized")
)

// One outstanding fence per worker/workspace, never a second durable queue.
// Bind immutable fields only: progress, revision and timestamps of transitions
// legitimately change as the original Store commits the operation.
type workspaceIngressChange struct {
	id, kind, workspace, actor, requestDigest string
	created                                   time.Time
	gate                                      *computeingressruntime.ControllerChange
}

func workspaceJobDigest(job consolejobs.Job) (string, error) {
	body, err := json.Marshal(job.Request)
	if err != nil {
		return "", errWorkspaceIngressIdentity
	}
	return consolejobs.DigestRequest(body), nil
}

func (g *workspaceIngressChange) matches(job consolejobs.Job) bool {
	digest, err := workspaceJobDigest(job)
	return g != nil && err == nil && job.Action == nil && job.Mutating &&
		g.id == job.ID && g.kind == job.Kind && g.workspace == job.WorkspaceID &&
		g.actor == job.CreatedBy && g.created.Equal(job.CreatedAt) && g.requestDigest == digest
}

func (e *Executor) workspaceChange(workspace string) *workspaceIngressChange {
	e.ingressMu.Lock()
	defer e.ingressMu.Unlock()
	return e.ingressChanges[workspace]
}

// Only a durable, matching terminal may free launch admission. A canceled
// queued job can outlive its HTTP waiter while the original drain finishes.
// Interrupted/uncertain jobs retain the fence until the existing compensation
// workflow acknowledges them; missing jobs never mean successful completion.
func (e *Executor) retireWorkspaceChange(ctx context.Context, workspace string) error {
	g := e.workspaceChange(workspace)
	if g == nil {
		return nil
	}
	job, err := e.store.Get(ctx, g.id)
	if err != nil || !g.matches(job) {
		return errWorkspaceIngressIdentity
	}
	if !moduleActionTerminal(job.Status) {
		return nil
	}
	if job.NeedsCompensationCheck {
		return consolejobs.ErrCompensationRequired
	}
	ready, _ := g.gate.Poll()
	if !ready {
		return errWorkspaceIngressPending
	}
	if err := g.gate.Release(); err != nil {
		return errWorkspaceIngressIdentity
	}
	e.ingressMu.Lock()
	defer e.ingressMu.Unlock()
	if e.ingressChanges[workspace] != g {
		return errWorkspaceIngressIdentity
	}
	delete(e.ingressChanges, workspace)
	return nil
}

// ClaimNext's observer may veto before running is persisted. Starting a drain
// here performs only bounded in-memory coordination; it NEVER waits, reenters
// the job store, takes a workspace lock or performs network work on jobs.lock.
// The independent controller goroutine can thus use the same shared queue.
func (e *Executor) claimWorkspaceJob(ctx context.Context, workspace string) (consolejobs.Job, bool, error) {
	if err := e.retireWorkspaceChange(ctx, workspace); err != nil {
		return consolejobs.Job{}, false, err
	}
	var candidate *consolejobs.Job
	var rejection string
	audit := e.jobAuditObserver(deploymentaudit.StageJobStartAuthorized, "")
	observer := consolejobs.JobCommitObserverFunc(func(ctx context.Context, intent consolejobs.JobCommitIntent) error {
		job := intent.Next
		if intent.Previous == nil || intent.Previous.Status != consolejobs.StatusQueued || job.Action != nil ||
			job.WorkspaceID != workspace || !job.Mutating {
			return errWorkspaceIngressIdentity
		}
		candidate = &job
		if e.authorize != nil && e.authorize(ctx, job) != nil {
			rejection = "job_authorization_revoked"
			return errWorkspaceIngressDenied
		}
		if e.ingressCoordinator == nil {
			return audit.BeforeJobCommit(ctx, intent)
		}
		g := e.workspaceChange(workspace)
		if g == nil {
			// Audit an authorized start attempt before stopping the old owner.
			// This is not a claim/result: the job stays queued until drain ends.
			if err := audit.BeforeJobCommit(ctx, intent); err != nil {
				return err
			}
			digest, err := workspaceJobDigest(job)
			if err != nil {
				return err
			}
			gate, err := e.ingressCoordinator.BeginChange(ctx, []string{workspace})
			if errors.Is(err, computeingressruntime.ErrControllerChange) {
				return errWorkspaceIngressPending
			}
			if err != nil {
				return err
			}
			g = &workspaceIngressChange{id: job.ID, kind: job.Kind, workspace: workspace, actor: job.CreatedBy,
				created: job.CreatedAt, requestDigest: digest, gate: gate}
			e.ingressMu.Lock()
			e.ingressChanges[workspace] = g
			e.ingressMu.Unlock()
		}
		if !g.matches(job) {
			return errWorkspaceIngressIdentity
		}
		ready, err := g.gate.Poll()
		if !ready {
			return errWorkspaceIngressPending
		}
		if err != nil {
			rejection = "ingress_drain_failed"
			return computeingressruntime.ErrControllerDrain
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		return audit.BeforeJobCommit(ctx, intent)
	})
	job, found, err := e.store.ClaimNextObserved(ctx, workspace, observer)
	if err == nil || candidate == nil || rejection == "" {
		return job, found, err
	}
	// Do not translate a failed drain into a running job or user cancellation.
	// Recheck the original immutable input and queued state under jobs.lock.
	expected := *candidate
	digest, digestErr := workspaceJobDigest(expected)
	if digestErr != nil {
		return consolejobs.Job{}, false, digestErr
	}
	bound := &workspaceIngressChange{id: expected.ID, kind: expected.Kind, workspace: expected.WorkspaceID,
		actor: expected.CreatedBy, created: expected.CreatedAt, requestDigest: digest}
	_, rejectErr := e.store.RejectQueuedObserved(ctx, expected.ID,
		consolejobs.TransitionInput{Error: &consolejobs.JobError{Code: rejection, Message: "workspace operation was not started"}},
		consolejobs.EventInput{Kind: "rejected", Data: map[string]any{"code": rejection}},
		consolejobs.JobCommitObserverFunc(func(ctx context.Context, i consolejobs.JobCommitIntent) error {
			if i.Previous == nil || !bound.matches(*i.Previous) || i.Previous.Status != consolejobs.StatusQueued {
				return errWorkspaceIngressIdentity
			}
			return e.jobAuditObserver(deploymentaudit.StageJobFailedAuthorized, rejection).BeforeJobCommit(ctx, i)
		}))
	if errors.Is(rejectErr, consolejobs.ErrConflict) {
		current, getErr := e.store.Get(ctx, expected.ID)
		if getErr == nil && bound.matches(current) && current.StartedAt == nil && moduleActionTerminal(current.Status) {
			rejectErr = nil
		}
	}
	return consolejobs.Job{}, false, rejectErr
}

func (e *Executor) checkWorkspaceExecution(ctx context.Context, expected consolejobs.Job) error {
	if e.ingressCoordinator == nil && e.authorize == nil {
		return nil
	}
	current, err := e.store.Get(ctx, expected.ID)
	if err != nil || current.Status != consolejobs.StatusRunning || current.StartedAt == nil || expected.StartedAt == nil ||
		!current.StartedAt.Equal(*expected.StartedAt) {
		return errWorkspaceIngressIdentity
	}
	digest, err := workspaceJobDigest(expected)
	if err != nil {
		return err
	}
	bound := &workspaceIngressChange{id: expected.ID, kind: expected.Kind, workspace: expected.WorkspaceID,
		actor: expected.CreatedBy, created: expected.CreatedAt, requestDigest: digest}
	if !bound.matches(current) {
		return errWorkspaceIngressIdentity
	}
	if e.ingressCoordinator != nil {
		g := e.workspaceChange(expected.WorkspaceID)
		if !g.matches(current) {
			return errWorkspaceIngressIdentity
		}
		if ready, err := g.gate.Poll(); !ready || err != nil {
			return errWorkspaceIngressIdentity
		}
	}
	if e.authorize != nil && e.authorize(ctx, current) != nil {
		return errWorkspaceIngressDenied
	}
	return ctx.Err()
}
