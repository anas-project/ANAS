package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"
	"time"
)

var errFixtureEngineAdmission = errors.New("guest engine admission rejected")

// The real shell admission is exercised separately. This adapter models its
// failure after a confirmed create/start but before any guest token read.
type rejectedEngineCompute struct {
	fakeCompute
	cleanupErr     error
	deleteAttempts int
	boundedCleanup bool
}

func (f *rejectedEngineCompute) Inspect(_ context.Context, id string) (Instance, error) {
	for _, removed := range f.deleted {
		if removed == id {
			return Instance{ID: id, State: "missing"}, nil
		}
	}
	for _, spec := range f.created {
		if spec.ID == id {
			return Instance{ID: id, State: "running", WorkloadID: spec.WorkloadID}, nil
		}
	}
	return Instance{ID: id, State: "missing"}, nil
}

func (f *rejectedEngineCompute) ExecStdin(_ context.Context, id string, _ []string, _ io.Reader) error {
	f.execID = id
	return errFixtureEngineAdmission
}

func (f *rejectedEngineCompute) Delete(ctx context.Context, id string) error {
	f.deleteAttempts++
	deadline, ok := ctx.Deadline()
	f.boundedCleanup = ok && time.Until(deadline) <= 2*time.Minute && ctx.Err() == nil
	if f.cleanupErr != nil {
		return f.cleanupErr
	}
	return f.fakeCompute.Delete(ctx, id)
}

func TestEngineAdmissionRejectionRetiresInstanceAndRegistration(t *testing.T) {
	for _, retry := range []bool{false, true} {
		name := "confirmed-cleanup"
		if retry {
			name = "retry-before-unavailable-queue"
		}
		t.Run(name, func(t *testing.T) {
			c, api, _, store, _ := controllerFixture()
			compute := &rejectedEngineCompute{}
			if retry {
				compute.cleanupErr = errors.New("temporary deletion failure")
			}
			c.compute = compute
			api.jobs = []ActionJob{{ID: 42, Handle: "engine-unavailable", Status: "waiting", RunsOn: []string{"docker"}}}
			ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
			defer cancel()
			err := c.Reconcile(ctx)
			if !errors.Is(err, errFixtureEngineAdmission) || api.created != 1 || len(compute.created) != 1 || len(compute.started) != 1 || compute.execID == "" {
				t.Fatal("engine failure was hidden or provisioning was not exercised", err)
			}
			if !compute.boundedCleanup || compute.deleteAttempts != 1 || len(api.deleted) != 1 {
				t.Fatal("missing bounded compensation")
			}
			body, err := json.Marshal(store.state)
			if err != nil || strings.Contains(string(body), api.registration.Token) || compute.stdin != "" {
				t.Fatal("token leaked into state or guest adapter")
			}
			if retry {
				if store.state.Workloads["engine-unavailable"].Phase != "retiring" {
					t.Fatal("failed cleanup lost durable retirement")
				}
				compute.cleanupErr = nil
				api.listErr = errors.New("queue unavailable")
				if err := c.Reconcile(ctx); !errors.Is(err, api.listErr) {
					t.Fatal(err)
				}
				if compute.deleteAttempts != 2 {
					t.Fatal("unavailable queue blocked retirement")
				}
			}
			if len(store.state.Workloads) != 0 || len(compute.deleted) != 1 || api.created != 1 {
				t.Fatal("rejected engine stranded or reprovisioned an instance")
			}
		})
	}
}

func TestFailedEngineCompensationStillConsumesScopeCapacity(t *testing.T) {
	c, api, _, store, _ := controllerFixture()
	c.cfg.MaxPerScope = 1
	compute := &rejectedEngineCompute{cleanupErr: errors.New("deletion temporarily unavailable")}
	c.compute = compute
	api.jobs = []ActionJob{
		{ID: 41, Handle: "first-engine-failure", Status: "waiting", RunsOn: []string{"docker"}},
		{ID: 42, Handle: "second-engine-failure", Status: "waiting", RunsOn: []string{"docker"}},
	}
	if err := c.Reconcile(context.Background()); !errors.Is(err, errFixtureEngineAdmission) {
		t.Fatal("expected an observed engine failure", err)
	}
	// Map iteration chooses either handle. Regardless of order, an instance
	// whose cleanup is uncertain must occupy the scope's only slot immediately.
	if api.created != 1 || len(compute.created) != 1 || len(store.state.Workloads) != 1 {
		t.Fatalf("failed compensation bypassed scope quota: registrations=%d instances=%d retained=%d", api.created, len(compute.created), len(store.state.Workloads))
	}
	for _, workload := range store.state.Workloads {
		if workload.Phase != "retiring" {
			t.Fatal("uncertain cleanup must retain retirement intent")
		}
	}
	// Successful retirement on the next pass releases exactly that slot. The
	// old handle backs off; the other waiting job may now make one attempt.
	compute.cleanupErr = nil
	if err := c.Reconcile(context.Background()); !errors.Is(err, errFixtureEngineAdmission) {
		t.Fatal(err)
	}
	if api.created != 2 || len(compute.created) != 2 || len(store.state.Workloads) != 0 {
		t.Fatal("confirmed retirement did not release scope capacity correctly")
	}
}

func TestConfirmedEngineCompensationDoesNotReservePhantomScopeSlot(t *testing.T) {
	c, api, _, store, _ := controllerFixture()
	c.cfg.MaxPerScope = 1
	compute := &rejectedEngineCompute{}
	c.compute = compute
	api.jobs = []ActionJob{
		{ID: 41, Handle: "first-cleaned-failure", Status: "waiting", RunsOn: []string{"docker"}},
		{ID: 42, Handle: "second-cleaned-failure", Status: "waiting", RunsOn: []string{"docker"}},
	}
	if err := c.Reconcile(context.Background()); !errors.Is(err, errFixtureEngineAdmission) {
		t.Fatal(err)
	}
	if api.created != 2 || len(compute.deleted) != 2 || len(store.state.Workloads) != 0 {
		t.Fatal("completed compensation unnecessarily blocked the other waiting job")
	}
}
