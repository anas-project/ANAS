package deploymentaudit

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/anas-project/ANAS/internal/consolejobs"
)

func TestObserverKeepsBindingsAndDoesNotForwardRequestValues(t *testing.T) {
	intent := consolejobs.JobCommitIntent{Next: consolejobs.Job{ID: "job", Kind: "deployment.apply", CreatedBy: "actor", WorkspaceID: "workspace", Request: map[string]any{"expected_digest": "digest", "password": "PRIVATE", "module": "module"}}, PlanJobID: "plan"}
	errRejected := errors.New("audit unavailable")
	observer := ObserveJobCommit(SinkFunc(func(_ context.Context, event Event) error {
		if event.JobID != "job" || event.PlanJobID != "plan" || event.Actor != "override" || event.PlanDigest != "digest" || event.TargetID != "module" || event.Stage != StageJobCreateAuthorized {
			t.Fatalf("wrong bindings: %#v", event)
		}
		data, _ := json.Marshal(event)
		if strings.Contains(string(data), "PRIVATE") {
			t.Fatal("raw request crossed audit boundary")
		}
		return errRejected
	}), Event{Actor: "override", Stage: StageJobCreateAuthorized})
	if err := observer.BeforeJobCommit(context.Background(), intent); !errors.Is(err, errRejected) {
		t.Fatalf("audit rejection lost: %v", err)
	}
	if err := ObserveJobCommit(nil, Event{}).BeforeJobCommit(context.Background(), intent); err == nil {
		t.Fatal("missing audit accepted")
	}
}

func TestAuditRejectionDoesNotCommitJobOrConsumeIdempotency(t *testing.T) {
	store, err := consolejobs.Open(filepath.Join(t.TempDir(), "jobs"), consolejobs.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	spec := consolejobs.CreateSpec{Kind: ActionApply, WorkspaceID: "workspace", Mutating: true, Request: map[string]any{}, Idempotency: consolejobs.IdempotencyInput{Principal: "operator", Method: "POST", CanonicalPath: "/api/v1/jobs", Key: "key", RequestDigest: consolejobs.DigestRequest([]byte("request"))}}
	rejected := errors.New("audit rejected")
	ctx := context.Background()
	_, err = store.CreateOrGetObserved(ctx, spec, ObserveJobCommit(SinkFunc(func(context.Context, Event) error { return rejected }), Event{}))
	if !errors.Is(err, rejected) {
		t.Fatalf("rejection lost: %v", err)
	}
	jobs, err := store.List(ctx)
	if err != nil || len(jobs) != 0 {
		t.Fatalf("state committed: %v %v", jobs, err)
	}
	result, err := store.CreateOrGetObserved(ctx, spec, ObserveJobCommit(SinkFunc(func(context.Context, Event) error { return nil }), Event{}))
	if err != nil || result.Existing {
		t.Fatalf("idempotency consumed: %v %v", result, err)
	}
}
