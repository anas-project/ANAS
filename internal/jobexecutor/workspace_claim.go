package jobexecutor

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/anas-project/ANAS/internal/consolejobs"
	"github.com/anas-project/ANAS/internal/deploymentaudit"
)

var (
	errWorkspaceJobIdentity = errors.New("workspace job binding changed; recovery is required")
	errWorkspaceJobDenied   = errors.New("workspace job is no longer authorized")
)

// Bind immutable fields only: progress, revision and timestamps of transitions
// legitimately change as the original Store commits the operation.
type workspaceJobBinding struct {
	id, kind, workspace, actor, requestDigest string
	created                                   time.Time
}

func workspaceJobDigest(job consolejobs.Job) (string, error) {
	body, err := json.Marshal(job.Request)
	if err != nil {
		return "", errWorkspaceJobIdentity
	}
	return consolejobs.DigestRequest(body), nil
}

func bindWorkspaceJob(job consolejobs.Job) (workspaceJobBinding, error) {
	digest, err := workspaceJobDigest(job)
	if err != nil {
		return workspaceJobBinding{}, err
	}
	return workspaceJobBinding{id: job.ID, kind: job.Kind, workspace: job.WorkspaceID, actor: job.CreatedBy,
		created: job.CreatedAt, requestDigest: digest}, nil
}

func (b workspaceJobBinding) matches(job consolejobs.Job) bool {
	digest, err := workspaceJobDigest(job)
	return err == nil && job.Action == nil && job.Mutating &&
		b.id == job.ID && b.kind == job.Kind && b.workspace == job.WorkspaceID &&
		b.actor == job.CreatedBy && b.created.Equal(job.CreatedAt) && b.requestDigest == digest
}

// claimWorkspaceJob re-authorizes the persisted actor inside ClaimNext's
// observer, before running is persisted. The observer never waits, reenters
// the job store or performs host work on jobs.lock. A revoked actor's job is
// rejected while still queued; it is not turned into a running job or a user
// cancellation.
func (e *Executor) claimWorkspaceJob(ctx context.Context, workspace string) (consolejobs.Job, bool, error) {
	var candidate *consolejobs.Job
	var rejection string
	audit := e.jobAuditObserver(deploymentaudit.StageJobStartAuthorized, "")
	observer := consolejobs.JobCommitObserverFunc(func(ctx context.Context, intent consolejobs.JobCommitIntent) error {
		job := intent.Next
		if intent.Previous == nil || intent.Previous.Status != consolejobs.StatusQueued || job.Action != nil ||
			job.WorkspaceID != workspace || !job.Mutating {
			return errWorkspaceJobIdentity
		}
		candidate = &job
		if e.authorize != nil && e.authorize(ctx, job) != nil {
			rejection = "job_authorization_revoked"
			return errWorkspaceJobDenied
		}
		return audit.BeforeJobCommit(ctx, intent)
	})
	job, found, err := e.store.ClaimNextObserved(ctx, workspace, observer)
	if err == nil || candidate == nil || rejection == "" {
		return job, found, err
	}
	// Recheck the original immutable input and queued state under jobs.lock.
	expected := *candidate
	bound, bindErr := bindWorkspaceJob(expected)
	if bindErr != nil {
		return consolejobs.Job{}, false, bindErr
	}
	_, rejectErr := e.store.RejectQueuedObserved(ctx, expected.ID,
		consolejobs.TransitionInput{Error: &consolejobs.JobError{Code: rejection, Message: "workspace operation was not started"}},
		consolejobs.EventInput{Kind: "rejected", Data: map[string]any{"code": rejection}},
		consolejobs.JobCommitObserverFunc(func(ctx context.Context, i consolejobs.JobCommitIntent) error {
			if i.Previous == nil || !bound.matches(*i.Previous) || i.Previous.Status != consolejobs.StatusQueued {
				return errWorkspaceJobIdentity
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

// checkWorkspaceExecution rereads the durable claim immediately before any
// factory runs, so a substituted request or a revoked actor cannot execute.
func (e *Executor) checkWorkspaceExecution(ctx context.Context, expected consolejobs.Job) error {
	if e.authorize == nil {
		return nil
	}
	current, err := e.store.Get(ctx, expected.ID)
	if err != nil || current.Status != consolejobs.StatusRunning || current.StartedAt == nil || expected.StartedAt == nil ||
		!current.StartedAt.Equal(*expected.StartedAt) {
		return errWorkspaceJobIdentity
	}
	bound, err := bindWorkspaceJob(expected)
	if err != nil {
		return err
	}
	if !bound.matches(current) {
		return errWorkspaceJobIdentity
	}
	if e.authorize(ctx, current) != nil {
		return errWorkspaceJobDenied
	}
	return ctx.Err()
}
