package consolejobs

import "context"

// RetainActionExecution gives an execution-owner adapter a current running job
// while retaining the SAME store's existing execution lease. It does not claim
// queued work, recover a previous owner, append a result, or authorize an actor.
// The adapter must check its frozen request/registry and hold release until all
// supervised work and I/O are confirmed stopped. A job id alone is not a lease.
func (store *Store) RetainActionExecution(ctx context.Context, lease *ExecutionLease, jobID, invocationID string) (Job, func(), error) {
	if store == nil || ctx == nil || lease == nil || jobID == "" || invocationID == "" {
		return Job{}, nil, ErrUnavailable
	}
	if err := ctx.Err(); err != nil {
		return Job{}, nil, err
	}
	release, err := lease.Retain()
	if err != nil {
		return Job{}, nil, err
	}
	var job Job
	err = lease.withOwnership(store.directory, func() error {
		var err error
		job, err = store.Get(ctx, jobID)
		if err != nil {
			return err
		}
		if job.Kind != ActionJobKind || job.Status != StatusRunning || job.StartedAt == nil ||
			job.Action == nil || job.Action.InvocationID != invocationID || job.Action.Outcome != "" {
			return ErrConflict
		}
		return ctx.Err()
	})
	if err != nil {
		release()
		return Job{}, nil, err
	}
	return job, release, nil
}
