package main

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"
)

type cancellationCompute struct {
	fakeCompute
	stage          string
	cancel         context.CancelFunc
	deleteErr      error
	deleteContexts []error
	beforeCreate   func(InstanceSpec)
}

func (f *cancellationCompute) Inspect(_ context.Context, id string) (Instance, error) {
	for _, spec := range f.created {
		if spec.ID == id {
			return Instance{ID: id, State: "running", WorkloadID: spec.WorkloadID}, nil
		}
	}
	return Instance{ID: id, State: "missing"}, nil
}

func (f *cancellationCompute) Create(ctx context.Context, spec InstanceSpec) error {
	if f.beforeCreate != nil {
		f.beforeCreate(spec)
	}
	_ = f.fakeCompute.Create(ctx, spec)
	if f.stage == "create" {
		f.cancel()
		return ctx.Err()
	}
	return nil
}
func (f *cancellationCompute) Start(ctx context.Context, id string) error {
	_ = f.fakeCompute.Start(ctx, id)
	if f.stage == "start" {
		f.cancel()
		return ctx.Err()
	}
	return nil
}
func (f *cancellationCompute) ExecStdin(ctx context.Context, id string, args []string, stdin io.Reader) error {
	if f.stage == "exec" {
		f.cancel()
		return ctx.Err()
	}
	return f.fakeCompute.ExecStdin(ctx, id, args, stdin)
}
func (f *cancellationCompute) Delete(ctx context.Context, id string) error {
	f.deleted = append(f.deleted, id)
	f.deleteContexts = append(f.deleteContexts, ctx.Err())
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if _, ok := ctx.Deadline(); !ok {
		return errors.New("cleanup has no deadline")
	}
	return f.deleteErr
}

func TestProvisionCancellationCleansUncertainCreateStartAndExec(t *testing.T) {
	for _, stage := range []string{"create", "start", "exec"} {
		t.Run(stage, func(t *testing.T) {
			c, api, _, store, _ := controllerFixture()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			provider := &cancellationCompute{stage: stage, cancel: cancel}
			c.compute = provider
			api.jobs = []ActionJob{{ID: 42, Handle: "cancel-job", Status: "waiting", RunsOn: []string{"docker"}}}
			err := c.Reconcile(ctx)
			if !errors.Is(err, context.Canceled) {
				t.Error("lost original cancellation", err)
			}
			if len(provider.deleted) != 1 || provider.deleted[0] != instanceIDFor("cancel-job") {
				t.Error("possibly created instance not reclaimed", provider.deleted)
			}
			for _, canceled := range provider.deleteContexts {
				if canceled != nil {
					t.Error("cleanup reused canceled context")
				}
			}
			if len(store.state.Workloads) != 0 {
				t.Error("successful cleanup left work pending")
			}
			if len(api.deleted) != 1 {
				t.Error("registration was not reclaimed")
			}
		})
	}
}

func TestProvisionPersistsInstanceIntentBeforeCreate(t *testing.T) {
	c, api, _, store, _ := controllerFixture()
	provider := &cancellationCompute{beforeCreate: func(spec InstanceSpec) {
		if store.state.Workloads[spec.WorkloadID].InstanceID != spec.ID {
			t.Error("Create ran before recoverable identity was stored")
		}
	}}
	c.compute = provider
	api.jobs = []ActionJob{{ID: 42, Handle: "intent-job", Status: "waiting", RunsOn: []string{"docker"}}}
	if err := c.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestCanceledCleanupFailureIsRetriedEvenWhileQueueIsUnavailable(t *testing.T) {
	c, api, _, store, _ := controllerFixture()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	provider := &cancellationCompute{stage: "start", cancel: cancel, deleteErr: errors.New("cleanup temporarily unavailable")}
	c.compute = provider
	api.jobs = []ActionJob{{ID: 42, Handle: "retry-job", Status: "waiting", RunsOn: []string{"docker"}}}
	if err := c.Reconcile(ctx); !errors.Is(err, context.Canceled) {
		t.Error("lost cancellation during failed cleanup", err)
	}
	if store.state.Workloads["retry-job"].Phase != "retiring" {
		t.Error("failed cleanup lacks durable retirement intent")
	}
	provider.stage, provider.deleteErr = "", nil
	api.listErr = errors.New("queue API unavailable")
	retry, stop := context.WithTimeout(context.Background(), time.Second)
	defer stop()
	_ = c.Reconcile(retry)
	if len(store.state.Workloads) != 0 || len(provider.deleted) != 2 {
		t.Error("retirement waited for queue observation")
	}
	if api.created != 1 {
		t.Error("retirement created a replacement")
	}
}
