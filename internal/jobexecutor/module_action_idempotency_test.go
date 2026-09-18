package jobexecutor

import (
	"context"
	"errors"
	"testing"

	"github.com/anas-project/ANAS/internal/consolejobs"
)

func TestModuleActionRetryReauthorizesTheCurrentCaller(t *testing.T) {
	store, _, original, job := moduleActionQueueFixture(t)
	definition, _ := original.lookup(job.Action.Name)
	if definition.Concurrency != consolejobs.ActionReject {
		t.Fatal("unspecified concurrency must default to reject")
	}
	allowed := false
	checks := 0
	definition.Check = func(_ context.Context, call ModuleActionCall) error {
		checks++
		if call.Actor != "new-admin" || !allowed {
			return errors.New("permission revoked")
		}
		return nil
	}
	registry, err := NewModuleActionRegistry([]ModuleActionDefinition{definition})
	if err != nil {
		t.Fatal(err)
	}
	spec := consolejobs.CreateSpec{
		WorkspaceID: job.WorkspaceID, Request: map[string]any{"mode": "inspect"},
		Idempotency: consolejobs.IdempotencyInput{Principal: "new-admin", Key: "test-key", Method: "CLI", CanonicalPath: "/cli/invoke"},
	}
	if _, err := registry.Create(context.Background(), store, spec, job.Action.Name, moduleActionAllowCommit()); !errors.Is(err, ErrModuleActionDenied) {
		t.Fatalf("retry bypassed current authorization: %v", err)
	}
	allowed = true
	retry, err := registry.Create(context.Background(), store, spec, job.Action.Name, moduleActionAllowCommit())
	if err != nil || !retry.Existing || retry.Job.ID != job.ID || checks != 2 {
		t.Fatalf("authorized cross-entry retry lost its job: %v", err)
	}
}

func TestModuleActionRejectsAChangedFrozenConcurrencyPolicy(t *testing.T) {
	store, lease, original, job := moduleActionQueueFixture(t)
	definition, _ := original.lookup(job.Action.Name)
	definition.Concurrency = consolejobs.ActionQueue
	registry, err := NewModuleActionRegistry([]ModuleActionDefinition{definition})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := registry.Run(context.Background(), store, lease, job.ID, nil, moduleActionAllowCommit()); !errors.Is(err, ErrModuleActionUnavailable) {
		t.Fatalf("changed policy was accepted for a queued job: %v", err)
	}
	current, err := store.Get(context.Background(), job.ID)
	if err != nil || current.StartedAt != nil || current.Status != consolejobs.StatusFailed || current.NeedsCompensationCheck {
		t.Fatalf("frozen-policy rejection executed or claimed side effects: %v", err)
	}
}
