package consolejobs

import (
	"errors"
	"fmt"
)

var (
	ErrUnavailable          = errors.New("console job store unavailable")
	ErrInvalid              = errors.New("invalid console job input")
	ErrNotFound             = errors.New("console job not found")
	ErrConflict             = errors.New("console job conflict")
	ErrIdempotencyConflict  = errors.New("idempotency key conflict")
	ErrEventGap             = errors.New("console job event gap")
	ErrWorkspaceBusy        = errors.New("workspace mutation already running")
	ErrCompensationRequired = errors.New("workspace compensation check required")
	ErrCapacity             = errors.New("console job capacity reached")
	ErrConfirmationInvalid  = errors.New("console job confirmation is invalid")
	ErrConfirmationConsumed = errors.New("console job confirmation was already consumed")
	ErrStepUpInvalid        = errors.New("console job step-up proof is invalid")
	ErrStepUpConsumed       = errors.New("console job step-up proof was already consumed")
)

type PersistenceError struct {
	Operation string
	Cause     error
}

func (err *PersistenceError) Error() string {
	if err == nil {
		return ErrUnavailable.Error()
	}
	return fmt.Sprintf("%s: %v", err.Operation, err.Cause)
}

func (err *PersistenceError) Unwrap() []error {
	if err == nil || err.Cause == nil {
		return []error{ErrUnavailable}
	}
	return []error{ErrUnavailable, err.Cause}
}

type IdempotencyConflictError struct {
	ExistingJobID string
}

func (err *IdempotencyConflictError) Error() string {
	return fmt.Sprintf("%s: key already belongs to job %s with a different request digest", ErrIdempotencyConflict, err.ExistingJobID)
}

func (*IdempotencyConflictError) Unwrap() error { return ErrIdempotencyConflict }

type EventGapError struct {
	JobID           string
	RequestedAfter  uint64
	PrunedThrough   uint64
	OldestAvailable uint64
	LatestID        uint64
}

func (err *EventGapError) Error() string {
	return fmt.Sprintf("%s: job %s cursor %d precedes prune watermark %d", ErrEventGap, err.JobID, err.RequestedAfter, err.PrunedThrough)
}

func (*EventGapError) Unwrap() error { return ErrEventGap }

type WorkspaceBusyError struct {
	WorkspaceID  string
	RunningJobID string
}

func (err *WorkspaceBusyError) Error() string {
	return fmt.Sprintf("%s: workspace %s is held by job %s", ErrWorkspaceBusy, err.WorkspaceID, err.RunningJobID)
}

func (*WorkspaceBusyError) Unwrap() error { return ErrWorkspaceBusy }

type CompensationRequiredError struct {
	WorkspaceID string
	JobIDs      []string
}

func (err *CompensationRequiredError) Error() string {
	return fmt.Sprintf("%s: workspace %s has %d unchecked interrupted jobs", ErrCompensationRequired, err.WorkspaceID, len(err.JobIDs))
}

func (*CompensationRequiredError) Unwrap() error { return ErrCompensationRequired }

type CapacityError struct {
	Resource string
	Limit    int
	Current  int
}

func (err *CapacityError) Error() string {
	return fmt.Sprintf("%s: %s has %d entries (limit %d)", ErrCapacity, err.Resource, err.Current, err.Limit)
}

func (*CapacityError) Unwrap() error { return ErrCapacity }

func invalidError(message string) error {
	return fmt.Errorf("%w: %s", ErrInvalid, message)
}
