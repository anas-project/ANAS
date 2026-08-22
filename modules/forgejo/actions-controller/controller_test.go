package main

import (
	"context"
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"
	"time"
)

type memoryStore struct{ state ControllerState }

func newMemoryStore() *memoryStore {
	return &memoryStore{state: ControllerState{Version: controllerStateVersion, Workloads: map[string]Workload{}, RetryAfter: map[string]time.Time{}}}
}

func (s *memoryStore) Load() (ControllerState, error) { return cloneState(s.state), nil }
func (s *memoryStore) Save(state ControllerState) error {
	s.state = cloneState(state)
	return nil
}

func cloneState(in ControllerState) ControllerState {
	out := ControllerState{Version: in.Version, Workloads: map[string]Workload{}, RetryAfter: map[string]time.Time{}}
	for key, value := range in.Workloads {
		out.Workloads[key] = value
	}
	for key, value := range in.RetryAfter {
		out.RetryAfter[key] = value
	}
	return out
}

type fakeForgejo struct {
	jobs         []ActionJob
	created      int
	deleted      []int64
	registration RunnerRegistration
	listErr      error
}

func (f *fakeForgejo) ListJobs(context.Context, Scope, string) ([]ActionJob, error) {
	return append([]ActionJob{}, f.jobs...), f.listErr
}
func (f *fakeForgejo) CreateRunner(context.Context, Scope, string) (RunnerRegistration, error) {
	f.created++
	return f.registration, nil
}
func (f *fakeForgejo) DeleteRunner(_ context.Context, _ Scope, id int64) error {
	f.deleted = append(f.deleted, id)
	return nil
}

type fakeCompute struct {
	created []InstanceSpec
	started []string
	deleted []string
	execID  string
	execArg []string
	stdin   string
}

func (f *fakeCompute) Create(_ context.Context, spec InstanceSpec) error {
	f.created = append(f.created, spec)
	return nil
}
func (f *fakeCompute) Inspect(context.Context, string) (Instance, error) { return Instance{}, nil }
func (f *fakeCompute) Start(_ context.Context, id string) error {
	f.started = append(f.started, id)
	return nil
}
func (f *fakeCompute) ExecStdin(_ context.Context, id string, args []string, stdin io.Reader) error {
	f.execID, f.execArg = id, append([]string{}, args...)
	body, _ := io.ReadAll(stdin)
	f.stdin = string(body)
	return nil
}
func (f *fakeCompute) Stop(context.Context, string) error { return nil }
func (f *fakeCompute) Delete(_ context.Context, id string) error {
	f.deleted = append(f.deleted, id)
	return nil
}
func (f *fakeCompute) ListManaged(context.Context) ([]Instance, error) { return nil, nil }

func controllerFixture() (*Controller, *fakeForgejo, *fakeCompute, *memoryStore, time.Time) {
	now := time.Date(2026, 8, 22, 12, 0, 0, 0, time.UTC)
	api := &fakeForgejo{registration: RunnerRegistration{ID: 7, UUID: "runner-uuid", Token: strings.Repeat("a", 40)}}
	compute := &fakeCompute{}
	store := newMemoryStore()
	cfg := Config{
		Scopes: []Scope{{Owner: "team", Repo: "repo"}}, RunnerImage: strings.Repeat("b", 64),
		RunnerLabel: "docker:docker://node:24", RunnerURL: "https://git.example.test/",
		WaitingTTL: 10 * time.Minute, JobTimeout: time.Hour, OperationTTL: 2 * time.Minute, MaxConcurrent: 4,
		MaxPerScope: 2,
		CPU:         2, MemoryMiB: 4096, DiskGiB: 20,
	}
	controller := NewController(cfg, api, compute, store)
	controller.now = func() time.Time { return now }
	return controller, api, compute, store, now
}

func TestControllerEnforcesPerScopeConcurrency(t *testing.T) {
	controller, api, compute, store, now := controllerFixture()
	store.state.Workloads["running-a"] = Workload{Handle: "running-a", Scope: "team/repo", CreatedAt: now}
	store.state.Workloads["running-b"] = Workload{Handle: "running-b", Scope: "team/repo", CreatedAt: now}
	api.jobs = []ActionJob{
		{ID: 1, Handle: "running-a", Status: "running", RunsOn: []string{"docker"}},
		{ID: 2, Handle: "running-b", Status: "running", RunsOn: []string{"docker"}},
		{ID: 3, Handle: "waiting-c", Status: "waiting", RunsOn: []string{"docker"}},
	}
	if err := controller.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	if api.created != 0 || len(compute.created) != 0 {
		t.Fatal("scope concurrency limit was bypassed")
	}
}

func TestControllerEmptyQueueCreatesNoRunnerOrVM(t *testing.T) {
	controller, api, compute, _, _ := controllerFixture()
	if err := controller.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	if api.created != 0 || len(compute.created) != 0 {
		t.Fatalf("empty queue created runner=%d vm=%d", api.created, len(compute.created))
	}
}

func TestControllerCreatesOneJobVMAndDoesNotPersistToken(t *testing.T) {
	controller, api, compute, store, _ := controllerFixture()
	api.jobs = []ActionJob{{ID: 42, Handle: "job-handle", Status: "waiting", RunsOn: []string{"docker"}}}
	if err := controller.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	if api.created != 1 || len(compute.created) != 1 || len(compute.started) != 1 {
		t.Fatalf("provision counts runner=%d create=%d start=%d", api.created, len(compute.created), len(compute.started))
	}
	if compute.stdin != strings.Repeat("a", 40) {
		t.Fatal("one-time token did not reach compute stdin")
	}
	if strings.Contains(strings.Join(compute.execArg, " "), compute.stdin) {
		t.Fatal("one-time token leaked into guest argv")
	}
	body := reflect.ValueOf(store.state.Workloads["job-handle"])
	for index := 0; index < body.NumField(); index++ {
		if strings.Contains(strings.ToLower(body.Type().Field(index).Name), "token") {
			t.Fatal("persistent workload state contains a token field")
		}
	}
}

func TestControllerCleansCompletedJob(t *testing.T) {
	controller, api, compute, store, now := controllerFixture()
	store.state.Workloads["done"] = Workload{
		Handle: "done", Scope: "team/repo", RunnerID: 9, InstanceID: "anas-fj-0123456789abcdef0123", CreatedAt: now,
	}
	if err := controller.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(api.deleted, []int64{9}) || !reflect.DeepEqual(compute.deleted, []string{"anas-fj-0123456789abcdef0123"}) {
		t.Fatalf("cleanup registration=%v vm=%v", api.deleted, compute.deleted)
	}
	if len(store.state.Workloads) != 0 {
		t.Fatalf("completed workload remains: %#v", store.state.Workloads)
	}
}

func TestControllerDoesNotDeleteWorkloadOnQueueAPIError(t *testing.T) {
	controller, api, compute, store, now := controllerFixture()
	api.listErr = errors.New("temporary API failure")
	store.state.Workloads["running"] = Workload{
		Handle: "running", Scope: "team/repo", RunnerID: 9,
		InstanceID: "anas-fj-0123456789abcdef0123", CreatedAt: now,
	}
	if err := controller.Reconcile(context.Background()); err == nil {
		t.Fatal("queue API failure was not reported")
	}
	if len(api.deleted) != 0 || len(compute.deleted) != 0 || len(store.state.Workloads) != 1 {
		t.Fatal("transient queue API failure deleted an active workload")
	}
}

func TestControllerWaitingTTLDeletesAndBacksOff(t *testing.T) {
	controller, api, compute, store, now := controllerFixture()
	api.jobs = []ActionJob{{ID: 42, Handle: "blocked", Status: "waiting", TaskID: 0, RunsOn: []string{"docker"}}}
	store.state.Workloads["blocked"] = Workload{
		Handle: "blocked", Scope: "team/repo", RunnerID: 9, InstanceID: "anas-fj-0123456789abcdef0123",
		CreatedAt: now.Add(-11 * time.Minute),
	}
	if err := controller.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	if api.created != 0 || len(compute.created) != 0 {
		t.Fatal("waiting TTL cleanup immediately reprovisioned without backoff")
	}
	if !store.state.RetryAfter["blocked"].After(now) {
		t.Fatalf("retry backoff was not persisted: %#v", store.state.RetryAfter)
	}
}

func TestParseScopesRejectsGlobalAndInvalidScopes(t *testing.T) {
	for _, value := range []string{"*", "/", "owner/repo/extra", "owner,owner"} {
		if _, err := ParseScopes(value); err == nil {
			t.Errorf("accepted invalid scope %q", value)
		}
	}
	scopes, err := ParseScopes("team/repo,team")
	if err != nil || len(scopes) != 2 {
		t.Fatalf("valid scopes = %#v, %v", scopes, err)
	}
}
