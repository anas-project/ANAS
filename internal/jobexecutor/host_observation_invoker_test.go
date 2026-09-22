package jobexecutor

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/anas-project/ANAS/internal/actionabi"
	"github.com/anas-project/ANAS/internal/consolejobs"
	"github.com/anas-project/ANAS/internal/hostaction"
	"github.com/anas-project/ANAS/internal/incusingresshost"
)

func jobObservationRequest() incusingresshost.ProjectionRequest {
	return incusingresshost.ProjectionRequest{Schema: incusingresshost.ProjectionSchema, ScopeID: "main", Epoch: strings.Repeat("a", 64), Deployment: "deployment-one",
		Lease: incusingresshost.Lease{Consumer: "forgejo", Resource: "runners"}, InstanceID: "anas-fj-job1", WorkloadID: "job:123", GuestPort: 7000}
}

func jobObservationResponse(r incusingresshost.ProjectionRequest) incusingresshost.ProjectionResponse {
	return incusingresshost.ProjectionResponse{Schema: r.Schema, ObservationID: r.ObservationID, ScopeID: r.ScopeID, Epoch: r.Epoch, Deployment: r.Deployment, ServerUUID: "11111111-1111-4111-8111-111111111111",
		Authorized: []incusingresshost.AuthorizedHTTPLease{{Lease: r.Lease, ResourceID: "compute.runners", Project: "anas-runners", Interface: "incus_container", InstancePrefix: "anas-fj-", AllowedPorts: []uint16{7000}, Auth: "none"}},
		Identity:   incusingresshost.ProjectionHTTPIdentity{Lease: r.Lease, InstanceID: r.InstanceID, WorkloadID: r.WorkloadID, InstanceUUID: "22222222-2222-4222-8222-222222222222", Incarnation: strings.Repeat("b", 64), State: "Running", GuestIP: "10.42.0.2", GuestMAC: "00:16:3e:01:02:03", HostVethName: "vethguest0", HostVethMAC: "02:00:00:00:00:10", HostVethPeerIfIndex: 77, GuestPort: r.GuestPort}}
}

func observationServiceRuntime(t *testing.T, r *serviceTestRuntime, store *consolejobs.Store, lease *consolejobs.ExecutionLease, transform func(context.Context, string, []byte) []byte) {
	t.Helper()
	r.execute = func(ctx context.Context, id string, o consolejobs.JobCommitObserver) (consolejobs.Job, error) {
		job, err := store.Get(ctx, id)
		if err != nil {
			return job, err
		}
		body, err := parametersForExecution(job)
		if err != nil {
			return job, err
		}
		var req incusingresshost.ProjectionRequest
		if json.Unmarshal(body, &req) != nil || req.Validate() != nil {
			return job, hostaction.ErrRequest
		}
		value, _ := json.Marshal(jobObservationResponse(req))
		if transform != nil {
			value = transform(ctx, id, value)
		}
		changed := false
		return store.CompleteActionObserved(ctx, lease, actionabi.Event{ABI: actionabi.Version, JobID: id, InvocationID: job.Action.InvocationID, Type: "result", Result: &actionabi.Result{Outcome: actionabi.Succeeded, Changed: &changed, Value: value}}, o)
	}
}

func TestObservationInvokerUsesNewSharedJobsAndRejectsOldResults(t *testing.T) {
	for _, replay := range []bool{false, true} {
		t.Run(map[bool]string{false: "fresh", true: "replay"}[replay], func(t *testing.T) {
			s, r, store, lease := serviceFixture(t, nil)
			var prior []byte
			var runs atomic.Int32
			observationServiceRuntime(t, r, store, lease, func(_ context.Context, _ string, body []byte) []byte {
				runs.Add(1)
				if prior == nil {
					prior = append([]byte{}, body...)
				} else if replay {
					return prior
				}
				return body
			})
			stop, done := runServiceFixture(t, s)
			defer func() {
				stop()
				if err := <-done; err != nil {
					t.Error(err)
				}
			}()
			client := incusingresshost.ProjectionClient{Invoker: HostObservationInvoker{Service: s, Actor: "alice", WorkspaceID: "main"}}
			first, err := client.ObserveHTTP(context.Background(), jobObservationRequest())
			if err != nil {
				t.Fatal(err)
			}
			second, err := client.ObserveHTTP(context.Background(), jobObservationRequest())
			if replay {
				if err == nil {
					t.Fatal("old job response became fresh evidence")
				}
			} else if err != nil || second.ObservationID == first.ObservationID {
				t.Fatal("observations reused job/result", err)
			}
			if runs.Load() != 2 {
				t.Fatal("observation was coalesced or returned from history", runs.Load())
			}
		})
	}
}

func TestObservationJobCannotSelectAnotherWorkspaceAtAdmissionOrRecovery(t *testing.T) {
	s, r, store, _ := serviceFixture(t, nil)
	r.execute = func(context.Context, string, consolejobs.JobCommitObserver) (consolejobs.Job, error) {
		t.Error("cross-workspace observation executed")
		return consolejobs.Job{}, hostaction.ErrDenied
	}
	request := jobObservationRequest()
	request.ScopeID = "other"
	request.ObservationID = strings.Repeat("b", 64)
	body, _ := json.Marshal(request)
	frozen, err := HostActionRequest(hostaction.ActionObserveHTTP, s.options.Release, body)
	if err != nil {
		t.Fatal(err)
	}
	created, err := store.CreateActionWithPolicyObserved(context.Background(), consolejobs.CreateSpec{WorkspaceID: "main", Request: frozen, Idempotency: consolejobs.IdempotencyInput{Principal: "alice", Method: "POST", CanonicalPath: "/test"}}, hostaction.ActionObserveHTTP, "observation-wrong-scope", consolejobs.ActionReject, consolejobs.JobCommitObserverFunc(func(context.Context, consolejobs.JobCommitIntent) error { return nil }))
	if err != nil {
		t.Fatal(err)
	}
	stop, done := runServiceFixture(t, s)
	defer func() {
		stop()
		if err := <-done; err != nil {
			t.Error(err)
		}
	}()
	if _, err := s.Invoke(context.Background(), "alice", "main", hostaction.ActionObserveHTTP, body, ""); !errors.Is(err, hostaction.ErrDenied) {
		t.Fatal("admission permitted cross-workspace scope", err)
	}
	job := awaitServiceJob(t, store, created.Job.ID)
	if job.StartedAt != nil || job.Status != consolejobs.StatusFailed {
		t.Fatal("queued scope mismatch was not rejected before start")
	}
}

func TestObservationWaitCancellationDoesNotCancelOwnedSharedJob(t *testing.T) {
	s, r, store, lease := serviceFixture(t, nil)
	entered := make(chan string, 1)
	release := make(chan struct{})
	var once sync.Once
	defer once.Do(func() { close(release) })
	observationServiceRuntime(t, r, store, lease, func(ctx context.Context, id string, body []byte) []byte {
		entered <- id
		select {
		case <-release:
		case <-ctx.Done():
		}
		return body
	})
	stop, done := runServiceFixture(t, s)
	defer func() {
		once.Do(func() { close(release) })
		stop()
		if err := <-done; err != nil {
			t.Error(err)
		}
	}()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	finished := make(chan error, 1)
	go func() {
		_, err := (incusingresshost.ProjectionClient{Invoker: HostObservationInvoker{Service: s, Actor: "alice", WorkspaceID: "main"}}).ObserveHTTP(ctx, jobObservationRequest())
		finished <- err
	}()
	var id string
	select {
	case id = <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("shared observation did not start")
	}
	cancel()
	select {
	case err := <-finished:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("wait ignored cancellation")
	}
	job, err := store.Get(context.Background(), id)
	if err != nil || job.Status != consolejobs.StatusRunning || job.Action.Cancellation != nil {
		t.Fatal("subscriber cancellation altered owned execution", err)
	}
	once.Do(func() { close(release) })
	if job := awaitServiceJob(t, store, id); job.Status != consolejobs.StatusSucceeded {
		t.Fatal("owned observation could not finish independently")
	}
}
