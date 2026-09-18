package deploymentaudit

import (
	"context"
	"testing"

	"github.com/anas-project/ANAS/internal/actionabi"
	"github.com/anas-project/ANAS/internal/consolejobs"
)

func TestActionJoinAuditUsesInvokingActorAndOriginalJob(t *testing.T) {
	var recorded Event
	observer := ObserveJobCommit(SinkFunc(func(_ context.Context, event Event) error {
		recorded = event
		return nil
	}), Event{Stage: StageJobCreateAuthorized})
	job := consolejobs.Job{
		ID: "job-original", Kind: consolejobs.ActionJobKind, CreatedBy: "original-admin", WorkspaceID: "workspace",
		Action: &consolejobs.ActionState{ABI: actionabi.Version, Name: "module.example.inspect", InvocationID: "original-invocation"},
	}
	if err := observer.BeforeJobCommit(context.Background(), consolejobs.JobCommitIntent{
		Operation: consolejobs.JobCommitActionJoin, Actor: "joining-admin", Previous: &job, Next: job,
	}); err != nil {
		t.Fatal(err)
	}
	if recorded.Actor != "joining-admin" || recorded.JobID != job.ID || recorded.Action != job.Action.Name || recorded.Stage != StageJobJoinedAuthorized {
		t.Fatalf("join audit confused the caller, creator or execution: %#v", recorded)
	}
	if job.CreatedBy != "original-admin" {
		t.Fatal("joining an action changed its immutable creator")
	}
}
