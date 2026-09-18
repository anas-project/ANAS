package consolejobs

import (
	"errors"
	"sort"
)

// ErrActionExecutionBlocked means a previous action may still own processes or
// an in-flight event writer. It is not an ordinary compensation requirement.
// Recreating a registry, reopening the store or acknowledging compensation does
// not prove that the previous execution has stopped.
var ErrActionExecutionBlocked = errors.New("action process cleanup is unconfirmed; job execution is blocked")

// ActionExecutionBlockedError identifies the durable receipts that require
// independent execution-owner recovery. Only opaque job identifiers are exposed.
type ActionExecutionBlockedError struct {
	JobIDs []string
}

func (*ActionExecutionBlockedError) Error() string { return ErrActionExecutionBlocked.Error() }
func (*ActionExecutionBlockedError) Unwrap() error { return ErrActionExecutionBlocked }

// actionExecutionBarrier is checked under jobs.lock BEFORE either read-only or
// mutating jobs can start. The error code is the supervisor/recovery's durable
// receipt, not a process state supplied by an executor. This deliberately spans
// workspaces sharing the same execution owner. No automatic reset is provided;
// a future recovery action must prove process/writer containment independently.
func (store *Store) actionExecutionBarrier() error {
	var blocked []string
	for _, job := range store.state.jobs {
		if job.Action == nil || job.Status != StatusInterrupted || job.Error == nil {
			continue
		}
		switch job.Error.Code {
		case "execution_containment_lost", "daemon_restarted":
			blocked = append(blocked, job.ID)
		}
	}
	if len(blocked) == 0 {
		return nil
	}
	sort.Strings(blocked)
	return &ActionExecutionBlockedError{JobIDs: blocked}
}
