package consolejobs

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/anas-project/ANAS/internal/actionabi"
)

func TestActionExecutionBarrierSelection(t *testing.T) {
	for _, test := range []struct {
		name    string
		action  bool
		status  Status
		code    string
		blocked bool
	}{
		{"containment", true, StatusInterrupted, "execution_containment_lost", true},
		{"restart", true, StatusInterrupted, "daemon_restarted", true},
		{"confirmed_cleanup_unknown_result", true, StatusInterrupted, "execution_unconfirmed", false},
		{"legacy_restart", false, StatusInterrupted, "daemon_restarted", false},
		{"ordinary_failure", true, StatusFailed, "action_failed", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			job := Job{ID: "old-job", WorkspaceID: "old-workspace", Status: test.status, Error: &JobError{Code: test.code}}
			if test.action {
				job.Action = &ActionState{}
			}
			store := &Store{state: &storeState{jobs: map[string]Job{job.ID: job}}, options: Options{MaxRunningJobs: 8}}
			for _, mutating := range []bool{false, true} {
				err := store.canStart(Job{ID: "new-job", WorkspaceID: "different-workspace", Mutating: mutating})
				if errors.Is(err, ErrActionExecutionBlocked) != test.blocked {
					t.Fatalf("mutating=%v: got %v, blocked=%v", mutating, err, test.blocked)
				}
			}
		})
	}
}

func TestActionExecutionBarrierSurvivesReopenAndCompensationAcknowledgement(t *testing.T) {
	for _, recovery := range []bool{false, true} {
		name := "supervisor_containment"
		if recovery {
			name = "daemon_restart"
		}
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			store, directory := openStoreForTest(t, Options{})
			observer := JobCommitObserverFunc(func(context.Context, JobCommitIntent) error { return nil })
			lease, err := AcquireExecutionLease(ctx, directory)
			if err != nil {
				t.Fatal(err)
			}
			defer lease.Close()
			created, err := store.CreateActionObserved(ctx, testCreateSpec("unclean", "workspace-a", true), "module.incus.fixture", "invocation-unclean", observer)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := store.StartActionObserved(ctx, created.Job.ID, lease, observer); err != nil {
				t.Fatal(err)
			}
			if recovery {
				err = store.RecoverInterruptedJobsObserved(ctx, lease, observer)
			} else {
				_, err = store.CompleteActionObserved(ctx, lease, actionabi.Event{
					ABI: actionabi.Version, JobID: created.Job.ID, InvocationID: created.Job.Action.InvocationID, Type: "error",
					Error: &actionabi.Failure{Outcome: actionabi.Unknown, Code: "execution_containment_lost", Message: "Process cleanup could not be confirmed"},
				}, observer)
			}
			if err != nil {
				t.Fatal(err)
			}
			if _, err := store.AcknowledgeCompensation(ctx, created.Job.ID, "Ordinary compensation acknowledgement"); err != nil {
				t.Fatal(err)
			}
			if err := store.Compact(ctx); err != nil {
				t.Fatal(err)
			}
			if err := store.Close(); err != nil {
				t.Fatal(err)
			}
			reopened, err := Open(directory, Options{})
			if err != nil {
				t.Fatal(err)
			}
			defer reopened.Close()
			assertBlocked := func(err error) {
				t.Helper()
				var blocked *ActionExecutionBlockedError
				if !errors.Is(err, ErrActionExecutionBlocked) || !errors.As(err, &blocked) || !reflect.DeepEqual(blocked.JobIDs, []string{created.Job.ID}) {
					t.Fatalf("want durable execution barrier for %s, got %v", created.Job.ID, err)
				}
			}
			for _, mutating := range []bool{false, true} {
				key := "read-only"
				if mutating {
					key = "mutating"
				}
				legacy := createJobForTest(t, reopened, testCreateSpec(key, "workspace-b", mutating))
				_, err := reopened.Start(ctx, legacy.ID)
				assertBlocked(err)
			}
			_, _, err = reopened.ClaimNextObserved(ctx, "workspace-b", observer)
			assertBlocked(err)
			next, err := reopened.CreateActionObserved(ctx, testCreateSpec("next-action", "workspace-c", false), "module.incus.fixture", "invocation-next", observer)
			if err != nil {
				t.Fatal(err)
			}
			_, err = reopened.StartActionObserved(ctx, next.Job.ID, lease, observer)
			assertBlocked(err)
			// Reading/replay and cancelling a job that never started remain
			// available; the barrier is not a lockout from diagnosis.
			page, err := reopened.ReplayAction(ctx, created.Job.ID, 0, 10)
			if err != nil || page.Outcome != actionabi.Unknown {
				t.Fatalf("read unknown receipt: outcome=%s, err=%v", page.Outcome, err)
			}
			if _, err := reopened.CancelQueuedActionObserved(ctx, next.Job.ID, next.Job.Action.InvocationID, observer); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestActionExecutionBarrierReportsStableDetachedIDs(t *testing.T) {
	makeJob := func(id string) Job {
		return Job{ID: id, Status: StatusInterrupted, Action: &ActionState{}, Error: &JobError{Code: "execution_containment_lost"}}
	}
	store := &Store{state: &storeState{jobs: map[string]Job{"z": makeJob("z"), "a": makeJob("a")}}}
	var first *ActionExecutionBlockedError
	if !errors.As(store.actionExecutionBarrier(), &first) || !reflect.DeepEqual(first.JobIDs, []string{"a", "z"}) {
		t.Fatalf("unexpected blocked job IDs: %v", first)
	}
	first.JobIDs[0] = "changed-by-caller"
	var second *ActionExecutionBlockedError
	if !errors.As(store.actionExecutionBarrier(), &second) || !reflect.DeepEqual(second.JobIDs, []string{"a", "z"}) {
		t.Fatalf("caller mutated durable blocker IDs: %v", second)
	}
}
