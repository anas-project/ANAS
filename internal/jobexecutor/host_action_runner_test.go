package jobexecutor

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/anas-project/ANAS/internal/actionabi"
	"github.com/anas-project/ANAS/internal/consolejobs"
	"github.com/anas-project/ANAS/internal/hostaction"
	"github.com/anas-project/ANAS/internal/incushost"
)

// The real Store, lease and recorder are used; hostd is a scripted stream and
// ledger. These tests are not systemd or socket acceptance.
func hostRunnerFixture(t *testing.T) (*hostActionRunner, *consolejobs.Store, *consolejobs.ExecutionLease, consolejobs.Job, consolejobs.JobCommitObserver) {
	t.Helper()
	ctx := context.Background()
	dir := filepath.Join(t.TempDir(), "jobs")
	store, err := consolejobs.Open(dir, consolejobs.Options{})
	if err != nil {
		t.Fatal(err)
	}
	lease, err := consolejobs.AcquireExecutionLease(ctx, dir)
	if err != nil {
		store.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = lease.Close()
		if err := store.Close(); err != nil {
			t.Error(err)
		}
	})
	release := hostaction.ReleaseIdentity{Version: "0.1.1", Commit: strings.Repeat("a", 40)}
	request, err := HostJobRequest(release)
	if err != nil {
		t.Fatal(err)
	}
	spec := consolejobs.CreateSpec{WorkspaceID: "fixture-workspace", Request: request, Idempotency: consolejobs.IdempotencyInput{Principal: "fixture-actor", Method: "POST", CanonicalPath: "/host/actions"}}
	observer := consolejobs.JobCommitObserverFunc(func(context.Context, consolejobs.JobCommitIntent) error { return nil })
	created, err := store.CreateActionWithPolicyObserved(ctx, spec, hostaction.ActionStatus, "host-call", consolejobs.ActionReject, observer)
	if err != nil {
		t.Fatal(err)
	}
	job, err := store.StartActionObserved(ctx, created.Job.ID, lease, observer)
	if err != nil {
		t.Fatal(err)
	}
	r, err := newHostActionRunner(store, lease, release, func(context.Context, consolejobs.Job) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	r.poll, r.absent = 10*time.Millisecond, 50*time.Millisecond
	r.dial = func(context.Context, actionabi.Request) (hostStream, error) {
		t.Fatal("unexpected dial")
		return nil, nil
	}
	r.query = func(context.Context, string, string) (hostaction.InvocationStatus, error) {
		t.Fatal("unexpected ledger query")
		return hostaction.InvocationStatus{}, nil
	}
	return r, store, lease, job, observer
}

type scriptedStream struct {
	io.Reader
	closed bool
}

func (s *scriptedStream) Close() error { s.closed = true; return nil }

func preflightResult(t *testing.T, job consolejobs.Job) actionabi.Event {
	t.Helper()
	report, err := incushost.Preflight(incushost.Facts{OS: "linux", Architecture: "amd64", Release: incushost.Release{ID: "debian", Version: "13"}, Systemd: true}, incushost.Options{})
	if err != nil {
		t.Fatal(err)
	}
	value, _ := json.Marshal(report)
	changed := false
	return actionabi.Event{ABI: actionabi.Version, JobID: job.ID, InvocationID: job.Action.InvocationID, Type: "result",
		Result: &actionabi.Result{Outcome: actionabi.Succeeded, Changed: &changed, Value: value}}
}

func preflightFailure(job consolejobs.Job) actionabi.Event {
	return actionabi.Event{ABI: actionabi.Version, JobID: job.ID, InvocationID: job.Action.InvocationID, Type: "error",
		Error: &actionabi.Failure{Outcome: actionabi.Failed, Code: "host_observation_failed", Message: "Host preflight could not be completed"}}
}

func frames(t *testing.T, events ...actionabi.Event) *scriptedStream {
	t.Helper()
	var buffer bytes.Buffer
	for _, event := range events {
		body, err := actionabi.EncodeExecutorEvent(event)
		if err != nil {
			t.Fatal(err)
		}
		buffer.Write(body)
	}
	return &scriptedStream{Reader: &buffer}
}

func TestHostRunnerRecordsTheStreamedTerminal(t *testing.T) {
	for name, want := range map[string]actionabi.Outcome{"success": actionabi.Succeeded, "failure": actionabi.Failed} {
		t.Run(name, func(t *testing.T) {
			r, store, _, job, observer := hostRunnerFixture(t)
			terminal := preflightResult(t, job)
			if want == actionabi.Failed {
				terminal = preflightFailure(job)
			}
			var sent actionabi.Request
			stream := frames(t, terminal)
			r.dial = func(_ context.Context, request actionabi.Request) (hostStream, error) {
				sent = request
				return stream, nil
			}
			finished, err := r.ExecutePreflight(context.Background(), job.ID, observer)
			if err != nil || finished.Action.Outcome != want || !stream.closed {
				t.Fatal(finished.Status, err)
			}
			if sent.JobID != job.ID || sent.InvocationID != job.Action.InvocationID || sent.Action != hostaction.ActionStatus || string(sent.Parameters) != "{}" {
				t.Fatal("hostd received a request other than the frozen job", sent)
			}
			page, err := store.ReplayAction(context.Background(), job.ID, 0, 100)
			if err != nil || len(page.Events) != 1 || page.Outcome != want {
				t.Fatal("terminal not committed to shared replay", err)
			}
		})
	}
}

func TestHostRunnerSettlesABrokenStreamFromTheLedger(t *testing.T) {
	cases := map[string]struct {
		states      []hostaction.InvocationState
		want        actionabi.Outcome
		containment bool
	}{
		"finished after running": {states: []hostaction.InvocationState{hostaction.InvocationRunning, hostaction.InvocationFinished}, want: actionabi.Succeeded},
		"lost":                   {states: []hostaction.InvocationState{hostaction.InvocationLost}, want: actionabi.Unknown, containment: true},
		"never began":            {states: []hostaction.InvocationState{hostaction.InvocationAbsent}, want: actionabi.Failed},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			r, _, lease, job, observer := hostRunnerFixture(t)
			// A progress-free stream that ends without a terminal frame.
			r.dial = func(context.Context, actionabi.Request) (hostStream, error) { return frames(t), nil }
			queries := 0
			r.query = func(_ context.Context, jobID, invocationID string) (hostaction.InvocationStatus, error) {
				if jobID != job.ID || invocationID != job.Action.InvocationID {
					t.Fatal("ledger queried for another invocation")
				}
				state := c.states[min(queries, len(c.states)-1)]
				queries++
				status := hostaction.InvocationStatus{Schema: hostaction.InvocationStatusSchema, State: state}
				if state == hostaction.InvocationFinished {
					terminal := preflightResult(t, job)
					status.Terminal = &terminal
				}
				return status, nil
			}
			finished, err := r.ExecutePreflight(context.Background(), job.ID, observer)
			if finished.Action == nil || finished.Action.Outcome != c.want {
				t.Fatal(finished, err)
			}
			if c.containment != errors.Is(err, consolejobs.ErrActionContainment) {
				t.Fatal("containment barrier mismatch", err)
			}
			if c.containment != errors.Is(lease.Close(), consolejobs.ErrExecutionRetained) {
				t.Fatal("execution retention mismatch after settlement")
			}
		})
	}
}

func TestHostRunnerWithoutHostdOrAuthorityNeverSends(t *testing.T) {
	r, _, _, job, observer := hostRunnerFixture(t)
	r.dial = func(context.Context, actionabi.Request) (hostStream, error) { return nil, hostaction.ErrNoExecution }
	finished, err := r.ExecutePreflight(context.Background(), job.ID, observer)
	if err != nil || finished.Action.Outcome != actionabi.Failed || finished.Error == nil || finished.Error.Code != "host_action_unavailable" {
		t.Fatal("missing hostd was not a plain failure", finished.Error, err)
	}

	r, _, _, job, observer = hostRunnerFixture(t)
	r.authorize = func(context.Context, consolejobs.Job) error { return errors.New("revoked") }
	finished, err = r.ExecutePreflight(context.Background(), job.ID, observer)
	if err != nil || finished.Action.Outcome != actionabi.Failed || finished.Error == nil || finished.Error.Code != "job_authorization_revoked" {
		t.Fatal("revoked actor reached hostd", finished.Error, err)
	}
}

func TestHostRunnerRejectsAJobItDidNotStart(t *testing.T) {
	r, store, lease, job, observer := hostRunnerFixture(t)
	if _, err := r.ExecutePreflight(context.Background(), "missing", observer); err == nil {
		t.Fatal("unknown job executed")
	}
	cancelled, err := store.CompleteActionObserved(context.Background(), lease, preflightFailure(job), observer)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.ExecutePreflight(context.Background(), cancelled.ID, observer); err == nil {
		t.Fatal("finished job executed again")
	}
}

func TestHostRunnerStopsWaitingOnALedgerThatCannotAnswer(t *testing.T) {
	r, _, _, job, observer := hostRunnerFixture(t)
	r.dial = func(context.Context, actionabi.Request) (hostStream, error) { return frames(t), nil }
	queries := 0
	r.query = func(context.Context, string, string) (hostaction.InvocationStatus, error) {
		queries++
		return hostaction.InvocationStatus{}, hostaction.ErrUnavailable
	}
	finished, err := r.ExecutePreflight(context.Background(), job.ID, observer)
	if !errors.Is(err, consolejobs.ErrActionContainment) || finished.Action == nil || finished.Action.Outcome != actionabi.Unknown || queries != 5 {
		t.Fatal("unanswerable ledger was not bounded", queries, err)
	}
}
