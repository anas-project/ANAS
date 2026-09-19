package jobexecutor

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/anas-project/ANAS/internal/actionabi"
	"github.com/anas-project/ANAS/internal/consolejobs"
	"github.com/anas-project/ANAS/internal/hostaction"
	"github.com/anas-project/ANAS/internal/incushost"
)

// This fake supplies synthetic manager wait evidence only. The real Store and
// recorder are used, but these tests are not systemd or kernel acceptance.
type completionSession struct {
	exit              actionabi.ExitState
	exitErr, closeErr error
	observed, closed  bool
	onObserve         func()
}

func (*completionSession) Serve(context.Context, hostaction.ReleaseIdentity, hostaction.JobBinding, hostaction.AuditJournal) error {
	return nil
}
func (*completionSession) WaitExecutor(context.Context) error { return nil }
func (*completionSession) GrantPossible() bool                { return true }
func (s *completionSession) ObserveSystemdExit(context.Context) (actionabi.ExitState, error) {
	if s.onObserve != nil {
		s.onObserve()
	}
	s.observed = s.exitErr == nil
	return s.exit, s.exitErr
}
func (s *completionSession) Close() error {
	if !s.observed {
		return hostaction.ErrBrokerExecutorRunning
	}
	s.closed = true
	return s.closeErr
}

func completionFixture(t *testing.T) (*HostJobBinding, *completionSession, *consolejobs.Store, *consolejobs.ExecutionLease, consolejobs.JobCommitObserver, []byte) {
	t.Helper()
	store, lease, job, release, observer := hostBindingFixture(t, nil, true)
	b, err := NewHostJobBinding(context.Background(), store, lease, job.ID, release, func(context.Context, consolejobs.Job, hostaction.PeerIdentity) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	s := &completionSession{exit: actionabi.ExitState{ProcessExited: true, ExitCode: 0}}
	if err := b.attachBroker(s); err != nil {
		t.Fatal(err)
	}
	if err := b.WithHostInvocation(context.Background(), hostWire(job), release, hostPeer(), func(context.Context) error { return nil }); err != nil {
		t.Fatal(err)
	}
	b.brokerFinished(nil)
	t.Cleanup(func() {
		// Synthetic fixture containment recovery only; no production reset API.
		s.observed = true
		s.closeErr = nil
		b.completionQuarantined = false
		if err := b.Close(); err != nil {
			t.Error(err)
		}
	})
	report, err := incushost.Preflight(incushost.Facts{OS: "linux", Architecture: "amd64", Release: incushost.Release{ID: "debian", Version: "13"}, Systemd: true}, incushost.Options{})
	if err != nil {
		t.Fatal(err)
	}
	value, _ := json.Marshal(report)
	changed := false
	body, err := actionabi.EncodeExecutorEvent(actionabi.Event{ABI: actionabi.Version, JobID: job.ID, InvocationID: job.Action.InvocationID, Type: "result", Result: &actionabi.Result{Outcome: actionabi.Succeeded, Changed: &changed, Value: value}})
	if err != nil {
		t.Fatal(err)
	}
	return b, s, store, lease, observer, body
}

func TestHostCompletionRequiresManagerEvidenceBeforeTerminal(t *testing.T) {
	b, s, store, lease, observer, body := completionFixture(t)
	s.onObserve = func() {
		j, err := store.Get(context.Background(), b.job.ID)
		if err != nil || j.Status != consolejobs.StatusRunning || j.Action.LastSeq != 0 {
			t.Fatal("terminal preceded exit evidence", err)
		}
		if !errors.Is(lease.Close(), consolejobs.ErrExecutionRetained) {
			t.Fatal("execution ownership released early")
		}
	}
	checked := false
	auditObserver := consolejobs.JobCommitObserverFunc(func(ctx context.Context, intent consolejobs.JobCommitIntent) error {
		if !s.observed || !s.closed {
			t.Fatal("terminal preceded manager handle cleanup")
		}
		checked = true
		return observer.BeforeJobCommit(ctx, intent)
	})
	j, err := b.completePreflight(context.Background(), bytes.NewReader(body), func() error { return nil }, auditObserver)
	if err != nil || j.Status != consolejobs.StatusSucceeded || j.Action.Outcome != actionabi.Succeeded || !checked {
		t.Fatal(j.Status, err)
	}
	page, err := store.ReplayAction(context.Background(), j.ID, 0, 100)
	if err != nil || len(page.Events) != 1 || page.Events[0].Seq != 1 || page.Outcome != actionabi.Succeeded {
		t.Fatal("terminal not committed to shared replay", err)
	}
	if _, err := b.completePreflight(context.Background(), bytes.NewReader(body), func() error { return nil }, observer); err == nil {
		t.Fatal("completion replay accepted")
	}
}

func TestHostCompletionContradictoryOrUnapprovedOutputIsUnknown(t *testing.T) {
	for _, name := range []string{"exit code", "forced", "trailing data", "unapproved readiness", "missing output", "close output", "broker failure", "authority revoked", "cancelled owner"} {
		t.Run(name, func(t *testing.T) {
			b, s, _, _, observer, body := completionFixture(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			closeOutput := func() error { return nil }
			switch name {
			case "exit code":
				s.exit.ExitCode = 7
			case "forced":
				s.exit.Forced = true
			case "trailing data":
				body = append(body, 'x')
			case "unapproved readiness":
				body = bytes.Replace(body, []byte(`"compute_ready":false`), []byte(`"compute_ready":true`), 1)
			case "missing output":
				body = nil
			case "close output":
				closeOutput = func() error { return errors.New("private-marker") }
			case "broker failure":
				b.exchangeErr = errors.New("private-marker")
			case "authority revoked":
				s.onObserve = func() {
					b.authorize = func(context.Context, consolejobs.Job, hostaction.PeerIdentity) error {
						return errors.New("private-marker")
					}
				}
			case "cancelled owner":
				cancel()
			}
			j, err := b.completePreflight(ctx, bytes.NewReader(body), closeOutput, observer)
			if !errors.Is(err, actionabi.ErrUnknownOutcome) || j.Action == nil || j.Action.Outcome != actionabi.Unknown {
				t.Fatal("uncertain completion accepted", j.Status, err)
			}
			if strings.Contains(err.Error(), "private-marker") {
				t.Fatal("raw failure leaked")
			}
		})
	}
}

func TestHostCompletionMissingExitPersistsContainmentBarrier(t *testing.T) {
	for _, name := range []string{"missing status", "cleanup failure"} {
		t.Run(name, func(t *testing.T) {
			b, s, store, lease, observer, body := completionFixture(t)
			if name == "missing status" {
				s.exitErr = errors.New("private-marker")
			} else {
				s.closeErr = errors.New("private-marker")
			}
			j, err := b.completePreflight(context.Background(), bytes.NewReader(body), func() error { return nil }, observer)
			if !errors.Is(err, consolejobs.ErrActionContainment) || !consolejobs.ActionContainmentLost(j) {
				t.Fatal("missing durable containment barrier", err)
			}
			if !errors.Is(b.Close(), hostaction.ErrBrokerExecutorRunning) || !errors.Is(lease.Close(), consolejobs.ErrExecutionRetained) {
				t.Fatal("unknown containment released owner")
			}
			request, _ := HostJobRequest(b.release)
			next, err := store.CreateActionWithPolicyObserved(context.Background(), consolejobs.CreateSpec{WorkspaceID: "fixture-workspace", Request: request, Idempotency: consolejobs.IdempotencyInput{Principal: "fixture-actor", Method: "POST", CanonicalPath: "/host/actions"}}, "incus.status", "next-call", consolejobs.ActionReject, observer)
			if errors.Is(err, consolejobs.ErrActionExecutionBlocked) {
				return // Admission itself enforces the durable barrier.
			}
			if err != nil {
				t.Fatal(err)
			}
			if _, err := store.StartActionObserved(context.Background(), next.Job.ID, lease, observer); !errors.Is(err, consolejobs.ErrActionExecutionBlocked) {
				t.Fatal("later job bypassed barrier", err)
			}
		})
	}
}

func TestHostCompletionFailureFrameRequiresNonzeroExit(t *testing.T) {
	b, s, _, _, observer, _ := completionFixture(t)
	s.exit.ExitCode = 1
	body, _ := actionabi.EncodeExecutorEvent(actionabi.Event{ABI: actionabi.Version, JobID: b.job.ID, InvocationID: b.job.Action.InvocationID, Type: "error", Error: &actionabi.Failure{Outcome: actionabi.Failed, Code: "host_observation_failed", Message: "Host preflight could not be completed"}})
	j, err := b.completePreflight(context.Background(), bytes.NewReader(body), func() error { return nil }, observer)
	if err != nil || j.Status != consolejobs.StatusFailed || j.Action.Outcome != actionabi.Failed {
		t.Fatal(j.Status, err)
	}
}

func TestHostCompletionAuditFailureLeavesNoSuccess(t *testing.T) {
	b, _, store, _, _, body := completionFixture(t)
	observer := consolejobs.JobCommitObserverFunc(func(context.Context, consolejobs.JobCommitIntent) error { return io.ErrClosedPipe })
	if _, err := b.completePreflight(context.Background(), bytes.NewReader(body), func() error { return nil }, observer); err == nil {
		t.Fatal("failed audit allowed success")
	}
	j, err := store.Get(context.Background(), b.job.ID)
	if err != nil || j.Status != consolejobs.StatusRunning || j.Action.LastSeq != 0 {
		t.Fatal("failed audit persisted terminal", err)
	}
}
