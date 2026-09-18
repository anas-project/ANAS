package jobexecutor

import (
	"errors"
	"fmt"
	"testing"

	"github.com/anas-project/ANAS/internal/consolejobs"
)

func TestModuleActionQueueWaitIsNotAnExecutionFailure(t *testing.T) {
	for _, test := range []struct {
		name string
		err  error
		wait bool
	}{
		{name: "queue", err: &consolejobs.ActionInFlightError{ExistingJobID: "earlier", Policy: consolejobs.ActionQueue}, wait: true},
		{name: "wrapped-queue", err: fmt.Errorf("wrapped: %w", &consolejobs.ActionInFlightError{ExistingJobID: "earlier", Policy: consolejobs.ActionQueue}), wait: true},
		{name: "capacity", err: consolejobs.ErrCapacity, wait: true},
		{name: "workspace", err: consolejobs.ErrWorkspaceBusy, wait: true},
		{name: "compensation", err: consolejobs.ErrCompensationRequired, wait: true},
		{name: "reject", err: &consolejobs.ActionInFlightError{ExistingJobID: "earlier", Policy: consolejobs.ActionReject}},
		{name: "ambiguous-start", err: consolejobs.ErrConflict},
		{name: "containment", err: ErrModuleActionContainment},
		{name: "durable-barrier", err: consolejobs.ErrActionExecutionBlocked},
		{name: "other", err: errors.New("unconfirmed execution")},
		{name: "nil"},
		{name: "typed-nil", err: (*consolejobs.ActionInFlightError)(nil)},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := moduleActionQueueBlocked(test.err); got != test.wait {
				t.Fatalf("wait = %v, want %v", got, test.wait)
			}
		})
	}
}
