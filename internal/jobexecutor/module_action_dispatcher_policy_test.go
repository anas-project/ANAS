package jobexecutor

import (
	"context"
	"errors"
	"testing"

	"github.com/anas-project/ANAS/internal/consolejobs"
)

func TestModuleDispatcherKeylessCoalescingDoesNotInventRetryAliases(t *testing.T) {
	dispatcher, store, _ := moduleDispatcherFixture(t)
	definition, _ := dispatcher.registry.lookup("module.fixture.inspect")
	definition.Concurrency = consolejobs.ActionCoalesce
	registry, err := NewModuleActionRegistry([]ModuleActionDefinition{definition})
	if err != nil {
		t.Fatal(err)
	}
	dispatcher.registry = registry // No worker/process is started by this test.
	id := ""
	for index := 0; index < consolejobs.MaxActionRetryKeys+2; index++ {
		created, err := dispatcher.Invoke(context.Background(), "creator", "workspace-fixture", definition.Name, "", nil)
		if err != nil {
			t.Fatal(err)
		}
		if id == "" {
			id = created.Job.ID
		}
		if created.Job.ID != id || len(created.Job.Action.Policy.RetryKeys) != 0 {
			t.Fatal("keyless coalescing invented keys or another execution")
		}
	}
	jobs, err := store.List(context.Background())
	if err != nil || len(jobs) != 1 {
		t.Fatalf("keyless coalescing changed queue size: %v", err)
	}
}

func TestModuleDispatcherJoinReauthorizesTheJoiningActorAtCommit(t *testing.T) {
	dispatcher, store, _ := moduleDispatcherFixture(t)
	definition, _ := dispatcher.registry.lookup("module.fixture.inspect")
	definition.Concurrency = consolejobs.ActionCoalesce
	registry, err := NewModuleActionRegistry([]ModuleActionDefinition{definition})
	if err != nil {
		t.Fatal(err)
	}
	dispatcher.registry = registry
	original := invokeModuleDispatcherFixture(t, dispatcher, "original-key")
	joinChecked := false
	dispatcher.authorize = func(_ context.Context, access ModuleActionAccess) error {
		if access.Operation == "invoke" && access.Job.ID == original.ID {
			joinChecked = true
			if access.Actor != "joining-admin" {
				t.Error("join was authorized as the original creator")
			}
			return ErrModuleActionDenied
		}
		return nil
	}
	_, err = dispatcher.Invoke(context.Background(), "joining-admin", "workspace-fixture", definition.Name, "new-key", nil)
	if !errors.Is(err, ErrModuleActionDenied) || !joinChecked {
		t.Fatalf("join skipped current authorization: %v", err)
	}
	current, err := store.Get(context.Background(), original.ID)
	if err != nil || current.Revision != original.Revision || len(current.Action.Policy.RetryKeys) != 1 {
		t.Fatalf("rejected join persisted a key: %v", err)
	}
}

func TestModuleDispatcherRetryAndConflictRequireReadPermission(t *testing.T) {
	dispatcher, _, _ := moduleDispatcherFixture(t)
	original := invokeModuleDispatcherFixture(t, dispatcher, "existing-key")
	dispatcher.authorize = func(_ context.Context, access ModuleActionAccess) error {
		if access.Operation == "read" && access.Job.ID == original.ID {
			return ErrModuleActionDenied
		}
		return nil
	}
	for _, parameters := range []map[string]any{nil, {"different": true}} {
		result, err := dispatcher.Invoke(context.Background(), "other-admin", "workspace-fixture", "module.fixture.inspect", "existing-key", parameters)
		if !errors.Is(err, ErrModuleActionDenied) || result.Job.ID != "" {
			t.Fatalf("invoke exposed a job outside current read permissions: %v", err)
		}
		var conflict *consolejobs.IdempotencyConflictError
		if errors.As(err, &conflict) {
			t.Fatal("conflict exposed a hidden job ID")
		}
	}
}
