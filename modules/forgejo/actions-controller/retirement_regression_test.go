package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

type retiringCompute struct {
	fakeCompute
	instance Instance
	list     []Instance
}

func (f *retiringCompute) Inspect(_ context.Context, id string) (Instance, error) {
	if f.instance.ID == id {
		return f.instance, nil
	}
	return Instance{ID: id, State: "missing"}, nil
}
func (f *retiringCompute) ListManaged(context.Context) ([]Instance, error) { return f.list, nil }
func (f *retiringCompute) Delete(ctx context.Context, id string) error {
	f.instance = Instance{ID: id, State: "missing"}
	return f.fakeCompute.Delete(ctx, id)
}

func TestUnknownCreateRemainsRecoverableUntilLateInstanceAppears(t *testing.T) {
	c, api, _, store, now := controllerFixture()
	id := instanceIDFor("late-job")
	store.state.Workloads["late-job"] = Workload{Handle: "late-job", Scope: "team/repo", InstanceID: id, CreatePending: true, Phase: "creating", CreatedAt: now}
	provider := &retiringCompute{}
	c.compute = provider
	api.jobs = []ActionJob{{ID: 42, Handle: "late-job", Status: "waiting", RunsOn: []string{"docker"}}}
	if err := c.Reconcile(context.Background()); err == nil {
		t.Fatal("temporary absence was reported as completed cleanup")
	}
	if !store.state.Workloads["late-job"].CreatePending || len(provider.deleted) != 0 || api.created != 0 {
		t.Fatal("uncertain creation lost its guard")
	}
	provider.instance = Instance{ID: id, State: "running", WorkloadID: "late-job"}
	if err := c.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(store.state.Workloads) != 0 || len(provider.deleted) != 1 || api.created != 0 {
		t.Fatal("late create did not converge without immediate reprovisioning")
	}
}

type blockedQueue struct {
	*fakeForgejo
	before func()
}

func (f blockedQueue) ListJobs(ctx context.Context, _ Scope, _ string) ([]ActionJob, error) {
	f.before()
	<-ctx.Done()
	return nil, ctx.Err()
}

func TestRetirementRunsBeforeBlockedQueueConsumesBudget(t *testing.T) {
	c, api, compute, store, now := controllerFixture()
	store.state.Workloads["retiring-job"] = Workload{Handle: "retiring-job", Scope: "team/repo", InstanceID: instanceIDFor("retiring-job"), Phase: "retiring", CreatedAt: now}
	c.forgejo = blockedQueue{api, func() {
		if len(compute.deleted) != 1 {
			t.Error("queue request started before retirement")
		}
	}}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if err := c.Reconcile(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
	if len(store.state.Workloads) != 0 {
		t.Fatal("blocked queue starved retirement")
	}
}

func TestCleanupAllCannotSweepAnInstanceWhoseOwnershipWasRejected(t *testing.T) {
	c, _, _, store, now := controllerFixture()
	id := instanceIDFor("collision")
	store.state.Workloads["collision"] = Workload{Handle: "collision", Scope: "team/repo", InstanceID: id, Phase: "retiring", CreatedAt: now}
	other := Instance{ID: id, State: "running", WorkloadID: "different-job"}
	provider := &retiringCompute{instance: other, list: []Instance{other}}
	c.compute = provider
	if err := c.CleanupAll(context.Background()); err == nil {
		t.Fatal("ownership mismatch accepted")
	}
	if len(provider.deleted) != 0 || len(store.state.Workloads) != 1 {
		t.Fatal("orphan sweep bypassed failed ownership check")
	}
}

func TestFileStateStoreDoesNotReusePredictableTemporaryFile(t *testing.T) {
	dir := t.TempDir()
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	store := FileStateStore{Path: filepath.Join(dir, "state.json")}
	if err := os.WriteFile(store.Path+".tmp", []byte("unrelated unfinished state"), 0600); err != nil {
		t.Fatal(err)
	}
	state := newMemoryStore().state
	state.Workloads["pending"] = Workload{Handle: "pending", Phase: "retiring", InstanceID: instanceIDFor("pending"), CreatePending: true}
	if err := store.Save(state); err != nil {
		t.Fatal(err)
	}
	loaded, err := store.Load()
	if err != nil || !loaded.Workloads["pending"].CreatePending {
		t.Fatal("reopened state lost uncertain create", err)
	}
	if data, err := os.ReadFile(store.Path + ".tmp"); err != nil || string(data) != "unrelated unfinished state" {
		t.Fatal("old temporary file overwritten", err)
	}
}

type failedStateStore struct{ *memoryStore }

func (s failedStateStore) Save(ControllerState) error { return errors.New("state storage unavailable") }

func TestRegistrationIsCompensatedEvenWhenAllStateWritesFail(t *testing.T) {
	c, api, compute, store, _ := controllerFixture()
	c.store = failedStateStore{store}
	api.jobs = []ActionJob{{ID: 42, Handle: "disk-failed", Status: "waiting", RunsOn: []string{"docker"}}}
	if err := c.Reconcile(context.Background()); err == nil {
		t.Fatal("failed persistence was hidden")
	}
	if len(api.deleted) != 1 || len(compute.created) != 0 {
		t.Fatal("failed storage stranded registration or permitted a create")
	}
}

func TestNewComputeAcceptsCancellationBeforeConfigurationWrites(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	dir := filepath.Join(t.TempDir(), "not-created")
	if _, err := newCompute(ctx, Config{ConfigDir: dir}); !errors.Is(err, context.Canceled) {
		t.Fatal("factory lost caller context", err)
	}
	if _, err := os.Lstat(dir); !os.IsNotExist(err) {
		t.Fatal("canceled factory created configuration", err)
	}
}
